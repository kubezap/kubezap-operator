# KubeZap Project Schedule

## How to Maintain This Schedule

- Mark `[x]` when a task is complete.
- Add new tasks at the bottom of each section (not inline).
- Update priorities or dependencies as the project evolves.
- Reference this file at the start of each Claude session to orient quickly — no need to re-read the full repo.

---

## Status Key

- `[x]` Done
- `[ ]` Pending

---

## Prioritization rationale

Items are ordered to minimize rework:

1. **Research first** — find breaking bugs and interference issues before writing tests or building features on top of them. Tests written against broken behavior must be rewritten after the fix.
2. **Tests before features** — tests are more stable when written against confirmed-correct behavior.
3. **MockEndpoint replacement (docs + existing examples) before new examples** — new examples 7-9 all use mock HTTP servers. Building them with MockEndpoints and then migrating to Mockoon is double work. Write Mockoon-based examples from day 1.
4. **`type: http` Integration before new examples** — completed examples embed credentials inline. Building examples 7-9 without `integrationRef` means updating all their manifests and READMEs again after the feature lands.
5. **MockEndpoint code removal last** — safe to delete only after all examples, guides, and tests are migrated.
6. **Example 6 (K8s ITSM) last among examples** — blocked on `type: resource` trigger (Future/Backlog). Other examples can proceed independently.

---

## 1. Research — Critical Bug Hunt (R2)

> **Do this first.** Critical bugs found here could change FlowRun phase transitions, retry
> behavior, or gateway semantics. Tests written before these fixes may test wrong behavior
> and need to be rewritten. Identify bugs now; fix before building further.

**Goal:** Read-only audit of the live codebase for critical or breaking issues. Focus on correctness bugs that could cause data loss, silent failures, or stuck resources in production.

- [x] `internal/controller/flowrun_controller.go`: scan for unhandled error paths, missing finalizer removal conditions, or incorrect phase transitions that could leave FlowRuns permanently stuck
- [x] `internal/controller/trigger_controller.go`: look for reconcile loops that could cause infinite requeuing or missed status updates
- [x] `internal/gateway/webhook/handler.go`: check request body handling edge cases — empty body, non-JSON body with `resultMappings`, body at the size limit boundary
- [x] `internal/gateway/kafka/watcher.go`, `amqp/watcher.go`, `nats/watcher.go`: check for goroutine leak scenarios — contexts not cancelled, subscriptions not cleaned up on watcher shutdown
- [x] `cmd/main.go`: verify leader election, metric registration, and scheme setup are correct for production use
- [x] Based on findings: add schedule items for any critical bugs; skip LOW/cosmetic issues (those belong in a general debt review)

### R2 Findings — Bug Fixes

- [ ] **BUG — `BodyTruncated` not set on mid-range bodies** (`internal/gateway/webhook/handler.go:281-284`): bodies between 4097 bytes and 4 MB are accepted with HTTP 202, but stored truncated to 4096 chars in `TriggerData.Body` with `BodyTruncated: false`. Users relying on `$(trigger.body)` cannot detect a partial body. Fix: set `bodyTruncated = true` when `len(bodyBytes) > 4096` before truncating `bodyString`. Also add coverage to T2 (body-at-size-limit boundary).

---

## 2. Research — Multi-Type Interference Audit (R1)

> **Do this before writing T1-T8.** Interference bugs in cron scheduling or FlowRun naming
> affect what T1 and T4 test directly. Fixing them after tests are written means test rewrites.

**Goal:** Find cases where having multiple trigger types or integration types active in the same namespace could interfere — shared resource conflicts, name collisions, owner-ref races.

**Known starting point:** `kubezap-gateway` SA/Role/RoleBinding is shared by kafka/amqp/nats integrations — see `docs/tech-debt/rbac-ownership-gaps.md#issue-3` for the ownership fix (2026-03-20). Audit whether similar sharing exists elsewhere.

