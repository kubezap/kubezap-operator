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

- [ ] Helm chart in `charts/kubezap/`
- [ ] OLM bundle finalized and validated with `operator-sdk bundle validate`
- [ ] OperatorHub submission PR
- [ ] `docs/overview.md` Installation section completed
- [ ] Compatibility matrix updated (OpenShift 4.12+)

---

## 8. Technical Debt

> Items identified by technical debt review on 2026-03-16.
> **BLOCKER** items must be resolved before new feature development proceeds.

### Blockers

- [ ] **BLOCKER** `flowrun_controller.go`: FlowRun step execution loop is synchronous and blocking — a single long-running HTTP step or a long wait-step requeue holds the reconciler goroutine for the full duration. Under the default controller-runtime concurrency limit this starves other FlowRuns. Steps must be made non-blocking (e.g., per-FlowRun concurrency, or step-granularity requeueing without blocking the goroutine).
- [x] **BLOCKER** `flowrun_controller.go` `executeHTTPStep`: `defer resp.Body.Close()` inside a retry loop (`//nolint:gocritic`) leaks the response body of all retries except the last — body is only closed when the enclosing function returns, not after each iteration. All non-final response bodies are leaked.
- [x] **BLOCKER** `flowrun_controller.go`: A new Kafka `sarama.SyncProducer` is created on every `type: publish` step execution (`publishToKafka`). Producers are expensive to open and are not reused or pooled. At any meaningful call rate this will exhaust connections and degrade the Kafka broker.
- [ ] **BLOCKER** `flowrun_controller.go`: The `Reconcile` function does not hold a finalizer on FlowRun resources. If the controller pod is deleted mid-execution, the FlowRun will sit in `Running` phase indefinitely with no mechanism to detect or recover the orphan (no timeout at the FlowRun level, no heartbeat condition).
- [x] **BLOCKER** `cmd/webhook-gateway/main.go` `ServeHTTP`: FlowRun is created with `context.Background()` instead of the request context (`r.Context()`). If the client disconnects, the create call is not cancelled and the FlowRun is still created — this is likely intentional for fire-and-forget semantics, but means the traced span loses its parent context. More critically, a panicking request handler will leave a dangling goroutine; the handler has no recover/panic boundary.
- [x] **BLOCKER** `trigger_controller.go`: `LastTriggeredTime` is unconditionally set to `metav1.Now()` for any enabled Trigger, even webhooks (line 123–124). This pollutes status with a fake timestamp before any trigger has actually fired. The field should only be updated when the trigger actually fires.
- [x] **BLOCKER** `integration_controller.go` `reconcilePluginRBAC`: plugin SA, Role, and RoleBinding are only created — never updated if they drift. If the rules are changed in code (e.g., new verbs added), existing deployments will retain the old rules until the resources are manually deleted. The pattern used for the Kafka gateway (read-then-update) should be applied here as well.

### High Priority

