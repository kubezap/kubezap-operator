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

## 1. Foundation (v0.1 — MVP)

### Scaffolding & CRD Types
- [x] Kubebuilder v4 skeleton with Operator SDK plugins
- [x] `Trigger` CRD type (webhook/cron/pubsub spec, status, cooldown)
- [x] Basic Trigger reconciler (marks enabled Triggers as Accepted)
- [x] CI/CD: test, lint, e2e pipelines
- [x] Devcontainer, distroless Dockerfile, OLM bundle scaffolding
- [x] `Flow` CRD Go types in `api/v1alpha1/flow_types.go`
- [x] `FlowRun` CRD Go types in `api/v1alpha1/flowrun_types.go`
- [x] `Integration` CRD Go types in `api/v1alpha1/integration_types.go`
- [x] `MockEndpoint` CRD Go types in `api/v1alpha1/mockendpoint_types.go`
- [x] Run `make generate && make manifests` after each CRD type addition
- [x] Sample CRs in `config/samples/` for each new CRD
- [x] Add `CronTrigger` sub-spec to `trigger_types.go` (`schedule` string)
- [x] Add `PubSubTrigger` sub-spec to `trigger_types.go` (`type`, `integrationRef`, `topic`, `consumerGroup`)
- [x] Add `spec.webhook.auth` stub field (`WebhookAuth`) to `WebhookTrigger` in `trigger_types.go`
- [x] Add `spec.maxFlowRuns` to `TriggerSpec` for FlowRun GC cap
- [x] Run `make generate && make manifests` after Trigger type additions

### Webhook Gateway
- [x] HTTP server skeleton in `cmd/webhook-gateway/main.go`
- [x] Dynamic route registration from Trigger CRDs (watch + reconcile)
- [x] Route deregistration on Trigger delete/disable
- [x] FlowRun creation on incoming webhook request
- [x] FlowRun naming: `<trigger>-<timestamp>-<random>`
- [x] HMAC authentication support
- [x] Bearer token authentication support
- [x] OIDC/JWT authentication support
- [x] API-key header and IP allowlist authentication support
- [x] `/mock/*` path support for MockEndpoint CRDs
- [x] Structured JSON access logs (source IP in logs only, not Prometheus labels)
- [x] HPA configuration for webhook gateway Deployment
- [x] Controller manages webhook gateway Deployment lifecycle (one per namespace)
- [x] **Gateway ServiceAccount + Role + RoleBinding** created by controller alongside Deployment (deploy blocker — see `docs/architecture.md#gateway-serviceaccount-and-rbac`)
- [x] **Webhook gateway TLS termination** — server-side TLS: cert + key via user-provided Secret (cert-manager compatible). Operator mounts Secret as volume, configures HTTP server with `tls.Config`. Transport-layer concern, independent of auth. Configured via `kubezap.io/webhook-tls-secret` Namespace annotation. _(done 2026-03-18)_
- [x] **Webhook gateway mTLS auth** — inbound client cert verification. Requires server TLS (above) to be enabled first. CA cert configured via `kubezap.io/webhook-mtls-ca-secret` Namespace annotation. Sets `tls.Config.ClientAuth = tls.RequireAndVerifyClientCert` with the user-provided CA. _(done 2026-03-18)_

### Cron Trigger
- [x] Cron scheduler implementation in controller (`robfig/cron v3`)
- [x] FlowRun creation on schedule fire: `<trigger>-<scheduled-time>` naming
- [x] Cooldown enforcement for cron triggers

### Controller: FlowRun Execution
- [x] `FlowRun` reconciler in `internal/controller/flowrun_controller.go`
- [x] Fetch referenced Flow and resolve steps in dependency order
- [x] Execute HTTP action steps
- [x] Step result passing (`$(steps.<name>.results.<key>)` and `$(trigger.*)` substitution — prompt 12); CEL evaluation pending
- [x] FlowRun status conditions (Running, Succeeded, Failed)
- [x] FlowRun GC: `spec.ttlAfterFinished`, operator flags `--flowrun-ttl-succeeded` / `--flowrun-ttl-failed`
- [x] `kubezap.io/retain=true` annotation exempts FlowRun from GC

---

## 2. Flow Engine (v0.2)

### Flow Reconciler
- [x] `Flow` reconciler validates spec and sets Ready condition
- [x] **CEL `when` expression evaluation** — use `google/cel-go`; variables: `trigger.*`, `steps.<name>.status`, `steps.<name>.results.*`; see `docs/api/flow.md#conditions-and-cel`
- [x] **`Skipped` step phase** — when `when` is false; cascade skip downstream when entire `runAfter` set is skipped; see `docs/api/flow.md#skipped-steps-and-dependency-cascading`
- [x] Step input/output data passing between steps (`$(steps.<name>.results.<key>)` substitution)
- [x] Data transformation step type (`type: transform`)
- [x] Retry policies with exponential backoff per step
- [x] Flow-level and per-step timeout enforcement

