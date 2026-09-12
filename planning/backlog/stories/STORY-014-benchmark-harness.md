# STORY-014: Build and run the execution-latency benchmark harness

**Epic:** EPIC-004 — Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step)
**Status:** Backlog — blocked on STORY-013
**Size:** M — cannot size precisely until STORY-013 fixes the exact run-count/scenario, but "build two fixture setups + a runner script + a results collector" is a bounded, well-understood shape regardless of those specifics

## Description

Build the harness implementing STORY-013's methodology: stand up both systems (KubeZap and Argo Workflows) in the same Kind cluster, run the defined scenario the defined number of times against each, and collect raw per-run results.

## Acceptance Criteria

- [ ] KubeZap side: the webhook→3-HTTP-calls scenario from STORY-013, deployed the same way `test/e2e/` fixtures are (Mockoon + Flow + Trigger), fired the agreed number of times, per-step timing pulled from `FlowRun.status`.
- [ ] Argo side: an equivalent `Workflow` (3 sequential HTTP steps against the same Mockoon target) installed via the pinned Argo version from STORY-013, fired the same number of times, per-step timing pulled from `Workflow.status.nodes`.
- [ ] Resource-overhead sampling running concurrently with both (per STORY-013's `kubectl top pod` polling approach) — same polling interval and Kind cluster/node sizing for both systems, so the comparison is apples-to-apples.
- [ ] Raw results (not just summary stats) written to disk in a consistent, diffable format (e.g. one JSON file per run) so STORY-015 can recompute p50/p95 rather than trusting a single computed number.
- [ ] The harness is re-runnable — a single entry-point script, not a sequence of manual steps someone has to remember.

## File / Module Footprint

- `benchmarks/execution-latency/` — new directory: setup manifests for both systems' scenario, a runner script, a results directory (raw output, likely gitignored except a `.gitkeep` or a small representative sample — decide at implementation time whether raw per-run JSON is worth committing or just the STORY-015 summary)
- Does not touch any existing KubeZap source, CRD, or controller — this only *uses* KubeZap as a black box via its public API, same as any other e2e-style consumer

## Dependencies

- Depends on: STORY-013 (methodology must be fixed first — scenario, metrics, run count)
- Blocks: STORY-015 (write-up needs this harness's actual output)

## Notes

Explicitly out of `test/e2e/`'s scope — this is a one-off measurement tool, not part of the regular test suite, so it lives in its own top-level directory rather than inside `test/`.
