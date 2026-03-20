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

## 1. Testing — Targeted Coverage Gaps

> Ginkgo tests in `internal/controller/` or `internal/gateway/` unless otherwise noted.

- [ ] **T1 — Cron trigger integration test**: Create Trigger with `schedule: "*/1 * * * *"`, advance fake clock 65s (use `clock.FakeClock` from `k8s.io/utils/clock/testing`), assert exactly one FlowRun exists named `<trigger>-<scheduled-time>` and is `Succeeded`. File: `internal/controller/cron_scheduler_test.go` (extend existing).
- [ ] **T2 — HMAC auth reject/accept E2E**: Start a real webhook gateway HTTP server in test, send request with valid HMAC → assert 202 + FlowRun created; send with wrong signature → assert 401 + no FlowRun; send with missing header → assert 401. File: `internal/gateway/webhook/handler_test.go` (new table-driven cases).
- [ ] **T3 — CEL skip cascade test**: Flow with steps A → B → C where B has `when` that is false and C has `runAfter: [B]`. Assert: B=Skipped, C=Skipped (cascade), overall phase=Succeeded. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T4 — FlowRun GC maxSucceeded enforcement**: Create trigger with `flowRunGC.maxSucceeded: 3`. Create 5 Succeeded FlowRuns. Trigger reconcile. Assert only 3 remain (oldest 2 deleted). Assert a FlowRun annotated `kubezap.io/retain=true` is never deleted even when over limit. File: `internal/controller/gc_policy_test.go` (extend existing).
- [ ] **T5 — Cooldown window suppression**: Trigger with `maxInvocations: 2, window: 10s`. Fire 5 requests in sequence. Assert 2 FlowRuns created, 3 suppressed (metric `kubezap_webhook_rate_limited_total` incremented by 3). File: `internal/gateway/webhook/handler_test.go`.
- [ ] **T6 — FlowRun orphan recovery**: Create FlowRun in `Running` phase with finalizer set, no active execution context. Advance time past the orphan timeout (`--flowrun-ttl-failed` default). Assert controller transitions phase to `Failed` with reason `OrphanTimeout` and removes finalizer. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T7 — Step retry with exponential backoff**: Mock HTTP server that returns 503 for first 2 calls, 200 on 3rd. Step has `retryPolicy: {maxRetries: 3, backoffType: Exponential, initialDelay: 10ms, maxDelay: 100ms}`. Assert: step `attempts == 3`, step phase `Succeeded`, delay durations recorded in step status. File: `internal/controller/flowrun_controller_test.go`.
- [ ] **T8 — Transform step + result chaining**: Flow with `type: transform` step that maps `$(trigger.body.orderId)` to result `orderId`, followed by HTTP step using `$(steps.transform.results.orderId)` in URL. Assert the HTTP call URL contains the correct substituted value. Use `httptest.NewServer` for the target. File: `internal/controller/flowrun_controller_test.go`.

---

## 2. Deployment & Distribution

- [ ] OperatorHub submission PR _(PAUSED — owner request 2026-03-20; do not start until explicitly unblocked)_

---

## 3. Examples — Real-World (v0.4)

> Each example lives in `examples/<slug>/` with a `README.md` and all required manifests.
> Completed examples: order-router, kafka-enrichment, incident-escalation, github-autolabel, slack-router, nightly-export.

### Example 6 — Kubernetes Resource Event → ITSM Ticket

**External dependencies:**
- A running cluster where the example namespace has Pods that can be set to `Failed` phase (easily done with an invalid image)
- A ServiceNow developer instance **or** Jira Cloud **or** a mock HTTP server as ITSM stand-in (recommended for self-contained example)
- Credentials for the ITSM API stored in a Kubernetes Secret (not needed if using mock)

> **Blocked on:** `type: resource` Kubernetes resource-event trigger (listed in Future/Backlog). Write manifests and README assuming that feature is available; mark as `(requires kubernetes trigger type — not yet implemented)`.

**What it demonstrates:** Kubernetes resource-event trigger (watches for Pod phase=Failed), extracting pod name and namespace from the event, opening a ticket via HTTP, deduplication via pod UID as FlowRun name idempotency key.