### Integration CRD & Kafka Gateway
- [x] `Integration` reconciler in `internal/controller/integration_controller.go`
- [x] Kafka gateway skeleton in `cmd/kafka-gateway/main.go`
- [x] Dynamic topic subscription from Trigger CRDs (sarama ConsumerGroup, TLS/SASL from Integration spec)
- [x] FlowRun creation per Kafka message: `<trigger>-p<partition>-offset-<offset>` (dedup key)
- [x] KEDA ScaledObject for Kafka gateway (partition-bounded scaling; graceful no-op if KEDA absent)
- [x] Controller manages Kafka gateway Deployment lifecycle (one per namespace × Kafka cluster)
- [x] AMQP gateway skeleton in `cmd/amqp-gateway/main.go` (`type: amqp`, versions 0-9-1 and 1.0)
  - [x] Add `amqp` to `PubSubTrigger.Type` enum in `trigger_types.go`; run `make generate && make manifests`
  - [x] Add `github.com/rabbitmq/amqp091-go` and `github.com/Azure/go-amqp` dependencies via `go get`
  - [x] Implement `internal/gateway/amqp/watcher.go` — polls/watches Trigger CRDs for `pubsub.type=amqp`, manages channel subscriptions (mirrors kafka/watcher.go pattern)
  - [x] Implement `internal/gateway/amqp/handler.go` — converts AMQP deliveries into FlowRun CRDs; dedup key `<trigger>-<queue>-<delivery-tag>`
  - [x] Implement `cmd/amqp-gateway/main.go` binary entry point (mirrors cmd/kafka-gateway/main.go)
  - [x] Add `Dockerfile.amqp-gateway` (mirrors Dockerfile.kafka-gateway)
  - [x] Extend `integration_controller.go` to handle `type: amqp` — create/update AMQP gateway Deployment (one per namespace × broker URL)
  - [x] Add sample CR `config/samples/automation_v1alpha1_integration_amqp.yaml`
  - [x] Add sample Trigger CR `config/samples/automation_v1alpha1_trigger_amqp.yaml`
  - [x] Write Ginkgo unit tests in `internal/gateway/amqp/` and `internal/controller/integration_controller_test.go` (amqp cases)
- [x] NATS gateway skeleton in `cmd/nats-gateway/main.go` (`type: nats`, Core + JetStream)
  - [x] Add `nats` to `PubSubTrigger.Type` enum in `trigger_types.go`; run `make generate && make manifests`
  - [x] Add `github.com/nats-io/nats.go` dependency via `go get`
  - [x] Implement `internal/gateway/nats/watcher.go` — watches Trigger CRDs for `pubsub.type=nats`, manages Core subscriptions and JetStream durable consumers
  - [x] Implement `internal/gateway/nats/handler.go` — converts NATS messages into FlowRun CRDs; JetStream dedup key `<trigger>-seq-<sequence>`, Core key `<trigger>-<timestamp>-<random>`
  - [x] Implement `cmd/nats-gateway/main.go` binary entry point (mirrors cmd/kafka-gateway/main.go)
  - [x] Add `Dockerfile.nats-gateway` (mirrors Dockerfile.kafka-gateway)
  - [x] Extend `integration_controller.go` to handle `type: nats` — create/update NATS gateway Deployment (one per namespace × NATS cluster)
  - [x] Add sample CR `config/samples/automation_v1alpha1_integration_nats.yaml`
  - [x] Add sample Trigger CR `config/samples/automation_v1alpha1_trigger_nats.yaml`
  - [x] Write Ginkgo unit tests in `internal/gateway/nats/` and `internal/controller/integration_controller_test.go` (nats cases)
- [x] `type: publish` step action — controller calls plugin `/publish` endpoint

### MockEndpoint CRD
- [x] MockEndpoint reconciler — registers routes on webhook gateway
- [x] Captured request storage in CRD status (gateway writes directly to MockEndpoint status via k8sClient)

---

## MVP Demo Milestone

These items are needed to demonstrate a working end-to-end flow to stakeholders.
Target: webhook → transform → conditional mock notify with two branches.

- [x] **Gateway ServiceAccount + Role + RoleBinding** — controller must create these alongside the webhook gateway Deployment. Required for the gateway to create FlowRuns and write MockEndpoint status. See `docs/architecture.md#gateway-serviceaccount-and-rbac` for the required permissions.
- [x] **CEL `when` evaluation** — required for conditional step branching (see Flow Reconciler section above)
- [x] **`Skipped` step phase** — required for `when` to be observable (see Flow Reconciler section above)
- [x] **Demo sample CRs** — `config/samples/demo/` — the order-router scenario from `docs/guides/getting-started.md`; must `kubectl apply` cleanly and produce a working FlowRun
- [x] **`docs/guides/getting-started.md`** complete and validated against actual behavior ✅

## Demo Scenarios

Additional demonstration scenarios targeting acquisition/enterprise stakeholders.

### Demo 1 — Kafka Event Enrichment Pipeline
- [x] Sample CRs in `config/samples/demo/kafka-enrichment/` (Integration, Trigger, Flow, MockEndpoints)
- [x] Guide at `docs/guides/kafka-enrichment.md` (setup, produce messages, inspect FlowRuns, retry demo)
- [x] `type: publish` PublishAction documented in `docs/api/flow.md`

### Demo 3 — Incident Response Escalation
- [x] Design wait/requeue primitive (delayed step re-evaluation without blocking)
- [x] Sample CRs in `config/samples/demo/incident-escalation/`
- [x] Guide at `docs/guides/incident-escalation.md`

### Demo Validation
- [x] Manual walkthrough checklist for Demo 1 (Kafka Event Enrichment Pipeline) — step-by-step kubectl commands to apply CRs, produce a Kafka message, inspect FlowRun, verify results
- [x] Manual walkthrough checklist for Demo 3 (Incident Response Escalation) — step-by-step kubectl commands to apply CRs, send alert webhook, observe Waiting phase, verify escalation step skipped

---

## Demo Scenarios — Real-World (v0.4)

> These demos target acquisition/enterprise stakeholders with real integration patterns.
> Each requires: sample CRs in `config/samples/demo/<slug>/`, a step-by-step guide in `docs/guides/<slug>.md`, and a walkthrough checklist entry in `docs/guides/demo-walkthrough.md`.

