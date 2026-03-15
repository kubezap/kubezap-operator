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
- [ ] HPA configuration for webhook gateway Deployment
- [x] Controller manages webhook gateway Deployment lifecycle (one per namespace)
- [x] **Gateway ServiceAccount + Role + RoleBinding** created by controller alongside Deployment (deploy blocker — see `docs/architecture.md#gateway-serviceaccount-and-rbac`)

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
- [ ] AMQP gateway skeleton in `cmd/amqp-gateway/main.go` (`type: amqp`, versions 0-9-1 and 1.0)
- [ ] NATS gateway skeleton in `cmd/nats-gateway/main.go` (`type: nats`, Core + JetStream)
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

- [ ] `WATCH_NAMESPACES` env var support (AllNamespaces / MultiNamespace / SingleNamespace / OwnNamespace)
- [ ] OwnNamespace/SingleNamespace modes use `Role` (not `ClusterRole`)
- [ ] All four OLM install modes supported in CSV bundle

---

## 6. Testing

- [x] Ginkgo unit tests for Flow reconciler
- [x] Ginkgo unit tests for FlowRun reconciler
- [x] Ginkgo unit tests for Integration reconciler
- [x] Ginkgo unit tests for MockEndpoint reconciler
- [ ] E2E tests: webhook trigger → FlowRun creation → step execution
- [ ] E2E tests: cron trigger fires on schedule
- [ ] E2E tests: Kafka trigger → FlowRun with dedup key
- [ ] E2E tests: FlowRun GC respects TTL and retain annotation

---

## 7. Deployment & Distribution (v0.3)

- [ ] Helm chart in `charts/kubezap/`
- [ ] OLM bundle finalized and validated with `operator-sdk bundle validate`
- [ ] OperatorHub submission PR
- [ ] `docs/overview.md` Installation section completed
- [ ] Compatibility matrix updated (OpenShift 4.12+)

---

## 8. Future / Backlog

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
- [ ] Fix pre-existing `flow_controller_test.go` failure: `when spec.steps is empty` test case
