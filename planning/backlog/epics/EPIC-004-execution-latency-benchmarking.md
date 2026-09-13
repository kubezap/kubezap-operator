# EPIC-004: Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step)

**Status:** In Progress — all 3 stories through review; closes once PR #199 merges
**PI:** PI-1

## Problem

KubeZap's step execution model — the controller resolves an HTTP step in-memory and dispatches it to a dedicated `http-executor` pod via an internal RPC call — is architecturally lighter than workflow engines that spin up a Kubernetes Pod per step (the typical Argo Workflows execution model). This is currently an *architectural claim* embedded in the project's own positioning (README's "Why KubeZap" section and `docs/overview.md`'s "Simple by design" section, both added 2026-09-12) but has never been measured. We don't know the actual latency/overhead delta, whether it holds up under concurrent FlowRun load, or whether the gap is large enough to be worth calling out as a differentiator.

## Goal

Produce real, repeatable latency and resource-overhead numbers comparing KubeZap's per-step RPC execution path against a Pod-per-step workflow engine baseline, for a representative lightweight automation (webhook → 2-3 HTTP calls). Use the numbers to make an explicit call on whether "faster/lighter than Pod-per-step engines" is worth pursuing further (as an engineering focus and/or a stated differentiator) or whether the gap isn't meaningful enough to invest in.

## Success Metric

A written benchmark report exists (methodology, scenario definition, p50/p95 step latency, and resource overhead for both approaches) with an explicit go/no-go recommendation on pursuing this as a differentiator.

## Related Design Docs

- None yet — this is a research spike, not an implementation change. If the results lead to a performance-affecting design change, that gets its own design record at that point.
- Context: README's "Why KubeZap" section and `docs/overview.md`'s "Simple by design" / "Secure by construction" sections (added 2026-09-12) are the current unverified positioning this benchmark would validate or walk back.

## Candidate Stories

Groomed 2026-09-12 (`/groom-backlog`) — all three fully groomed, no blocking design record needed (this is a research spike: no CRD/controller/security-posture change, and the Argo Workflows comparison baseline is a one-off local measurement tool, not a shipped KubeZap dependency):

- [x] [STORY-013](../stories/STORY-013-benchmark-methodology.md) — Define benchmark methodology (scenario, baseline version, metrics, run parameters). Done — PR #189, merged.
- [x] [STORY-014](../stories/STORY-014-benchmark-harness.md) — Build and run the harness against both systems. Done — PR #195, merged.
- [ ] [STORY-015](../stories/STORY-015-benchmark-writeup.md) — Write up results and a pursue/don't-pursue recommendation. PR #199 open, awaiting merge — go/no-go: **pursue** (see `benchmarks/execution-latency/RESULTS.md`).

## Dependencies

- Depends on: none
- Blocks: none

## Notes

- Explicitly a research spike, not a committed feature — keep it small; don't over-scope into a build commitment.
- Raised 2026-09-12 during an open-source-positioning discussion (competitive differentiation against Pod-per-step workflow engines like Argo Workflows).
- Committed to PI-1 (2026-09-12, via `/plan-pi`) as a parallel track alongside `EPIC-001` — not blocked on EPIC-001 closing (see `planning/roadmap/pi-plan.md`'s Revisions).
- Groomed 2026-09-12 (`/groom-backlog`): all 3 stories fully groomed in one pass (unlike EPIC-003, nothing here needed a blocking design record first). Sequential dependency chain (013 → 014 → 015) — dispatch STORY-013 first.
