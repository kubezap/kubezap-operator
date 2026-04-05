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
2. **Bug fixes before validation that exercises the same code** — running a manual E2E pass over broken behavior produces misleading results. Fix known bugs first.
3. **Prerequisites before dependents** — if item B requires a type, API, or behavior introduced by item A, A must come first.
4. **Tests before new feature development in the same area** — once an area has confirmed-correct behavior, test it before adding more features on top.
5. **P0 security fixes before architectural refactors in the same code area** — a security vulnerability should not be blocked waiting for a large refactor.
6. **Public readiness (§16) gates OperatorHub submission (§1)** — all P0 items in §16 must be complete before the OperatorHub submission PR is opened. P1 items should be resolved first; P2 items are nice-to-have.
7. **§18 P1 items before §16 P1 validation pass** — the VALIDATION item is a full E2E system exercise. Running it before §18 P1 items (status reporting bug, security checklist) gives incomplete results and may need to be re-run.
8. **§22 (automated e2e) before §19 (manual e2e)** — automated tests must pass before manual validation is meaningful. Fix e2e suite health first.
9. **§20/§21 research (code review, doc review) before §19 manual E2E** — code review may surface bugs that invalidate manual validation results; doc review may expose example incorrectness. Run both before the full manual pass.
10. **§23/§24 P0 fixes before §19 manual E2E** — the P0 findings from §20/§21 (code bugs, doc-reality mismatches) must be resolved before manual validation. E2E over broken or incorrectly documented behavior produces misleading results and may need to be re-run.

---

## 1. Deployment & Distribution

> **Unblocked 2026-03-21.** OperatorHub submission is now a target, but gated on §16 P1 VALIDATION (expanded in §19) and §18 P1 items. **Paused 2026-03-22 pending §16 completion.**
> GitHub org migration to `kubezap/kubezap-operator` complete (2026-03-22). Module path is `github.com/kubezap/kubezap-operator`.
> OLM bundle passes `bundle validate` and `scorecard` as of 2026-03-21.

- [ ] OperatorHub submission PR — gates on: §18 P1 items complete (✓), §22 VERIFY (e2e tests green), §20 code review clean, §21 doc review clean, §16 P1 VALIDATION (all §19 tasks complete)

---

## 15. Dashboard / Monitoring UI

> **Complete (2026-03-21).** CLI `watch` command and read-only Vue web dashboard both shipped. Phase 3 items below are deferred.

### Phase 3 — Future (Tier 3, deferred)

- [ ] **FUTURE** — Integration health page (`/api/v1/:ns/integrations`, `IntegrationList.vue`)
- [ ] **FUTURE** — Activity graph: FlowRun rate over time from in-process Prometheus registry
- [ ] **FUTURE** — Search: `?q=` substring filter on trigger/FlowRun name
- [ ] **FUTURE** — OIDC auth (`--ui-oidc-issuer` etc.) or document kube-rbac-proxy as the recommended production auth path

---

## 18. Code Quality — 2026-03-27 Review

> Items from the periodic health review. Ordered P1 → P2. **Run §18 P1 items before §16 P1 VALIDATION** (see prioritization rationale rule 7).

### P1 — Should fix before public (run before §16 VALIDATION)

- [x] **BUG** — `internal/controller/flowrun_controller.go` line ~442: `StepRunStatus.Attempts` hardcoded to 1 regardless of actual retry count. Thread attempt count through `executeHTTPStep` and publish step return path. Known TODO(T7). **Owner input needed** — see `docs/tech-debt/pending-input-required.md` §2026-03-27.
- [x] **DOCS** — `docs/api/trigger.md`: add alpha-stability callout to the Resource Trigger section (prominent warning, not just in overview.md). Users reading API docs without reading overview may not realise `type: resource` is alpha-quality.
- [x] **DOCS** — Write `docs/guides/security-checklist.md`: single operator-facing page linking all security considerations (webhook auth, SSRF, RBAC, NetworkPolicy, mTLS, OwnNamespace default, plugin image digest, CEL cost limits). Required before OperatorHub submission — evaluators expect a runnable security checklist.

### P2 — Nice to have before public