- [ ] **HIGH** `flowrun_controller.go`: The `evaluateWhen` function creates a new CEL `env` and compiles every `when` expression on every reconcile pass. CEL environments and compiled programs are expensive and should be cached (keyed by Flow generation or expression text).
- [ ] **HIGH** `cron_scheduler.go`: The cron job closure captures `flowRef` by value at registration time (line 70–73). If the Trigger's `flowRef` is updated, the in-process cron job continues firing with the stale FlowRef. The Register path removes and re-adds the job, but only when the reconciler processes the updated Trigger — there is a window where stale FlowRuns are created.
- [ ] **HIGH** `cron_scheduler.go`: Cooldown logic uses `LastTriggeredTime` as the window-start reference, but this field is set only after a successful `Status().Patch` call. If the patch fails (transient API error), the field is not updated and the trigger will fire again immediately on the next tick — invocation count is not reliably enforced.
- [ ] **HIGH** `internal/gateway/webhook/watcher.go` `buildRouteEntry`: Secrets are read synchronously inside the informer event handler (on the informer goroutine). A slow or failing API server will block event processing for all subsequent informer events, causing the route registry to fall behind. Secret fetches should use a timeout context.
- [ ] **HIGH** `internal/gateway/kafka/watcher.go`: Uses a polling loop (`time.NewTicker(30 * time.Second)`) rather than a controller-runtime informer or watch. New/deleted/modified Triggers are picked up with up to 30 s delay. During this window, Kafka messages are consumed but FlowRuns cannot be created (missing flowRef), and deleted Triggers continue consuming. Should use an informer-based watch (same pattern as `webhook/watcher.go`).
- [ ] **HIGH** `internal/gateway/webhook/handler.go`: Webhook request body is stored in `FlowRun.Spec.TriggerData.Body` truncated to 4 KiB. The raw body read is limited to 4 MiB. There is no rejection or `413` response for oversized payloads — the body is silently truncated and the FlowRun is created with a partial payload that may cause downstream step failures with no clear error.
- [ ] **HIGH** `trigger_controller.go` `ensureWebhookGateway`: The webhook gateway Deployment update path only syncs the container image. Any other field drift (env vars, security contexts, resource limits, args) is silently ignored. The Deployment will only be corrected if the image also changes.
- [ ] **HIGH** `integration_controller.go` `reconcilePluginDeployment`: Same issue as above — only the image is synced on update. All other Deployment fields (env vars, resources, probes) drift silently.
- [ ] **HIGH** `flowrun_controller.go`: No `observedGeneration` field is set on FlowRun status conditions. Consumers of FlowRun conditions (e.g., automated tooling, `kubectl wait`) cannot distinguish a stale condition from a fresh one when the spec has been updated.
- [ ] **HIGH** `api/v1alpha1/trigger_types.go`: `WebhookAuth.Type` enum includes `basic` and `mtls` but neither is implemented in `webhook/handler.go` (`authenticateRequest`). Triggering either type silently falls through to the `default` case and allows all requests unauthenticated. This is a silent security bypass.
- [ ] **HIGH** `api/v1alpha1/flow_types.go` `WaitAction.Duration`: The validation regex `^[0-9]+(ns|us|µs|ms|s|m|h)$` does not match compound durations like `"1h30m"`. Go's `time.ParseDuration` accepts compound durations but the CRD schema will reject them. The regex should be loosened or removed in favour of a CEL validation rule.
- [ ] **HIGH** `go.mod`: `github.com/IBM/sarama` is listed as `// indirect` despite being directly used in `flowrun_controller.go` and `kafka/watcher.go`. This implies it was never explicitly added via `go get` and may be pinned at an incorrect version inherited from a transitive dependency. The dependency should be explicit in `require`.
- [ ] **HIGH** `cmd/main.go`: Leader election is disabled by default (`--leader-elect=false`). Running multiple controller replicas without leader election will cause split-brain: multiple reconcilers will simultaneously create/update resources, causing conflicts and duplicate FlowRuns. Production deployments require leader election to be the default-on.
- [ ] **HIGH** `internal/controller/trigger_controller_test.go`: The test is almost entirely scaffold boilerplate with placeholder `TODO(user)` comments and no meaningful assertions. It exercises zero actual trigger behaviour (no condition check, no status check, no webhook gateway assertion). This means the trigger reconciler has no unit test coverage.

### Backlog

