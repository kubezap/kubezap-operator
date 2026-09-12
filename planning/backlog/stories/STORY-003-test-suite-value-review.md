# STORY-003: Test suite value review

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Groomed — partially done
**Size:** L

## Description

Go through the test suites (unit, envtest, e2e) and confirm each is actually asserting something meaningful rather than exercising code for coverage's sake; look for brittle/tautological tests. Directly motivated by the 2026-09-12 finding that `make test-e2e` silently ran 0 of 45 specs for an extended period (webhook-admission-cert crash-loop in `BeforeSuite`, fixed in PR #155) with nobody noticing — a signal that test-suite *health* wasn't being watched closely, separate from whether the tests' *content* is any good.

**Progress (PR #174, 2026-09-12):** all 4 pre-existing e2e failures were root-caused and fixed — a `checkSSRF` bypass-scoping bug, a metrics-test ordering dependency, a redundant controller redeploy that intermittently wiped ad-hoc test-cluster patches (the actual root cause of the SSRF-related failures' flakiness), plus 4 more latent bugs (two wrong JSONPaths, a dead CRD field referenced in a test fixture, a real `dependenciesMet` controller bug) that were only reachable once the above fixes let Ginkgo run past earlier failures in the same `Ordered` containers. `make test-e2e` now runs 28/45 specs clean (14 pending on broker infra not available in CI, 3 skipped). AC #2 below is satisfied by that work. AC #1 and #3 are unaffected — still open.

## Acceptance Criteria

- [ ] Every `Describe`/`It` in `internal/*/​*_test.go` reviewed for whether it would actually fail if the behavior it names regressed (spot-check, not necessarily 100% line-by-line).
- [x] `test/e2e/*.go` reviewed for the same, plus specifically whether the 4 known-pre-existing e2e failures get root-caused or explicitly written off — done via PR #174, all root-caused and fixed (see Description).
- [ ] A short findings write-up (brittle tests found, coverage gaps found, the e2e failures resolved or explicitly deferred with a filed follow-up) — PR #174's own description/commit message covers the e2e-failures half of this; the brittle-test-review half (AC #1) still needs its own write-up once done.

## File / Module Footprint

Starts as a read-only investigation — no footprint until findings are in. Any fixes found necessary get their own follow-up story via `/groom-backlog` rather than expanding this one's scope after the fact.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

This is a research-shaped story (like a `/backlog` RESEARCH item in the old process) — expect it to produce new candidate stories via `follow-ups.md` rather than close cleanly in one pass.
