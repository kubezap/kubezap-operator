# STORY-003: Test suite value review

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (2026-09-13, findings write-up above — no PR yet, staged)
**Size:** L

## Description

Go through the test suites (unit, envtest, e2e) and confirm each is actually asserting something meaningful rather than exercising code for coverage's sake; look for brittle/tautological tests. Directly motivated by the 2026-09-12 finding that `make test-e2e` silently ran 0 of 45 specs for an extended period (webhook-admission-cert crash-loop in `BeforeSuite`, fixed in PR #155) with nobody noticing — a signal that test-suite *health* wasn't being watched closely, separate from whether the tests' *content* is any good.

**Progress (PR #174, 2026-09-12):** all 4 pre-existing e2e failures were root-caused and fixed — a `checkSSRF` bypass-scoping bug, a metrics-test ordering dependency, a redundant controller redeploy that intermittently wiped ad-hoc test-cluster patches (the actual root cause of the SSRF-related failures' flakiness), plus 4 more latent bugs (two wrong JSONPaths, a dead CRD field referenced in a test fixture, a real `dependenciesMet` controller bug) that were only reachable once the above fixes let Ginkgo run past earlier failures in the same `Ordered` containers. `make test-e2e` now runs 28/45 specs clean (14 pending on broker infra not available in CI, 3 skipped). AC #2 below is satisfied by that work. AC #1 and #3 are unaffected — still open.

## Acceptance Criteria

- [x] Every `Describe`/`It` in `internal/*/​*_test.go` reviewed for whether it would actually fail if the behavior it names regressed (spot-check, not necessarily 100% line-by-line) — done 2026-09-13, all 30 `internal/*/*_test.go` files spot-checked (see Findings below).
- [x] `test/e2e/*.go` reviewed for the same, plus specifically whether the 4 known-pre-existing e2e failures get root-caused or explicitly written off — done via PR #174, all root-caused and fixed (see Description).
- [x] A short findings write-up (brittle tests found, coverage gaps found, the e2e failures resolved or explicitly deferred with a filed follow-up) — see Findings below.

## Findings (2026-09-13, AC #1 spot-check)

Split into two passes: `internal/controller/*_test.go` (15 files) and everything else under `internal/*` (15 files: executor/http, gateway/{amqp,kafka,nats,redact,secretindex,webhook}, webhook).

**`internal/controller/` — no brittle or tautological tests found.** Close read on the correctness-critical files (`ssrf_test.go`, `executor_reconciler_test.go`, `flowrun_controller_test.go`, `trigger_controller_test.go`, `gc_policy_test.go`, `cron_scheduler_test.go`, `resource_watcher_test.go`), pattern scan on the rest. A few things a heuristic flagged turned out to be false positives on inspection (nested-closure assertions in `resource_watcher_test.go`, single-`Expect()` table-style tests in `substitute_vars_test.go`/`integration_controller_test.go` that are the idiomatic Ginkgo shape for pure-function/condition testing, not weak). No `Skip`/`PIt`/`XIt`/`FIt`/`FDescribe`/`PDescribe` anywhere — nothing silently excluded from the run. `executor_reconciler_test.go`'s NetworkPolicy test (exact `PodSelector`/`NamespaceSelector`/CIDR-exception assertions, not just "no error") is the strong end of the spectrum and worth treating as house style.

**Rest of `internal/*` — one real, security-relevant coverage gap.** `internal/gateway/webhook/handler.go`'s `authenticateRequest` implements 7 auth types (`hmac`, `bearer`, `apiKey`, `oidc`, `basic`, `ipAllowlist`, `headerEquals`); only 3 (`hmac`, `ipAllowlist`, `oidc`) have real accept/reject behavioral tests. **`bearer`, `apiKey`, `basic`, and `headerEquals` have zero functional coverage of their actual auth logic** — the only test touching them (`registry_test.go`'s `TestRegister_RedactsSecretsFromLogOutput`) checks secret redaction in logs, never that a valid credential passes or an invalid one is rejected. Filed as a follow-up (`planning/backlog/follow-ups.md`, 2026-09-13 entry) rather than fixed here, per this story's own footprint rule below. Everything else in scope — SSRF blocklist, OIDC validation, trust-boundary/proxy-CIDR checks, secret redaction, Kafka dedup/header handling, the WebhookGatewayConfig singleton-admission webhook — had genuine positive- and negative-path coverage on spot-check; no `t.Skip`/TODO/broker-gated tests found in any of the 15 files.

## File / Module Footprint

Read-only investigation, as planned — no code footprint. The one real gap found (webhook auth test coverage) was filed as a follow-up rather than fixed here, per this story's own scope rule: fixes found necessary get their own follow-up story via `/groom-backlog` rather than expanding this one's scope after the fact.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

This is a research-shaped story (like a `/backlog` RESEARCH item in the old process) — expect it to produce new candidate stories via `follow-ups.md` rather than close cleanly in one pass.
