# Typed Phase/FailurePolicy Enums

> Status: Approved
> Date: 2026-09-11
> Related: `api/v1alpha1/{flow,flowrun}_types.go`

## Problem

`FlowRunStatus.Phase`, `StepRunStatus.Phase`, `FlowSpec.FailurePolicy`, and `FlowStep.OnFailure` are plain `string` fields, validated only at the API server by a `+kubebuilder:validation:Enum` marker, and written/compared as bare literals throughout the controller, CLI, and tests. This already caused one shipped bug: `docs/api/flowrun.md` documented `Waiting` as a valid FlowRun-level phase when it's only valid at the step level — the two fields share no type, so nothing caught the mismatch. `make lint`'s `goconst` linter flagged the repetition (`Succeeded`/`Failed` alone appear 70+/38+ times package-wide across 16 files); an earlier lint-cleanup pass deferred the typed-enum decision, and the owner has now decided to do it.

## Constraints

- No CRD wire-format change — `config/crd/bases/*.yaml` and its chart/bundle mirrors must be byte-identical after `make generate && make manifests`; a named Go string type serializes identically to a plain string.
- No behavior change — every phase/failure-policy value, comparison, and transition stays exactly as documented in `docs/architecture/flowrun-state-model.md`; this is a Go-side type refactor only.
- Four distinct types, not one shared type, so the compiler catches a value from the wrong set being used in the wrong place (the motivating bug class): `FlowRunPhase` (`Pending`/`Running`/`Succeeded`/`Failed`/`Cancelled`), `StepPhase` (`Pending`/`Running`/`Succeeded`/`Failed`/`Skipped`/`Waiting`), `FailurePolicy` (`Fail`/`Continue`), `OnFailureAction` (`Fail`/`Continue`/`Skip`).
- Every helper function taking/returning a bare `string` for one of these four concepts must have its signature updated to the named type — Go doesn't implicitly convert across function boundaries (only bare literals get that leniency); this is what makes the compiler enumerate every call site needing attention.
- Metric label values and `metav1.Condition.Type` strings are a different concept even when spelled identically, and are out of scope — nothing compares a `Condition.Type` against a `FlowRunPhase`, so there's no cross-type-confusion bug possible there.
- `test/e2e/*.go` stays plain string literals — it asserts against `kubectl`/JSONPath text output from a separate process, not in-process struct fields, so there's no compile-time safety to gain.
- The `+kubebuilder:validation:Enum` marker remains the source of truth for CRD-valid values; the new constants must mirror it exactly.
- `go build ./...`, `go vet ./...`, and `make test` pass with zero changes to existing test expectations — assertions are re-typed, not re-targeted.

## Rejected Alternatives

- **A single shared `Phase` type for both FlowRun and Step phases** — reproduces the exact bug this design fixes: a `StepPhase`-only value like `Waiting` could still be assigned to `FlowRunStatus.Phase` with no compiler complaint.
- **Leave `OnFailure`/`FailurePolicy` as plain strings, only type the two Phase fields** — they're compared against the same shared literal (`"Continue"`) in combined conditions, keeping the same typo-prone surface for two more small enums not worth carving out separately.
- **Codegen the constants from the `+kubebuilder:validation:Enum` marker text** — no existing tool in this project's generate/manifests pipeline does this, and building one for four small, rarely-changing enums is disproportionate; hand-written constants next to the marker (with a linking comment) is simpler.
- **Retrofit `test/e2e/*.go` to use the typed constants** — those tests compare against `kubectl`-emitted text, not a Go struct field, so there's no safety gain, only added import surface for cosmetic consistency.

## Decision

Add four named string types with constants matching each field's existing `+kubebuilder:validation:Enum` marker exactly — `FlowRunPhase`, `StepPhase`, `FailurePolicy`, `OnFailureAction` (all in `api/v1alpha1`, colocated with the owning struct). Change `FlowRunStatus.Phase`, `StepRunStatus.Phase`, `FlowSpec.FailurePolicy`, `FlowStep.OnFailure` to these types (CRD schema unchanged, verified by diff after `make generate && make manifests`). Update every helper function signature the compiler flags (`finishFlowRun`, `cancelFlowRun`, `failFlowRun`, `enforceMaxFlowRunsByPhase`, `dependenciesMet`, etc.), then replace remaining bare-literal comparisons/assignments across `internal/controller/`, `internal/cli/`, `internal/metrics/`, and `cmd/kubezap/` with the named constants — excluding `metav1.Condition.Type` strings, Prometheus label arguments, and `test/e2e/*.go`. Remove the now-unneeded `goconst` path exclusion for the two `flowrun_controller(_test)?.go` files in `.golangci.yml`.

- This is a real, moderate-sized refactor (four new types, ~16 files, 100+ call sites) rather than a pure lint fix — accepted since the owner explicitly asked for it after weighing the tradeoff.
- Four small types instead of one shared type is slightly more surface area for a new contributor to learn — accepted because the cross-type-confusion bug class it prevents already shipped once.
- Narrowed helper-function signatures cost a small ergonomic overhead (one more type name to remember) in exchange for the compiler enumerating every call site needing attention, permanently.
