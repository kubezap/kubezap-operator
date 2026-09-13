# STORY-020: Design record — sub-second step-timing visibility on FlowRun

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Done (PR #204)
**Size:** XS — design record only, no implementation

## Description

STORY-015's execution-latency benchmark (EPIC-004) confirmed KubeZap's real per-step dispatch latency is ~5-10ms — but `StepRunStatus.StartTime`/`CompletionTime` (`api/v1alpha1/flowrun_types.go`, both `*metav1.Time`) round to whole seconds, so **594 of 600 step observations in that benchmark's raw data read as an exact `0.000s`**. The only place the real number is currently visible is a one-off Prometheus scrape of `kubezap_step_duration_seconds` (`internal/metrics/metrics.go`, recorded via real `time.Since()` in `internal/controller/flowrun_controller.go`) — not `kubectl get flowrun -o yaml`, which is what a user actually reaches for. If "faster/lighter than Pod-per-step engines" becomes a stated differentiator (STORY-015's go/no-go: **pursue**), users should be able to see this on the object itself.

This is a CRD status-schema change — per `planning/process/design-process.md`'s trigger list, it needs a design record before implementation, same as STORY-008 was for `WebhookGatewayConfig`.

## Acceptance Criteria

- [ ] Design record answering: **(a)** sub-second-precision timestamp fields (e.g. `StartTime`/`CompletionTime` as a higher-precision type, or additional fields) vs. **(b)** an explicit `DurationMillis`/`Duration` field on `StepRunStatus` populated directly from the same `time.Since()` value already computed for the Prometheus histogram (simpler, avoids `metav1.Time` precision questions entirely, doesn't need a new timestamp type). Recommend (b) unless the record surfaces a real reason timestamps (not just a duration) are needed.
- [ ] Decide whether `FlowRunStatus`'s own top-level `StartTime`/`CompletionTime` (end-to-end duration) get the same treatment, or whether per-step is sufficient for now (the benchmark's own end-to-end number, `kubezap_flowrun_duration_seconds`, has the identical quantization problem).
- [ ] Confirm the chosen shape doesn't require a `v1alpha2` bump — additive `+optional` fields should be safe, but state this explicitly per `design-process.md`'s API-compatibility section.
- [ ] File under `docs/design/`, indexed in `docs/design/README.md`, per the standard six-section format.

## File / Module Footprint

- `docs/design/YYYY-MM-DD-flowrun-step-timing-precision.md` (new)
- `docs/design/README.md` (index entry)

## Dependencies

- Depends on: none
- Blocks: STORY-021 (implementation)

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-13, from STORY-015's benchmark write-up). Routed to EPIC-002 rather than a new epic — owner decision at the 2026-09-13 checkpoint: this is exactly the kind of post-release hardening item that epic exists for, not large enough to need its own epic.
