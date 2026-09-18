# Single-Pass Template Interpolation

> Status: Approved
> Date: 2026-09-11
> Related: `internal/controller/flowrun_controller.go` (`substituteVars`, `substituteVarsWithSecrets`)

## Problem

`substituteVars`/`substituteVarsWithSecrets` resolve `$(...)` placeholders via sequential scan-and-`ReplaceAll` passes, each re-scanning the *already-substituted* string for the next placeholder. Two passes (`trigger.body.<field>`, `trigger.headers.<name>`) evaluate their loop condition against the string *after* replacement, so a resolved value containing the same placeholder text is found and resolved again. Two confirmed, attacker-triggerable consequences: (1) a trigger body/header field whose value is `$(trigger.body.<same-field>)` (or the header equivalent) hangs the reconcile goroutine at 100% CPU indefinitely — confirmed live, and with `MaxConcurrentReconciles` (default 10), ten such requests exhaust the controller's entire reconcile capacity; (2) `substituteVarsWithSecrets` resolves non-secret placeholders first, then scans the *result* for `$(secrets.*)` — if a step already contains one legitimate secret reference, an attacker controlling any other interpolated field can inject literal `$(secrets.<any-name>.<any-key>)` text that gets resolved and exfiltrated, reading any secret in the namespace. Separately (non-security): `resolveBodyPath`'s `fmt.Sprintf("%v", v)` fallback renders large JSON numbers in scientific notation and nested objects as Go's `map[...]` syntax instead of valid JSON.

## Constraints

- No CRD schema change — controller-behavior fix only.
- Every existing case in `substitute_vars_test.go` must produce an identical result — deliberate, tested distinctions: `$(trigger.body.<field>)` is left verbatim when `TriggerData.Body` is empty/unparseable, but bare `$(trigger.body)` always resolves (to `""` if empty); an unresolvable `$(steps.*)`/`$(params.*)` is left verbatim; a missing header or a present-but-missing body path resolves to `""` (not verbatim); `$(trigger.body.<field>)` resolves independently of a co-occurring bare `$(trigger.body)` in the same string.
- `parseTriggerBody`'s decoding must not change — `trigger.bodyFields` in the CEL activation map (`docs/design/cel-trigger-body-fields.md`) depends on it returning native `float64`/`bool`/nested types for typed comparisons; the numeric-rendering fix must live in the interpolation-output path (`resolveBodyPath`), not JSON decoding.
- A `$(secrets.*)` fetch error must still abort the entire substitution with no partial/mixed result, matching current behavior exactly.
- No new RBAC, no new external dependency, no new FlowRun phase; same O(n) scan plus the same secret `Get` calls already made today.
- A resolved placeholder's value is never re-examined for further placeholders — the scan position always advances past the original `$(...)` in the input template.
- Every `$(...)` token resolves from the template's original text exactly once.
- A secret-fetch error aborts the whole call with no output written; a successful call's display string never contains a real secret value, only `[REDACTED]`.

## Rejected Alternatives

- **Patch each vulnerable loop individually (cap iterations)** — treats the symptom, not the cause; the step-results/params loops and the secrets loop share the same "re-scan already-substituted output" flaw, they just don't hang today because they're bounded by a finite key set — an iteration cap stops the DoS but leaves the secret-exfiltration vector, which has the identical root cause, completely open.
- **Sanitize/reject trigger data containing `$(` before interpolation** — `$(` is unremarkable in real payloads (Markdown, shell snippets, templated JSON); stripping it breaks legitimate content for reasons unrelated to security, and doesn't address the same injection risk from other attacker-adjacent sources (step results from a called API).
- **Keep `substituteVars`/`substituteVarsWithSecrets` as fully separate single-pass rewrites** — the two functions already share nearly all resolution logic; two independent scanners risk the exact kind of drift that caused this bug (the unexamined boundary between one function's output and the other's secret-scan input). One shared scanner with a nil-able secret resolver removes that seam.

## Decision

Replace the sequential scan-and-`ReplaceAll` passes with one shared, single-pass tokenizer (`interpolateTemplate`) that walks the template left-to-right once, extracts each `$(...)` token from the *original* text, resolves it via a dispatch table (`steps.*`, `params.*`, `trigger.body`/`trigger.body.*`/`trigger.headers.*`/`trigger.topic`/`trigger.partition`/`trigger.offset`/`trigger.scheduledTime`, and `secrets.*` when a resolver is supplied), and appends the result directly to output — the scan position always advances past the original closing `)`, making substituted content structurally unreachable by the scanner. `substituteVars` calls it with a `nil` secret resolver (so `$(secrets.*)` stays verbatim, matching today); `substituteVarsWithSecrets` supplies a resolver closing over `ctx`/`namespace`/`fetchSecretValue`, aborting on the first fetch error. `resolveBodyPath`'s scalar rendering switches to `strconv.FormatFloat(f, 'f', -1, 64)` (no scientific notation) and marshals map/slice leaves back to JSON instead of `%v`; `parseTriggerBody`'s decoding is untouched.

- Drops the old fast-path optimization (skip building a parallel display string when the template has no `$(secrets.` substring) — the single-pass version always builds two buffers in lockstep, negligible next to the secret-fetch API calls already made.
- Fixes a known minor pre-existing bug as a side effect: the old code `break`s out of the whole secrets-scanning loop after the first malformed `$(secrets.<name>)` with no key; the rewrite just leaves an unmatched token verbatim and continues — noted so it isn't mistaken for separate scope.
- Numeric rendering uses `strconv.FormatFloat` (shortest round-trip decimal) rather than switching to `json.Number` decoding — a smaller, more conservative fix that corrects display within float64's exact-integer range (±2^53, comfortably covering epoch-millis and typical IDs) without breaking the CEL dependency above; doesn't fix precision loss for integers beyond 2^53 (19-digit snowflake IDs), which are already lossy at `json.Unmarshal` — out of scope here.