### Demo 4 — GitHub Webhook → Auto-Label PR

**External dependencies:**
- A GitHub repository with admin access (to configure webhooks and create a GitHub App or Personal Access Token with `pull_requests: write` scope)
- The KubeZap webhook gateway exposed externally (ngrok, LoadBalancer, or Ingress) — GitHub cannot reach an in-cluster-only endpoint
- A Kubernetes Secret containing the GitHub webhook secret (for HMAC) and the GitHub API token (for the label call)

**What it demonstrates:** HMAC-signed webhook auth, `$(trigger.headers.*)` access, GitHub API integration with bearer token, `resultMappings` to extract PR number and repo from payload, conditional labeling logic via CEL.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/github-autolabel/`: Trigger (hmac auth), Flow (transform → http label call), Secrets placeholder comments
- [ ] Create guide at `docs/guides/github-autolabel.md`: GitHub webhook setup, ngrok/Ingress exposure, secret creation, applying CRs, sending a test PR event, verifying label applied
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

### Demo 5 — Slack Slash Command Router

**External dependencies:**
- A Slack workspace with permission to create a Slash Command app (free tier sufficient)
- The KubeZap webhook gateway exposed externally — Slack POSTs to a public URL
- The Slack signing secret (for HMAC verification of `X-Slack-Signature`) stored in a Kubernetes Secret
- Optional: a Slack incoming webhook URL for posting responses

**What it demonstrates:** `application/x-www-form-urlencoded` payload parsing, IP allowlist (Slack's published IP ranges), HMAC signature validation using Slack's signing algorithm, multi-branch CEL routing based on command text, fire-and-forget response pattern.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/slack-router/`: Trigger (hmac + ip allowlist), Flow (transform → branch A / branch B / fallback), MockEndpoints for each branch
- [ ] Create guide at `docs/guides/slack-router.md`: Slack app creation, slash command config, applying CRs, sending `/kubezap <command>` from Slack, inspecting FlowRun + MockEndpoint
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

### Demo 6 — Nightly Database Export + S3 Upload

**External dependencies:**
- An internal or mock export API (can use MockEndpoint as the export source in the demo)
- An S3-compatible bucket (AWS S3 or MinIO in-cluster for local testing); MinIO is preferred for the sample CRs so the demo is self-contained
- AWS credentials or MinIO access key/secret stored in Kubernetes Secrets
- Optional: a Slack incoming webhook for the summary notification step

**What it demonstrates:** Cron trigger (timezone-aware), chaining step results across 3 steps (export → upload → notify), `retryPolicy` on the upload step, secrets for AWS/MinIO credentials, `$(trigger.scheduledTime)` in the export URL, `failurePolicy: Continue` so the summary posts even on partial failure.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/nightly-export/`: CronTrigger, Flow (3 steps), Integration or Secrets placeholders, MinIO Deployment + Service (for local testing)
- [ ] Create guide at `docs/guides/nightly-export.md`: MinIO setup (in-cluster option), secret creation, applying CRs, manually triggering via FlowRun, verifying S3 object created, checking Slack summary
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

### Demo 7 — Kubernetes Resource Event → ITSM Ticket

**External dependencies:**
- A running cluster where the demo namespace has Pods that can be set to `Failed` phase (easily done with an invalid image)
- A ServiceNow developer instance (free at developer.servicenow.com) **or** a Jira Cloud instance **or** a MockEndpoint as the ITSM stand-in (recommended for self-contained demo)
- Credentials for the ITSM API stored in a Kubernetes Secret (not needed if using MockEndpoint)

> **Note:** This demo depends on the `kubernetes` resource-event trigger type, which is in the Future/Backlog section of the schedule. The demo CRs and guide should be written assuming that feature is available, and marked as `(requires kubernetes trigger type)` so they can be activated once implemented.

**What it demonstrates:** Kubernetes resource-event trigger (watches for Pod phase=Failed), extracting pod name and namespace from the event, opening a ticket via HTTP, deduplication (same pod failure should not open duplicate tickets — use pod UID as idempotency key in FlowRun name).

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/k8s-pod-failure-ticket/`: KubernetesTrigger (placeholder spec), Flow (transform event → http ticket create), MockEndpoint as ITSM
- [ ] Create guide at `docs/guides/k8s-pod-failure-ticket.md`: applying CRs, causing a Pod failure with `kubectl run bad --image=does-not-exist`, verifying FlowRun created, inspecting MockEndpoint capture; note dependency on kubernetes trigger type
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`
- [ ] Mark guide as `(requires kubernetes trigger type — not yet implemented)` at the top

---

### Demo 8 — Dead-Letter Queue Handler

**External dependencies:**
- A Kafka cluster accessible from within the cluster (same as Demo 1 — Strimzi or in-cluster Kafka)
- Two Kafka topics: a DLQ topic (e.g., `orders.dlq`) and the original delivery topic (e.g., `orders`)
- A `kafka` Integration CRD pointing at the cluster
- A Kubernetes Secret with Kafka credentials if the cluster requires SASL

**What it demonstrates:** Kafka trigger on a DLQ topic, logging the failed message to a MockEndpoint, attempting re-delivery via `type: publish` back to the original topic, conditional escalation step (fire webhook) if re-delivery fails, dedup key encodes partition + offset so replaying the DLQ is safe.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/dlq-handler/`: Integration (kafka), Trigger (DLQ topic), Flow (log → re-publish → escalate-on-failure), MockEndpoint for escalation
- [ ] Create guide at `docs/guides/dlq-handler.md`: create Kafka topics (commands for Strimzi), applying CRs, producing a poison message to the DLQ, watching FlowRun, verifying message re-published to original topic, testing the escalation path
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