- [x] **TECH DEBT** — `internal/controller/resource_watcher.go`: missing kubebuilder RBAC markers for dynamic informer watches. Document dynamic watch requirements; add to RBAC generation.
- [x] **TECH DEBT** — `internal/controller/executor_reconciler.go`: executor Deployment missing `terminationGracePeriodSeconds`. Set to 30s or the configured step timeout, whichever is larger.
- [x] **TECH DEBT** — `internal/controller/flowrun_controller.go`: executor RPC transport failures should requeue with exponential backoff rather than immediately failing the step. **Owner input needed** — see `docs/tech-debt/pending-input-required.md` §2026-03-27.
- [x] **OBSERVABILITY** — `internal/controller/flowrun_controller.go` Kafka producer pool: add debug-level log events for cache hits, cache misses, evictions, and creation.
- [x] **TESTING** — `internal/controller/executor_mtls.go`: add test for concurrent bundle access during the 23h rotation window.
- [x] **TESTING** — `internal/controller/flowrun_controller.go` Kafka producer cache: add unit test for idle TTL eviction (10m).

---

## 16. Pre-Public Readiness — 2026-03-22 Review

> OperatorHub submission is **paused** pending this section's completion. Run §18 P1 items first (see §18 header).

### P1 — Should fix before public

- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. **Broken down into per-example tasks in §19 below** — all §18 P1 items are complete; validation can now proceed after §22 VERIFY and §20/§21 research complete.

---

## 17. Security Hardening — 2026-03-24 Review

> All items complete as of 2026-03-27.

---

## 22. E2E Test Fix — WATCH_NAMESPACES (2026-04-04)

> Root cause analysis in `docs/tech-debt/e2e-test-status-2026-04-04.md`.

- [x] **BUG** — `test/e2e/e2e_suite_test.go` BeforeSuite does not set `WATCH_NAMESPACES=*` before waiting for controller. Controller runs in OwnNamespace mode, never reconciles e2e test namespaces → all trigger tests time out. **Fixed (2026-04-04)**: added `kubectl set env deployment/kubezap-controller-manager WATCH_NAMESPACES=*` after `make deploy`.

- [x] **VERIFY** — Re-run `make test-e2e` to confirm all e2e tests pass with the WATCH_NAMESPACES fix. Fixed root cause: `ExecutorReconciler` was gated on FlowRun existence — executor Deployment never created before first FlowRun. Fix: removed FlowRun gate from `Reconcile`; added `Trigger` watch in `SetupWithManager` so executor is pre-provisioned as soon as a Trigger exists in a namespace (before any FlowRun). Also improved BeforeSuite readiness check from pod phase to container ready status.

---

## 20. Code Review — Full Codebase Bug Hunt (2026-04-04)

> Context doc: `docs/tech-debt/code-review-context-2026-04-04.md`.
> Run this as a research-only task (no code changes) first, then file schedule items for findings.
> Use **Opus model** for deeper reasoning on complex controller logic.
> Output: `docs/tech-debt/code-review-results-YYYY-MM-DD.md` + new schedule items.
> **Must precede §19 manual E2E** — findings may surface bugs that invalidate manual validation results (rule 9).

- [x] **RESEARCH** — Full codebase code review: logic bugs, design flaws, edge cases, security gaps beyond §17, performance issues, observability gaps. Focus on `internal/controller/` (flowrun_controller, resource_watcher, executor_reconciler, trigger_reconciler, executor_mtls). Cross-check behavior against API docs. Produce `docs/tech-debt/code-review-results-YYYY-MM-DD.md` and add findings as schedule items. See context doc for full methodology. **Complete (2026-04-04)**: Results in `docs/tech-debt/code-review-results-2026-04-04.md`. Findings filed as §23 schedule items.

---

## 21. Documentation Review — Quality and Completeness (2026-04-04)

> Context doc: `docs/tech-debt/doc-review-context-2026-04-04.md`.
> Run this as a research-only task first, then file schedule items for findings and fix LOW/MEDIUM issues inline.
> Use **Opus model** for thorough cross-referencing between code and docs.
> Output: `docs/tech-debt/doc-review-results-YYYY-MM-DD.md` + new schedule items.
> **Must precede §19 manual E2E** — doc review may expose example incorrectness (rule 9).

