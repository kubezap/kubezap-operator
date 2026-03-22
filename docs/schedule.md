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
7. **P0 security fixes before architectural refactors in the same code area** — a security vulnerability should not be blocked waiting for a large refactor even if the refactor would reduce rework. Fix the vulnerability now; port the fix after the refactor if needed.

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

- [x] **BUG — `BodyTruncated` not set on mid-range bodies** (`internal/gateway/webhook/handler.go:281-284`): bodies between 4097 bytes and 4 MB are accepted with HTTP 202, but stored truncated to 4096 chars in `TriggerData.Body` with `BodyTruncated: false`. Users relying on `$(trigger.body)` cannot detect a partial body. Fix: set `bodyTruncated = true` when `len(bodyBytes) > 4096` before truncating `bodyString`. Also add coverage to T2 (body-at-size-limit boundary).

---

## 2. Research — Multi-Type Interference Audit (R1)

> **Do this before writing T1-T8.** Interference bugs in cron scheduling or FlowRun naming
> affect what T1 and T4 test directly. Fixing them after tests are written means test rewrites.

**Goal:** Find cases where having multiple trigger types or integration types active in the same namespace could interfere — shared resource conflicts, name collisions, owner-ref races.

**Known starting point:** `kubezap-gateway` SA/Role/RoleBinding is shared by kafka/amqp/nats integrations (owner-ref bug fixed 2026-03-20; shared RBAC intentionally not owned by any single Integration). Audit whether similar sharing exists elsewhere.

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

- [x] **BUG — Webhook FlowRun random suffix too short** (`internal/gateway/webhook/handler.go`): `randomHex(4)` produces 4 hex chars (2 bytes = 65 536 values per second per Trigger). Birthday collision probability at 100 req/s on the same Trigger is ~7.5%/second. On collision the handler returns HTTP 202 with the existing FlowRun name — but that FlowRun contains the body/headers of the **first** request, not the colliding one. Silent data loss for the colliding request. Fix: change `randomHex(4)` → `randomHex(8)` (4 bytes → 1/4 294 967 296 collision rate).

---

## 3. Testing — Targeted Coverage Gaps (T1-T8)

> Write after R1 and R2 so tests are not written against behavior that is about to change.

- [x] **T1 — Cron trigger integration test**: Create Trigger with `schedule: "*/1 * * * *"`, advance fake clock 65s (use `clock.FakeClock` from `k8s.io/utils/clock/testing`), assert exactly one FlowRun exists named `<trigger>-<scheduled-time>` and is `Succeeded`. File: `internal/controller/cron_scheduler_test.go` (extend existing).
- [x] **T2 — HMAC auth reject/accept E2E**: Start a real webhook gateway HTTP server in test, send request with valid HMAC → assert 202 + FlowRun created; send with wrong signature → assert 401 + no FlowRun; send with missing header → assert 401. File: `internal/gateway/webhook/handler_test.go` (new table-driven cases).
- [x] **T3 — CEL skip cascade test**: Flow with steps A → B → C where B has `when` that is false and C has `runAfter: [B]`. Assert: B=Skipped, C=Skipped (cascade), overall phase=Succeeded. File: `internal/controller/flowrun_controller_test.go`.
- [x] **T4 — FlowRun GC maxSucceeded enforcement**: Create trigger with `flowRunGC.maxSucceeded: 3`. Create 5 Succeeded FlowRuns. Trigger reconcile. Assert only 3 remain (oldest 2 deleted). Assert a FlowRun annotated `kubezap.io/retain=true` is never deleted even when over limit. File: `internal/controller/gc_policy_test.go` (extend existing).
- [x] **T5 — Cooldown window suppression**: Trigger with `maxInvocations: 2, window: 10s`. Fire 5 requests in sequence. Assert 2 FlowRuns created, 3 suppressed (metric `kubezap_webhook_rate_limited_total` incremented by 3). File: `internal/gateway/webhook/handler_test.go`.
- [x] **T6 — FlowRun orphan recovery**: Create FlowRun in `Running` phase with finalizer set, no active execution context. Advance time past the orphan timeout (`--flowrun-ttl-failed` default). Assert controller transitions phase to `Failed` with reason `OrphanTimeout` and removes finalizer. File: `internal/controller/flowrun_controller_test.go`.
- [x] **T7 — Step retry with exponential backoff**: Mock HTTP server that returns 503 for first 2 calls, 200 on 3rd. Step has `retryPolicy: {maxRetries: 3, backoffType: Exponential, initialDelay: 10ms, maxDelay: 100ms}`. Assert: step `attempts == 3`, step phase `Succeeded`, delay durations recorded in step status. File: `internal/controller/flowrun_controller_test.go`.
- [x] **T8 — Transform step + result chaining**: Flow with `type: transform` step that maps `$(trigger.body.orderId)` to result `orderId`, followed by HTTP step using `$(steps.transform.results.orderId)` in URL. Assert the HTTP call URL contains the correct substituted value. Use `httptest.NewServer` for the target. File: `internal/controller/flowrun_controller_test.go`.
- [x] **T9 — Webhook FlowRun name uniqueness under concurrent load**: Fire 500 concurrent webhook requests at the same Trigger using a goroutine pool. Assert all 500 FlowRuns are created with unique names (no silent `AlreadyExists` drops). Collect all created FlowRun names and assert zero duplicates. Requires the `randomHex(8)` fix (R1 bug above) to pass reliably. File: `internal/gateway/webhook/handler_test.go`.

