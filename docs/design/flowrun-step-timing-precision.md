# FlowRun Step-Timing Precision

> Status: Approved
> Date: 2026-09-13
> Related: `api/v1alpha1/flowrun_types.go`, `internal/controller/flowrun_controller.go`, `internal/metrics/metrics.go`, `benchmarks/execution-latency/RESULTS.md`

## Problem

`StepRunStatus`/`FlowRunStatus`'s `StartTime`/`CompletionTime` are `*metav1.Time`, which marshals to whole-second precision — a hard property of the type. The execution-latency benchmark hit this directly: real per-step dispatch latency is ~5–10ms, but 594 of 600 step observations in the benchmark's raw `FlowRun.status` JSON read as an exact `0.000s` (RESULTS.md §2.1); the same quantization affects `FlowRunStatus` (a 537ms mean end-to-end duration would show as `0s` or `1s`, RESULTS.md §2.3). The true numbers are visible only via a one-off Prometheus scrape (`kubezap_step_duration_seconds`/`kubezap_flowrun_duration_seconds`, full-precision `time.Since()`), not via `kubectl get flowrun -o yaml` — the tool a user actually reaches for. Real latency is invisible on the object whose purpose is to report what happened, making genuinely fast execution look broken or unmeasured.

## Constraints

- API stability (v1alpha1, no field removals without deprecation) — `StartTime`/`CompletionTime` at both levels stay exactly as they are, same type and meaning.
- Any new field must come from a value the reconciler already computes, not a new measurement path: per step, `time.Since(stepStart)` in the wave-execution goroutine (~line 584), carried through `stepResult.duration` and observed into `kubezap_step_duration_seconds` (~line 643); per FlowRun, `time.Since(flowRun.Status.StartTime.Time)` at both terminal-transition sites (inline Succeeded path ~line 692, and `finishFlowRun` ~line 1478) immediately before observing `kubezap_flowrun_duration_seconds`. A design requiring a *second* timing measurement (e.g. re-subtracting persisted timestamps later) is disfavored — redundant, and a second source of truth that could disagree with the metric.
- No behavior change to reconciliation, phase transitions, or existing conditions — status-schema addition only.
- Needs `make generate && make manifests`; must show up correctly in `config/crd/bases/automation.kubezap.io_flowruns.yaml`.
- Metrics remain canonical for cross-run aggregation — this is about per-object visibility only, not a replacement for the histograms in p50/p95 aggregation across runs.
- A step/FlowRun that hasn't reached a terminal state has no duration value set — mirrors the existing `CompletionTime == nil` convention exactly; the field's presence must never disagree with whether `CompletionTime` is set.
- The persisted duration is always numerically identical to what the corresponding histogram recorded for that execution — no independent recomputation path that could drift.
- `StartTime`/`CompletionTime` keep their existing meaning (ordering, correlation with events/logs, the existing `ExecutionTimeout` check at ~lines 317–321, which already truncates to `time.Second`) — nothing repurposes or deprecates them.
- Existing `FlowRun` objects remain valid — the new field is `+optional`, decodes as absent/zero on old objects.

## Rejected Alternatives

- **Sub-second timestamps via `metav1.MicroTime`, replacing or augmenting `StartTime`/`CompletionTime`** — answers a question nobody asked (the problem is duration, not exact instant); still forces every consumer to subtract two timestamps themselves; doesn't cleanly reuse the already-computed `time.Duration` (would need a second `time.Now()` capture, a second source of truth that could drift from the metric); no existing or planned consumer needs absolute time beyond `FlowRunStatus`'s existing second-precision `ExecutionTimeout` check; and retyping `StartTime` is a larger, riskier change (marshal/CRD-schema/downstream-consumer impact) for a capability nothing here needs.
- **Sub-second timestamps as new additional fields (`StartTimeMicro`/`CompletionTimeMicro`), alongside the untouched originals** — four new fields instead of one or two, the same client-side-subtraction downside as above, and doubles the timestamp footprint for a value nobody needs more precisely than duration.
- **Leave the status types alone, treat the Prometheus histograms as sufficient** — the status quo this record is about: fine for cross-run p50/p95 aggregation, not for "what did *this* FlowRun cost" from a single `kubectl get flowrun -o yaml`.
- **Apply the duration field only to `StepRunStatus`, defer `FlowRunStatus` for later** — the same evidence (RESULTS.md's cumulative-counter caveat) applies at both levels, and the reconciler already computes the precise duration at both terminal-transition sites, so deferring saves no real implementation cost while creating a "half the object is fixed" inconsistency.

## Decision

Add `DurationMillis *int64` (`json:"durationMillis,omitempty"`, `+optional`) to both `StepRunStatus` and `FlowRunStatus` in `api/v1alpha1/flowrun_types.go` — applied at both levels, not per-step only. `StartTime`/`CompletionTime` stay unchanged at both levels. The controller sets the new field directly from the `time.Duration` value already computed and observed into the corresponding histogram — `res.duration.Milliseconds()` when folding `stepResult` into a persisted `StepRunStatus`, and `duration.Milliseconds()` at both `FlowRunStatus` terminal-transition sites — so there's exactly one measurement per execution feeding both the histogram and the status field, with no second clock read. No `v1alpha2` bump is required: this adds two `+optional` fields and removes or retypes nothing, so existing persisted objects and unaware clients are unaffected. Code review should confirm `DurationMillis` is only set once a step/FlowRun reaches the same terminal condition that already sets `CompletionTime`, so the two never disagree on "has this finished."

- Milliseconds loses precision below 1ms — accepted; the benchmark's own 5–10ms numbers make ms resolution comfortably sufficient, and it matches the unit already used throughout `RESULTS.md`.
- A plain `int64` (not `metav1.Duration`) loses a self-describing unit, but `time.Duration.Milliseconds()` is a one-line, allocation-free conversion at the exact call site, versus wrapping in `metav1.Duration`; matches the existing convention (numeric, no wrapping) already used for the parallel Prometheus float64-seconds observation.
- Doesn't fix cross-run aggregation — a script wanting p50/p95 across many FlowRuns should still use the Prometheus histograms, not paginate through `FlowRun` objects; this field is for single-object visibility only.