- [x] **RESEARCH** — Full documentation review: inaccuracies, missing coverage, broken links, example correctness, doc-reality mismatches. Cross-check all `docs/api/*.md` against `api/v1alpha1/*_types.go`. Verify CLI and dashboard docs exist. Produce `docs/tech-debt/doc-review-results-YYYY-MM-DD.md` and add findings as schedule items. See context doc for full methodology. **Complete 2026-04-04**: results in `docs/tech-debt/doc-review-results-2026-04-04.md`, 14 inline fixes applied, 7 HIGH + 4 P1 items filed as §24.

---

## 23. Code Review Findings — 2026-04-04

> Source: `docs/tech-debt/code-review-results-2026-04-04.md`
> P0 items must be fixed before public release. P1 before GA. P2 are nice-to-have.
> **Must precede §19 manual E2E** — P0 bugs in this section invalidate E2E results if not fixed first (rule 10).

### P0 — Fix before public release

- [x] **BUG** — `internal/controller/flowrun_controller.go:202`: Cancelled FlowRuns are exempt from GC (TTL and count-based). Add `"Cancelled"` to the GC phase check so they are garbage collected like Succeeded/Failed.
- [x] **SECURITY** — `internal/gateway/webhook/handler.go:181`: Basic auth credential comparison uses non-constant-time `!=`. Replace with `subtle.ConstantTimeCompare` to prevent timing side-channel attacks.

### P1 — Fix before GA

- [x] **PERFORMANCE** — `internal/controller/flowrun_controller.go:943`: mTLS-enabled executor calls allocate a new `http.Client`/`http.Transport` per call, defeating connection reuse and causing TLS handshake overhead. Create the mTLS client once at startup and reuse.
- [x] **BUG** — `internal/controller/flowrun_controller.go:612`: Post-completion failure check ignores `flow.Spec.FailurePolicy`. FlowRuns with flow-level `failurePolicy: Continue` may incorrectly transition to Failed after all steps complete.
- [x] **BUG** — `internal/controller/flowrun_controller.go:2079`: Kafka publish producer ignores TLS/SASL config from Integration spec. Only plaintext, unauthenticated Kafka clusters work for publish steps. Port TLS/SASL config from kafka/watcher.go.
- [x] **BUG** — `internal/controller/flowrun_controller.go:464`: Wait step `StartTime` is overwritten on re-entry (requeue), masking the actual start time. Preserve existing StartTime from prior status.
- [x] **TECH DEBT** — `internal/controller/resource_watcher.go:293`: cooldownTracker map grows without bound. Clear entries on `Deregister()`; add periodic eviction of entries older than cooldown duration.
- [x] **TECH DEBT** — `internal/controller/trigger_controller.go:119`: Webhook gateway resources (Deployment, Service, SA, Role, RoleBinding, HPA) are not cleaned up when the last webhook Trigger in a namespace is disabled or deleted. Add reference counting or periodic sweep.

### P2 — Nice to have

- [x] **BUG** — `internal/controller/flowrun_controller.go:1026`: Kafka publish step has no retry support. `RetryPolicy` from step spec is ignored; attempts always 1.
- [x] **BUG** — `internal/controller/flowrun_controller.go:1087`: Plugin publish step has no retry support. Same issue as Kafka publish.
- [x] **BUG** — `internal/gateway/webhook/handler.go:337`: Body truncated to 4096 bytes without setting `bodyTruncated` flag on TriggerData.
- [x] **BUG** — `internal/controller/executor_reconciler.go:151`: Executor container `--port` arg is hardcoded to 8091, not derived from `ExecutorPort` field. Custom port config is silently ignored.
- [ ] **VALIDATION** — `api/v1alpha1/trigger_types.go:277`: ResourceTrigger.Events has conflicting `MinItems=1` and `+optional` markers. Remove `MinItems=1` since the code handles empty gracefully.
- [ ] **VALIDATION** — `api/v1alpha1/flow_types.go:65`: FlowStep.Name lacks uniqueness validation. Duplicate step names cause undefined runtime behavior.
- [ ] **VALIDATION** — `api/v1alpha1/flow_types.go:205`: RetryPolicy.MaxRetries lacks `+kubebuilder:validation:Minimum=0`. Negative values cause zero-execution steps.
- [ ] **OBSERVABILITY** — `internal/gateway/webhook/handler.go:401`: Trace context lost on `context.Background()` fallback for FlowRun creation. Extract span context before checking Err().
- [ ] **TECH DEBT** — `internal/gateway/kafka/watcher.go:119`: `Start` returns `ctx.Err()` instead of nil on graceful shutdown, causing spurious error logs.