### Demo 9 — Multi-Tenant Webhook Fan-Out

**External dependencies:**
- No external services required — demo uses MockEndpoints as the three tenant endpoints
- Optional: real per-tenant API URLs substituted for MockEndpoints in the production adaptation section of the guide

**What it demonstrates:** Single inbound webhook triggers parallel execution of 3 steps (same `runAfter` set), per-tenant configuration extracted from Secrets using `$(secrets.<tenant-secret>.<key>)`, `failurePolicy: Continue` so a failure for one tenant does not block the others, per-step retry policies, FlowRun status shows all three outcomes independently.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/multi-tenant-fanout/`: Trigger (no auth — add note that production should use HMAC/bearer), Flow (3 parallel http steps), 3 MockEndpoints, 3 Secrets (placeholder values for tenant config)
- [ ] Create guide at `docs/guides/multi-tenant-fanout.md`: applying CRs, sending a single webhook, inspecting parallel step execution in FlowRun, patching one MockEndpoint to return 500 to demonstrate `Continue` policy, verifying other tenants still succeed
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

### Demo 10 — OIDC-Secured API Gateway Webhook

**External dependencies:**
- An OIDC provider. Options (in order of ease for local testing):
  - **Dex** (in-cluster, no external dependency — recommended for sample CRs)
  - **Keycloak** (in-cluster via Helm)
  - **Okta developer account** (free, cloud-hosted)
  - Any OIDC-compliant provider
- A client that can obtain a JWT from the provider (a simple Go/Python script or `curl` with client credentials flow)
- The JWKS endpoint of the provider must be reachable from within the cluster

**What it demonstrates:** OIDC/JWT authentication on a webhook trigger, JWKS background refresh (the shared `jwk.Cache`), `requiredClaims` enforcement (e.g., `roles: kubezap-caller`), extracting a claim value from the JWT payload in the Flow, routing based on the claim.

**Implementation tasks:**
- [ ] Create sample CRs in `config/samples/demo/oidc-webhook/`: Dex deployment + config (in-cluster OIDC provider), Trigger (oidc auth with issuer + audience + requiredClaims), Flow (CEL branch on claim value), MockEndpoints for each route
- [ ] Create guide at `docs/guides/oidc-webhook.md`: Dex setup (apply Helm chart, configure client), obtaining a JWT via client credentials curl command, applying CRs, calling the webhook with the JWT in Authorization header, verifying FlowRun created, testing rejection with an invalid token
- [ ] Add walkthrough checklist to `docs/guides/demo-walkthrough.md`

---

## 6b. Additional Tests (targeted coverage gaps)

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

## 3. Plugin System

- [x] Plugin contract documented in `docs/api/plugin-contract.md` (subscriber + publisher roles, dedup keys, observability, security)
- [x] Operator creates plugin Deployment for `type: plugin` Integrations
- [x] Namespace-scoped RBAC granted to plugin Deployment (SA + Role + RoleBinding auto-created by controller)
- [x] Env injection: `KUBEZAP_NAMESPACE`, `KUBEZAP_INTEGRATION_NAME`, `KUBEZAP_PUBLISHER_PORT`, `KUBEZAP_LOG_LEVEL`
- [x] Secret injection via `spec.plugin.secretRefs` + `envVarMappings`
- [x] Readiness probe: `GET /healthz` → 200
- [x] Controller routes `type: publish` step calls to plugin `/publish` endpoint
- [x] Plugin trust model documented as a security consideration (see `docs/api/plugin-contract.md#security-considerations`)

---

## 4. Observability

- [x] Prometheus metrics: trigger firings, FlowRun durations, step outcomes
- [x] OpenTelemetry traces for FlowRun execution and step calls (OTLP gRPC exporter, W3C traceparent propagation)
- [x] Structured JSON access logs on webhook gateway (source IP, path, status, duration)
- [x] Source IP cardinality guard: `/24`-bucketed `source_range` on `ip_blocked` metric only
- [x] Observability guide updated in `docs/guides/observability.md`
- [x] ServiceMonitor usage documented (not auto-created by operator)

---

## 5. Multi-Namespace & RBAC

- [x] `WATCH_NAMESPACES` env var support (AllNamespaces / MultiNamespace / SingleNamespace / OwnNamespace)
- [x] OwnNamespace/SingleNamespace modes use `Role` (not `ClusterRole`)
- [x] All four OLM install modes supported in CSV bundle

---

## 6. Testing

- [x] Ginkgo unit tests for Flow reconciler
- [x] Ginkgo unit tests for FlowRun reconciler
- [x] Ginkgo unit tests for Integration reconciler
- [x] Ginkgo unit tests for MockEndpoint reconciler
- [x] E2E tests: webhook trigger → FlowRun creation → step execution
- [x] E2E tests: cron trigger fires on schedule
- [x] E2E tests: Kafka trigger → FlowRun with dedup key (skipped unless `KAFKA_BOOTSTRAP_SERVERS` set)
- [x] E2E tests: FlowRun GC respects TTL and retain annotation

---

## 7. Deployment & Distribution (v0.3)

