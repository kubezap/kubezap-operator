# Flow Parameters

> Status: Approved
> Date: 2026-09-10
> Related: `docs/api/flow.md`, `docs/api/flowrun.md`

## Problem

`FlowSpec.params`, `FlowRunSpec.params`, and `$(params.<name>)` interpolation are fully specified in the API (`flow_types.go`, `flowrun_types.go`) and documented in `docs/api/flow.md`, but none of it is implemented — `substituteVars` and the CEL `when:` activation map have no `params` case, so setting `spec.params` is silently accepted and ignored, and any `$(params.x)` is left as an unresolved literal. Separately, there's a real functional gap: no supported way to invoke a Flow with explicit, validated inputs independent of a Trigger event — every example depends on `$(trigger.body.<field>)`, which requires a real triggering payload, and there's no default-value or required-field validation for any interpolated input today.

## Constraints

- No CRD schema change — `FlowSpec.Params`/`FlowRunSpec.Params`/`ParamValue`/`ParamDeclaration` already exist in v1alpha1 and are already accepted by the API server; this is controller-behavior only.
- Zero-params Flows (the overwhelming majority of existing Flows) must behave identically before and after.
- No new RBAC — all data needed (`Flow.spec.params`, `FlowRun.spec.params`, `FlowRun.spec.triggerData`) is already fetched by the reconciler for other reasons.
- No new FlowRun phase — a required-param validation failure is a normal `Failed` transition via the existing `failFlowRun` path, same as "Flow not found" today.
- No measurable reconcile-loop cost addition — in-memory map construction and string parsing, reusing existing body-parsing helpers.
- Must work uniformly across every trigger type, hanging off the same shared `TriggerData.Body`/`ContentType` parsing.
- Resolving the same `FlowRunSpec` + `Flow.spec.params` must produce the same values on every reconcile (`FlowRunSpec` is immutable after creation) — recomputed fresh each time, same pattern as `stepResults`.
- A FlowRun with a `required` param and no supplied value or default must fail before any step is dispatched.
- An explicit `FlowRunSpec.Params` entry always wins over an auto-derived value from the trigger body.
- Existing interpolation sources (`trigger.body`, `trigger.headers`, `steps.*.results`, `secrets.*`) are unaffected — `params` is purely additive.

## Rejected Alternatives

- **Gateway-side auto-population** (each of the four gateway binaries looks up the Flow and builds `FlowRunSpec.Params` itself) — couples every gateway to a new `Flow` read/RBAC surface the gateways are deliberately kept minimal without, duplicates resolution logic four times, and moves business logic out of the controller where the rest of interpolation already lives.
- **Fully implicit resolution only, no `FlowRunSpec.Params`** — removes the ability to invoke a Flow directly with explicit params and no real triggering event, the core gap this closes.
- **Persist resolved params to `FlowRun.status`** — adds a new status field and write for no behavioral benefit; resolution is cheap and already re-derived fresh every reconcile for `stepResults`.
- **A dedicated `ValidatingParams` FlowRun phase** — the state model defines exactly five phases; "Flow not found" is an analogous pre-execution failure today and goes straight to `Failed`, so param validation follows the same precedent.
- **Allow `default` values to use `$(...)` interpolation** — adds complexity (what a default may reference, in what order) for a case a plain literal default adequately serves; revisit if a real need shows up.

## Decision

Implement Flow-level parameters entirely in `internal/controller/flowrun_controller.go`, no gateway or CRD changes. At the `""|"Pending" → "Running"` transition, before any step dispatches, build a `paramsMap map[string]string` per `Flow.spec.params[i]` declaration in order: (1) a matching `FlowRunSpec.Params` entry, resolved through the existing `substituteVars`; (2) else a same-named top-level field from `TriggerData.Body` (JSON or form-urlencoded, reusing existing body-parsing helpers); (3) else the declared `Default` literal; (4) else fail via `failFlowRun` if `Required`; (5) else resolve to `""` (matches the existing missing-field precedent). Thread `paramsMap` into `substituteVars` as `$(params.<name>)` and into `evaluateWhen`'s CEL activation map as `params`. Update `docs/api/flow.md`/`docs/api/flowrun.md` to drop the "not implemented" warnings once this lands. `FlowStep.params` is explicitly out of scope — file a follow-up decision (implement with clarified purpose, or remove from the API) rather than guess at intent here.

- Auto-mapping a declared param to a same-named top-level `trigger.body` field is convenient (matches what existing `flow.md` examples already assume) but is one more implicit rule to know — mitigated by explicit `FlowRunSpec.Params` always taking priority, and by logging resolved param names (not values) at debug level so the winning source is diagnosable.
- Literal-only defaults are less powerful than interpolated defaults, but simpler; acceptable initial scope.
