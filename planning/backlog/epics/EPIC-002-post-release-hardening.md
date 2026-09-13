# EPIC-002: Post-Release Hardening & Feature Backlog

**Status:** In Progress — STORY-017/018/019/020/022 done, STORY-021 unblocked but not yet groomed, STORY-028 new (not yet footprinted)
**PI:** —

## Problem

A catch-all for small, well-scoped hardening/cleanup items that surface during other work — real gaps, but not big enough or urgent enough to justify their own epic, and not part of `EPIC-001`'s open-source-readiness scope. Two concrete items exist as of the 2026-09-12 checkpoint (see Candidate Stories); more will accumulate here over time as `/checkpoint` triages `planning/backlog/follow-ups.md`.

## Goal

Each item closed independently — there's no single "done" state for this epic the way there is for `EPIC-001`'s release gate or `EPIC-003`'s CRD. Review at each `/checkpoint` whether it's grown enough unrelated scope to warrant splitting a themed item out into its own epic.

## Success Metric

No fixed metric for the epic as a whole — each story carries its own (see story files).

## Related Design Docs

None yet. Individual stories may need one if their fix touches a design-process.md trigger (neither of the two current stories does — see each story's own scope).

## Candidate Stories

Groomed 2026-09-12 (checkpoint triage):

- [x] [STORY-017](../stories/STORY-017-goconst-cleanup.md) — `goconst` cleanup (59 real production-code findings surfaced by the `golangci-lint` v2.13 bump, tracked since the post-dependabot validation pass). Done — PR #198, merged.
- [x] [STORY-018](../stories/STORY-018-webhook-marker-fix.md) — Fix the `+kubebuilder:webhook` marker placement bug so the Trigger/FlowRun `ValidatingWebhookConfiguration` manifests actually get generated (found while building `EPIC-003`'s webhook). Done — PR #194, merged.
- [x] [STORY-019](../stories/STORY-019-flowrun-webhook-marker.md) — Add the missing `+kubebuilder:webhook` marker for FlowRun (`failurePolicy: Ignore`, owner-decided 2026-09-13). Done — PR #203, merged.
- [x] [STORY-020](../stories/STORY-020-flowrun-step-timing-precision-design.md) — Design record: sub-second step-timing visibility on `FlowRun` (real per-step latency is currently only visible via a Prometheus scrape, not `kubectl get flowrun`). Done — PR #204, merged.
- [ ] [STORY-021](../stories/STORY-021-flowrun-step-timing-precision-impl.md) — Implementation of STORY-020's decisions. Unblocked (STORY-020 Done); not yet groomed/footprinted.
- [x] [STORY-022](../stories/STORY-022-webhook-auth-test-coverage.md) — Add missing functional test coverage for `bearer`/`apiKey`/`basic`/`headerEquals` webhook auth types (found during STORY-003's test-suite spot-check — only 3 of 7 auth types had real accept/reject tests). Done — PR #210, merged.

Groomed 2026-09-13 (readiness-review triage):

- [ ] [STORY-028](../stories/STORY-028-container-image-and-sast-scanning.md) — Container image CVE scanning (Trivy) + static analysis (CodeQL) — closes the gap between Dependabot/`govulncheck` (declared dependencies only) and the actual built images/source. Backlog, not yet footprinted.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

This epic's name/scope was reserved in `backlog.md` since the 2026-09-12 PI-1 planning session but never populated until this checkpoint. Not committed to any PI yet (`PI: —`) — both current stories are small enough to dispatch opportunistically without a formal `/plan-pi` slot, but flag to the user if this epic accumulates enough scope to need one.
