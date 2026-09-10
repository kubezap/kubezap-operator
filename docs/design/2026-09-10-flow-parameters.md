# Flow Parameters

> Status: Draft
> Related: `docs/schedule.md` §31 (spec drift finding), `docs/api/flow.md`, `docs/api/flowrun.md`

## 1. Problem Statement

`FlowSpec.params` (`ParamDeclaration`: `name`, `description`, `required`, `default`), `FlowRunSpec.params` (`ParamValue`: `name`, `value`), and `$(params.<name>)` interpolation are fully specified in the Go API (`api/v1alpha1/flow_types.go`, `flowrun_types.go`), fully documented in `docs/api/flow.md`, and used as the primary trigger→flow data-passing pattern in most of that doc's worked examples — but none of it is implemented. A repo-wide grep for `.Params` outside `api/v1alpha1/` returns nothing; `substituteVars` and the CEL `when:` activation map have no `params` case.

Today, setting `spec.params` on a Flow or FlowRun is silently accepted and silently ignored. Any `$(params.x)` in a step's URL, body, header, or `when:` expression is left as that literal unresolved string in the outgoing request or condition — a silent-failure trap, not a loud error.

Separately from the bug, there is a real functional gap this closes: **there is currently no supported way to invoke a Flow with explicit, validated inputs independent of a Trigger event.** `FlowRunSpec.TriggerData` is optional; nothing fills the gap when it's absent. Every working example depends on `$(trigger.body.<field>)`, which requires a real triggering event with a matching payload shape. There is also no validation or default-value mechanism for any interpolated input today — a missing `trigger.body` field silently resolves to an empty string rather than failing loudly or falling back to a declared default.

## 2. Constraints

- No CRD schema change. `FlowSpec.Params`, `FlowRunSpec.Params`, `ParamValue`, `ParamDeclaration` already exist in `v1alpha1` and are already accepted by the API server — this is a controller-behavior change, not an API change.
- Zero-params Flows (the overwhelming majority of existing Flows, including every currently-validated example) must behave identically before and after this change. No new required field, no new default behavior that affects a Flow with an empty `params` list.
- No new RBAC. All data needed to resolve params (`Flow.spec.params`, `FlowRun.spec.params`, `FlowRun.spec.triggerData`) is already fetched by the reconciler for other reasons. No new API calls, no new watched resource type.
- Must reuse the existing terminal-phase model (`docs/architecture/flowrun-state-model.md`) — no new FlowRun phase. A validation failure (missing required param) is a normal `Failed` transition via the existing `failFlowRun` path, same as "Flow not found" today.
- Reconcile loop budget: resolving params is in-memory map construction and string parsing (reusing existing body-parsing helpers) — no measurable addition to the reconcile loop's cost, consistent with the < 1s budget already assumed elsewhere in this reconciler.
- Must work uniformly across every trigger type (webhook, kafka, amqp, nats, cron, resource), since it hangs off the same `TriggerData.Body`/`ContentType` parsing already shared by all of them.
- Idempotency: resolving the same `FlowRunSpec` + `Flow.spec.params` must produce the same param values on every reconcile (`FlowRunSpec` is immutable after creation) — no caching or status field required to satisfy this; recomputing from spec each time is already how `stepResults` and interpolation generally work in this reconciler.

## 3. Invariants

- A `FlowRun` whose Flow declares a `required` param with no supplied value and no default **must fail before any step is dispatched** — never partially execute with an unresolved `$(params.x)` reaching a step's action.
- `$(params.<name>)` resolves identically on every reconcile of the same `FlowRun` (see idempotency constraint above).
- Resolution never mutates `FlowRunSpec` or `Flow.spec` — params are computed into an in-memory map per reconcile, the same pattern already used for `stepResults`.
- A `FlowRunSpec.Params` entry always wins over any auto-derived value from the trigger body for the same name (explicit beats implicit).
- Existing interpolation sources (`trigger.body`, `trigger.headers`, `steps.*.results`, `secrets.*`) are completely unaffected — `params` is purely additive to `substituteVars` and the CEL activation map.

## 4. Rejected Alternatives

