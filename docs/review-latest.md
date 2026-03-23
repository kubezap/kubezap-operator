# Review: 2026-03-22

## Doc issues

- **CRITICAL** — `docs/api/plugin-contract.md`, `docs/guides/amqp-setup.md`, `docs/guides/nats-setup.md`, `docs/guides/troubleshooting.md`: reference old `type: pubsub` / `spec.pubsub.*` API. Breaks plugin developers following the spec. (Tracked: §16 P1 pubsub terminology pass)
- **HIGH** — `docs/guides/observability.md` metrics table lists `kubezap_webhook_requests_total` and `kubezap_webhook_request_body_bytes` — neither exists in `internal/metrics/metrics.go`. (Tracked: §16 P1)
- **HIGH** — `README.md`: features section omits AMQP, NATS, resource trigger, web dashboard, CLI. Stale "coming in v0.3" markers on Helm chart and OperatorHub sections. (Fixed: heading text updated; features text tracked §16 P1)
- **MEDIUM** — `docs/overview.md` line ~493 and `docs/api/flow.md`: claim XPath support in `resultMappings`. No XPath parser exists. (Tracked: §16 P1)
- **MEDIUM** — `docs/architecture.md`: component table missing AMQP and NATS gateways. (Tracked: §16 P2)
- **MEDIUM** — `docs/api/flowrun.md` line 115: resource trigger label was "_(planned)_". (Fixed: changed to "_(alpha)_")
- **MEDIUM** — `docs/architecture.md` line 321: "KEDA planned for v0.3" — stale. (Fixed: updated to current status)
- **MEDIUM** — `docs/guides/amqp-setup.md`, `docs/guides/nats-setup.md`: cross-reference `#pubsubtrigger` which no longer exists. (Tracked: §16 P1 pubsub pass)
- **LOW** — `docs/api/flow.md` line 158: `$(trigger.type)` docs list `pubsub` instead of `kafka, amqp, nats, resource`. (Tracked: §16 P1 pubsub pass)
- **LOW** — `docs/api/integration.md` line ~105: example uses `spec.type: pubsub` nested structure. (Tracked: §16 P1 pubsub pass)

## Code issues

- **HIGH** — `resource_watcher.go:95`: watcher goroutine context derived from `context.Background()` — not cancelled on manager shutdown. Goroutine leak, unclean teardown, E2E test hangs. Added to §16 P1; owner decision needed on severity.
- **HIGH** — Gateway Deployments (all four) missing `livenessProbe`/`readinessProbe`. Required by OperatorHub scorecard. Added to §16 P1.
- **MEDIUM** — `flowrun_controller.go` publish step: response body discarded on 4xx/5xx — publish failures cannot be debugged from FlowRun status. Added to §16 P2.
- **MEDIUM** — `flowrun_controller.go` ~line 402: `goto allStepsDone` for control flow. Style violation; replace with helper function. Added to §16 P2.
- **MEDIUM** — Missing `kubezap_when_expression_errors_total` metric for CEL `when` evaluation failures. Added to §16 P2.
- **MEDIUM** — No E2E tests for `publish` → Kafka Integration path. Added to §16 P2.
- **LOW** — `integration_controller.go:109-180`: three near-identical blocks for kafka/amqp/nats gateway condition. Extract into helper.
- **LOW** — HTTP response body silently truncated at 64KB with no indicator in step results.

## Schedule corrections

- `docs/api/flowrun.md`: resource trigger label `_(planned)_` → `_(alpha)_`
- `docs/architecture.md`: KEDA statement updated from "planned for v0.3" to current status
- `README.md`: Helm chart and OperatorHub heading markers updated
- 9 new items added to §16 P1/P2 from code review findings

## Decisions needed

- **Resource watcher context (P0 vs P1?)** — goroutine lifecycle bug, affects clean shutdown and E2E stability. Details in `docs/tech-debt/pending-input-required.md`.
- **Gateway health probes (P1 vs P2?)** — required for OperatorHub scorecard. Mechanical fix. Details in `docs/tech-debt/pending-input-required.md`.

## Files changed

- `docs/api/flowrun.md` — fix resource trigger label
- `docs/architecture.md` — fix KEDA statement
- `README.md` — fix stale v0.3 heading markers
- `docs/tech-debt/code-debt-2026-03-21.md` — append 4 new findings from 2026-03-22 review
- `docs/tech-debt/pending-input-required.md` — append 2026-03-22 decisions section
- `docs/schedule.md` — add 9 new §16 items from code review
- `docs/review-latest.md` — this file

---

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
- **Tech debt (Medium)** — CEL expression cache (`sync.Map`) in `flowrun_controller.go` has no eviction policy. Decision resolved (Q3): cache stays unbounded; `--disable-cel-cache` flag added as escape hatch. See `docs/tech-debt/pending-input-required.md` for full trade-off analysis.
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

- **Q1 — RESOLVED**: `docs/api/mock-endpoint.md` deleted.
- **Q2 — RESOLVED**: `type: resource` is **alpha**. Four bugs found (naive pluralization, FlowRun name collision, no retry on sync failure, no cooldown). Scheduled in §11. CLAUDE.md updated to label it "(alpha)".
- **Q3 — open**: See explanation below.

Full context in `docs/tech-debt/pending-input-required.md`.

## Files changed

- `docs/tech-debt/pending-input-required.md` — created (Q2 resolved inline; Q3 open)
- `docs/tech-debt/code-debt-2026-03-21.md` — created (detailed code findings)
- `docs/schedule.md` — added §11 with 10 new backlog items (6 original + 4 resource watcher bugs)
- `docs/api/mock-endpoint.md` — deleted
- `docs/architecture.md` — resource trigger status + Future Trigger Types table corrected
- `docs/overview.md` — Contributing section added
- `CLAUDE.md` — removed MockEndpoint references, removed stale Status column from Core CRDs table, updated Trigger Types list, updated Plugin model built-in types
- `docs/review-latest.md` — this file