---

## 24. Documentation Review Findings — 2026-04-04

> Source: `docs/tech-debt/doc-review-results-2026-04-04.md`
> LOW/MEDIUM issues were fixed inline during the review. Only HIGH and P1 items are listed below.
> **Must precede §19 manual E2E** — P0 doc-reality mismatches in this section would invalidate manual validation of those features (rule 10).

### P0 — Fix before public release

- [ ] **DOC FIX** — `docs/guides/webhook-security.md`: HMAC/OIDC/Bearer/Basic auth field mismatch with Go types. Doc shows fields (`header`, `algorithm`, `prefix`, `encoding` on HMAC; `requiredClaims`, `jwksUri`, `jwksCacheTTL` on OIDC; `trustedProxies`; `secretRef` on Bearer vs `tokenSecretRef`) that don't exist in Go types. Either implement the fields or rewrite the security guide to match current API. Highest-impact doc-reality mismatch.
- [ ] **DOC FIX** — `docs/api/integration.md`: IntegrationStatus fields diverge from Go types. Doc lists `gatewayDeployments` ([]GatewayDeploymentRef), `connectedTriggers`, `phase: Failed` — Go types have `GatewayDeploymentName` (string), no `connectedTriggers`, `phase: Pending`. Align doc with Go types.
- [ ] **DOC FIX** — `docs/api/integration.md`: KafkaIntegrationSpec (`producerConfig`, `consumerConfig`) and PluginIntegrationSpec (`replicas`, `resources`, `config`, `imagePullSecrets`) documented but not in Go types. Remove phantom fields from docs or implement them.

### P1 — Fix before GA

- [ ] **DOC FIX** — `docs/api/integration.md`: Broken link to `docs/tech-debt/gateway-shutdown-correctness.md` (file does not exist). Create file or update reference.
- [ ] **DOC FIX** — `docs/architecture.md`: Broken anchor `#trust-model` in link to `integration.md`. Fix to `#plugin-integration-type`.
- [ ] **DOC FIX** — `docs/architecture.md`: Container images table missing `http-executor` row. Add it.
- [ ] **DOC FIX** — `docs/guides/webhook-security.md`: "Combining Methods" section implies multiple auth types can be active simultaneously, but `WebhookAuth.Type` is a single enum. Clarify or note as planned.

---

## 19. Manual E2E Validation — Per-Example Tasks (2026-04-04)

> Expands §16 VALIDATION. Context doc: `docs/tech-debt/manual-e2e-context-2026-04-04.md`.
> Target cluster: k3s (`kubectl --context default`). KubeZap controller is deployed and running.
> Tasks marked **[AUTO]** can be executed by Claude when running locally. Tasks marked **[USER]** require credentials or external services that only the owner can provide.
> **Run after §22 VERIFY, §20, §21, §23 P0, and §24 P0 complete** — see prioritization rationale rules 8–10.

### Automation-ready examples (no external services required)

- [ ] **[AUTO] E2E — order-router** — Apply `examples/order-router/`, verify Trigger accepted, Mockoon running, port-forward to 8080, fire two curl requests (express + standard paths), inspect FlowRun step phases, verify Mockoon captured both notifications. Covers: webhook trigger, transform step, CEL branching, step result passing, Mockoon admin API. See `examples/order-router/README.md`.

- [ ] **[AUTO] E2E — incident-escalation** — Apply `examples/incident-escalation/`, fire alert webhook, observe `Waiting` phase (2-minute wait step), confirm `escalate` is Skipped, verify all step phases. Covers: parallel steps, wait/resume, conditional skip. **Requires internet access from k3s** (uses httpbin.org). See `examples/incident-escalation/README.md`.

- [ ] **[AUTO] E2E — nightly-export** — Deploy MinIO, apply `examples/nightly-export/`, create manual FlowRun to trigger immediately (skip waiting for 02:00 cron), verify export-data step results, verify MinIO upload, simulate upload failure to test `failurePolicy: Continue`. Covers: cron trigger, failurePolicy, retry, step result chain. See `examples/nightly-export/README.md`.

