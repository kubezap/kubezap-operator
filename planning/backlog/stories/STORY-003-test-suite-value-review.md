# STORY-003: Test suite value review

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Backlog
**Size:** L

## Description

Go through the test suites (unit, envtest, e2e) and confirm each is actually asserting something meaningful rather than exercising code for coverage's sake; look for brittle/tautological tests. Directly motivated by the 2026-09-12 finding that `make test-e2e` silently ran 0 of 45 specs for an extended period (webhook-admission-cert crash-loop in `BeforeSuite`, fixed in PR #155) with nobody noticing — a signal that test-suite *health* wasn't being watched closely, separate from whether the tests' *content* is any good.

## Acceptance Criteria

- [ ] Every `Describe`/`It` in `internal/*/​*_test.go` reviewed for whether it would actually fail if the behavior it names regressed (spot-check, not necessarily 100% line-by-line).
- [ ] `test/e2e/*.go` reviewed for the same, plus specifically whether the 4 known-pre-existing e2e failures (see PR #155's `docs/design/2026-09-11-executor-egress-networkpolicy.md` addendum — SSRF-bypass-flag test-design tension, an unexplained missing `controller_runtime_reconcile_total` metric) get root-caused or explicitly written off.
- [ ] A short findings write-up (brittle tests found, coverage gaps found, the e2e failures resolved or explicitly deferred with a filed follow-up).

## File / Module Footprint

Starts as a read-only investigation — no footprint until findings are in. Any fixes found necessary get their own follow-up story via `/groom-backlog` rather than expanding this one's scope after the fact.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

This is a research-shaped story (like a `/backlog` RESEARCH item in the old process) — expect it to produce new candidate stories via `follow-ups.md` rather than close cleanly in one pass.