---

## 4. MockEndpoint Replacement — Docs and Existing Examples

> **Complete Phases 0-3 before building Examples 7-9.** All pending examples use mock HTTP
> servers. Building them with MockEndpoints would require full rewrites during Phase 2-3.
> Phase 0 is already unblocked — tool selected (see note).

### Phase 0 — Tool selection

- [x] Choose replacement mock tool — **Mockoon** selected (see `docs/tech-debt/pending-input-required.md`, answered 2026-03-20)

### Phase 1 — Write replacement documentation

- [x] Convert `docs/api/mock-endpoint.md` to `docs/guides/mocking-http-endpoints.md`: explain why MockEndpoint is removed, document Mockoon's in-cluster deployment (Docker image + Kubernetes `Deployment` + `Service`), show how to define stub responses, show how to inspect captured requests, cross-link to each example that uses it
- [x] Add in-cluster `Deployment` + `Service` YAML for Mockoon as a reusable snippet referenced by examples and the guide
- [x] Update `docs/overview.md` CRD Overview table: remove `MockEndpoint` row; add note redirecting to `docs/guides/mocking-http-endpoints.md`
- [x] Update `docs/architecture.md`: remove all MockEndpoint references; update the "Webhook gateway also serves `/mock/*` paths" note to reflect removal
- [x] Update `docs/guides/troubleshooting.md`: replace "MockEndpoint not capturing requests" section with Mockoon equivalent

### Phase 2 — Update existing example manifests

- [x] `config/samples/automation_v1alpha1_mockendpoint.yaml` — delete file; remove from `config/samples/kustomization.yaml` and OLM bundle alm-examples
- [x] `examples/order-router/` — replace MockEndpoint resources with Mockoon stub configs; update `kustomization.yaml` and `README.md`
- [x] `examples/kafka-enrichment/` — replace `enterprise-sink`, `standard-sink`, `trial-sink`, `customer-profile` MockEndpoints with Mockoon stub configs; update `kustomization.yaml` and `README.md`
- [x] `examples/slack-router/` — replace MockEndpoint resources with Mockoon; update `kustomization.yaml` and `README.md`
- [x] `examples/incident-escalation/` — audit for MockEndpoint usage; update if present (no MockEndpoints found — clean)
- [x] `examples/nightly-export/` — audit for MockEndpoint usage; update if present

### Phase 3 — Update existing guides

- [x] `docs/guides/getting-started.md` — replace all MockEndpoint steps with Mockoon equivalent; update every `kubectl apply` command and expected output block

---

## 5. `type: http` Integration

> **Implement before Examples 7-9.** Without this, examples must embed credentials (Slack
> webhook URLs, API tokens) inline in Flow specs. Building examples that way means updating
> all manifests and READMEs again when `type: http` lands. Build it once, correctly.
>
> Completed examples (github-autolabel, slack-router, nightly-export) also have inline
> credentials — update them after this is implemented.

- [x] Add `http` to the `IntegrationSpec.Type` enum in `api/v1alpha1/integration_types.go`
- [x] Add `HttpIntegrationSpec` struct: `baseUrl`, `auth` (types: `bearer`, `basic`, `apiKey`, `secretUrl`), `defaultHeaders`, auth `secretRef` fields
- [x] Add `integrationRef` field to `HTTPAction` in `api/v1alpha1/flow_types.go`; controller merges Integration auth headers before making the step request
- [x] Add `get` on `integrations` to RBAC markers in `flowrun_controller.go` (secrets `get` already present); run `make manifests` (RBAC marker was already present; agent added secrets `get` marker which was missing)
- [x] Run `make generate && make manifests`
- [x] Add sample CR `config/samples/automation_v1alpha1_integration_http.yaml`
- [x] Update `docs/api/integration.md` with the new type, fields, and examples
- [x] Update completed examples to use `integrationRef`: `examples/nightly-export/` (Slack notify), `examples/github-autolabel/` (GitHub API token), `examples/slack-router/` (step-level credentials — N/A, only Mockoon URLs remain)

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
- [x] Create manifests in `examples/dlq-handler/`: Integration (kafka), Trigger (DLQ topic), Flow (log → re-publish → escalate-on-failure), Mockoon deployment for escalation endpoint
- [x] Create `examples/dlq-handler/README.md`: create Kafka topics (commands for Strimzi), applying manifests, producing a poison message to the DLQ, watching FlowRun, verifying message re-published, testing the escalation path