- [ ] `flowrun_controller.go` `substituteVars`: The `$(trigger.body.<field>)` extraction only resolves top-level JSON fields. Nested field access (e.g., `$(trigger.body.order.id)`) silently returns an empty string. This limitation is not documented in the CRD field description.
- [ ] `flowrun_controller.go` `extractSimpleJSONPath`: Only supports single-level `$.field` paths despite the field being named `resultMappings`. Any multi-level JSONPath expression silently returns empty. Should either implement full JSONPath or document and enforce the limitation via a validation marker.
- [ ] `flowrun_controller.go` `enforceMaxFlowRuns`: On every reconcile of a terminal FlowRun, the controller lists ALL FlowRuns for the trigger with no field selector. For triggers with many FlowRuns this is an unbounded list scan. A field selector or label-indexed list should be used.
- [ ] `internal/gateway/webhook/oidc.go`: The OIDC validator holds the JWKS keyset in memory per-`RouteEntry`. When there are many webhook triggers with OIDC auth, there is one keyset cache per route. There is no shared cache or background refresh — keys only refresh on request failure (key rotation retry). A background refresh goroutine would improve reliability.
- [ ] `internal/gateway/webhook/handler.go` `redactHeader`: Only `Authorization` and `X-Api-Key` are redacted in the access log header map that is stored in `FlowRun.Spec.TriggerData.Headers`. Other sensitive headers (e.g., `Cookie`, `X-Auth-Token`, custom bearer headers) are stored unredacted in the CRD object and visible to anyone with `get flowruns` permission.
- [ ] `internal/controller/mockendpoint_controller.go`: `status.URL` is constructed from `KUBEZAP_GATEWAY_BASE_URL` env var. If the env var is unset (the common case in-cluster where the URL must be inferred), the URL is built as `/mock/<path>` with no host — a relative path that is not useful. There is no validation or warning when the env var is absent.
- [x] `internal/controller/integration_controller.go` `reconcileKafkaGateway`: The kafka gateway SA and RoleBinding are never updated after creation (only the Role is updated). If the SA or RoleBinding drift they will not be reconciled.
- [ ] `internal/controller/integration_controller.go` `buildKafkaTriggers`: For a Kafka integration with N topics and M consumer groups, the KEDA ScaledObject gets N×M trigger entries. This cross-product is almost certainly unintentional — each topic should have exactly one consumer group, not all consumer groups.
- [ ] `api/v1alpha1/flowrun_types.go`: `FlowRunSpec.FlowRef` is a `corev1.LocalObjectReference` (name only, no namespace). Cross-namespace flows are architecturally desired (see backlog) but the type does not support it. A `FlowReference` type (matching `trigger_types.go`) should be used to allow namespace to be specified.
- [ ] `api/v1alpha1/trigger_types.go`: `TriggerSpec` has both `FlowRef` and an inline `Action` field with no validation ensuring exactly one is set. A Trigger with neither (or both) will silently proceed: with neither, FlowRun `spec.flowRef.name` will be empty; with both, Action is ignored. A CEL validation rule (`has(self.flowRef) != has(self.action)` or similar) should enforce mutual exclusivity.
- [ ] `internal/controller/cron_scheduler.go`: The cron scheduler does not validate the `Timezone` field from `CronTrigger.Timezone`. The `robfig/cron` library supports `CRON_TZ=` prefix in the schedule string, but the Timezone field is never applied to the cron entry. Schedules always fire in the controller pod's local timezone (UTC in distroless), silently ignoring the user-specified timezone.
- [ ] `internal/gateway/webhook/watcher.go`: `ctrl.GetConfigOrDie()` is called inside `NewTriggerWatcher` in addition to being called in `main.go`. This is an anti-pattern — the REST config should be passed in from the caller rather than fetched again. Two fetches of the kube config is harmless but fragile (the second fetch could theoretically produce a different config).
- [ ] `config/rbac/role.yaml`: The generated ClusterRole grants `rbac.authorization.k8s.io/roles` and `rolebindings` verbs without `delete`. The integration reconciler and gateway deployment helper use `Create`/`Update`/`Patch` but never `Delete` — orphaned Roles and RoleBindings accumulate if the associated Integration or gateway is removed. Owner references on Kafka gateway RBAC resources are not set (unlike the plugin RBAC path which does set them).
- [ ] `cmd/main.go`: `Development: true` is hardcoded in the zap options for the controller binary (line 93). Development mode emits caller information and uses a human-readable format rather than JSON — inappropriate for production. This should be configurable via flag (the flag is already bound but `Development` is not derived from it).
- [ ] Missing tests: `substituteVars` has no unit tests for edge cases (nested body fields, headers case-insensitivity, unresolved placeholders left verbatim). `evaluateWhen` has no unit tests. `enforceMaxFlowRuns` has no unit tests. `cron_scheduler.go` cooldown logic has no unit tests. These are all critical hot-path functions with observable correctness requirements.
- [ ] `internal/gateway/kafka/watcher.go` `startSubscription`: the subscription goroutine uses `context.WithCancel(context.Background())` rather than deriving from the caller's context. This means if the watcher's `Start` context is cancelled (e.g., pod shutdown), in-flight consumer group sessions are not cleanly cancelled before the goroutine drains — the 5 s retry backoff means shutdown can take up to 5 s per subscription beyond the graceful shutdown window.

---

## 9. Future / Backlog

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