- [x] Audit `reconcileKafkaGateway`, `reconcileAmqpGateway`, `reconcileNatsGateway`: verify all three produce the same `kubezap-gateway` Role rules and that concurrent reconciles of different integration types in the same namespace converge correctly
- [x] Audit webhook gateway: if both a webhook Trigger and a pubsub Trigger exist in the same namespace, does the webhook gateway Deployment lifecycle interfere with pubsub gateway Deployments? Check `trigger_controller.go` for shared-name risk between gateway types
- [x] Audit cron scheduler: if two Triggers with the same `schedule` string exist in the same namespace (or across namespaces), do cron entries conflict or double-fire? Check `cron_scheduler.go` entry keying
- [x] Audit FlowRun naming: check for name collision risk when multiple Triggers reference the same Flow — do cron (`<trigger>-<scheduled-time>`), webhook (`<trigger>-<timestamp>-<random>`) naming schemes create collision risk under concurrent load?
- [x] Based on findings: add schedule items for confirmed bugs; add test items (T9+) for collision/interference scenarios with no existing coverage

### R1 Findings — Confirmed Clean

- [x] **Shared RBAC convergence**: all three reconcilers produce identical `kubezap-gateway` Role rules (`triggers:get/list/watch`, `integrations:get`, `flowruns:create`). `CreateOrUpdate` + identical rule sets → concurrent reconciles always converge. **No bug.**
- [x] **Gateway name isolation**: webhook gateway uses `kubezap-webhook-gateway` prefix for all resources; broker gateways use `kubezap-gateway` (shared RBAC) + `kubezap-{type}-gateway-{name}` (per-Integration Deployments). Completely disjoint names — no lifecycle interference possible. **No bug.**
- [x] **Cron double-fire**: scheduler entries keyed by `"<namespace>/<name>"` — unique per Trigger. `Register()` removes existing entry before adding new. Two Triggers sharing a schedule string = two independent entries, fire independently. Cross-namespace same schedule = separate keys. **No bug.**

### R1 Findings — Bug Fixes

- [ ] **BUG — Webhook FlowRun random suffix too short** (`internal/gateway/webhook/handler.go`): `randomHex(4)` produces 4 hex chars (2 bytes = 65 536 values per second per Trigger). Birthday collision probability at 100 req/s on the same Trigger is ~7.5%/second. On collision the handler returns HTTP 202 with the existing FlowRun name — but that FlowRun contains the body/headers of the **first** request, not the colliding one. Silent data loss for the colliding request. Fix: change `randomHex(4)` → `randomHex(8)` (4 bytes → 1/4 294 967 296 collision rate).

---

## 3. Testing — Targeted Coverage Gaps (T1-T8)

> Write after R1 and R2 so tests are not written against behavior that is about to change.

- [ ] **T1 — Cron trigger integration test**: Create Trigger with `schedule: "*/1 * * * *"`, advance fake clock 65s (use `clock.FakeClock` from `k8s.io/utils/clock/testing`), assert exactly one FlowRun exists named `<trigger>-<scheduled-time>` and is `Succeeded`. File: `internal/controller/cron_scheduler_test.go` (extend existing).
- [ ] **T2 — HMAC auth reject/accept E2E**: Start a real webhook gateway HTTP server in test, send request with valid HMAC → assert 202 + FlowRun created; send with wrong signature → assert 401 + no FlowRun; send with missing header → assert 401. File: `internal/gateway/webhook/handler_test.go` (new table-driven cases).
- [ ] **T3 — CEL skip cascade test**: Flow with steps A → B → C where B has `when` that is false and C has `runAfter: [B]`. Assert: B=Skipped, C=Skipped (cascade), overall phase=Succeeded. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T4 — FlowRun GC maxSucceeded enforcement**: Create trigger with `flowRunGC.maxSucceeded: 3`. Create 5 Succeeded FlowRuns. Trigger reconcile. Assert only 3 remain (oldest 2 deleted). Assert a FlowRun annotated `kubezap.io/retain=true` is never deleted even when over limit. File: `internal/controller/gc_policy_test.go` (extend existing).
- [ ] **T5 — Cooldown window suppression**: Trigger with `maxInvocations: 2, window: 10s`. Fire 5 requests in sequence. Assert 2 FlowRuns created, 3 suppressed (metric `kubezap_webhook_rate_limited_total` incremented by 3). File: `internal/gateway/webhook/handler_test.go`.
- [ ] **T6 — FlowRun orphan recovery**: Create FlowRun in `Running` phase with finalizer set, no active execution context. Advance time past the orphan timeout (`--flowrun-ttl-failed` default). Assert controller transitions phase to `Failed` with reason `OrphanTimeout` and removes finalizer. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T7 — Step retry with exponential backoff**: Mock HTTP server that returns 503 for first 2 calls, 200 on 3rd. Step has `retryPolicy: {maxRetries: 3, backoffType: Exponential, initialDelay: 10ms, maxDelay: 100ms}`. Assert: step `attempts == 3`, step phase `Succeeded`, delay durations recorded in step status. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T8 — Transform step + result chaining**: Flow with `type: transform` step that maps `$(trigger.body.orderId)` to result `orderId`, followed by HTTP step using `$(steps.transform.results.orderId)` in URL. Assert the HTTP call URL contains the correct substituted value. Use `httptest.NewServer` for the target. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T9 — Webhook FlowRun name uniqueness under concurrent load**: Fire 500 concurrent webhook requests at the same Trigger using a goroutine pool. Assert all 500 FlowRuns are created with unique names (no silent `AlreadyExists` drops). Collect all created FlowRun names and assert zero duplicates. Requires the `randomHex(8)` fix (R1 bug above) to pass reliably. File: `internal/gateway/webhook/handler_test.go`.