---

### Example 8 — Multi-Tenant Webhook Fan-Out

**External dependencies:**
- No external services required — example uses Mockoon as the three tenant endpoints

**What it demonstrates:** Single inbound webhook triggers parallel execution of 3 steps (same `runAfter` set), per-tenant credentials via `integrationRef` to `type: http` Integrations, `failurePolicy: Continue` so a failure for one tenant does not block others, per-step retry policies, FlowRun status shows all three outcomes independently.

**Implementation tasks:**
- [x] Create manifests in `examples/multi-tenant-fanout/`: Trigger (no auth — note production should use HMAC/bearer), Flow (3 parallel http steps with `integrationRef`), 3 `type: http` Integrations, Mockoon deployment
- [x] Create `examples/multi-tenant-fanout/README.md`: applying manifests, sending a single webhook, inspecting parallel step execution in FlowRun, simulating a 500 on one tenant to demonstrate `Continue` policy

---

### Example 9 — OIDC-Secured API Gateway Webhook

**External dependencies:**
- An OIDC provider — **Dex** (in-cluster, recommended for self-contained example), Keycloak, Okta, or any OIDC provider
- The JWKS endpoint must be reachable from within the cluster

**What it demonstrates:** OIDC/JWT authentication on a webhook trigger, JWKS background refresh (shared `jwk.Cache`), `requiredClaims` enforcement, extracting a claim value from the JWT payload, routing based on the claim.

**Implementation tasks:**
- [x] Create manifests in `examples/oidc-webhook/`: Dex deployment + config, Trigger (oidc auth with issuer + audience + requiredClaims), Flow (CEL branch on claim value), Mockoon deployment for each route
- [x] Create `examples/oidc-webhook/README.md`: Dex setup (Helm chart), obtaining a JWT via client credentials, calling the webhook with JWT, verifying FlowRun created, testing rejection with invalid token

---

### Example 6 — Kubernetes Resource Event → ITSM Ticket _(partially blocked)_

> **Partially blocked:** `type: resource` trigger is implemented but alpha-quality. Known bugs
> (naive pluralization, FlowRun name collision — see §11) must be fixed before the README
> can be updated to remove the alpha warning. Placeholder manifests are already complete.

**External dependencies:**
- A running cluster where the example namespace has Pods that can be set to `Failed` phase
- A mock HTTP server (Mockoon) as ITSM stand-in for the self-contained version

**What it demonstrates:** Kubernetes resource-event trigger (watches for Pod phase=Failed), extracting pod name and namespace from the event, opening a ticket via HTTP, deduplication via pod UID as FlowRun name idempotency key.

**Implementation tasks:**
- [x] Create manifests in `examples/k8s-pod-failure-ticket/`: KubernetesTrigger (placeholder spec), Flow (transform event → http ticket create), Mockoon deployment
- [x] Create `examples/k8s-pod-failure-ticket/README.md`: applying manifests, causing Pod failure, verifying FlowRun created; mark as `(requires kubernetes trigger type — not yet implemented)`

---

## 7. MockEndpoint Removal — Tests and Code

> Complete only after all examples (section 6) are updated to use Mockoon.
> Phases 4-5 are separated from Phases 1-3 so new examples can be built clean first.

### Phase 4 — Update tests

- [x] `internal/controller/mockendpoint_controller_test.go` — delete entire file
- [x] `internal/controller/suite_test.go` (or equivalent) — remove MockEndpoint type registration if present
- [x] `test/e2e/` — search for all MockEndpoint usage; replace with HTTP calls to Mockoon stub server deployed in the test namespace; update `BeforeSuite` setup if the test suite relies on the webhook gateway's `/mock/*` serving
- [x] `test/e2e/webhook_test.go` — update the E2E scenario (webhook → transform → http step → MockEndpoint → verify FlowRun Succeeded) to target Mockoon instead
- [x] Audit all test files: `grep -r "MockEndpoint\|mockendpoint\|mock-endpoint" --include="*.go"` — fix every hit

### Phase 5 — Remove code and CRD

> Do not begin until Phase 4 is complete and all tests pass.

