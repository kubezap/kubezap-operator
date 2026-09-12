# Single-Pass Template Interpolation

> Status: Approved
> Related: `internal/controller/flowrun_controller.go` (`substituteVars`, `substituteVarsWithSecrets`)

## 1. Problem Statement

`substituteVars` and `substituteVarsWithSecrets` resolve `$(...)` placeholders with a sequence of scan-and-`ReplaceAll` passes, each of which re-scans the *already-substituted* string for the next placeholder type. Two of these passes (`$(trigger.body.<field>)` and `$(trigger.headers.<name>)`) scan for their own prefix in a `for { idx := strings.Index(s, prefix); ...; s = strings.ReplaceAll(s, placeholder, value) }` loop — the loop condition is evaluated against `s` *after* the replacement, so a resolved value containing the same placeholder text is found and "resolved" again.

Two confirmed, exploitable consequences, both driven by attacker-controlled trigger data (an unauthenticated webhook body or header):

1. **Reconciler hang (DoS).** A trigger body field whose value is itself `$(trigger.body.<same-field>)` (or a header whose value is `$(trigger.headers.<same-name>)`) never terminates — the loop keeps finding and "replacing" the same placeholder text forever. Confirmed live: `POST {"a": "$(trigger.body.a)"}` against a Flow that interpolates `$(trigger.body.a)` hangs the reconcile goroutine at 100% CPU indefinitely. With `MaxConcurrentReconciles` (default 10), ten such requests exhaust the controller's entire reconcile capacity.
2. **Secret exfiltration.** `substituteVarsWithSecrets` resolves non-secret placeholders first, then scans the *result* for `$(secrets.*)`. If a Flow step already contains one legitimate `$(secrets.*)` reference anywhere in the same string (a common shape: an auth token alongside interpolated user data), an attacker who controls a body field, header, step result, or resolved param can inject literal `$(secrets.<any-name>.<any-key>)` text that gets resolved and sent to whatever the attacker controls — reading **any** secret in the namespace, not just ones the Flow author referenced.

Separately (not a security issue, found while investigating the above): non-string JSON leaf values render incorrectly. `resolveBodyPath` falls back to `fmt.Sprintf("%v", v)`, and `encoding/json`'s default decode makes every JSON number a `float64`. An integer above ~1e6 (order IDs, epoch-millis timestamps) renders in scientific notation (`1234567` → `"1.234567e+06"`), and a nested object/array renders as Go syntax (`{"id":1}` → `"map[id:1]"`) rather than valid JSON — silent output corruption for any Flow step that embeds such a field in an outgoing JSON body.

## 2. Constraints

- No CRD schema change — controller-behavior fix only.
- **Every existing edge case in `substitute_vars_test.go` must produce an identical result.** These are deliberate, tested distinctions, not incidental behavior:
  - `$(trigger.body.<field>)` is left **verbatim** (not replaced) when `TriggerData.Body == ""` or is unparseable — but bare `$(trigger.body)` always resolves (to `""` if the body is empty).
  - An unresolvable `$(steps.*)`/`$(params.*)` placeholder is left **verbatim**.
  - A missing `$(trigger.headers.<name>)` or a present-but-missing `$(trigger.body.<field>)` path resolves to an **empty string** (not verbatim) — different from the steps/params case above.
  - `$(trigger.body.<field>)` must resolve independently of, and without corrupting, a co-occurring bare `$(trigger.body)` in the same string.
- `trigger.bodyFields` in the CEL activation map (added earlier this session, `docs/design/2026-09-11-cel-trigger-body-fields.md`) depends on `parseTriggerBody` returning native `float64`/`bool`/nested types so CEL can do typed numeric comparisons (`trigger.bodyFields.total >= 1000.0`) without casts. **`parseTriggerBody`'s decoding must not change** — the numeric-rendering fix has to live in the string-interpolation output path (`resolveBodyPath`), not in JSON decoding, or it silently breaks that CEL feature.
- A `$(secrets.*)` fetch error must still abort the *entire* substitution and return an error — no partial/mixed result, matching current behavior exactly (a Flow author must never receive a request sent with a missing/garbage secret value silently substituted).
- No new RBAC, no new external dependency, no new FlowRun phase. Reconcile budget: still O(n) over the template string plus the same secret Get calls already made today — the fix is a control-flow change, not new work.

## 3. Invariants

