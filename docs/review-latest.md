# Review: 2026-03-21

## Doc issues

- `docs/api/mock-endpoint.md` still exists as a full doc page after MockEndpoint CRD removal; added deprecation notice with redirect to `docs/guides/mocking-http-endpoints.md` (decision on whether to delete pending — see `docs/tech-debt/pending-input-required.md` Q1)
- `docs/architecture.md` §"Resource Trigger spec" still labelled "(planned)" but implementation is complete; updated to reflect current status and flagged production-readiness question (Q2)
- `docs/architecture.md` "Future Trigger Types" table incorrectly listed Kubernetes resource events, NATS, and AMQP as unimplemented; updated to reflect current state
- `docs/overview.md` had no link to `docs/contributing.md`; added Contributing section at page bottom
- `docs/api/integration.md` labels AMQP and NATS as "_(beta)_" but both are fully implemented; label meaning undefined — flagged for owner decision (not edited; needs clarity on what "beta" means)
- All other cross-references verified valid — no broken links found

## Code issues

- **BUG (High)** — `internal/controller/flow_controller.go` `validateFlowSpec()` missing `case "wait":` — Flows with `action.type: wait` fail admission even though the schema and runtime support the type fully. Scheduled for fix in §11.
- **Testing gap (Medium)** — No Ginkgo tests for `executeWaitStep` or wait step timeout/requeue behavior. Scheduled in §11.
- **Tech debt (Medium)** — CEL expression cache (`sync.Map`) in `flowrun_controller.go` has no eviction policy; unbounded memory growth in long-running operators with diverse `when` expressions. Decision pending (Q3).
- **Tech debt (Medium)** — Kafka producer pool (`kafkaProducers` map) has no TTL or health check; stale connections not detected until next publish attempt.
- **Tech debt (Low)** — HTTP Integration fetched from API server on every step execution; no per-reconcile caching.
- **Tech debt (Low)** — CEL environment init failure cached forever via `sync.Once`; silent degradation instead of controlled restart.
- **Cleanup (Low)** — Stale TODO comment in `trigger_controller.go` lines 48–53 (ResourceWatcher wiring already complete in `cmd/main.go`). Scheduled in §11.

## Schedule corrections

- Added new §11 "Bug Fixes — 2026-03-21 Review" with 6 actionable items
- No existing `[x]` items found to be unimplemented
- No existing `[ ]` items found to be already fully implemented
- `[x] Kubernetes resource-event trigger type` in §10 confirmed correct — code is implemented

## Decisions needed

- **Q1**: Should `docs/api/mock-endpoint.md` be deleted, converted to redirect stub, or retained as archived doc?
- **Q2**: Is `type: resource` trigger production-ready or experimental? (affects architecture.md and Example 6 README)
- **Q3**: Should the CEL expression cache in `flowrun_controller.go` get an eviction policy? (LRU / TTL / unbounded / remove cache)

Full decision context in `docs/tech-debt/pending-input-required.md`.

## Files changed

- `docs/tech-debt/pending-input-required.md` — created (new file, 3 decision questions)
- `docs/tech-debt/code-debt-2026-03-21.md` — created (new file, detailed code findings)
- `docs/schedule.md` — added §11 with 6 new backlog items
- `docs/api/mock-endpoint.md` — added deprecation notice with redirect
- `docs/architecture.md` — updated resource trigger status label + Future Trigger Types table
- `docs/overview.md` — added Contributing section with link to `docs/contributing.md`
- `docs/review-latest.md` — this file