- [x] Delete `api/v1alpha1/mockendpoint_types.go`; run `make generate && make manifests`
- [x] Delete `internal/controller/mockendpoint_controller.go`
- [x] Remove MockEndpoint controller registration from `cmd/main.go` (scheme + `SetupWithManager` call)
- [x] Remove `/mock/*` route handling from `cmd/webhook-gateway/main.go` and `internal/gateway/webhook/handler.go`
- [x] Remove MockEndpoint RBAC markers from all controllers; run `make manifests`
- [x] Delete `config/crd/bases/automation.kubezap.io_mockendpoints.yaml`
- [x] Delete `bundle/manifests/automation.kubezap.io_mockendpoints.yaml` (if present); regenerate bundle with `make bundle`
- [x] Update `charts/kubezap/crds/` to remove MockEndpoint CRD YAML
- [x] Run `go build ./...`, `go vet ./...`, `go test ./... -count=1` — all must pass
- [x] Update `docs/overview.md` CRD table: set MockEndpoint status to "Removed — see mocking guide"

---

## 8. Deployment & Distribution

> **Unblocked 2026-03-21.** OperatorHub submission is now a target, but gated on §12 architecture blockers and OLM readiness tasks below.

- [ ] OperatorHub submission PR — gates on §12 completion and OLM readiness tasks below

### OLM Readiness (required before submission)

- [x] **OLM** — Complete required CSV fields in `bundle/manifests/kubezap.clusterserviceversion.yaml`: `spec.description` (full feature overview), `spec.icon` (base64 PNG), `spec.maintainers`, `spec.provider.name`, `spec.maturity` (`alpha`), `spec.links` (docs, source). These are required for OperatorHub acceptance.
- [x] **OLM** — Run `operator-sdk bundle validate ./bundle` and fix all failures. Must pass before submission.
- [x] **OLM** — Run `operator-sdk scorecard ./bundle` against a live cluster and fix all failures. Both `basic` and `olm` suites must pass.
- [x] **OLM** — Add resource trigger RBAC caveat to CSV description: in AllNamespaces mode, user-configured `type: resource` triggers may require the controller SA to have broad watch permissions on target resource types. Users must grant these explicitly.

---

## 9. Observability — Port and TLS Normalization

> **Context**: The controller currently defaults to HTTPS on `:8443` (cert-manager-issued TLS) while gateways default to HTTP on `:8080`. The webhook gateway also shares port `:8080` between the hook server (`/hooks/*`) and the metrics endpoint (`/metrics`). This section standardises all three components: HTTP default everywhere, dedicated metrics port `:9090` (standard Prometheus port) for all, with optional TLS on the metrics server.

### Implementation

- [x] **Controller — default metrics to HTTP on `:9090`** (`cmd/main.go`):
  Change `--metrics-secure` default from `true` to `false`. Change `--metrics-bind-address` default and help text to suggest `:9090`. Update inline comment/example that currently references `:8443`. Verify cert-watcher setup is still initialised only when `--metrics-secure=true` and `--metrics-cert-path` is provided — no regression in TLS-on path.

- [x] **Webhook gateway — split metrics onto dedicated port** (`cmd/webhook-gateway/main.go`):
  Add `--metrics-port` flag (default `9090`). Remove `/metrics` route from the main mux (port 8080) and start a second `http.Server` bound to `--metrics-port` serving only `GET /metrics` (Prometheus handler). Add `--metrics-tls-cert-file` and `--metrics-tls-key-file` flags; when both are set, the metrics server upgrades to HTTPS. Main hook server (port 8080) TLS flags (`--tls-cert-file`, `--tls-key-file`, `--mtls-ca-file`) are unchanged.

- [x] **Kafka gateway — add dedicated metrics server** (`cmd/kafka-gateway/main.go`):
  Add `--metrics-port` flag (default `9090`). Start an HTTP server on that port serving `GET /metrics`. Add `--metrics-tls-cert-file` and `--metrics-tls-key-file` flags for optional TLS. Mirror the pattern from the updated webhook gateway implementation.

- [x] **Update Kubernetes manifests**:
  In `config/` (Deployment args, Service port definitions, ServiceMonitor port names) update any hardcoded `:8443` metrics references to `:9090`. Add a named `metrics` port (9090) to the controller Service alongside the existing `webhook` port. Add a named `metrics` port (9090) to the webhook-gateway and kafka-gateway Services. Ensure `config/rbac/` and generated role YAML are unaffected (metrics serving requires no additional RBAC).

- [x] **Remove cert-manager metrics dependency from controller**:
  Since the controller metrics endpoint now defaults to HTTP, the cert-manager Certificate and Issuer resources provisioned for controller metrics (if any exist in `config/certmanager/` or `config/default/`) should be made optional or removed. Retain the cert-manager webhook TLS resources — those are unaffected. Run `make manifests` after any marker changes.

