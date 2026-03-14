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
- [ ] HMAC authentication support
- [ ] Bearer token authentication support
- [ ] OIDC/JWT authentication support
- [ ] Basic auth, mTLS, API-key header, IP allowlist support
- [ ] `/mock/*` path support for MockEndpoint CRDs
- [x] Structured JSON access logs (source IP in logs only, not Prometheus labels)
- [x] Fix: add source IP (`RemoteAddr`) to access log in `internal/gateway/webhook/handler.go`
- [x] Fix: remove dead `RouteRegistry.ServeHTTP` method from `internal/gateway/webhook/registry.go`
- [ ] HPA configuration for webhook gateway Deployment
- [x] Controller manages webhook gateway Deployment lifecycle (one per namespace)

### Cron Trigger
- [x] Cron scheduler implementation in controller (`robfig/cron v3`)
- [x] FlowRun creation on schedule fire: `<trigger>-<scheduled-time>` naming
- [ ] Cooldown enforcement for cron triggers

### Controller: FlowRun Execution
- [x] `FlowRun` reconciler in `internal/controller/flowrun_controller.go`
- [x] Fetch referenced Flow and resolve steps in dependency order
- [x] Execute HTTP action steps
- [ ] Step result passing and CEL expression evaluation
- [x] FlowRun status conditions (Running, Succeeded, Failed)
- [ ] FlowRun GC: `spec.ttlAfterFinished`, operator flags `--flowrun-ttl-succeeded` / `--flowrun-ttl-failed`
- [ ] `kubezap.io/retain=true` annotation exempts FlowRun from GC

---

## 2. Flow Engine (v0.2)

### Flow Reconciler
- [ ] `Flow` reconciler validates spec and sets Ready condition
- [ ] Conditional step execution via CEL expressions (`when` field)
- [ ] Step input/output data passing between steps
- [ ] Data transformation step type (`type: transform`)
- [ ] Retry policies with exponential backoff per step
- [ ] Flow-level timeout enforcement

### Integration CRD & Kafka Gateway
- [ ] `Integration` reconciler in `internal/controller/integration_controller.go`
- [ ] Kafka gateway skeleton in `cmd/kafka-gateway/main.go`
- [ ] Dynamic topic subscription from Trigger CRDs
- [ ] FlowRun creation per Kafka message: `<trigger>-p<partition>-offset-<offset>` (dedup key)
- [ ] KEDA ScaledObject for Kafka gateway (partition-bounded scaling)
- [ ] Controller manages Kafka gateway Deployment lifecycle (one per namespace × Kafka cluster)
- [ ] `type: publish` step action — controller calls plugin `/publish` endpoint

### MockEndpoint CRD
- [ ] MockEndpoint reconciler — registers routes on webhook gateway
- [ ] Captured request storage in CRD status

---

## 3. Plugin System

- [ ] Plugin contract documented in `docs/api/integration.md` (subscriber + publisher roles)
- [ ] Operator creates plugin Deployment for `type: plugin` Integrations
- [ ] Namespace-scoped RBAC granted to plugin Deployment
- [ ] Env injection: `KUBEZAP_NAMESPACE`, `KUBEZAP_INTEGRATION_NAME`, `KUBEZAP_PUBLISHER_PORT`, `KUBEZAP_LOG_LEVEL`
- [ ] Secret injection via `spec.plugin.secretRefs` + `envVarMappings`
- [ ] Readiness probe: `GET /healthz` → 200
- [ ] Controller routes `type: publish` step calls to plugin `/publish` endpoint
- [ ] Plugin trust model documented as a security consideration

---

## 4. Observability

- [ ] Prometheus metrics: trigger firings, FlowRun durations, step outcomes
- [ ] OpenTelemetry traces for FlowRun execution and step calls
- [ ] Structured JSON access logs on webhook gateway (source IP, path, status, duration)
- [ ] Source IP cardinality guard: `/24`-bucketed `source_range` on `ip_blocked` metric only
- [ ] Observability guide updated in `docs/guides/observability.md`
- [ ] ServiceMonitor usage documented (not auto-created by operator)

---

## 5. Multi-Namespace & RBAC

- [ ] `WATCH_NAMESPACES` env var support (AllNamespaces / MultiNamespace / SingleNamespace / OwnNamespace)
- [ ] OwnNamespace/SingleNamespace modes use `Role` (not `ClusterRole`)
- [ ] All four OLM install modes supported in CSV bundle

---

## 6. Testing

- [ ] Ginkgo unit tests for Flow reconciler
- [ ] Ginkgo unit tests for FlowRun reconciler
- [ ] Ginkgo unit tests for Integration reconciler
- [ ] Ginkgo unit tests for MockEndpoint reconciler
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
- [ ] Additional message brokers: NATS, RabbitMQ, ActiveMQ, Solace, GCP Pub/Sub
- [ ] Web UI for flow monitoring
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] Plugin marketplace / integration catalog
- [ ] S3/Git event trigger source
