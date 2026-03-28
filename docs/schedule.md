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
6. **Example 6 (K8s ITSM) last among examples** — blocked on `type: resource` trigger stabilization (bugs tracked in §16 P1/P2).
7. **P0 security fixes before architectural refactors in the same code area** — a security vulnerability should not be blocked waiting for a large refactor even if the refactor would reduce rework. Fix the vulnerability now; port the fix after the refactor if needed.
8. **Public readiness (§16) gates OperatorHub submission (§1)** — all P0 items in §16 must be complete before the OperatorHub submission PR is opened. P1 items should be resolved first; P2 items are nice-to-have.
9. **Security hardening (§17) gates OperatorHub submission** — §17 P0 items (SSRF, cross-namespace authz, secrets RBAC) must be resolved before public release. Several items require owner input on design direction — see `docs/tech-debt/pending-input-required.md`.
10. **§17 P0 security changes before §16 P1 manual E2E validation pass** — the manual E2E pass is a validation exercise for the complete system. Running it before §17 P0 security changes are in place means running it twice. Implement all §17 P0 items first, then do the validation pass.

---

## 1. Deployment & Distribution

> **Unblocked 2026-03-21.** OperatorHub submission is now a target, but gated on §16 items and OLM readiness tasks below. **Paused 2026-03-22 pending §16 completion.**
> GitHub org migration to `kubezap/kubezap-operator` complete (2026-03-22). Module path is `github.com/kubezap/kubezap-operator`.

- [ ] OperatorHub submission PR — gates on §16 P0 completion and OLM readiness tasks below

### Release Process (document and automate before first public release)

> Define the full release runbook so that cutting a release is a single documented procedure rather than ad-hoc steps.