- [x] Helm chart in `charts/kubezap/`
  - [x] Scaffold chart skeleton: `charts/kubezap/Chart.yaml`, `charts/kubezap/values.yaml`, `charts/kubezap/templates/`
  - [x] Controller Deployment template with `WATCH_NAMESPACES`, `--leader-elect`, image, resources, securityContext (non-root, readOnlyRootFilesystem)
  - [x] Controller ServiceAccount + ClusterRole/Role (conditional on `controller.watchNamespaces`) + ClusterRoleBinding/RoleBinding
  - [x] Bundle CRD manifests from `config/crd/bases/` into `charts/kubezap/crds/` (Helm manages CRD lifecycle)
  - [x] Values: `controller.image`, `controller.watchNamespaces`, `controller.leaderElect`, `controller.resources`, `controller.replicas`
  - [x] Values: gateway images (`webhookGateway.image`, `kafkaGateway.image`, `amqpGateway.image`, `natsGateway.image`) — images referenced by controller at runtime
  - [x] `_helpers.tpl` for label/selector helpers following `app.kubernetes.io/` conventions
  - [x] `NOTES.txt` with post-install instructions
  - [x] `helm lint` and `helm template` validation pass _(verified 2026-03-20: `helm lint` 0 failures, `helm template` renders cleanly)_
  - [x] Document Helm installation in `docs/overview.md` Installation section
- [x] OLM bundle finalized and validated with `operator-sdk bundle validate` _(done 2026-03-20: alm-examples populated, minKubeVersion set, zero warnings)_
- [ ] OperatorHub submission PR _(PAUSED — owner request 2026-03-20; do not start until explicitly unblocked)_
- [x] `docs/overview.md` Installation section completed _(done 2026-03-20)_
- [x] Compatibility matrix updated (OpenShift 4.12+) _(done 2026-03-20)_

---

## 8. Technical Debt

> Items identified by technical debt review on 2026-03-16.
> **BLOCKER** items must be resolved before new feature development proceeds.

### Blockers

- [x] **BLOCKER** `flowrun_controller.go`: FlowRun step execution loop is synchronous and blocking — a single long-running HTTP step or a long wait-step requeue holds the reconciler goroutine for the full duration. Under the default controller-runtime concurrency limit this starves other FlowRuns. Steps must be made non-blocking (e.g., per-FlowRun concurrency, or step-granularity requeueing without blocking the goroutine).
- [x] **BLOCKER** `flowrun_controller.go` `executeHTTPStep`: `defer resp.Body.Close()` inside a retry loop (`//nolint:gocritic`) leaks the response body of all retries except the last — body is only closed when the enclosing function returns, not after each iteration. All non-final response bodies are leaked.
- [x] **BLOCKER** `flowrun_controller.go`: A new Kafka `sarama.SyncProducer` is created on every `type: publish` step execution (`publishToKafka`). Producers are expensive to open and are not reused or pooled. At any meaningful call rate this will exhaust connections and degrade the Kafka broker.
- [x] **BLOCKER** `flowrun_controller.go`: The `Reconcile` function does not hold a finalizer on FlowRun resources. If the controller pod is deleted mid-execution, the FlowRun will sit in `Running` phase indefinitely with no mechanism to detect or recover the orphan (no timeout at the FlowRun level, no heartbeat condition).
- [x] **BLOCKER** `cmd/webhook-gateway/main.go` `ServeHTTP`: FlowRun is created with `context.Background()` instead of the request context (`r.Context()`). If the client disconnects, the create call is not cancelled and the FlowRun is still created — this is likely intentional for fire-and-forget semantics, but means the traced span loses its parent context. More critically, a panicking request handler will leave a dangling goroutine; the handler has no recover/panic boundary.
- [x] **BLOCKER** `trigger_controller.go`: `LastTriggeredTime` is unconditionally set to `metav1.Now()` for any enabled Trigger, even webhooks (line 123–124). This pollutes status with a fake timestamp before any trigger has actually fired. The field should only be updated when the trigger actually fires.
- [x] **BLOCKER** `integration_controller.go` `reconcilePluginRBAC`: plugin SA, Role, and RoleBinding are only created — never updated if they drift. If the rules are changed in code (e.g., new verbs added), existing deployments will retain the old rules until the resources are manually deleted. The pattern used for the Kafka gateway (read-then-update) should be applied here as well.

### High Priority

