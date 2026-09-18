# CEL Access to Nested Trigger Body Fields

> Status: Approved
> Date: 2026-09-11
> Related: `docs/api/flow.md`

## Problem

CEL `when:` conditions can't navigate into a parsed trigger body for any content type — `evaluateWhen`'s activation map always sets `trigger.body` to the raw request-body string, so `trigger.body.eventType == "x"` raises a CEL runtime error. This is a real functional gap, not just a doc bug: `$(...)` interpolation already supports full dot-path traversal into JSON/form-urlencoded bodies via `parseTriggerBody`/`resolveBodyPath`, but CEL conditions have no equivalent. A Flow that needs to branch on a nested field (`order.customer.tier`) has no working option today — a same-named top-level Flow param can't reach nested fields, and raw-string `.contains()` matching is fragile.

## Constraints

- No CRD schema change — controller-behavior only.
- `trigger.body`'s existing raw-string behavior must not change for any content type; several existing conditions/tests rely on `trigger.body == "..."`/`.contains(...)` against the raw string, and it must resolve to exactly what `$(trigger.body.<field>)` interpolation would return pre-stringification, so the two mechanisms never disagree.
- Must reuse the existing `parseTriggerBody` helper (already shared by `substituteVars`/`resolveFlowParams`), not a second, divergent parser.
- No new RBAC, no new external dependency, no new FlowRun phase.
- No measurable reconcile-loop cost — reuses an already-in-memory parse, or adds one parse of an already-in-memory string.
- Must work uniformly across every trigger type via the shared `TriggerData.Body`/`ContentType` fields.
- An unparseable body (XML, plain text, binary, or unset) resolves the new field to an empty map, never a CEL evaluation error.
- Resolving the same `TriggerData` produces the same result every reconcile (pure function, no caching).

## Rejected Alternatives

- **Make `trigger.body` itself dynamically typed** (string vs. map depending on content type) — the same `when:` expression would silently behave differently depending on the request's `Content-Type`, and would break every existing raw-string condition the moment a body happened to be valid JSON.
- **Extend `ParamDeclaration.Name` to accept dot-paths** for nested auto-derived params, reused via `params.*` in CEL — `Name` is a flat identifier everywhere else in the interpolation grammar, and doesn't help a Flow with no `params` declared at all.
- **Register a custom CEL function** (`jsonPath(trigger.body, "$.order.customer.tier")`) — more implementation complexity for no benefit over a native map value, since CEL's default type adapter already navigates nested `map[string]interface{}` via ordinary field-select/index syntax.
- **Add a new top-level activation variable** (e.g. `bodyFields`, not nested under `trigger`) — breaks the existing `trigger.*` namespacing convention and doesn't match how interpolation names the same data (`$(trigger.body.field)`).

## Decision

Add a `trigger.bodyFields` key to the CEL activation map built in `evaluateWhen` (`internal/controller/flowrun_controller.go`), populated via the existing `parseTriggerBody(triggerData)` helper, defaulting to an empty `map[string]interface{}{}` when parsing fails or the body is empty/nil. No `cel.NewEnv(...)` schema change is needed — `trigger` is already `cel.MapType(cel.StringType, cel.DynType)`. `trigger.body` itself is left untouched. Ginkgo coverage is added directly against `evaluateWhen`: nested JSON access, array indexing, form-urlencoded access, unparseable-body-to-empty-map, and native-CEL-type comparison for non-string JSON fields. `docs/api/flow.md` ("Using Trigger Data in CEL Conditions", CEL Quick Reference, Limitations) is updated to describe the now-implemented capability.

- Naming asymmetry: CEL access is `trigger.bodyFields.<field>`, interpolation access is `$(trigger.body.<field>)` — same underlying value, different surface names, since `trigger.body` can't safely mean both raw string and parsed map at once. Cross-referenced explicitly in the docs.
- `trigger.bodyFields` values keep native JSON types (CEL `double`/`bool`/nested structures) rather than being pre-stringified like `steps.*.results`/`params.*` — a net improvement, but means "typed or string" now depends on which activation variable a value came from.
- Doesn't solve the XML/non-JSON case — `bodyFields` stays an empty map for any content type `parseTriggerBody` doesn't already handle; `.contains()`-on-raw-string remains the only option there.
