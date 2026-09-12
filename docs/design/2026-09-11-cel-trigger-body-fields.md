# CEL Access to Nested Trigger Body Fields

> Status: Approved
> Related: `docs/api/flow.md`

## 1. Problem Statement

CEL `when:` conditions cannot navigate into a parsed trigger body for any content type. `evaluateWhen`'s activation map always sets `trigger.body` to the raw request-body string (`triggerData.Body`), even when the body is JSON or form-urlencoded. Attempting `trigger.body.eventType == "x"` in a `when:` expression raises a CEL runtime error (selecting a field on a string), which the controller surfaces as a `when expression error` and fails the step.

This is a real functional gap, not just a doc bug (found and merely documented in §34, PR #147): `$(...)` string interpolation already supports full dot-path traversal into JSON/form-urlencoded bodies (`$(trigger.body.order.customer.tier)`, via `parseTriggerBody`/`resolveBodyPath`), but CEL conditions have no equivalent. The only workaround today is declaring a same-named top-level Flow `param` (auto-derived from the body, per the Flow-parameters design) — which cannot reach nested fields at all — or falling back to fragile raw-string `.contains()` substring matching. A Flow that needs to branch on `order.customer.tier` (a nested field) has no working option today.

## 2. Constraints

- No CRD schema change — this is a controller-behavior change only (mirrors the Flow-parameters design's constraint).
- `trigger.body`'s existing behavior (always the raw string) must not change. Several existing conditions and tests rely on `trigger.body == "..."` and `trigger.body.contains(...)` working on the raw string; a content-type-dependent change in what `trigger.body` resolves to would silently break them for JSON bodies specifically, and no `when:` expression could be written to work uniformly across content types.
- Must reuse the existing `parseTriggerBody` helper (already shared by `substituteVars` and `resolveFlowParams`) rather than a second, divergent body-parsing implementation — this is the established single-source-of-truth pattern already used for the JSON/form-urlencoded content-type decision.
- No new RBAC, no new external dependency, no new FlowRun phase.
- Reconcile loop budget: this reuses an existing in-memory parse already paid for elsewhere in the same reconcile (or, if not otherwise triggered, adds one more parse of an already-in-memory string) — no measurable addition to the < 1s budget.
- Must work uniformly across every trigger type, since it hangs off the same `TriggerData.Body`/`ContentType` fields already shared by all of them.

## 3. Invariants

- `trigger.body` in CEL remains the raw string for every content type, unconditionally — completely unaffected by this change.
- The new field resolves to exactly what `$(trigger.body.<field>)` interpolation would return before its final stringification step — i.e. the same parsed tree, so the two mechanisms never disagree about what a given trigger body contains.
- A trigger body that cannot be parsed (XML, plain text, binary, or unset) resolves the new field to an empty map, never a CEL evaluation error — `has(trigger.bodyFields.x)` and direct access both behave safely, matching the existing "missing body field resolves to empty" precedent used elsewhere in this reconciler.
- Resolving the same `TriggerData` produces the same result on every reconcile (pure function, no caching needed — same as `parseTriggerBody`'s existing callers).

## 4. Rejected Alternatives

**A. Make `trigger.body` itself dynamically typed** (string for unparseable content, map for JSON/form-urlencoded).
Rejected: violates the constraint above — the same `when:` expression would silently behave differently (or error) depending on the triggering request's `Content-Type`, which is exactly the kind of content-type-dependent footgun this project avoids elsewhere. It would also break every existing `trigger.body == "..."`/`.contains(...)` condition the moment the body happened to be valid JSON.

**B. Extend `ParamDeclaration.Name` to accept dot-paths**, letting a Flow declare a param like `name: order.customer.tier` and auto-derive it as a nested lookup, then use `params.*` in CEL as today.
Rejected: `ParamDeclaration.Name` is documented and used everywhere else as a flat identifier (`$(params.<name>)` has no path semantics in the interpolation grammar); overloading it to sometimes mean "a JSON path into the trigger body" is inconsistent with every other use of `params` and doesn't help a Flow with no `params` declared at all.

**C. Register a custom CEL function** (e.g. `jsonPath(trigger.body, "$.order.customer.tier")`) instead of exposing a native map value.
Rejected: significantly more implementation complexity (writing and registering a CEL function, its own error handling and type-checking inside the CEL runtime) for no benefit over exposing a native Go map — CEL's default type adapter already navigates nested `map[string]interface{}`/`[]interface{}` values via ordinary field-select and index syntax, exactly as `steps.*.results` and `params.*` already do.

**D. Add a new top-level activation variable** (e.g. `bodyFields`, not nested under `trigger`).
Rejected: breaks the existing `trigger.*` namespacing convention (`.topic`, `.partition`, `.offset`, `.scheduledTime`, `.headers` are all nested under `trigger`), and doesn't match how the interpolation side names the same data (`$(trigger.body.field)`). Nesting under `trigger` keeps the two mechanisms' naming as close as their differing type constraints allow.

## 5. Tradeoffs

- **Naming asymmetry**: CEL access is `trigger.bodyFields.<field>`, while interpolation access is `$(trigger.body.<field>)` — same underlying parsed value, different surface names, because a single `trigger.body` cannot safely mean both "raw string" and "parsed map" at once (see Rejected Alternative A). Mitigated with explicit cross-referencing in the docs.
- **Typed values, inconsistently with the rest of CEL's data sources**: `trigger.bodyFields` values keep their native JSON types (numbers as CEL `double`, booleans as CEL `bool`, nested objects/arrays navigable directly) rather than being pre-stringified like `steps.*.results` and `params.*` are. This is a net improvement (no `double()`/`bool()` casts needed for trigger-body-derived conditions) but means "is this value typed or a string" now depends on which activation variable it came from — worth calling out explicitly in the docs so it isn't a surprise.
- **Does not solve the XML/non-JSON case.** `trigger.bodyFields` is an empty map for any content type `parseTriggerBody` doesn't already parse (XML, plain text, binary). The `.contains()`-on-raw-string fallback remains the only option for those — this change closes the JSON/form-urlencoded gap only, which is the gap that's actually reachable without a much larger XML-parsing investment (out of scope here, and independently not something CEL access alone would justify).

## 6. Final Decision

Add a new `trigger.bodyFields` key to the CEL activation map built in `evaluateWhen` (`internal/controller/flowrun_controller.go`), populated by calling the existing `parseTriggerBody(triggerData)` helper — the same one `substituteVars` and `resolveFlowParams` already call — and defaulting to an empty `map[string]interface{}{}` when it returns `nil` (unparseable body, nil `TriggerData`, or empty body). No change to any `cel.NewEnv(...)` variable declaration is needed: `trigger` is already declared as `cel.MapType(cel.StringType, cel.DynType)`, so a new dynamically-typed key requires no schema change, only a new entry in the activation value built at eval time.

`trigger.body` is left completely untouched. Ginkgo coverage is added directly against `evaluateWhen` in `evaluate_when_test.go`: nested JSON field access, array indexing, form-urlencoded top-level access, XML/unparseable body defaulting to an empty map (`has()` returns false, no error), and a JSON body with a non-string field (number/bool) comparing correctly as a native CEL type. Docs (`docs/api/flow.md`'s "Using Trigger Data in CEL Conditions" section, CEL Quick Reference, and the `## Limitations` bullet added in §34) are updated to describe the real, now-implemented capability and its one remaining boundary (XML/non-parseable content types).