- [x] **HIGH** `flowrun_controller.go`: The `evaluateWhen` function creates a new CEL `env` and compiles every `when` expression on every reconcile pass. CEL environments and compiled programs are expensive and should be cached (keyed by Flow generation or expression text). _(done: `celEnvOnce` + `celCache sync.Map`)_
- [x] **HIGH** `cron_scheduler.go`: The cron job closure captures `flowRef` by value at registration time (line 70–73). _(confirmed non-issue: Go string value capture is immutable; reconciler re-registers on update; bounded staleness window is by design — not a bug)_
- [x] **HIGH** `cron_scheduler.go`: Cooldown logic uses `LastTriggeredTime` as the window-start reference, but this field is set only after a successful `Status().Patch` call. If the patch fails (transient API error), the field is not updated and the trigger will fire again immediately on the next tick — invocation count is not reliably enforced. _(fixed: nil-`LastTriggeredTime` path now patches `LastTriggeredTime=now, CurrentInvocationCount=1` BEFORE FlowRun creation; returns on patch failure so window is authoritative)_
- [x] **HIGH** `internal/gateway/webhook/watcher.go` `buildRouteEntry`: Secrets are read synchronously inside the informer event handler. _(done: `handleTrigger` creates `context.WithTimeout(..., 5s)` before calling `buildRouteEntry`)_
- [x] **HIGH** `internal/gateway/kafka/watcher.go`: Uses a polling loop (`time.NewTicker(30 * time.Second)`) rather than a controller-runtime informer or watch. _(done: refactored to informer/cache in 2026-03-16 debt sprint)_
- [x] **HIGH** `internal/gateway/webhook/handler.go`: No `413` response for oversized payloads. _(done: handler returns `StatusRequestEntityTooLarge` when `bodyTruncated`)_
- [x] **HIGH** `trigger_controller.go` `ensureWebhookGateway`: Deployment update path only syncs the container image. _(done: `CreateOrUpdate` with full `desired.Spec` overwrite; HPA replica count preserved)_
- [x] **HIGH** `integration_controller.go` `reconcilePluginDeployment`: Only the image is synced on update. _(done: `CreateOrUpdate` with full `desired.Spec` overwrite)_
- [x] **HIGH** `flowrun_controller.go`: No `observedGeneration` field is set on FlowRun status conditions. _(done: `flowRun.Status.ObservedGeneration = flowRun.Generation` set before status patch)_
- [x] **HIGH** `api/v1alpha1/trigger_types.go` + `webhook/handler.go`: Implement `basic` auth; remove `mtls`; add TLS termination to webhook gateway server. _(done: 2026-03-17 debt sprint)_
- [x] **HIGH** `api/v1alpha1/flow_types.go` `WaitAction.Duration`: The validation regex `^[0-9]+(ns|us|µs|ms|s|m|h)$` does not match compound durations like `"1h30m"`. Go's `time.ParseDuration` accepts compound durations but the CRD schema will reject them. The regex should be loosened or removed in favour of a CEL validation rule.
- [x] **HIGH** `go.mod`: `github.com/IBM/sarama` is listed as `// indirect` despite being directly used in `flowrun_controller.go` and `kafka/watcher.go`. This implies it was never explicitly added via `go get` and may be pinned at an incorrect version inherited from a transitive dependency. The dependency should be explicit in `require`.
- [x] **HIGH** `cmd/main.go`: Leader election is disabled by default (`--leader-elect=false`). Running multiple controller replicas without leader election will cause split-brain: multiple reconcilers will simultaneously create/update resources, causing conflicts and duplicate FlowRuns. Production deployments require leader election to be the default-on.
- [x] **HIGH** `internal/controller/trigger_controller_test.go`: The test is almost entirely scaffold boilerplate with placeholder `TODO(user)` comments and no meaningful assertions. It exercises zero actual trigger behaviour (no condition check, no status check, no webhook gateway assertion). This means the trigger reconciler has no unit test coverage.

### Backlog

- [x] `flowrun_controller.go` `substituteVars`: The `$(trigger.body.<field>)` extraction only resolves top-level JSON fields. Nested field access (e.g., `$(trigger.body.order.id)`) silently returns an empty string. _(fixed 2026-03-20: limitation documented in code comment and API field description)_
- [x] `flowrun_controller.go` `extractSimpleJSONPath`: Only supports single-level `$.field` paths despite the field being named `resultMappings`. Any multi-level JSONPath expression silently returns empty. _(fixed 2026-03-20: limitation documented in function comment; field description updated)_
- [x] `flowrun_controller.go` `enforceMaxFlowRuns`: On every reconcile of a terminal FlowRun, the controller lists ALL FlowRuns for the trigger with no field selector. For triggers with many FlowRuns this is an unbounded list scan. _(fixed 2026-03-20: `kubezap.io/phase` label added on phase transitions; `enforceMaxFlowRunsByPhase` filters by both trigger and phase labels)_
- [x] `internal/gateway/webhook/oidc.go`: The OIDC validator holds the JWKS keyset in memory per-`RouteEntry`. When there are many webhook triggers with OIDC auth, there is one keyset cache per route. There is no shared cache or background refresh — keys only refresh on request failure (key rotation retry). A background refresh goroutine would improve reliability. _(fixed 2026-03-20: replaced hand-rolled per-validator cache with a single shared `jwk.Cache` (lestrrat-go/jwx/v2 built-in) created in `cmd/webhook-gateway/main.go` and passed through to each `oidcValidator`; all routes sharing the same JWKS URL share one cache entry; background refresh every 15 min via `jwk.Cache` internal goroutine; `NewJWKSCache` factory exported from `oidc.go`; `RegisterJWKSURL` performs initial warm fetch on route registration)_
- [x] `internal/gateway/webhook/handler.go` `redactHeader`: Only `Authorization` and `X-Api-Key` are redacted in the access log header map that is stored in `FlowRun.Spec.TriggerData.Headers`. Other sensitive headers (e.g., `Cookie`, `X-Auth-Token`, custom bearer headers) are stored unredacted in the CRD object and visible to anyone with `get flowruns` permission. _(fixed: expanded to `authorization`, `x-api-key`, `cookie`, `set-cookie`, `x-auth-token`, `proxy-authorization` using a map for O(1) lookup)_
- [x] `internal/controller/mockendpoint_controller.go`: Added `log.Info` warning when `KUBEZAP_GATEWAY_BASE_URL` is unset so operators can diagnose relative-path URLs. _(fixed 2026-03-20)_
- [x] `internal/controller/integration_controller.go` `reconcileKafkaGateway`: The kafka gateway SA and RoleBinding are never updated after creation (only the Role is updated). If the SA or RoleBinding drift they will not be reconciled. _(fixed: all three now use CreateOrUpdate)_
- [x] `internal/controller/integration_controller.go` `buildKafkaTriggers`: For a Kafka integration with N topics and M consumer groups, the KEDA ScaledObject gets N×M trigger entries. _(fixed: replaced separate topic/cg sets with `kafkaTopicCGPair` set; each Trigger contributes exactly one pair)_
- [x] `api/v1alpha1/flowrun_types.go`: `FlowRunSpec.FlowRef` is a `corev1.LocalObjectReference` (name only, no namespace). Cross-namespace flows are architecturally desired (see backlog) but the type does not support it. A `FlowReference` type (matching `trigger_types.go`) should be used to allow namespace to be specified. _(fixed: all callers updated from `corev1.LocalObjectReference` to `automationv1alpha1.FlowReference`)_
- [x] `api/v1alpha1/trigger_types.go`: `TriggerSpec` has both `FlowRef` and an inline `Action` field with no validation ensuring exactly one is set. _(fixed 2026-03-20: added `+kubebuilder:validation:XValidation` CEL rule `has(self.flowRef) != has(self.action)` with message "exactly one of flowRef or action must be set"; CRD manifests regenerated)_
- [x] `internal/controller/cron_scheduler.go`: Timezone field now applied via `CRON_TZ=<tz>` prefix on schedule string before passing to `cron.AddFunc`. _(fixed 2026-03-20)_
- [x] `internal/gateway/amqp/watcher.go` and `internal/gateway/nats/watcher.go`: Both use a polling loop (`time.NewTicker(30 * time.Second)`) rather than a controller-runtime informer or watch — same issue that was fixed in the Kafka gateway. Reaction time to Trigger changes is up to 30 s. Should be refactored to informer/cache pattern (mirrors `internal/gateway/kafka/watcher.go` post-refactor). _(fixed 2026-03-18: refactored both to informer/cache pattern mirroring kafka/watcher.go)_
- [x] `internal/gateway/webhook/watcher.go`: `NewTriggerWatcher` now accepts `*rest.Config` from caller instead of calling `ctrl.GetConfigOrDie()` internally. `cmd/webhook-gateway/main.go` updated to pass the config it already holds. _(fixed 2026-03-20)_
- [x] `config/rbac/role.yaml` and `config/rbac/namespaced_role.yaml`: Both ClusterRole and namespaced Role are missing the `delete` verb for `roles`, `rolebindings`, and `serviceaccounts` — orphaned RBAC resources accumulate when Integrations or gateways are removed. Additionally, Kafka gateway SA/Role/RoleBinding are created without `ctrl.SetControllerReference` — they are never garbage-collected on Integration delete (unlike the plugin RBAC path which does set owner refs). See `docs/tech-debt/rbac-ownership-gaps.md` for full analysis and fix steps. _(fixed 2026-03-18: delete verb added to RBAC markers; run make manifests to regenerate)_
- [x] `cmd/main.go`: `Development: true` is hardcoded in the zap options for the controller binary (line 93). Development mode emits caller information and uses a human-readable format rather than JSON — inappropriate for production. _(fixed: `--development` flag now correctly applied after `flag.Parse()`, overriding `--zap-devel` when set)_
- [x] Missing tests: `substituteVars`, `evaluateWhen`, `enforceMaxFlowRunsByPhase`, cron cooldown — all covered by new tests in `internal/controller/` (substitute_vars_test.go, evaluate_when_test.go, gc_policy_test.go, cron_scheduler_test.go). _(done 2026-03-20)_
- [x] `internal/gateway/kafka/watcher.go` `startSubscription`: the subscription goroutine uses `context.WithCancel(context.Background())` rather than deriving from the caller's context. _(fixed: changed to `context.WithCancel(ctx)` so pod shutdown cleanly cancels all in-flight consumer sessions)_