- [x] **Run `make generate && make manifests && go build ./...`** and verify no regressions.

### Documentation

- [x] **Update `docs/guides/observability.md` — metrics port table**:
  Change controller row from `:8443 HTTPS (cert-manager)` to `:9090 HTTP (default); optional HTTPS via `--metrics-tls-cert-file`/`--metrics-tls-key-file``. Change gateway rows from `:8080` to `:9090`. Remove the note about shared port on the webhook gateway; add a note that the hook server remains on `:8080` and the metrics server is now a separate process on `:9090`.

- [x] **Update `docs/guides/observability.md` — ServiceMonitor templates**:
  Controller ServiceMonitor: change `scheme: https` → `scheme: http`; remove `tlsConfig` block; update port name to `metrics` on `9090`. Add a short note explaining that HTTPS can be re-enabled by setting `--metrics-secure=true` and providing cert-manager certs. Webhook gateway and Kafka gateway ServiceMonitors: update port number to `9090` (no scheme change needed — already `http`).

- [x] **Update `docs/guides/observability.md` — configuration flag reference**:
  Add a new "Metrics Server Configuration" table listing `--metrics-bind-address` / `--metrics-port`, `--metrics-secure` (controller only), `--metrics-tls-cert-file`, `--metrics-tls-key-file` across all three components, their defaults, and a short description.

- [x] **Audit `docs/architecture.md` and `docs/overview.md`** for any mention of `:8443` or shared-port metrics; update to reflect the new ports.

---

## 11. Bug Fixes — 2026-03-21 Review

- [x] **BUG** — Add `case "wait":` to `validateFlowSpec()` in `internal/controller/flow_controller.go` (lines ~107–129); Flow resources with `action.type: wait` currently fail admission even though the schema and runtime support it. Add Ginkgo test in `internal/controller/flowrun_controller_test.go` covering wait step timeout + requeue behavior.
- [x] **CLEANUP** — Remove stale TODO comment (lines 48–53) in `internal/controller/trigger_controller.go`; ResourceWatcher wiring is already done in `cmd/main.go`.
- [x] **DOCS** — Resolve `docs/api/mock-endpoint.md` status: deleted (Q1 resolved 2026-03-21).
- [x] **BUG (alpha)** — Resource watcher naive pluralization (`resource_watcher.go:113`): `strings.ToLower(kind) + "s"` silently fails for irregular plurals (`Ingress`, `NetworkPolicy`, etc.). Fix: use discovery API to resolve correct plural form. Blocked on design decision (adds API server roundtrip at registration time). Design options documented in `docs/tech-debt/pending-input-required.md`.
- [x] **BUG (alpha)** — Resource watcher FlowRun name collision: timestamp has second precision, no random suffix. Two events for same resource+eventtype within one second → second FlowRun silently dropped. Fix: add `randomHex(4)` suffix (same fix pattern as R1 webhook bug).
- [x] **RELIABILITY (alpha)** — Resource watcher no retry on cache sync failure: goroutine exits permanently if sync times out. Fix: add backoff retry loop before exiting, or signal the TriggerReconciler to re-register.
- [x] **MISSING FEATURE (alpha)** — Resource triggers have no cooldown/rate-limit mechanism. Other trigger types have `maxInvocations`/`window`; resource triggers have no equivalent. Add `cooldown` field to `ResourceTriggerSpec`.
- [x] **DOCS** — Update `examples/k8s-pod-failure-ticket/README.md`: remove "not yet implemented" warning; add note that resource trigger is alpha with known limitations (link to `docs/tech-debt/`). Do after pluralization bug is fixed.
- [x] **DOCS** — Add `docs/contributing.md` link to `docs/overview.md` (done 2026-03-21 review pass).
- [x] **DOCS** — Clarify AMQP/NATS stability in `docs/api/integration.md`: headings say "_(beta)_" but both gateways are fully implemented and in examples. Either define what "beta" means (known limitations) or upgrade the label.
- [x] **TECH DEBT (Medium)** — Kafka producer pool (`kafkaProducers` map in `flowrun_controller.go`) has no TTL or health check. Stale connections survive indefinitely and are not detected until the next publish attempt fails. Add idle TTL eviction or a periodic health-check probe.
- [x] **TECH DEBT (Low)** — `type: http` Integration is fetched from the API server on every step execution (no per-reconcile caching). Adds unnecessary latency and load on the API server for Flows with many HTTP steps. Cache the Integration object for the lifetime of a single reconcile pass.
- [x] **TECH DEBT (Low)** — CEL environment init failure is cached permanently via `sync.Once` in `flowrun_controller.go`. A transient error at startup (e.g., missing CEL extension) permanently disables `when` evaluation for the pod lifetime. Replace with a re-initializable init path or log a clear fatal on startup failure.