**Implementation tasks:**
- [ ] Create manifests in `examples/k8s-pod-failure-ticket/`: KubernetesTrigger (placeholder spec), Flow (transform event → http ticket create), mock HTTP server deployment
- [ ] Create `examples/k8s-pod-failure-ticket/README.md`: applying manifests, causing a Pod failure with `kubectl run bad --image=does-not-exist`, verifying FlowRun created; note dependency on kubernetes trigger type
- [ ] Mark README as `(requires kubernetes trigger type — not yet implemented)` at the top

---

### Example 7 — Dead-Letter Queue Handler

**External dependencies:**
- A Kafka cluster accessible from within the cluster (Strimzi or in-cluster Kafka)
- Two Kafka topics: a DLQ topic (e.g., `orders.dlq`) and the original delivery topic (e.g., `orders`)
- A `kafka` Integration CRD pointing at the cluster
- A Kubernetes Secret with Kafka credentials if the cluster requires SASL

**What it demonstrates:** Kafka trigger on a DLQ topic, logging the failed message, attempting re-delivery via `type: publish` back to the original topic, conditional escalation step (fire webhook) if re-delivery fails, dedup key encodes partition + offset so replaying the DLQ is safe.

**Implementation tasks:**
- [ ] Create manifests in `examples/dlq-handler/`: Integration (kafka), Trigger (DLQ topic), Flow (log → re-publish → escalate-on-failure), mock HTTP server for escalation
- [ ] Create `examples/dlq-handler/README.md`: create Kafka topics (commands for Strimzi), applying manifests, producing a poison message to the DLQ, watching FlowRun, verifying message re-published to original topic, testing the escalation path

---

### Example 8 — Multi-Tenant Webhook Fan-Out

**External dependencies:**
- No external services required — example uses a mock HTTP server as the three tenant endpoints

**What it demonstrates:** Single inbound webhook triggers parallel execution of 3 steps (same `runAfter` set), per-tenant configuration extracted from Secrets using `$(secrets.<tenant-secret>.<key>)`, `failurePolicy: Continue` so a failure for one tenant does not block the others, per-step retry policies, FlowRun status shows all three outcomes independently.

**Implementation tasks:**
- [ ] Create manifests in `examples/multi-tenant-fanout/`: Trigger (no auth — note production should use HMAC/bearer), Flow (3 parallel http steps), 3 Secrets (placeholder values for tenant config), mock HTTP server deployment
- [ ] Create `examples/multi-tenant-fanout/README.md`: applying manifests, sending a single webhook, inspecting parallel step execution in FlowRun, simulating a 500 on one tenant to demonstrate `Continue` policy

---

### Example 9 — OIDC-Secured API Gateway Webhook

**External dependencies:**
- An OIDC provider — **Dex** (in-cluster, recommended for self-contained example), Keycloak, Okta, or any OIDC provider
- The JWKS endpoint must be reachable from within the cluster

**What it demonstrates:** OIDC/JWT authentication on a webhook trigger, JWKS background refresh (shared `jwk.Cache`), `requiredClaims` enforcement, extracting a claim value from the JWT payload in the Flow, routing based on the claim.

**Implementation tasks:**
- [ ] Create manifests in `examples/oidc-webhook/`: Dex deployment + config, Trigger (oidc auth with issuer + audience + requiredClaims), Flow (CEL branch on claim value), mock HTTP server for each route
- [ ] Create `examples/oidc-webhook/README.md`: Dex setup (Helm chart), obtaining a JWT via client credentials, calling the webhook with JWT, verifying FlowRun created, testing rejection with invalid token

---

## 4. Future / Backlog

- [ ] `docs/guides/amqp-setup.md` — write full AMQP setup guide (stub exists)
- [ ] `docs/guides/nats-setup.md` — write full NATS setup guide (stub exists)
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows (cross-namespace FlowRun)
- [ ] Kubernetes resource-event trigger type (`type: resource` — dynamic informers in controller; see `docs/architecture.md#kubernetes-resource-event-triggers` for design)
- [ ] `type: http` Integration — base URL + auth credentials (bearer/basic/apiKey) stored in Integration, referenced by Flow steps via `integrationRef`; add `HttpIntegrationSpec` to `api/v1alpha1/integration_types.go`; add `integrationRef` to `HTTPAction` in `flow_types.go`
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] Web UI for flow monitoring
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source