### New — Identified 2026-03-18 (codebase review)

- [x] **HIGH** `internal/gateway/amqp/watcher.go` and `internal/gateway/nats/watcher.go` `startSubscription`: both use `context.WithCancel(context.Background())` instead of `context.WithCancel(ctx)`. _(fixed 2026-03-18: both changed to `context.WithCancel(ctx)`)_
- [x] **MEDIUM** `internal/gateway/nats/watcher.go` `startSubscription`: NATS NKey/JWT credentials written to `/tmp/nats-creds-<trigger.Name>.creds` — predictable path, no cleanup on subscription stop, name collision risk across namespaces. _(fixed 2026-03-18: replaced with `os.CreateTemp` + cleanup in subscription goroutine)_
- [x] **HIGH** `internal/controller/integration_controller.go` `reconcileKafkaGateway`: Kafka gateway SA, Role, and RoleBinding are created without `ctrl.SetControllerReference` on the Integration — they are orphaned on Integration delete and accumulate indefinitely. Fix requires adding `delete` verb to controller RBAC first (schedule backlog line 264). See `docs/tech-debt/rbac-ownership-gaps.md#issue-1`. _(fixed 2026-03-18: SA/Role/RoleBinding all use SetControllerReference via CreateOrUpdate; Role pattern refactored from manual Get/Create to CreateOrUpdate)_
- [x] **DESIGN** `api/v1alpha1/trigger_types.go`: `spec.maxFlowRuns` replaced with `spec.flowRunGC` struct providing per-state caps (`maxSucceeded`, `maxFailed`) and per-trigger TTL overrides (`ttlAfterSucceeded`, `ttlAfterFailed`). Follows Kubernetes Job history limits pattern. Controller updated (`enforceFlowRunGCPolicy` + `enforceMaxFlowRunsByPhase`). CRD YAML regenerated. _(done 2026-03-18)_
- [x] **LOW** `test/e2e/e2e_test.go`: E2E workflow scenario added in `test/e2e/webhook_test.go` — webhook trigger → transform → http step → MockEndpoint → verify FlowRun Succeeded. Skip with `SKIP_WEBHOOK_E2E=true`. _(done 2026-03-20)_

### Documentation Gaps — Identified 2026-03-18

