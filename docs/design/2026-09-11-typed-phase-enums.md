# Typed Phase/FailurePolicy Enums

> Status: Draft
> Related: `docs/schedule.md` §36, `docs/tech-debt/pending-input-required.md` ("Lint Deferred Items — 2026-09-11")

## 1. Problem Statement

`FlowRunStatus.Phase`, `StepRunStatus.Phase`, `FlowSpec.FailurePolicy`, and `FlowStep.OnFailure` are all plain `string` fields, each with a documented value set enforced only by a `+kubebuilder:validation:Enum` marker (API-server-side admission validation) and otherwise written/compared as bare string literals throughout the controller, CLI, and tests. This has already caused one real, previously-shipped bug this session found and fixed: `docs/api/flowrun.md` documented `Waiting` as a valid FlowRun-level phase, when it is only ever a valid *step*-level phase — the two fields share no type, so nothing caught a FlowRun-level phase check being written against the step-level value set. Nothing stops the same class of mistake from happening again in either direction, or a plain typo (`"Succeeeded"`) from compiling silently and only failing at runtime as an unmatched comparison.

This was flagged by `make lint`'s `goconst` linter (`docs/schedule.md` §36: `true`, `Pending`, `Skipped`, `Waiting`, `Cancelled`, `Running`, `Continue` each repeated 3+ times in `internal/controller/flowrun_controller.go`/`_test.go` — the visible finding undercounts the real scope: `Succeeded` and `Failed` alone each appear 70+/38+ times package-wide, and a full-repo grep for these values touches 16 files). §36 deferred a decision (introduce a typed enum vs. leave the suppression in place) rather than guess; the owner has now decided: do it.

## 2. Constraints

- **No CRD wire-format change.** `config/crd/bases/*.yaml` (and its mirrors in `charts/kubezap-operator/crds/`, `bundle/manifests/`) must be byte-identical after `make generate && make manifests` — a named Go string type (`type FlowRunPhase string`) serializes identically to a plain `string` in the generated OpenAPI schema (`type: string` + the same `enum:` list from the existing kubebuilder markers, unchanged). This is a Go-side type-safety change only, not an API change.
- **No behavior change.** Every phase/failure-policy value, comparison, and transition stays exactly as documented in `docs/architecture/flowrun-state-model.md` — this is a refactor of *how* the values are spelled in Go source, not what the reconciler does.
- Four distinct value sets get four distinct types, not one shared type, specifically so the compiler catches a value from the wrong set being used in the wrong place (the motivating `Waiting`-as-a-FlowRun-phase bug class):
  - `FlowRunPhase`: `Pending`, `Running`, `Succeeded`, `Failed`, `Cancelled`
  - `StepPhase`: `Pending`, `Running`, `Succeeded`, `Failed`, `Skipped`, `Waiting`
  - `FailurePolicy` (`FlowSpec.FailurePolicy`): `Fail`, `Continue`
  - `OnFailureAction` (`FlowStep.OnFailure`): `Fail`, `Continue`, `Skip`
- Go does not implicitly convert a named string type to/from a plain `string` across a function boundary (only bare literal constants get that leniency) — every helper function that currently takes/returns a bare `string` for one of these four concepts must have its signature updated to the named type. This is expected and is exactly the mechanism that gives the safety benefit: the compiler enumerates every real call site that needs attention, rather than relying on a manual audit.
- Metric label values and `metav1.Condition.Type` strings that happen to share the same text (e.g. a Prometheus label `"Succeeded"`, or a `Condition.Type: "Succeeded"`) are a **different concept** from `FlowRunStatus.Phase` even when spelled identically, and are explicitly out of scope — `metav1.Condition.Type` is a standard Kubernetes API string field with its own (unrelated) conventions, and Prometheus client libraries require plain `string` label values. Converting these would be scope creep with no safety benefit (nothing is ever compared against a `Condition.Type` value expecting a `FlowRunPhase`, so there is no cross-type-confusion bug class to prevent there).
- `test/e2e/*.go` largely asserts against `kubectl`/JSONPath *text output* (a separate process, parsed as plain strings), not in-process Go struct fields — these are left as plain string literals; there is no compile-time safety to gain by importing `api/v1alpha1` into e2e tests just to wrap a value that was never a typed field access to begin with.

## 3. Invariants