---

---

## 12. Architecture Review — Pre-Submission Blockers (v0.4)

> Items from the 2026-03-21 architecture review that must be resolved before OperatorHub submission.
> Ordered: API changes first (§12a), then P0 security fix (§12c — moved before §12b per rule 7), then execution model (§12b), then cleanup (§12d).

### 12a — API: Promote `type: pubsub` to individual trigger types

> **Breaking change, but v1alpha1 is explicitly unstable. Do before submission to avoid a post-GA migration.**

- [x] **API** — Rename `type: pubsub` → individual trigger types `kafka`, `amqp`, `nats` in `TriggerSpec.Type` enum (`api/v1alpha1/trigger_types.go`). Update kubebuilder validation marker: `+kubebuilder:validation:Enum=webhook;cron;kafka;amqp;nats;resource`
- [x] **API** — Split `PubSubTrigger` struct into dedicated `KafkaTrigger`, `AmqpTrigger`, `NatsTrigger` structs, each with only their own fields. Add top-level `spec.kafka`, `spec.amqp`, `spec.nats` fields to `TriggerSpec` (mirroring the `spec.webhook`, `spec.cron`, `spec.resource` pattern). Remove `spec.pubsub`.
- [x] **API** — Run `make generate && make manifests` after type changes.
- [x] **API** — Update `internal/controller/trigger_controller.go` and `internal/controller/integration_controller.go` wherever `spec.PubSub` or `trigger.Spec.PubSub.Type` is referenced.
- [x] **API** — Update all gateway watchers (`internal/gateway/kafka/watcher.go`, `amqp/watcher.go`, `nats/watcher.go`) that read `trigger.Spec.PubSub.*` fields.
- [x] **API** — Update all example manifests and docs referencing `type: pubsub`.
- [x] **API** — Update `config/samples/` and `docs/api/trigger.md` spec reference.

### 12c — Security: Secret value redaction

> **P0 — must fix before any public or OperatorHub release. Moved before §12b per prioritization rule 7.**
> `$(secrets.name.key)` is substituted before HTTP calls. On failure, the resolved URL/headers/body (containing the secret value) is written to `StepRunStatus.Message` in the FlowRun, persisted in etcd, and visible to anyone with `kubectl get flowrun`.

- [x] **SECURITY (P0)** — Track which segments of URLs and header values originated from secret interpolation. Redact those segments in `StepRunStatus.Message` and any error strings passed to `r.failFlowRun()`. Pattern: replace secret-origin values with `[REDACTED]` after substitution but before use in error messages. File: `internal/controller/flowrun_controller.go` (`substituteVars`, `executeHTTPStep`, `executePublishStep`)
- [x] **SECURITY (P0)** — Add test coverage: assert that a failed HTTP step with a secret-bearing URL does NOT store the raw secret value in FlowRun status. File: `internal/controller/flowrun_controller_test.go`

### 12b — Architecture: FlowRun execution model

> **Current model executes all steps in a single reconcile loop (blocking goroutine for entire flow duration). Fix: one step per reconcile.**
> Also fixes the doc/implementation mismatch: parallel steps (same `runAfter`) are documented but run sequentially.

- [x] **ARCHITECTURE** — Refactor `flowrun_controller.go` `Reconcile()` to execute exactly one ready step per call, then return `ctrl.Result{Requeue: true}`. Steps that are already `Succeeded`/`Skipped`/`Failed` are skipped cheaply. When all steps are terminal, transition the FlowRun to its final phase. This frees the reconcile goroutine between steps and prevents starvation under load. File: `internal/controller/flowrun_controller.go`
- [x] **ARCHITECTURE** — Implement true parallel execution of steps with the same `runAfter` set. When multiple steps are simultaneously ready (all their `runAfter` deps satisfied and none yet started), launch them as goroutines within a single reconcile and collect results before updating status. This aligns the implementation with the documented behavior. File: `internal/controller/flowrun_controller.go`
- [x] **SCALABILITY** — Increase `--max-concurrent-flowruns` default from `10` to `25`. The bottleneck is API server writes (one per step), not CPU; 10 is too conservative for an enterprise-grade operator. Add tuning guidance to `docs/guides/` or `docs/architecture.md`. File: `cmd/main.go`
- [x] **TESTING** — Update Ginkgo tests for the new one-step-per-reconcile model. Multi-step flows will require multiple reconcile calls in tests; update test helpers accordingly. File: `internal/controller/flowrun_controller_test.go`