- [x] `docs/api/flowrun.md`: `Waiting` step phase and `resumeAfter` field missing from Status Reference. _(fixed 2026-03-18)_
- [x] `docs/api/integration.md`: AMQP and NATS marked "planned" despite being implemented (beta). Limitations section contradicts examples. _(fixed 2026-03-18)_
- [x] `docs/guides/getting-started.md`: Stale `# replaced with CEL/jq extraction once implemented` comment in transform step example. _(fixed 2026-03-18)_
- [x] `docs/api/trigger.md`: `WebhookAuth` spec reference lacks the full list of supported auth types and field descriptions. Types are documented in `docs/guides/webhook-security.md` but the core API reference is incomplete for users who don't follow cross-links. _(fixed 2026-03-18: added WebhookAuth and WebhookBasicAuth field tables based on Go types)_
- [x] `docs/api/flow.md`: `type: publish` step action missing implementation detail — how the controller calls the Integration's `/publish` endpoint, error handling, interaction with retry policy. _(fixed 2026-03-18: added Execution subsection with routing, HTTP contract, timeout, failure, retry details)_
- [x] `docs/api/integration.md`: KEDA ScaledObject for Kafka gateway not documented anywhere. _(fixed 2026-03-18: added KEDA section under Kafka type in integration.md)_
- [x] `docs/guides/observability.md`: ServiceMonitor "not auto-created" caveat is buried in the guide; users who skim to the Prometheus section won't see it until after deployment. Promote to a callout box near the top of the Prometheus section. _(fixed 2026-03-18: added callout note near top of Prometheus section)_

---

## 9. `kubezap` CLI Tool

> **Scope decision (2026-03-18)**: read-only for all CRDs. FlowRun history querying is the
> primary use case — this is where `kubectl` falls short. Triggers, Flows, and Integrations
> get richer status views than `kubectl get` but no write operations. Write support
> (editor+template) deferred to future backlog.
>
> **Repo decision (2026-03-18)**: monorepo. CLI lives in `cmd/kubezap/` and imports
> `api/v1alpha1` directly. Separate release artifact via Goreleaser. Split only if CLI
> ever needs to talk to a hosted API rather than Kubernetes directly.

### Design

- [x] Design doc in `docs/design/cli.md` — full command surface, output formats, kubeconfig/context handling, kubectl plugin installation, Goreleaser distribution _(done 2026-03-18)_

### Core Commands — FlowRun History (primary use case)

- [x] `kubezap history [-n <ns>] [--trigger <name>] [--flow <name>] [--phase <phase>] [--since <duration>]` — filtered FlowRun list: name, trigger, flow, phase, duration, age; uses label/field selectors _(done 2026-03-20)_
- [x] `kubezap history <flowrun-name>` — single FlowRun detail: spec summary, per-step timeline (name, phase, duration, attempts), result values, skip/failure reasons _(done 2026-03-20)_
- [x] `kubezap history --watch` — live tail of FlowRun completions (2s polling loop; TODO: replace with real Watch) _(done 2026-03-20)_

### Read-Only Status Commands

- [x] `kubezap triggers [-n <ns>]` — table: name, type, status, last fired, active FlowRun count, effective GC policy _(done 2026-03-20)_
- [x] `kubezap flows [-n <ns>]` — table: name, step count, ready condition, last used (inferred from most recent FlowRun) _(done 2026-03-20)_
- [x] `kubezap integrations [-n <ns>]` — table: name, type, plugin health (readiness probe status), associated gateway Deployment status _(done 2026-03-20)_
- [x] `kubezap version` — CLI version + operator version (from operator Deployment image tag) _(done 2026-03-20)_

### Implementation

- [x] Scaffold CLI binary in `cmd/kubezap/main.go` using `cobra`; internal commands in `internal/cli/`
- [x] Kubernetes client setup: respect `KUBECONFIG`, `--context`, `--namespace` / `-n` flags (mirrors kubectl conventions); use `api/v1alpha1` types directly (same module, no versioning complexity)
- [x] Output formatters in `internal/cli/output/`: table (default), JSON (`-o json`), YAML (`-o yaml`) _(done 2026-03-20)_
- [x] Step timeline renderer for `history <name>`: ASCII table with step name, phase icon, start→end duration, attempt count, result key=value pairs _(done 2026-03-20)_
- [x] `Makefile` target `make build-cli` — produces `bin/kubezap` _(pre-existing, verified 2026-03-20)_
- [x] Goreleaser config: multi-platform CLI binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) released alongside operator image _(done 2026-03-20)_

### Distribution

- [x] Distributed as `kubectl-kubezap` binary — users add to `$PATH` and invoke as `kubectl kubezap` or standalone `kubezap` _(done 2026-03-20)_
- [x] Document installation in `docs/overview.md` (brew tap, direct download, manual kubectl plugin install) _(done 2026-03-20)_

---

## 10. Future / Backlog

- [x] `docs/guides/using-the-cli.md` — user guide for the `kubezap` CLI _(created 2026-03-20)_
- [x] `docs/guides/cron-triggers.md` — how-to guide for scheduled workflows _(created 2026-03-20)_
- [x] `docs/guides/troubleshooting.md` — consolidated troubleshooting guide _(created 2026-03-20)_
- [x] `docs/guides/amqp-setup.md` stub created _(2026-03-20)_
- [ ] `docs/guides/amqp-setup.md` — write full AMQP setup guide (stub exists)
- [x] `docs/guides/nats-setup.md` stub created _(2026-03-20)_
- [ ] `docs/guides/nats-setup.md` — write full NATS setup guide (stub exists)
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows (cross-namespace FlowRun)
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] Web UI for flow monitoring
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] Plugin marketplace / integration catalog
- [ ] S3/Git event trigger source
- [x] Fix pre-existing `flow_controller_test.go` failure: `when spec.steps is empty` test case