- `automationv1alpha1.FlowRunPhase`, `StepPhase`, `FailurePolicy`, and `OnFailureAction` are each a `type X string` with named constants for every value already listed in that field's `+kubebuilder:validation:Enum` marker — the marker and the constant list must never drift apart (the marker remains the source of truth for what's CRD-valid; the constants must mirror it exactly).
- `config/crd/bases/*.yaml`, `charts/kubezap-operator/crds/*.yaml`, and `bundle/manifests/*.yaml` are unchanged by this refactor (verified by diffing before/after `make generate && make manifests`).
- Every in-process (non-e2e) production and test call site that assigns to or compares against `FlowRunStatus.Phase`, `StepRunStatus.Phase`, `FlowSpec.FailurePolicy`, or `FlowStep.OnFailure` uses the named constant, not a bare string literal, once this lands — this is what the `goconst` exclusion in `.golangci.yml` gets removed in exchange for.
- `go build ./...`, `go vet ./...`, and the full `make test` suite pass with zero changes to existing test *expectations* (test assertions are re-typed, not re-targeted) — this is a mechanical Go-type refactor, and the existing FlowRun state-transition test suite (§25/§28) is the safety net that a transition's actual behavior hasn't changed.

## 4. Rejected Alternatives

**A. A single shared `Phase` type used for both `FlowRunStatus.Phase` and `StepRunStatus.Phase`.**
Rejected: this is the exact shape of the bug that motivated this design (`Waiting` valid for one, not the other) — a shared type would let a `StepPhase`-only value like `Waiting` be assigned to `FlowRunStatus.Phase` without any compiler complaint, reproducing the original problem instead of fixing its root cause.

**B. Leave `FlowStep.OnFailure` and `FlowSpec.FailurePolicy` as plain strings, only type the two Phase fields.**
Rejected: `OnFailure`/`FailurePolicy` are compared against each other's shared literal value (`"Continue"`) in several combined conditions (`step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue"`) — leaving them untyped keeps exactly the same typo-prone surface the Phase fields were fixed for, in the same file, for no real savings in effort (they're two more small enums, not a large addition to scope).

**C. Generate the constants from the `+kubebuilder:validation:Enum` marker text automatically (codegen) rather than writing them by hand.**
Rejected: no existing tool in this project's `make generate`/`make manifests` pipeline does this, and building one for four small, rarely-changing enums is disproportionate tooling investment — hand-written constants next to the marker they mirror (with a comment linking them) is simpler and the marker/constant drift risk is low given how rarely CRD status enums change.

**D. Retrofit `test/e2e/*.go` to import `api/v1alpha1` and use the typed constants for its string comparisons against `kubectl`/JSONPath output.**
Rejected: those tests compare against text emitted by a separate `kubectl` process, not a Go struct field — there was never a compile-time type to get wrong there, so there's no safety gain, only added import surface for cosmetic consistency.

## 5. Tradeoffs

- This is a real, moderate-sized mechanical refactor (four new types, ~16 files touched, 100+ individual call sites once `Succeeded`/`Failed` are included) rather than a pure lint fix — accepted, since the owner explicitly asked for it after weighing the tradeoff themselves.
- Four small types instead of one shared "Phase" type is slightly more surface area to learn (a new contributor has to know there are two Phase types, not one) — accepted because the cross-type-confusion bug class this specifically prevents is a real bug this project already shipped once.
- Helper functions that currently take a bare `string` for phase/reason/message (e.g. `finishFlowRun`) get their signatures narrowed to the specific typed enum — a small ergonomic cost (one more type name to remember when calling them) in exchange for the compiler enumerating every call site that needs attention during this refactor, and permanently preventing a future call site from passing the wrong kind of value.

## 6. Final Decision

Add four named string types with constants matching each field's existing `+kubebuilder:validation:Enum` marker exactly: `FlowRunPhase`, `StepPhase`, `FailurePolicy`, `OnFailureAction` (all in `api/v1alpha1`, colocated with the struct that owns each field). Change `FlowRunStatus.Phase`, `StepRunStatus.Phase`, `FlowSpec.FailurePolicy`, `FlowStep.OnFailure` to these types (CRD schema unchanged — verified by diff after `make generate && make manifests`). Update every helper function signature that the compiler flags as a result (e.g. `finishFlowRun`, `cancelFlowRun`, `failFlowRun`, `enforceMaxFlowRunsByPhase`, `dependenciesMet`), then replace every remaining bare-literal comparison/assignment across `internal/controller/`, `internal/cli/`, `internal/metrics/`, and `cmd/kubezap/` with the named constants — explicitly excluding `metav1.Condition.Type` strings, Prometheus metric label arguments, and `test/e2e/*.go`'s text-based assertions, none of which are typed-field access to begin with. Remove the now-unneeded `goconst` path exclusion for the two `flowrun_controller(_test)?.go` files in `.golangci.yml` once the literals are gone.
