# STORY-013: Define execution-latency benchmark methodology

**Epic:** EPIC-004 — Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step)
**Status:** Implemented, PR #189 open (not yet merged)
**Size:** S

## Description

Define exactly what's being measured and how, before building anything. This is a methodology document, not a design record per `planning/process/design-process.md` — it doesn't change KubeZap's CRDs, controllers, security posture, or introduce a shipped dependency; Argo Workflows here is a one-off comparison baseline for a local measurement, not something KubeZap depends on.

**Delivered** (`benchmarks/execution-latency/METHODOLOGY.md`): correctly identified that Argo's native `http` template runs via a shared per-workflow Agent process, not per-step pods — using it would have invalidated the whole Pod-per-step comparison premise. Picked the classic `container` template instead.

## Acceptance Criteria

- [x] **Scenario defined**: webhook → 3 sequential HTTP calls (`runAfter`-chained) against a dedicated Mockoon fixture, mirroring `test/e2e/feature_matrix_test.go`'s pattern.
- [x] **Baseline pinned**: Argo Workflows v4.1.3 (flagged as needing re-verification at harness-build time).
- [x] **Metrics defined**: latency from each system's own status fields (verified `StepRunStatus.StartTime`/`CompletionTime` field names against `api/v1alpha1/flowrun_types.go`), resource overhead via `kubectl top pod` (flagged metrics-server's default 60s resolution as too coarse for this use, proposed 15s), two-part cold-start definition.
- [x] **Run parameters defined**: 200 sequential firings per system, justified via the standard `n(1-p) ≥ 10` threshold for p95 stability; raw per-run JSON output.

## File / Module Footprint

- `benchmarks/execution-latency/METHODOLOGY.md` (new — this repo doesn't have a `benchmarks/` directory yet, this story creates it)

## Dependencies

- Depends on: none
- Blocks: STORY-014 (can't build the harness without the scenario/metrics this defines)

## Notes

Keep this small — it's a methodology writeup, not the harness itself. Resist the urge to start scripting anything here; that's STORY-014.