- [ ] **[AUTO] E2E — k8s-pod-failure-ticket** — Apply `examples/k8s-pod-failure-ticket/`, cause a pod failure, verify FlowRun created, verify Mockoon captured ticket creation POST. Covers: resource trigger (alpha), dedup via pod UID. Note alpha limitations. See `examples/k8s-pod-failure-ticket/README.md`.

- [ ] **[AUTO] E2E — multi-tenant-fanout** — Apply `examples/multi-tenant-fanout/`, fire webhook, verify all 3 tenant steps ran in parallel, patch one tenant secret to simulate failure, verify `failurePolicy: Continue` keeps others running. Covers: parallel fan-out, per-tenant Integration, failurePolicy. See `examples/multi-tenant-fanout/README.md`.

- [ ] **[AUTO] E2E — oidc-webhook** — Apply `examples/oidc-webhook/`, port-forward Dex (5556) and gateway (8080), obtain JWT from Dex, send authenticated request, verify FlowRun created and completed, test rejection (no token, invalid token → 401). Covers: OIDC/JWT auth, required claims enforcement. See `examples/oidc-webhook/README.md`.

### External-credential examples

- [ ] **[AUTO] E2E — slack-router (simulated)** — Apply `examples/slack-router/`, simulate Slack slash command with curl + local HMAC signing (see README), verify `handle-deploy`, `handle-status`, `handle-unknown` branches route correctly. Remove ipAllowlist from Trigger for local testing. Covers: HMAC auth, form-encoded payload, multi-branch CEL routing. Full validation with real Slack: see USER task below.

- [ ] **[USER] E2E — slack-router (real Slack)** — Requires: Slack app with slash command, Signing Secret. See `examples/slack-router/README.md` for full setup. File follow-up tasks if issues found. **Pending input**: see `docs/tech-debt/pending-input-required.md` §2026-04-04.

- [ ] **[USER] E2E — github-autolabel** — Requires: GitHub repo with admin access, PAT with `repo` scope, ngrok or public gateway URL. See `examples/github-autolabel/README.md`. User must create Secrets and configure GitHub webhook before applying. **Pending input**: see `docs/tech-debt/pending-input-required.md` §2026-04-04.

### Kafka examples (Strimzi cluster available on k3s)

> Kafka bootstrap: `my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092` (Strimzi `my-cluster` in `kafka` ns)

- [ ] **[AUTO] E2E — kafka-enrichment** — Edit `examples/kafka-enrichment/integration.yaml` broker to `my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092`, apply `examples/kafka-enrichment/`, produce test message via kcat/kafka-console-producer, verify FlowRun created with `p<N>-offset-<N>` name, inspect step results (enrichment + conditional routing), verify enriched event on output topic. Covers: Kafka trigger, dedup, retry, fan-out to tiers, publish step. See `examples/kafka-enrichment/README.md`.

- [ ] **[AUTO] E2E — dlq-handler** — Create `orders.dlq` and `orders` Kafka topics on `my-cluster`, apply `examples/dlq-handler/`, produce poison message with `retry_count:1` (escalate Skipped), then `retry_count:5` (escalate fires), verify Mockoon captured escalation POST, verify re-publish to `orders` topic. Covers: DLQ pattern, conditional escalation, publish step. See `examples/dlq-handler/README.md`.

### CLI + Dashboard verification

- [ ] **[AUTO] CLI verification** — After running at least one example, verify all kubezap CLI subcommands: `kubezap watch`, `kubezap history <name>`, `kubezap triggers`, `kubezap flows`. Build from source: `go build -o bin/kubezap ./cmd/kubezap/`. File follow-up tasks for any issues.

- [ ] **[AUTO] Web dashboard verification** — Port-forward `svc/kubezap-ui 8082:8082 -n kubezap-system`, open `http://localhost:8082`, verify: namespace selector works, FlowRuns list updates live, Trigger and Flow listings populate, activity feed is present.

---

## 10. Future / Backlog

- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows — deferred to v1beta1; requires FlowGrant CRD (like Gateway API ReferenceGrant) for cross-namespace authorization. `FlowRef.Namespace` removed in v1alpha1 per §17 P0.
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source
