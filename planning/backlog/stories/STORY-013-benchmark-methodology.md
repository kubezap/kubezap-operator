# STORY-013: Define execution-latency benchmark methodology

**Epic:** EPIC-004 — Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step)
**Status:** Planned (WP-2 — see `planning/checkpoints/checkpoint-2026-09-12/work-packages-2.md`)
**Size:** S

## Description

Define exactly what's being measured and how, before building anything. This is a methodology document, not a design record per `planning/process/design-process.md` — it doesn't change KubeZap's CRDs, controllers, security posture, or introduce a shipped dependency; Argo Workflows here is a one-off comparison baseline for a local measurement, not something KubeZap depends on.

## Acceptance Criteria

- [ ] **Scenario defined**: a representative lightweight automation — webhook trigger → 3 sequential HTTP calls to an in-cluster Mockoon-style echo target (reuses the same pattern as `test/e2e/feature_matrix_test.go`'s Mockoon fixtures) — implemented once as a KubeZap `Trigger`+`Flow` and once as an equivalent Argo `Workflow`.
- [ ] **Baseline pinned**: exact Argo Workflows version to install (pick current stable at benchmark time, record the exact version in the methodology doc — don't leave it floating).
- [ ] **Metrics defined**: p50/p95 per-step latency (wall-clock from step start to step completion, read from each system's own status fields — `FlowRun.status.steps[].startTime/completionTime` vs. Argo's per-node `startedAt`/`finishedAt`), per-step resource overhead (peak pod memory/CPU during the run, sampled via `kubectl top pod` polling — document the polling interval), and cold-start cost (time from trigger fired to first step starting, isolating executor-pod/Argo-pod scheduling+startup latency from step execution time itself).
- [ ] **Run parameters defined**: number of trigger firings per system to get a stable p50/p95 (propose a number, e.g. 50, and justify it), sequential vs. concurrent firing (propose starting sequential — the epic's Goal doesn't require concurrency data for a first go/no-go call), and how results are collected (raw JSON per run, not just a summary, so STORY-015 can recompute if a stat is wrong).

## File / Module Footprint

- `benchmarks/execution-latency/METHODOLOGY.md` (new — this repo doesn't have a `benchmarks/` directory yet, this story creates it)

## Dependencies

- Depends on: none
- Blocks: STORY-014 (can't build the harness without the scenario/metrics this defines)

## Notes

Keep this small — it's a methodology writeup, not the harness itself. Resist the urge to start scripting anything here; that's STORY-014.