---

## 5. MockEndpoint Deprecation & Removal

> **Decision (2026-03-20):** Remove the `MockEndpoint` CRD entirely. Replace in all examples,
> tests, and docs with a lightweight third-party mock HTTP server deployed in-cluster.
> MockEndpoint solves a real problem but is not a KubeZap concern — operators should use
> purpose-built mocking tools.
>
> **Recommended replacement tool:** TBD — see `docs/tech-debt/pending-input-required.md`
> for the tool selection question (BACKLOG-PROMPT). Do not begin Phase 2 or later until
> that question is answered.
>
> **Ordering constraint:** Phases must be executed in order. Code removal (Phase 5) is last.
> Examples and tests must be updated before the CRD is deleted so the repo is never broken.

### Phase 0 — Design decision (BLOCKED on pending input)

- [ ] Choose replacement mock tool — see `docs/tech-debt/pending-input-required.md`; options: WireMock, Mockoon, or other. Decision gates all phases below.

### Phase 1 — Write replacement documentation

- [ ] Convert `docs/api/mock-endpoint.md` to `docs/guides/mocking-http-endpoints.md`: explain why MockEndpoint is removed, document the chosen tool's in-cluster deployment (Helm or raw YAML), show how to define stub responses, show how to inspect captured requests, cross-link to each example that uses it
- [ ] Add in-cluster `Deployment` + `Service` sample YAML for the chosen tool (as a reusable snippet referenced by examples and the guide)
- [ ] Update `docs/overview.md` CRD Overview table: remove `MockEndpoint` row; add note redirecting to `docs/guides/mocking-http-endpoints.md`
- [ ] Update `docs/architecture.md`: remove all MockEndpoint references; update the "Webhook gateway also serves `/mock/*` paths" note to reflect removal
- [ ] Update `docs/guides/troubleshooting.md`: replace "MockEndpoint not capturing requests" section with equivalent section for chosen tool

### Phase 2 — Update example manifests

- [ ] `config/samples/automation_v1alpha1_mockendpoint.yaml` — delete file; remove from `config/samples/kustomization.yaml` and OLM bundle alm-examples
- [ ] `examples/order-router/` — replace MockEndpoint resources with chosen-tool stub configs; update `kustomization.yaml` and `README.md`
- [ ] `examples/kafka-enrichment/` — replace `enterprise-sink`, `standard-sink`, `trial-sink`, `customer-profile` MockEndpoints with chosen-tool stub configs; update `kustomization.yaml` and `README.md`
- [ ] `examples/slack-router/` — replace MockEndpoint resources; update `kustomization.yaml` and `README.md`
- [ ] `examples/incident-escalation/` — audit for MockEndpoint usage; update if present
- [ ] `examples/nightly-export/` — audit for MockEndpoint usage; update if present

### Phase 3 — Update guides

- [ ] `docs/guides/getting-started.md` — replace all MockEndpoint steps with chosen-tool equivalent; update every `kubectl apply` command and expected output block
- [ ] All new examples (6–9) that reference MockEndpoints: replace with chosen tool before those examples are written

### Phase 4 — Update tests

- [ ] `internal/controller/mockendpoint_controller_test.go` — delete entire file
- [ ] `internal/controller/suite_test.go` (or equivalent) — remove MockEndpoint type registration if present
- [ ] `test/e2e/` — search for all MockEndpoint usage; replace with HTTP calls to chosen-tool stub server deployed in the test namespace; update `BeforeSuite` setup if the test suite relies on the webhook gateway's `/mock/*` serving
- [ ] `test/e2e/webhook_test.go` — update the E2E scenario (webhook → transform → http step → MockEndpoint → verify FlowRun Succeeded) to target the chosen tool instead
- [ ] Audit all test files: `grep -r "MockEndpoint\|mockendpoint\|mock-endpoint" --include="*.go"` — fix every hit

### Phase 5 — Remove code and CRD

> Do not begin until Phases 1–4 are complete and all tests pass.

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