- [x] **INFRA** — Set up GitHub Actions CI workflow (`.github/workflows/ci.yml`): runs on every push and PR; jobs: `lint` (`make lint`), `test` (`make test`), `build` (all five binaries), `docker-build` (build but don't push — validates Dockerfiles). Gate PRs on all jobs passing. Add badge to `README.md`.
- [x] **RELEASE** — Write `docs/releasing.md` (internal): step-by-step release runbook covering: (1) version bump in `go.mod`, `Chart.yaml`, CSV `spec.version`/`spec.replaces`, `CHANGELOG.md`; (2) `git tag vX.Y.Z` + push tag; (3) GoReleaser publish (`goreleaser release --clean`) → GitHub Release + CLI binaries; (4) GHCR image push; (5) Helm chart publish (OCI push to `ghcr.io/kubezap/charts/kubezap` or GitHub Pages chart repo); (6) OLM bundle regeneration (`make bundle`) + OperatorHub PR update; (7) post-release smoke test checklist.
- [x] **INFRA** — Add GoReleaser GitHub Actions workflow (`.github/workflows/release.yml`): triggers on `vX.Y.Z` tag push; builds CLI binaries for linux/darwin/windows amd64+arm64; builds and pushes all five container images to `ghcr.io/kubezap/*`; creates GitHub Release with changelog and binary attachments.
- [x] **INFRA** — Publish Helm chart: decide on distribution mechanism (OCI registry at `ghcr.io/kubezap/charts/kubezap` vs. GitHub Pages `helm repo`); add chart publish step to release workflow; document `helm repo add` or `helm install --oci` install path in `docs/overview.md`.
- [x] **INFRA** — Add `CHANGELOG.md` covering v0.1 → v0.3 milestones (required for enterprise evaluators and acquisition targets; also referenced from OperatorHub CSV `spec.replaces` chain). Follow Keep a Changelog format.
- [x] **INFRA** — Automate GHCR image publishing on merge to `main` (`:latest` tag) in addition to version tags, so contributors can always pull a fresh build without building locally.

### OLM Readiness

> All four items below were completed before OperatorHub submission was paused. OLM bundle passes `bundle validate` and `scorecard` as of 2026-03-21.

- [ ] OperatorHub submission PR — see top of §1 above.

---

## 15. Dashboard / Monitoring UI

> **Complete (2026-03-21).** CLI `watch` command and read-only Vue web dashboard both shipped. Phase 3 items below are deferred.

### Phase 3 — Future (Tier 3, deferred)

- [ ] **FUTURE** — Integration health page (`/api/v1/:ns/integrations`, `IntegrationList.vue`)
- [ ] **FUTURE** — Activity graph: FlowRun rate over time from in-process Prometheus registry
- [ ] **FUTURE** — Search: `?q=` substring filter on trigger/FlowRun name
- [ ] **FUTURE** — OIDC auth (`--ui-oidc-issuer` etc.) or document kube-rbac-proxy as the recommended production auth path

---

## 16. Pre-Public Readiness — 2026-03-22 Review

> Items from the pre-public readiness audit. Ordered P0 → P1 → P2.
> OperatorHub submission is **paused** pending this section's completion.

### P0 — Blocks public release

- [x] **TESTING** — E2E tests are failing. Triage failures (`make test-e2e`), determine if pre-existing or recent regressions, and fix. Must pass before public release.
- [x] **REPO HYGIENE** — Gitignore all Claude-related files before public availability. Currently `.gitignore` excludes `.claude/*` but re-includes `settings.json`, hooks, skills, commands, agents, and agent-memory. Decide which (if any) to retain for contributors; for a clean first-public commit, exclude everything under `.claude/`.
- [x] **BUG** — `internal/controller/resource_watcher.go:95`: watcher goroutine context derived from `context.Background()` instead of manager lifecycle context. Goroutine leaks on shutdown; prevents clean controller-manager teardown. E2E test hangs. Fix: derive context from manager or register as `mgr.Add()` Runnable. Evidenced by `docs/review-latest.md`.

### P1 — Should fix before public

- [x] **SECURITY** — `internal/gateway/webhook/handler.go`: bearer token and API key comparisons use plain string `!=` (not constant-time). HMAC already uses `hmac.Equal`. Fix: use `subtle.ConstantTimeCompare` for `bearer` and `apiKey` auth types. Must be fixed before the WebhookAuth struct restructure below, as both touch the same handler.
- [x] **DOCS/API** — `docs/api/trigger.md` + `docs/guides/webhook-security.md` + `api/v1alpha1/trigger_types.go` + `internal/gateway/webhook/handler.go`: **Decision made (2026-03-22):** Restructure Go types to use nested structs (`HMACConfig`, `BearerConfig`, `OIDCConfig`, `APIKeyConfig`, `IPAllowlistConfig`, `MTLSConfig`) inside `WebhookAuth`. Update handler to read from new nested fields. Update all YAML examples in docs to match new nested structure. Run `make generate && make manifests` after type changes.
- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. Do this after e2e tests are green. **Run after all §17 P0 items are complete** — validation before the security refactors (HTTP executor split, OwnNamespace default, FlowRef removal) would need to be re-run after, so sequence this last among P1 items.
- [x] **DOCS** — Improve `docs/contributing.md`: add architecture orientation section (binary layout, reconciler entry points, gateway entry points, key packages); add "first contribution" guide (good-first-issue labels, how to run a single test, how to add a new step action type); add section on running the UI locally (`make ui && go run cmd/main.go --enable-ui`); add section on generating and validating OLM bundle.
- [x] **DOCS** — **User-facing documentation rewrite (Opus research task).** Current `docs/` tree is development-oriented. Commission an Opus-model deep-dive to: (1) audit every doc file and classify as user-facing vs. internal/dev-only; (2) design a two-tier structure (`docs/` user-facing, `docs/dev/` internal); (3) draft a rewrite plan for each user-facing doc; (4) produce a priority-ordered implementation list. Research only — implementation is a separate task. **Prompt:** `/research` with scope: "audit all docs for user-facing vs internal classification, design a two-tier doc structure, and produce a rewrite plan ordered by user impact."
- [x] **DOCS** — `docs/guides/observability.md` metrics table: reconciled against actual registered metrics; phantom metrics removed, real metrics documented correctly.
- [x] **DOCS** — `pubsub` terminology: global replace pass complete across all 9 affected doc files; `type: pubsub` + nested `pubsub:` replaced with top-level `type: kafka/amqp/nats`.
- [x] **DOCS** — `README.md`: features section updated to include AMQP, NATS, resource trigger, web dashboard, CLI. Stale "coming in v0.3" text removed.
- [x] **DOCS** — `docs/api/flowrun.md`: resource trigger FlowRun naming pattern corrected to match `resource_watcher.go` implementation; `_(alpha)_` label already correct.
- [x] **BRANDING** — CSV `bundle/manifests/kubezap.clusterserviceversion.yaml`: version is `v0.0.1`, icon is a 1×1 placeholder PNG. Update version to match actual release; create a real icon (≥64×64).
- [x] **DOCS** — `docs/overview.md` line ~493: removed false XPath claim from `resultMappings` docs; JSONPath only.
- [x] **BUG** — `internal/controller/resource_watcher.go` line ~113: naive pluralization fixed — uses discovery API with `+s` fallback and warning log. Fixed in PR #48 (`backlog/resource-watcher-fixes`).
- [x] **BUG** — `internal/controller/resource_watcher.go` line ~207: FlowRun name collision fixed — 4-char `crypto/rand` hex suffix added. Fixed in PR #48 (`backlog/resource-watcher-fixes`).
- [x] **INFRA** — Gateway Deployments (webhook, kafka, amqp, nats) missing `livenessProbe`/`readinessProbe`. OperatorHub scorecard requires probes on managed Deployments. Target `GET /healthz` on each gateway's configured port. Evidenced by `docs/review-latest.md`.
- [x] **SECURITY** — Restrict secrets RBAC to operator namespace (OwnNamespace default). ClusterRole grants `get;list;watch` on secrets cluster-wide; default install should use namespace-scoped Role. **Resolved: subsumed by §17 P0 "Default to OwnNamespace + label-restricted AllNamespaces" (completed 2026-03-24).**

### P2 — Nice to have before public

- [x] **DOCS** — `docs/architecture.md`: Component Overview table and ASCII diagram list only 3 components; AMQP and NATS gateways are missing. Add them.
- [x] **DOCS** — `docs/architecture.md` line 321: "KEDA integration planned for v0.3" — v0.3 is complete. Fixed: updated to "KEDA is supported for partition-bounded scaling of Kafka gateways."
- [x] **CLEANUP** — Remove Kubebuilder scaffold boilerplate: `// TODO(user): If you enable certManager...` comment in `cmd/main.go`; `// EDIT THIS FILE! THIS IS SCAFFOLDING FOR YOU TO OWN!` in `api/v1alpha1/trigger_types.go`.
- [x] **DOCS** — `docs/overview.md` Getting Started link uses `../examples/order-router/` — relative path may break on hosted doc sites. Verify or use absolute GitHub link.
- [x] **BUG** — `internal/controller/resource_watcher.go`: no retry when informer cache sync fails (transient RBAC issue or API server blip exits the watcher goroutine permanently). Trigger stays "registered" but is dead until reconciler re-registers on next Trigger touch. Add retry with backoff. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- [x] **BUG** — `internal/controller/resource_watcher.go`: no cooldown mechanism. Rapidly-updated resources (e.g., Pod status churn) with no `watchFields` filter create a FlowRun on every update. Add `maxInvocations`/`window` rate-limiting consistent with other trigger types. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- [x] **TECH DEBT** — `internal/controller/flowrun_controller.go`: Kafka producer pool (`kafkaProducers` map) has no TTL or health check; stale connections not detected until next publish attempt fails. Add periodic health check or TTL eviction (implemented: `kafkaProducerIdleTTL = 10m`). Evidenced by `docs/review-latest.md`.
- [x] **TECH DEBT** — `internal/controller/flowrun_controller.go`: CEL environment init failure cached forever via `sync.Once` — a transient failure permanently disables CEL evaluation for the pod lifetime. Replace with retriable init that resets on failure. Evidenced by `docs/review-latest.md`.
- [x] **BUG** — `internal/controller/flowrun_controller.go` publish step: response body discarded on 4xx/5xx, making failures undebuggable from FlowRun status. Capture up to 1KB of error body in step message, consistent with HTTP step. Evidenced by `docs/review-latest.md`.
- [x] **TESTING** — Add E2E test for full `publish` → Kafka Integration path (Trigger → FlowRun → publishStep → Kafka producer). Current E2E suite covers HTTP steps only.
- [x] **OBSERVABILITY** — Add `kubezap_when_expression_errors_total{flow,reason}` Prometheus counter for CEL `when` evaluation failures. Currently errors are logged but not metered; makes per-flow skip-rate invisible at scale.
- [x] **TECH DEBT** — `internal/controller/flowrun_controller.go` ~line 402: `goto allStepsDone` for early loop exit. Replace with named helper function or structured break. Evidenced by `docs/review-latest.md`.
- [x] **TESTING** — Add feature-matrix E2E test suite: one focused test per functional axis (step types: http/transform/wait/publish; trigger types: cron/webhook/resource; flow control: when/onFailure/retry/chaining; integration auth: secretUrl/bearer; FlowRun lifecycle: TTL/timeout). Kafka/AMQP/NATS tests marked Pending (require external broker). Goal: if each axis passes, all examples work by composition. Use shared Mockoon fixture; purpose-built minimal testdata, not example kustomizations.

---

## 17. Security Hardening — 2026-03-24 Review

> Items from the security design review (`docs/security-review-2026-03-24.md`). Ordered P0 → P1 → P2.
> All design decisions resolved (2026-03-24) — see `docs/tech-debt/pending-input-required.md` §2026-03-24.

### P0 — Blocks public release

- [x] **SECURITY** — SSRF protection for HTTP step URLs. Block private IP ranges (RFC1918, link-local, loopback), cloud metadata IPs (169.254.169.254), and `.svc.cluster.local`. Resolve DNS before connecting; reject if resolved IP is in blocked range. Add `--http-step-blocked-cidrs` flag for customization. See §C1. **Decision (Q5): blocklist approach.**
- [x] **SECURITY/ARCH** — **[1/6] HTTP executor: define API contract.** Write `docs/dev/http-executor.md`: JSON schema for `POST /execute` request (method, url, headers, body, timeout, tlsSkipVerify) and response (statusCode, headers, body up to 4KB, error string). Document error codes (ssrf_blocked, dns_error, timeout, upstream_error). Document NetworkPolicy shape and mTLS handshake. No code changes. Gate: this doc must be approved before any implementation chunks begin.
- [x] **SECURITY/ARCH** — **[2/6] HTTP executor: binary scaffold + SSRF handler.** Create `cmd/http-executor/main.go` (flags: `--port`, `--blocked-cidrs`, `--mtls`). Create `internal/executor/http/` package. Implement `POST /execute` handler: DNS resolution, SSRF blocklist check (port the existing blocklist from `flowrun_controller.go`), outbound HTTP call, structured JSON response. SSRF blocklist must be the primary implementation source; controller copy removed in chunk 5. Add `/healthz` endpoint. Unit tests covering blocked IPs, allowed URLs, timeout.
- [x] **SECURITY/ARCH** — **[3/6] HTTP executor: controller manages Deployment.** Add reconciler logic (extend `internal/controller/` or new `executor_reconciler.go`) to create/update one `http-executor` Deployment per namespace. Create accompanying NetworkPolicy restricting ingress to the controller pod's IP only. Add RBAC markers. Add `--executor-image` flag to `cmd/main.go`. Update Helm `values.yaml` with `executor.image` and `executor.resources`.
- [x] **SECURITY/ARCH** — **[4/6] HTTP executor: FlowRun controller uses RPC.** In `flowrun_controller.go`, replace inline HTTP step execution with a call to the executor's `POST /execute` RPC. Resolve secrets in-memory (as today); pass fully-resolved request body to executor over plain HTTP to the in-cluster Service. Remove SSRF blocklist logic from controller (now owned by executor). Update HTTP step status mapping from executor JSON response.
- [x] **SECURITY/ARCH** — **[5/6] HTTP executor: mTLS (opt-in).** Add `--executor-mtls=true` flag. When enabled: controller generates a self-signed CA + leaf cert pair at startup, injects client cert into FlowRun controller TLS config, mounts CA + server cert into executor Deployment as a projected Secret. Executor verifies client cert on TLS handshake. Document in `docs/dev/http-executor.md`. Off by default; service-mesh users skip this.
- [x] **SECURITY/ARCH** — **[6/6] HTTP executor: E2E test + docs update.** Add E2E test: HTTP step routed through executor (assert step completes; assert SSRF-blocked URL returns step failure). Update `docs/architecture.md` Component Overview table and ASCII diagram to include the executor. Update `docs/overview.md` HTTP step section to note executor isolation. Add `config/samples/` NetworkPolicy sample.
- [x] **SECURITY** — Remove cross-namespace FlowRef. Delete `FlowRef.Namespace` field from `api/v1alpha1/flowrun_types.go`. Controller always uses FlowRun's own namespace to fetch the Flow. Add validation webhook to reject FlowRuns with non-empty `flowRef.namespace`. Run `make generate && make manifests`. Update docs. Cross-namespace flows deferred to v1beta1 with FlowGrant CRD. See §C2. **Decision (Q1): remove entirely.**
- [x] **SECURITY** — Default to OwnNamespace + label-restricted AllNamespaces. Change `WATCH_NAMESPACES` default behavior: empty = OwnNamespace (operator's own namespace only). New env var value `WATCH_NAMESPACES=*` for AllNamespaces mode, which restricts secrets RBAC to namespaces labeled `kubezap.io/managed=true`. Update `cmd/main.go` cache setup, RBAC markers, ClusterRole, and Helm chart values. See §C3. **Decision (Q4): OwnNamespace default + label restriction.**

### P1 — Should fix before public

- [x] **SECURITY** — Webhook auth admission warning. Add ValidatingWebhookConfiguration that emits an admission warning (not rejection) when a Trigger with `type: webhook` is created/updated without `spec.webhook.auth`. Non-breaking; raises visibility. See §H1. **Decision (Q2): warn, don't reject.**
- [x] **SECURITY** — Redact sensitive data from FlowRun TriggerData. Webhook body and non-standard auth headers persist in FlowRun objects readable by namespace users. Add configurable header redaction list and optional body truncation (`spec.webhook.redactBody`). See §H2.
- [x] **SECURITY** — Secret access audit logging. Add structured log entry + Prometheus counter (`kubezap_secret_accesses_total`) for every `$(secrets.*)` fetch during FlowRun execution. Required for PCI-DSS/SOC2 compliance. See §H3.
- [x] **SECURITY** — CEL expression cost limits. Add `cel.CostLimit()` budget to `evaluateWhen()`. Add `--cel-cost-limit` flag (default 10000). Prevents DoS via combinatorial comprehensions. See §H5.
- [x] **SECURITY** — FlowRun creation rate limiting. Add `spec.webhook.rateLimit` (requests per window) to Trigger CRD. Gateway enforces locally; controller enforces globally via FlowRun count. See §M1.

### P2 — Nice to have before public

- [x] **SECURITY** — Plugin image digest pinning. Add optional `spec.plugin.imageDigest` field to Integration CRD. When set, operator validates resolved digest matches before creating/updating plugin Deployment. See §H4. **Decision (Q3): optional field.**
- [x] **SECURITY** — Consistent header redaction across all gateways. Extract webhook redaction logic to shared `internal/gateway/redact` package. Apply to Kafka and AMQP gateways. See §M3.
- [x] **SECURITY** — Ship example NetworkPolicies in `config/network-policy/`: controller egress, webhook gateway ingress/egress, plugin egress. Document in security guide. See §M4.
- [x] **DOCS** — Plugin-to-controller communication security. Document plain-HTTP limitation for `/publish` endpoint. Recommend service mesh (Istio/Linkerd) sidecar for sensitive deployments. See §M2.
- [x] **TECH DEBT** — `internal/controller/integration_controller.go` lines 109–180: three near-identical conditional blocks for kafka/amqp/nats gateway condition updates. Extract into a shared helper function to reduce duplication and maintenance burden. Evidenced by `docs/review-latest.md` (2026-03-22 LOW finding).

---

## 10. Future / Backlog

- [x] Kubernetes resource-event trigger type (`type: resource`) — implemented (alpha). Four known bugs tracked in §16 P1/P2. Example 6 (K8s ITSM) is blocked on pluralization fix.
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows — deferred to v1beta1; requires FlowGrant CRD (like Gateway API ReferenceGrant) for cross-namespace authorization. `FlowRef.Namespace` removed in v1alpha1 per §17 P0.
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source