**A. Gateway-side auto-population.** Have each gateway (webhook, kafka, amqp, nats — four separate binaries) look up the target `Flow`, read its `spec.params` declarations, and construct a matching `FlowRunSpec.Params` list before creating the `FlowRun`.
Rejected: couples every gateway binary to a `Flow` read (new RBAC surface on binaries that are deliberately kept minimal — see `http-executor`'s "no secrets, no RBAC management" design goal, which the same minimalism principle applies to), duplicates the same resolution logic across four codebases, and moves business logic out of the controller, where the rest of the interpolation/execution logic already lives. The controller already has everything it needs to do this itself.

**B. Fully implicit resolution only — no `FlowRunSpec.Params` support at all.** Auto-map declared param names to same-named `trigger.body` fields and stop there.
Rejected: removes the ability to invoke a Flow directly with explicit params and no real triggering event (the core functional gap this design closes), and removes the ability to rename or combine trigger fields into a differently-named param.

**C. Persist resolved params to `FlowRun.status`.** Resolve once at the `Pending→Running` transition and store the result so later reconciles don't recompute it.
Rejected: adds a new status field and an extra status write for no behavioral benefit — resolution is cheap, deterministic, and already re-derived fresh every reconcile for `stepResults`/interpolation generally. Adds surface area without solving a real problem.

**D. A dedicated intermediate FlowRun phase for param validation** (e.g. `ValidatingParams`) rather than routing straight to `Failed`.
Rejected: the state model doc defines exactly five FlowRun phases; adding a sixth for one specific validation failure is disproportionate. "Flow not found" is an analogous pre-execution failure today and it goes straight to `Failed` — param validation follows the same precedent.

**E. Allow `default` values to use `$(...)` interpolation**, matching `ParamValue.value`.
Rejected for v1: adds complexity (what can a default legitimately reference — trigger data? other params? both, with what evaluation order?) for a case that's adequately served by a plain literal default. Revisit if a real need for a dynamic default shows up.

## 5. Tradeoffs

- **Implicit name-matching convenience vs. one more rule to know.** Auto-mapping a declared param to a same-named top-level `trigger.body` field is convenient (it's what most of the existing `flow.md` examples already assume happens) but is one more piece of "magic" a reader has to be aware of — mitigated by documenting it prominently and by explicit `FlowRunSpec.Params` always taking priority, so the behavior is overridable and never silent about what value was actually used (result should be visible somewhere in status/logs for debugging — see implementation notes).
- **Literal-only defaults are less powerful** than interpolated defaults, but simpler to reason about and implement. Acceptable initial scope.
- **`FlowStep.params` (`[]ParamValue` at the step level) is explicitly out of scope for this design.** Its purpose relative to `FlowRunSpec.params` is unclear — no example or doc passage explains what it's for beyond a one-line "input values for this step's action" description, and it is not used to distinguish itself from just referencing `$(params.name)` directly in a step's action fields. Leaving it unaddressed means one already-known phantom field stays phantom a little longer. This is a deliberate scope cut, not an oversight — see Final Decision.

## 6. Final Decision

Implement Flow-level parameters (`FlowSpec.params` / `FlowRunSpec.params` / `$(params.<name>)`) entirely in `internal/controller/flowrun_controller.go`, with no gateway or CRD changes.

At the start of FlowRun execution (the existing `""|"Pending" → "Running"` transition point), before any step is dispatched, build a `paramsMap map[string]string` by resolving each `Flow.spec.params[i]` declaration in this order:

1. If `FlowRunSpec.Params` has an entry with a matching name, resolve its `value` through the existing `substituteVars` (so it can itself reference `$(trigger.body.x)`, `$(secrets.x.y)`, etc. — matches the already-documented `ParamValue.value` behavior).
2. Else, if `TriggerData.Body` can be parsed (JSON or form-urlencoded, reusing the existing `resolveBodyPath`/`parseFormURLEncodedBody`/`isFormURLEncodedContentType` helpers already used by `substituteVars`) and has a top-level field with the same name as the declared param, use that value.
3. Else, if `Default` is set, use it as a literal.
4. Else, if `Required` is true, fail the FlowRun via the existing `failFlowRun` path with a message naming the missing parameter, before any step executes.
5. Else, resolve to an empty string (matches existing precedent: a missing `trigger.body` field already silently resolves to `""`).

Thread `paramsMap` into `substituteVars` as a new interpolation source (`$(params.<name>)`) and into the CEL `when:` activation map (`evaluateWhen`) as a new `params` variable (`map<string, string>`), alongside the existing `trigger`/`steps` variables.

`FlowStep.params` is out of scope for this design — file a follow-up decision (implement with a clarified purpose, or remove from the API) rather than guess at intent here.

### Implementation notes (non-binding, for the implementer)

- Surface resolved param values somewhere debuggable — e.g. a debug-level log line per FlowRun listing resolved names (not values, if any could be secret-derived) — so "which source won" is diagnosable without reading source.
- Update `docs/api/flow.md` / `docs/api/flowrun.md` to remove the "not implemented" warnings added during the spec-drift pass (§31) and restore accurate documentation of the feature, including the explicit-overrides-implicit resolution order.
- Re-validate the ~7 worked examples in `flow.md` that already use `$(params.*)` — most should now work as originally written since they rely on same-named top-level trigger-body fields; confirm this live against the cluster rather than assuming.
- Add Ginkgo coverage for: explicit override wins over auto-derived; auto-derivation from both JSON and form-urlencoded bodies; default used when no value resolves; required-and-unresolved fails before step dispatch; `$(params.x)` and CEL `params.x` both resolve the same value for the same FlowRun.