---

## 4. MockEndpoint Replacement — Docs and Existing Examples

> **Complete Phases 0-3 before building Examples 7-9.** All pending examples use mock HTTP
> servers. Building them with MockEndpoints would require full rewrites during Phase 2-3.
> Phase 0 is already unblocked — tool selected (see note).

### Phase 0 — Tool selection

- [x] Choose replacement mock tool — **Mockoon** selected (see `docs/tech-debt/pending-input-required.md`, answered 2026-03-20)

### Phase 1 — Write replacement documentation

- [ ] Convert `docs/api/mock-endpoint.md` to `docs/guides/mocking-http-endpoints.md`: explain why MockEndpoint is removed, document Mockoon's in-cluster deployment (Docker image + Kubernetes `Deployment` + `Service`), show how to define stub responses, show how to inspect captured requests, cross-link to each example that uses it
- [ ] Add in-cluster `Deployment` + `Service` YAML for Mockoon as a reusable snippet referenced by examples and the guide
- [ ] Update `docs/overview.md` CRD Overview table: remove `MockEndpoint` row; add note redirecting to `docs/guides/mocking-http-endpoints.md`
- [ ] Update `docs/architecture.md`: remove all MockEndpoint references; update the "Webhook gateway also serves `/mock/*` paths" note to reflect removal
- [ ] Update `docs/guides/troubleshooting.md`: replace "MockEndpoint not capturing requests" section with Mockoon equivalent

### Phase 2 — Update existing example manifests

- [ ] `config/samples/automation_v1alpha1_mockendpoint.yaml` — delete file; remove from `config/samples/kustomization.yaml` and OLM bundle alm-examples
- [ ] `examples/order-router/` — replace MockEndpoint resources with Mockoon stub configs; update `kustomization.yaml` and `README.md`
- [ ] `examples/kafka-enrichment/` — replace `enterprise-sink`, `standard-sink`, `trial-sink`, `customer-profile` MockEndpoints with Mockoon stub configs; update `kustomization.yaml` and `README.md`
- [ ] `examples/slack-router/` — replace MockEndpoint resources with Mockoon; update `kustomization.yaml` and `README.md`
- [ ] `examples/incident-escalation/` — audit for MockEndpoint usage; update if present
- [ ] `examples/nightly-export/` — audit for MockEndpoint usage; update if present

### Phase 3 — Update existing guides

- [ ] `docs/guides/getting-started.md` — replace all MockEndpoint steps with Mockoon equivalent; update every `kubectl apply` command and expected output block

---

## 5. `type: http` Integration

> **Implement before Examples 7-9.** Without this, examples must embed credentials (Slack
> webhook URLs, API tokens) inline in Flow specs. Building examples that way means updating
> all manifests and READMEs again when `type: http` lands. Build it once, correctly.
>
> Completed examples (github-autolabel, slack-router, nightly-export) also have inline
> credentials — update them after this is implemented.