- A resolved placeholder's value is **never re-examined for further placeholders** — the scan position always advances past the original `$(...)` in the *input* template, regardless of what the substituted value contains.
- Every `$(...)` token is resolved from the template's original text exactly once.
- `trigger.body` (CEL) and `parseTriggerBody`'s return type are unaffected by the interpolation-output-formatting fix.
- A secret-fetch error aborts the whole call with no output written; a successful call's `display` string never contains a real secret value, only `[REDACTED]` in its place.

## 4. Rejected Alternatives

**A. Patch each vulnerable loop individually** (cap iterations, or `break` after N replacements) rather than restructure the whole function.
Rejected: treats the symptom, not the cause. The step-results/params loops and the secrets loop have the *same* underlying flaw (each scans/replaces against a string that already contains prior substitutions) — they don't hang today only because they're bounded by a finite number of known keys, but the secret-exfiltration path is exactly this same "re-scan already-substituted output" behavior, just without a `for{}` loop making it visible as a hang. An iteration cap on the two literal hanging loops would stop the DoS but leave the exfiltration vector completely open.

**B. Sanitize/reject trigger data containing `$(` before interpolation.**
Rejected: `$(` is unremarkable in real payloads (Markdown, shell snippets, templated JSON bodies passed through verbatim); rejecting or stripping it changes what a Flow author receives for reasons that have nothing to do with security, and doesn't address the same-shaped `$(secrets.*)` injection risk arising from *any* attacker-influenced string (step results from a called external API are just as attacker-adjacent as body fields, and can't be "sanitized" without breaking legitimate use of those results).

**C. Keep the two-function split (`substituteVars` / `substituteVarsWithSecrets`) as fully separate implementations, just each rewritten to be single-pass independently.**
Rejected: the two functions already share nearly all resolution logic; maintaining two independent single-pass scanners risks exactly the kind of drift that caused this bug's more subtle half (the boundary *between* the two functions, where `substituteVars`'s output becomes `substituteVarsWithSecrets`'s secret-scan input, was never re-examined for cross-injection). One shared scanner with a nil-able secret resolver removes that seam entirely.

## 5. Tradeoffs

- The fast-path optimization in the old `substituteVarsWithSecrets` (skip building a parallel display string when the template has no `$(secrets.` substring at all) is dropped. The single-pass version always builds two output buffers in lockstep; the extra cost is one more `strings.Builder.WriteString` call per resolved token, negligible next to the API calls this function already makes for real secrets.
- The malformed-placeholder handling used to `break` out of the whole secrets-scanning loop after the first malformed `$(secrets.<name>)` (no key), a known minor pre-existing bug. The rewrite fixes this as a natural side effect (an unmatched token is simply left verbatim and scanning continues) rather than as a deliberately separate change — noted here so it isn't mistaken for scope creep.
- Fixing numeric rendering via `strconv.FormatFloat(f, 'f', -1, 64)` (shortest round-trip decimal, never scientific notation) rather than switching to `json.Number` decoding is a smaller, more conservative fix — it corrects display for every value within float64's exact-integer range (±2^53, comfortably covering epoch-millis timestamps and typical order/record IDs) without the CEL-breaking side effect described in Constraints. It does not fix precision loss for integers *larger* than 2^53 (e.g. 19-digit snowflake IDs) — those are already lossy at the `json.Unmarshal` step, before formatting is ever reached, and fixing that would require the `UseNumber()` change this design explicitly avoids. Out of scope here.

## 6. Final Decision

Replace the sequential scan-and-`ReplaceAll` passes with one shared, single-pass tokenizer (`interpolateTemplate`) that walks the template left-to-right exactly once, extracts each `$(...)` token from the *original* text, resolves it via a dispatch table (`steps.*`, `params.*`, `trigger.body`/`trigger.body.*`/`trigger.headers.*`/`trigger.topic`/`trigger.partition`/`trigger.offset`/`trigger.scheduledTime`, and `secrets.*` when a resolver is supplied), and appends the result directly to the output — the scan position always advances past the original closing `)`, so substituted content is structurally unreachable by the scanner. `substituteVars` calls it with a `nil` secret resolver (so `$(secrets.*)` is left verbatim, matching today); `substituteVarsWithSecrets` supplies a resolver closing over `ctx`/`namespace`/`fetchSecretValue`, aborting the whole call on the first fetch error. `resolveBodyPath`'s scalar-rendering fixes numeric formatting (`strconv.FormatFloat`, no scientific notation) and marshals map/slice leaves back to JSON instead of Go's `%v` syntax; `parseTriggerBody`'s decoding is untouched.