### 12d — Cleanup: Stale API fields

> Can be done as part of §12a (same file) or standalone after §12b.

- [x] **CLEANUP** — Remove the dead `Target *TargetResource` field from `TriggerSpec` (`api/v1alpha1/trigger_types.go` lines ~63-64). This field is superseded by `Resource *ResourceTrigger` and its presence is confusing. Run `make generate && make manifests` after removal. _(done as part of §12a)_

---

## 13. Observability Gaps (from architecture review) — COMPLETE

- [x] **OBSERVABILITY** — Add `kubezap_flowruns_active` gauge: number of FlowRuns currently in `Running` or `Pending` phase. Most useful metric for capacity planning, alerting, and HPA decisions on the controller. Files: `internal/metrics/metrics.go`, `internal/controller/flowrun_controller.go`
- [x] **OBSERVABILITY** — Add `kubezap_flowrun_queue_duration_seconds` histogram: time between FlowRun creation and first transition to `Running`. Measures controller queue backpressure. Files: `internal/metrics/metrics.go`, `internal/controller/flowrun_controller.go`
- [x] **OBSERVABILITY** — Propagate `traceparent` W3C header from inbound webhook HTTP request to the FlowRun `kubezap.io/traceparent` annotation. Currently the gateway trace and the controller execution trace are disconnected; this links them into a single end-to-end trace. File: `internal/gateway/webhook/handler.go`
- [x] **OBSERVABILITY** — Add webhook gateway request latency histogram: `kubezap_webhook_request_duration_seconds` labeled by `trigger` and `result` (accepted/rejected/rate_limited). File: `internal/gateway/webhook/handler.go`

---

## 14. UX Improvements (from architecture review)

- [x] **UX** — Implement full dot-path access in `$()` variable interpolation: `$(trigger.body.order.id)` should recursively traverse nested JSON, not silently return empty string. This is the most common evaluation complaint and a likely dealbreaker in demos. File: `internal/controller/flowrun_controller.go` (`substituteVars` function and callers)
- [x] **UX** — Add Ginkgo tests for nested dot-path access: `$(trigger.body.a.b.c)`, `$(trigger.body.arr.0)`, missing path returns empty string, non-object traversal returns empty string. File: `internal/controller/flowrun_controller_test.go`

---

## 15. Dashboard / Monitoring UI

> **Decision (2026-03-21):** Build both CLI and web UI. CLI first (lower effort, operator-day-to-day), web UI second (demo/stakeholder impact). Both read-only.

### Phase 1 — CLI `watch` command

- [x] **CLI** — Add `kubezap watch` subcommand (`cmd/kubezap/watch.go`): streams FlowRun events via the Watch API, renders a live terminal execution timeline (box-drawing characters, per-step status badges, elapsed duration, phase transitions). Register in `cmd/kubezap/main.go` `AddCommand` list.
- [x] **CLI** — Write tests for `watch` output formatting (unit tests against a fake Watch stream, assert terminal output structure). File: `cmd/kubezap/watch_test.go`
- [x] **DOCS** — Add `kubezap watch` to `docs/guides/using-the-cli.md`.

### Phase 2 — Read-only web dashboard

> **Design:** See `docs/design/dashboard.md` for full spec (stack, API contract, component tree, SSE protocol, auth decisions).
> **Stack:** Vue 3 + Vite + `go:embed` embedded in operator binary. SSE for live updates. No auth (port-forward model). Read-only.

#### Step 1 — Build pipeline

- [x] **BUILD** — Scaffold Vue 3 + Vite project in `ui/`: `npm create vue@latest ui` (select Router, no Pinia, no testing framework), add Tailwind CSS. Configure `vite.config.js` with `base: '/ui/'` and `outDir: '../ui/dist'`.
- [x] **BUILD** — Add `make ui` target: `cd ui && npm ci && npm run build`. Add `make build` dependency on `make ui`. Add `ui/node_modules/` and `ui/dist/` to `.gitignore`.

#### Step 2 — Go API layer

- [x] **API** — Create `internal/ui/api.go`: JSON handlers for `GET /api/v1/namespaces`, `GET /api/v1/:ns/flowruns` (with `?phase`, `?trigger`, `?flow`, `?limit`, `?since` query params), `GET /api/v1/:ns/flowruns/:name`, `GET /api/v1/:ns/triggers`, `GET /api/v1/:ns/flows`. Use the manager's `client.Client`. Response types defined in spec.
- [x] **API** — Create `internal/ui/sse.go`: `GET /api/v1/events?namespace=<ns>` SSE endpoint. Watches FlowRuns via `client.Watch`, writes `event: flowrun\ndata: <json>\n\n` on each event, flushes via `http.Flusher`, closes on client disconnect.
- [x] **API** — Create `internal/ui/server.go`: embeds `ui/dist` via `//go:embed dist`, serves Vue SPA at `/ui/*` (with SPA fallback to `index.html`), registers all API routes. Exports `StartUIServer(ctx, client, port, bearerToken string)`.
- [x] **API** — Write `internal/ui/api_test.go` and `internal/ui/sse_test.go`: HTTP response codes, `Content-Type` headers, JSON shape assertions using a fake controller-runtime client.