- [ ] Add `http` to the `IntegrationSpec.Type` enum in `api/v1alpha1/integration_types.go`
- [ ] Add `HttpIntegrationSpec` struct: `baseUrl`, `auth` (types: `bearer`, `basic`, `apiKey`, `secretUrl`), `defaultHeaders`, auth `secretRef` fields
- [ ] Add `integrationRef` field to `HTTPAction` in `api/v1alpha1/flow_types.go`; controller merges Integration auth headers before making the step request
- [ ] Add `get` on `integrations` to RBAC markers in `flowrun_controller.go` (secrets `get` already present); run `make manifests`
- [ ] Run `make generate && make manifests`
- [ ] Add sample CR `config/samples/automation_v1alpha1_integration_http.yaml`
- [ ] Update `docs/api/integration.md` with the new type, fields, and examples
- [ ] Update completed examples to use `integrationRef`: `examples/nightly-export/` (Slack notify), `examples/github-autolabel/` (GitHub API token), `examples/slack-router/` (step-level credentials)

---

## 6. Examples — Real-World (v0.4)

> Build after sections 4 and 5 so examples use Mockoon and `integrationRef` from day 1.
> Completed: order-router, kafka-enrichment, incident-escalation, github-autolabel, slack-router, nightly-export.

### Example 7 — Dead-Letter Queue Handler

**External dependencies:**
- A Kafka cluster accessible from within the cluster (Strimzi or in-cluster Kafka)
- Two Kafka topics: a DLQ topic (e.g., `orders.dlq`) and the original delivery topic (e.g., `orders`)
- A `kafka` Integration CRD pointing at the cluster
- A Kubernetes Secret with Kafka credentials if the cluster requires SASL

**What it demonstrates:** Kafka trigger on a DLQ topic, logging the failed message, attempting re-delivery via `type: publish` back to the original topic, conditional escalation step if re-delivery fails, dedup key encodes partition + offset so replaying the DLQ is safe.

**Implementation tasks:**
- [ ] Create manifests in `examples/dlq-handler/`: Integration (kafka), Trigger (DLQ topic), Flow (log → re-publish → escalate-on-failure), Mockoon deployment for escalation endpoint
- [ ] Create `examples/dlq-handler/README.md`: create Kafka topics (commands for Strimzi), applying manifests, producing a poison message to the DLQ, watching FlowRun, verifying message re-published, testing the escalation path

---

### Example 8 — Multi-Tenant Webhook Fan-Out

**External dependencies:**
- No external services required — example uses Mockoon as the three tenant endpoints

**What it demonstrates:** Single inbound webhook triggers parallel execution of 3 steps (same `runAfter` set), per-tenant credentials via `integrationRef` to `type: http` Integrations, `failurePolicy: Continue` so a failure for one tenant does not block others, per-step retry policies, FlowRun status shows all three outcomes independently.

**Implementation tasks:**
- [ ] Create manifests in `examples/multi-tenant-fanout/`: Trigger (no auth — note production should use HMAC/bearer), Flow (3 parallel http steps with `integrationRef`), 3 `type: http` Integrations, Mockoon deployment
- [ ] Create `examples/multi-tenant-fanout/README.md`: applying manifests, sending a single webhook, inspecting parallel step execution in FlowRun, simulating a 500 on one tenant to demonstrate `Continue` policy

---

### Example 9 — OIDC-Secured API Gateway Webhook

**External dependencies:**
- An OIDC provider — **Dex** (in-cluster, recommended for self-contained example), Keycloak, Okta, or any OIDC provider
- The JWKS endpoint must be reachable from within the cluster

**What it demonstrates:** OIDC/JWT authentication on a webhook trigger, JWKS background refresh (shared `jwk.Cache`), `requiredClaims` enforcement, extracting a claim value from the JWT payload, routing based on the claim.

**Implementation tasks:**
- [ ] Create manifests in `examples/oidc-webhook/`: Dex deployment + config, Trigger (oidc auth with issuer + audience + requiredClaims), Flow (CEL branch on claim value), Mockoon deployment for each route
- [ ] Create `examples/oidc-webhook/README.md`: Dex setup (Helm chart), obtaining a JWT via client credentials, calling the webhook with JWT, verifying FlowRun created, testing rejection with invalid token

