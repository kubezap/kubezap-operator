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
- [ ] [STORY-001](../stories/STORY-001-docs-cleanup-pass.md) — Final public-facing docs cleanup pass
- [ ] [STORY-002](../stories/STORY-002-contributing-docs-cleanup.md) — Clean up `docs/contributing.md`
- [ ] [STORY-003](../stories/STORY-003-test-suite-value-review.md) — Test suite value review
- [ ] [STORY-004](../stories/STORY-004-github-pages-website.md) — Product/docs website via GitHub Pages (blocked on content-scope decision)
- [ ] [STORY-005](../stories/STORY-005-community-health-files.md) — `CODE_OF_CONDUCT.md` + issue/PR templates
- [ ] [STORY-006](../stories/STORY-006-support-channel-decision.md) — Support/community channel decision
- [ ] [STORY-007](../stories/STORY-007-final-release-validation.md) — Final release validation and OperatorHub submission (rollup gate — last)

## Dependencies

- Depends on: none
- Blocks: the OperatorHub submission PR (whatever tracks it going forward)

## Notes

STORY-001/002/003/005/006 are independent of each other (disjoint footprints, see each story) and candidates for `/plan-parallel`. STORY-004 is explicitly not groomable yet (owner: "will need to lock down exactly what goes into the website content later"). STORY-007 must be last — it depends on every other story in this Epic plus the pre-existing OperatorHub gate list.