#### Step 3 — Wire into operator

- [x] **OPERATOR** — Add `--ui-port` flag to `cmd/main.go` (default `0` = disabled) and `--ui-bearer-token` flag (default empty = no auth). When `--ui-port > 0`, call `ui.StartUIServer` after manager start. Add `ui` named port (8082) to `config/default/` Service.

#### Step 4 — Vue: scaffold + FlowRun list

- [x] **VUE** — Scaffold `App.vue`, `NavBar.vue` (with `NamespaceSelect` calling `/api/v1/namespaces`), Vue Router with routes for all four views. Fetch namespace list on mount; default to first namespace or query-param override.
- [ ] **VUE** — Implement `FlowRunList.vue`: fetches `/api/v1/:ns/flowruns`, `FilterBar.vue` (phase/trigger/flow dropdowns + since picker), `FlowRunTable.vue` + `FlowRunRow.vue`, `PhaseChip.vue` (colour-coded badge), `RelativeTime.vue` (updates every 10s), `DurationCell.vue`.
- [ ] **VUE** — Add SSE to `FlowRunList`: `composables/useFlowRunEvents.ts` using `EventSource`. Merge incoming events into a reactive `Map<name, FlowRunSummary>` so in-flight updates appear without a full reload.

#### Step 5 — Vue: FlowRun detail

- [ ] **VUE** — Implement `FlowRunDetail.vue`: fetches `/api/v1/:ns/flowruns/:name`, `FlowRunHeader.vue` (name, trigger→flow link, phase, elapsed), `StepTimeline.vue` + `StepRow.vue`, `StepBadge.vue` (✓ ✗ ● ○ - matching CLI watch badges). SSE on the detail page re-fetches the single FlowRun on each event matching the viewed name.

#### Step 6 — Vue: trigger + flow lists

- [ ] **VUE** — Implement `TriggerList.vue` + `TriggerRow.vue` + `TriggerTypeChip.vue`: fetches `/api/v1/:ns/triggers`, shows type badge, ready status, last-fired time, active FlowRun count.
- [ ] **VUE** — Implement `FlowList.vue` + `FlowRow.vue`: fetches `/api/v1/:ns/flows`, shows step count, ready status, last-used time.

#### Step 7 — Docs + service manifest

- [ ] **DOCS** — Write `docs/guides/dashboard.md`: enabling `--ui-port`, port-forward access pattern, `--ui-bearer-token` for optional Ingress exposure, kube-rbac-proxy sidecar pattern for production auth.

### Phase 3 — Future (Tier 3, deferred)

> Design notes in `docs/design/dashboard.md` § "Phase 3 — Future Plans".

- [ ] **FUTURE** — Integration health page (`/api/v1/:ns/integrations`, `IntegrationList.vue`)
- [ ] **FUTURE** — Activity graph: FlowRun rate over time from in-process Prometheus registry
- [ ] **FUTURE** — Search: `?q=` substring filter on trigger/FlowRun name
- [ ] **FUTURE** — OIDC auth (`--ui-oidc-issuer` etc.) or document kube-rbac-proxy as the recommended production auth path

---

## 10. Future / Backlog

- [x] `docs/guides/using-the-cli.md` — CLI user guide (created 2026-03-20 review pass)
- [x] `docs/guides/cron-triggers.md` — cron trigger how-to: schedule syntax, timezones, FlowRun naming, GC policy (created 2026-03-20 review pass)
- [x] `docs/guides/troubleshooting.md` — consolidated troubleshooting guide: controller startup, trigger acceptance, webhook routing, FlowRun lifecycle, CEL errors, MockEndpoint, Kafka, RBAC, CLI (created 2026-03-20 review pass)
- [x] `docs/guides/amqp-setup.md` — write full AMQP setup guide (stub exists)
- [x] `docs/guides/nats-setup.md` — write full NATS setup guide (stub exists)
- [x] Kubernetes resource-event trigger type (`type: resource` — dynamic informers in controller; see `docs/architecture.md#kubernetes-resource-event-triggers` for design; required for Example 6)
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows (cross-namespace FlowRun)
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [x] Web UI for flow monitoring — promoted to active §15 (both CLI watch command and read-only web dashboard)
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source
