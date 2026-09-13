# EPIC-001: Open Source Release Readiness

**Status:** In Progress
**PI:** PI-1

## Problem

KubeZap is about to go public and be submitted to OperatorHub, but several release-readiness gaps remain: no community-health files (`CODE_OF_CONDUCT.md`, issue/PR templates), no decided public-facing website, docs that haven't had a public-facing polish pass, and no confirmed answer on whether the test suites actually catch regressions (motivated by a 2026-09-12 finding: `make test-e2e` silently ran 0 of 45 specs for an extended period before anyone noticed).

Some of this epic's scope is already done, from before this Epic existed as a tracked file: vulnerability/dependency management automation (Dependabot + `govulncheck`, PR #157), `SECURITY.md`, and a build/release automation review (fixed `http-executor` missing from the release pipeline, PR #157).

## Goal

Every item below closed, then the release-readiness rollup story (the last one) confirms the existing OperatorHub submission gates (spec-drift check, e2e green, code/doc review clean) are also satisfied, so the submission PR can actually open.

## Success Metric

- Public repo passes GitHub's own "community profile" checklist.
- `make test-e2e` has no undiagnosed silent failures (see STORY-003).
- The OperatorHub submission PR (tracked outside this Epic, in whatever replaces `docs/schedule.md` §1's old gate list) is unblocked by everything in this Epic's scope specifically.

## Related Design Docs

None yet specific to this epic — see `docs/design/README.md` for the technical decisions made along the way in PR #157 (dependency/toolchain bump) and PR #155 (executor NetworkPolicy fixes, found during the e2e validation pass that also motivated STORY-003 below).

## Candidate Stories

Already done (pre-dates this Epic file — see `git log` for PRs #155/#157, not re-tracked as Stories per the "no need to keep completed-item history" call):
- Vulnerability/dependency management automation (Dependabot, `govulncheck`, Go/Docker toolchain bump)
- `SECURITY.md` — vulnerability disclosure policy
- Build/release automation review (`http-executor` release-pipeline gap)

Open:
- [x] [STORY-001](../stories/STORY-001-docs-cleanup-pass.md) — Final public-facing docs cleanup pass
- [x] [STORY-002](../stories/STORY-002-contributing-docs-cleanup.md) — Clean up `docs/contributing.md`
- [x] [STORY-003](../stories/STORY-003-test-suite-value-review.md) — Test suite value review. Done — no PR (investigation/write-up), closed 2026-09-13. Unblocks STORY-007.
- [x] [STORY-004](../stories/STORY-004-github-pages-website.md) — Product/docs website via GitHub Pages (content scope decided 2026-09-12: rendered `docs/` only)
- [x] [STORY-005](../stories/STORY-005-community-health-files.md) — `CODE_OF_CONDUCT.md` + issue/PR templates
- [x] [STORY-006](../stories/STORY-006-support-channel-decision.md) — Support/community channel decision
- [ ] [STORY-007](../stories/STORY-007-final-release-validation.md) — Final release validation and OperatorHub submission (rollup gate — last). Blocker (STORY-024) merged; final scorecard re-check not yet re-run.
- [x] [STORY-016](../stories/STORY-016-docs-broken-links-cleanup.md) — Fix broken doc links/anchors surfaced by the MkDocs build. Done — PR #193, merged.
- [x] [STORY-023](../stories/STORY-023-outbound-tls-annotation-docs-fix.md) — Fix fictional outbound-TLS-annotation docs (3 documented Trigger annotations don't exist in code). Done — PR #211, merged.
- [x] [STORY-024](../stories/STORY-024-olm-csv-descriptors.md) — Fix OLM scorecard descriptor/resource gaps in the CSV, all 5 CRDs (found during STORY-007's validation pass; root cause was `PROJECT`-registration-gated CSV regeneration, not a bug). Done — PR #209, merged. Unblocks STORY-007.

## Dependencies

- Depends on: none
- Blocks: the OperatorHub submission PR (whatever tracks it going forward)

## Notes

All 7 stories were `Groomed` (2026-09-12 `/groom-backlog` session) — STORY-004's content-scope decision (rendered `docs/` only) and STORY-006's channel decision (GitHub Discussions) were made during that session. STORY-001/002/003/005/006 are independent of each other (disjoint footprints, see each story) and were `/plan-parallel`ed into two batches (`planning/checkpoints/checkpoint-2026-09-12/work-packages.md`). Batch 1 (STORY-001, 002, 005) dispatched via `/dispatch-work` and merged 2026-09-12 (PRs #181, #179, #180). STORY-003 is partially done (PR #174 satisfied its e2e-failures AC; brittle-test review + write-up still open). Batch 2 (STORY-004 → PR #184, STORY-006 → PR #183) merged 2026-09-12 — both have one remaining manual admin action outside code (enabling the Pages source and enabling Discussions respectively; see each story's Status). STORY-007 must be last — it depends on every other story in this Epic plus the pre-existing OperatorHub gate list.