---

### Example 6 — Kubernetes Resource Event → ITSM Ticket _(blocked)_

> **Blocked on:** `type: resource` Kubernetes resource-event trigger (see Future/Backlog).
> This example can be written as a placeholder to document the pattern, but the trigger
> type must be implemented before the example is functional. Do this last.

**External dependencies:**
- A running cluster where the example namespace has Pods that can be set to `Failed` phase
- A mock HTTP server (Mockoon) as ITSM stand-in for the self-contained version

**What it demonstrates:** Kubernetes resource-event trigger (watches for Pod phase=Failed), extracting pod name and namespace from the event, opening a ticket via HTTP, deduplication via pod UID as FlowRun name idempotency key.

**Implementation tasks:**
- [ ] Create manifests in `examples/k8s-pod-failure-ticket/`: KubernetesTrigger (placeholder spec), Flow (transform event → http ticket create), Mockoon deployment
- [ ] Create `examples/k8s-pod-failure-ticket/README.md`: applying manifests, causing Pod failure, verifying FlowRun created; mark as `(requires kubernetes trigger type — not yet implemented)`

---

## 7. MockEndpoint Removal — Tests and Code

> Complete only after all examples (section 6) are updated to use Mockoon.
> Phases 4-5 are separated from Phases 1-3 so new examples can be built clean first.

### Phase 4 — Update tests

- [ ] `internal/controller/mockendpoint_controller_test.go` — delete entire file
- [ ] `internal/controller/suite_test.go` (or equivalent) — remove MockEndpoint type registration if present
- [ ] `test/e2e/` — search for all MockEndpoint usage; replace with HTTP calls to Mockoon stub server deployed in the test namespace; update `BeforeSuite` setup if the test suite relies on the webhook gateway's `/mock/*` serving
- [ ] `test/e2e/webhook_test.go` — update the E2E scenario (webhook → transform → http step → MockEndpoint → verify FlowRun Succeeded) to target Mockoon instead
- [ ] Audit all test files: `grep -r "MockEndpoint\|mockendpoint\|mock-endpoint" --include="*.go"` — fix every hit

### Phase 5 — Remove code and CRD

> Do not begin until Phase 4 is complete and all tests pass.

- [ ] Delete `api/v1alpha1/mockendpoint_types.go`; run `make generate && make manifests`
- [ ] Delete `internal/controller/mockendpoint_controller.go`
- [ ] Remove MockEndpoint controller registration from `cmd/main.go` (scheme + `SetupWithManager` call)
- [ ] Remove `/mock/*` route handling from `cmd/webhook-gateway/main.go` and `internal/gateway/webhook/handler.go`
- [ ] Remove MockEndpoint RBAC markers from all controllers; run `make manifests`
- [ ] Delete `config/crd/bases/automation.kubezap.io_mockendpoints.yaml`
- [ ] Delete `bundle/manifests/automation.kubezap.io_mockendpoints.yaml` (if present); regenerate bundle with `make bundle`
- [ ] Update `charts/kubezap/crds/` to remove MockEndpoint CRD YAML
- [ ] Run `go build ./...`, `go vet ./...`, `go test ./... -count=1` — all must pass
- [ ] Update `docs/overview.md` CRD table: set MockEndpoint status to "Removed — see mocking guide"

---

## 8. Deployment & Distribution

- [ ] OperatorHub submission PR _(PAUSED — owner request 2026-03-20; do not start until explicitly unblocked)_

---

## 9. Future / Backlog

- [ ] `docs/guides/amqp-setup.md` — write full AMQP setup guide (stub exists)
- [ ] `docs/guides/nats-setup.md` — write full NATS setup guide (stub exists)
- [ ] Kubernetes resource-event trigger type (`type: resource` — dynamic informers in controller; see `docs/architecture.md#kubernetes-resource-event-triggers` for design; required for Example 6)
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows (cross-namespace FlowRun)
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] Web UI for flow monitoring
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source
