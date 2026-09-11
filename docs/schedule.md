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
> **§16 P1 VALIDATION now complete (2026-09-11 groom)** — see that item's note. Every gate-list item below is complete except the full spec-drift check, which by its own "recurring gate" wording (§25) must be re-run fresh at actual submission time, not satisfied by the one-time §31 baseline alone. Resolving the actual "start the submission PR" decision is still an owner call, not auto-applied by this grooming pass.
> GitHub org migration to `kubezap/kubezap-operator` complete (2026-03-22). Module path is `github.com/kubezap/kubezap-operator`.
> OLM bundle passes `bundle validate` and `scorecard` as of 2026-03-21.

- [ ] OperatorHub submission PR — gates on: §18 P1 items complete (✓), §22 VERIFY (e2e tests green), §20 code review clean, §21 doc review clean, §16 P1 VALIDATION (all §19 tasks complete), full spec drift check clean (`docs/guides/spec-drift.md` Steps 1–4 across all 4 CRD types, per the §25 recurring-gate rule — not just the lightweight per-PR check in `docs/guides/pre-merge-checklist.md` §6)

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

- [x] **VALIDATION (complete 2026-09-11 groom — stale checkbox, work already done)** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. **Broken down into per-example tasks in §19 below** — every §19 item is now `[x]` except one (`github-autolabel`), which is explicitly and permanently deferred by the owner (`docs/tech-debt/pending-input-required.md` "Manual E2E Validation — 2026-04-04": "still deferred, proceed with other backlog items"). A deliberately-deferred owner decision does not block this rollup item — checking it off here, same reasoning already applied elsewhere in this schedule to owner-deferred items.

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
- [x] **BUG** — `internal/controller/flowrun_controller.go:dependenciesMet`: `dependenciesMet` only accepts `Succeeded`/`Skipped` deps; never allowed `Failed` deps to satisfy `runAfter` even with `failurePolicy: Continue`. Downstream steps were silently skipped instead of executing. Fixed by passing `failurePolicyContinue bool` through both call sites.
- [x] **BUG** — `internal/controller/flowrun_controller.go:2079`: Kafka publish producer ignores TLS/SASL config from Integration spec. Only plaintext, unauthenticated Kafka clusters work for publish steps. Port TLS/SASL config from kafka/watcher.go.
- [x] **BUG** — `internal/controller/flowrun_controller.go:464`: Wait step `StartTime` is overwritten on re-entry (requeue), masking the actual start time. Preserve existing StartTime from prior status.
- [x] **TECH DEBT** — `internal/controller/resource_watcher.go:293`: cooldownTracker map grows without bound. Clear entries on `Deregister()`; add periodic eviction of entries older than cooldown duration.
- [x] **TECH DEBT** — `internal/controller/trigger_controller.go:119`: Webhook gateway resources (Deployment, Service, SA, Role, RoleBinding, HPA) are not cleaned up when the last webhook Trigger in a namespace is disabled or deleted. Add reference counting or periodic sweep.

### P2 — Nice to have

- [x] **BUG (fixed 2026-09-10)** — `internal/ui/server.go`: Dashboard SSE endpoint (`GET /api/v1/events`) returns 501 because `mgr.GetClient()` doesn't implement `client.WithWatch`. Switch to informer-based watch via `mgr.GetCache().GetInformer()` or use a dedicated `client.WithWatch` client. Discovered during §19 E2E — 2026-04-05. **Fix**: added `ui.WithCache(mgr.GetCache())` option; `handleSSE` streams via the shared informer (`GetInformer` + `AddEventHandler`) when a cache is configured, falling back to the original `client.WithWatch` path otherwise (used by existing tests with a fake client). Wired in `cmd/main.go`.

- [x] **BUG (verified 2026-09-11 — already fully resolved)** — `internal/gateway/webhook/watcher.go`: OIDC route registration was constructing JWKS URL as `issuer + "/.well-known/jwks.json"` which is wrong for providers like Dex that serve JWKS at a non-standard path. Fixed 2026-04-05 by fetching OIDC discovery doc and reading `jwks_uri` (`discoverJWKSURL` in `watcher.go:339`, confirmed present). `docs/guides/webhook-security.md:183` already documents the discovery-based flow accurately — no doc gap remained; this item was just an unchecked box for already-completed work.

- [x] **BUG** — `internal/controller/flowrun_controller.go:1026`: Kafka publish step has no retry support. `RetryPolicy` from step spec is ignored; attempts always 1.
- [x] **BUG** — `internal/controller/flowrun_controller.go:1087`: Plugin publish step has no retry support. Same issue as Kafka publish.
- [x] **BUG** — `internal/gateway/webhook/handler.go:337`: Body truncated to 4096 bytes without setting `bodyTruncated` flag on TriggerData.
- [x] **BUG** — `internal/controller/executor_reconciler.go:151`: Executor container `--port` arg is hardcoded to 8091, not derived from `ExecutorPort` field. Custom port config is silently ignored.
- [x] **VALIDATION** — `api/v1alpha1/trigger_types.go:277`: ResourceTrigger.Events has conflicting `MinItems=1` and `+optional` markers. Remove `MinItems=1` since the code handles empty gracefully.
- [x] **VALIDATION** — `api/v1alpha1/flow_types.go:65`: FlowStep.Name lacks uniqueness validation. Duplicate step names cause undefined runtime behavior.
- [x] **VALIDATION** — `api/v1alpha1/flow_types.go:205`: RetryPolicy.MaxRetries lacks `+kubebuilder:validation:Minimum=0`. Negative values cause zero-execution steps.
- [x] **OBSERVABILITY** — `internal/gateway/webhook/handler.go:401`: Trace context lost on `context.Background()` fallback for FlowRun creation. Extract span context before checking Err().
- [x] **TECH DEBT** — `internal/gateway/kafka/watcher.go:119`: `Start` returns `ctx.Err()` instead of nil on graceful shutdown, causing spurious error logs.

---

## 24. Documentation Review Findings — 2026-04-04

> Source: `docs/tech-debt/doc-review-results-2026-04-04.md`
> LOW/MEDIUM issues were fixed inline during the review. Only HIGH and P1 items are listed below.
> **Must precede §19 manual E2E** — P0 doc-reality mismatches in this section would invalidate manual validation of those features (rule 10).

### P0 — Fix before public release

- [x] **DOC FIX** — `docs/guides/webhook-security.md`: HMAC/OIDC/Bearer/Basic auth field mismatch with Go types. Doc shows fields (`header`, `algorithm`, `prefix`, `encoding` on HMAC; `requiredClaims`, `jwksUri`, `jwksCacheTTL` on OIDC; `trustedProxies`; `secretRef` on Bearer vs `tokenSecretRef`) that don't exist in Go types. Either implement the fields or rewrite the security guide to match current API. Highest-impact doc-reality mismatch.
- [x] **DOC FIX** — `docs/api/integration.md`: IntegrationStatus fields diverge from Go types. Doc lists `gatewayDeployments` ([]GatewayDeploymentRef), `connectedTriggers`, `phase: Failed` — Go types have `GatewayDeploymentName` (string), no `connectedTriggers`, `phase: Pending`. Align doc with Go types.
- [x] **DOC FIX** — `docs/api/integration.md`: KafkaIntegrationSpec (`producerConfig`, `consumerConfig`) and PluginIntegrationSpec (`replicas`, `resources`, `config`, `imagePullSecrets`) documented but not in Go types. Remove phantom fields from docs or implement them.

### P1 — Fix before GA

- [x] **DOC FIX** — `docs/api/integration.md`: Broken link to `docs/tech-debt/gateway-shutdown-correctness.md` (file does not exist). Create file or update reference.
- [x] **DOC FIX** — `docs/architecture.md`: Broken anchor `#trust-model` in link to `integration.md`. Fix to `#plugin-integration-type`.
- [x] **DOC FIX** — `docs/architecture.md`: Container images table missing `http-executor` row. Add it.
- [x] **DOC FIX** — `docs/guides/webhook-security.md`: "Combining Methods" section implies multiple auth types can be active simultaneously, but `WebhookAuth.Type` is a single enum. Clarify or note as planned.

---

## 19. Manual E2E Validation — Per-Example Tasks (2026-04-04)

> Expands §16 VALIDATION. Context doc: `docs/tech-debt/manual-e2e-context-2026-04-04.md`.
> Target cluster: k3s (`kubectl --context default`). KubeZap controller is deployed and running.
> Tasks marked **[AUTO]** can be executed by Claude when running locally. Tasks marked **[USER]** require credentials or external services that only the owner can provide.
> **Run after §22 VERIFY, §20, §21, §23 P0, and §24 P0 complete** — see prioritization rationale rules 8–10.

### Automation-ready examples (no external services required)

- [x] **[AUTO] E2E — order-router** — Apply `examples/order-router/`, verify Trigger accepted, Mockoon running, port-forward to 8080, fire two curl requests (express + standard paths), inspect FlowRun step phases, verify Mockoon captured both notifications. Covers: webhook trigger, transform step, CEL branching, step result passing, Mockoon admin API. See `examples/order-router/README.md`.

- [x] **[AUTO] E2E — incident-escalation** — Apply `examples/incident-escalation/`, fire alert webhook, observe `Waiting` phase (2-minute wait step), confirm `escalate` is Skipped, verify all step phases. Covers: parallel steps, wait/resume, conditional skip. **Requires internet access from k3s** (uses httpbin.org). See `examples/incident-escalation/README.md`.

- [x] **[AUTO] E2E — nightly-export** — Deploy MinIO, apply `examples/nightly-export/`, create manual FlowRun to trigger immediately (skip waiting for 02:00 cron), verify export-data step results, verify MinIO upload, simulate upload failure to test `failurePolicy: Continue`. Covers: cron trigger, failurePolicy, retry, step result chain. See `examples/nightly-export/README.md`.

- [x] **[AUTO] E2E — k8s-pod-failure-ticket** — Apply `examples/k8s-pod-failure-ticket/`, cause a pod failure, verify FlowRun created, verify Mockoon captured ticket creation POST. Covers: resource trigger (alpha), dedup via pod UID. Note alpha limitations. See `examples/k8s-pod-failure-ticket/README.md`.

- [x] **[AUTO] E2E — multi-tenant-fanout** — Apply `examples/multi-tenant-fanout/`, fire webhook, verify all 3 tenant steps ran in parallel, patch one tenant secret to simulate failure, verify `failurePolicy: Continue` keeps others running. Covers: parallel fan-out, per-tenant Integration, failurePolicy. See `examples/multi-tenant-fanout/README.md`.

- [x] **[AUTO] E2E — oidc-webhook** — Apply `examples/oidc-webhook/`, port-forward Dex (5556) and gateway (8080), obtain JWT from Dex, send authenticated request, verify FlowRun created and completed, test rejection (no token, invalid token → 401). Covers: OIDC/JWT auth, required claims enforcement. See `examples/oidc-webhook/README.md`. **Complete 2026-04-05**: fixed JWKS URL discovery (was guessing `issuer/.well-known/jwks.json`; now uses OIDC discovery doc to find correct URL). Fixed Dex `/tmp` writable via emptyDir. Fixed `grantTypes` and `passwordConnector` config for Dex ROPC flow. Fixed Mockoon selector label and image. All 3 assertions pass: valid JWT → 202 + FlowRun Succeeded; missing token → 401; invalid token → 401.

### External-credential examples

- [x] **[AUTO] E2E — slack-router (simulated)** — Apply `examples/slack-router/`, simulate Slack slash command with curl + local HMAC signing (see README), verify `handle-deploy`, `handle-status`, `handle-unknown` branches route correctly. Remove ipAllowlist from Trigger for local testing. Covers: HMAC auth, form-encoded payload, multi-branch CEL routing. Full validation with real Slack: see USER task below. **Complete 2026-04-05**: all 3 routing branches verified (deploy/status/unknown), bad HMAC → 401, fixed trigger.yaml (removed non-existent HMACConfig fields), fixed mockoon.yaml (image + probes). Note: gateway uses GitHub-style HMAC (X-Hub-Signature-256, sha256=hex); Slack v0= format and form-encoded bodies are future enhancements.

- [x] **[USER] E2E — slack-router (real Slack)** — Requires: Slack app with slash command, Signing Secret. See `examples/slack-router/README.md` for full setup. **Complete 2026-09-10**: real Slack request from live workspace authenticated correctly (`hmac.provider: slack`) and, after fixing §26 P0 (form-urlencoded body templating), routed correctly: `/kubezap deploy staging` → `handle-deploy` Succeeded, `handle-status`/`handle-unknown` Skipped, `commandText` resolved to `"deploy staging"`. Re-verified against the live cluster via simulated form-encoded payload matching Slack's exact shape.

- [ ] **[USER] E2E — github-autolabel** — Requires: GitHub repo with admin access, PAT with `repo` scope, ngrok or public gateway URL. See `examples/github-autolabel/README.md`. User must create Secrets and configure GitHub webhook before applying. **Pending input**: see `docs/tech-debt/pending-input-required.md` §2026-04-04.

### Kafka examples (Strimzi cluster available on k3s)

> Kafka bootstrap: `my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092` (Strimzi `my-cluster` in `kafka` ns)

- [x] **[AUTO] E2E — kafka-enrichment** — Edit `examples/kafka-enrichment/integration.yaml` broker to `my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092`, apply `examples/kafka-enrichment/`, produce test message via kcat/kafka-console-producer, verify FlowRun created with `p<N>-offset-<N>` name, inspect step results (enrichment + conditional routing), verify enriched event on output topic. Covers: Kafka trigger, dedup, retry, fan-out to tiers, publish step. See `examples/kafka-enrichment/README.md`. **Complete 2026-09-10**: real message produced against the live Strimzi cluster; `extract-customer` → `enrich-profile` (tier=enterprise) → `route-enterprise` Succeeded (others Skipped) → `publish-enriched` all verified via FlowRun step results and Mockoon's actual captured request. Required three real bug fixes to get here — see §27.

- [x] **[AUTO] E2E — dlq-handler** — Create `orders.dlq` and `orders` Kafka topics on `my-cluster`, apply `examples/dlq-handler/`, produce poison message with `retry_count:1` (escalate Skipped), then `retry_count:5` (escalate fires), verify Mockoon captured escalation POST, verify re-publish to `orders` topic. Covers: DLQ pattern, conditional escalation, publish step. See `examples/dlq-handler/README.md`. **Complete 2026-09-10**: both paths verified live — `retry_count:1` → `escalate` Skipped; `retry_count:5` → `escalate` Succeeded with Mockoon capturing the correct `POST /escalate` body; `orders` topic consumed showing re-published messages with `X-Retry-Attempt`/`X-DLQ-Reason` headers. Required a real flow.yaml bug fix — see §27.

### CLI + Dashboard verification

- [x] **[AUTO] CLI verification** — After running at least one example, verify all kubezap CLI subcommands: `kubezap watch`, `kubezap history <name>`, `kubezap triggers`, `kubezap flows`. Build from source: `go build -o bin/kubezap ./cmd/kubezap/`. File follow-up tasks for any issues. **Complete 2026-04-05**: all 4 commands verified. `watch` streams live FlowRun status, `history` shows step-by-step results, `triggers` lists all triggers, `flows` lists all flows.

- [x] **[AUTO] Web dashboard verification** — Port-forward `svc/kubezap-ui 8082:8082 -n kubezap-system`, open `http://localhost:8082`, verify: namespace selector works, FlowRuns list updates live, Trigger and Flow listings populate, activity feed is present. **Complete 2026-04-05**: HTML page loads (200), `/api/v1/namespaces`, `/api/v1/{ns}/flowruns`, `/api/v1/{ns}/triggers`, `/api/v1/{ns}/flows` all return correct data. SSE events (`/api/v1/events`) returns 501 — controller client doesn't implement `client.WithWatch`; filed as §23 bug item.

---

## 25. Workflow Improvements (2026-04-08)

> New process docs introduced to improve design rigor, review quality, and merge safety.
> See `docs/guides/` and `docs/architecture/` for the reference documents.

### Process Adoption

- [x] **PROCESS (done 2026-09-10)** — Adopt `docs/guides/design-process.md` for all new features: every non-trivial change must have a design record in `docs/design/` before implementation begins. Review the doc and confirm it is referenced from `CLAUDE.md` design philosophy section. Added a bullet under CLAUDE.md's Development Philosophy section.
- [x] **PROCESS (done 2026-09-10)** — Migrate code reviews to targeted patterns from `docs/guides/code-review-strategy.md`. Stop issuing open-ended "review the codebase" prompts; always name an invariant or concern as the entry point. Added a "Code Review Strategy" section to CLAUDE.md referencing the guide.
- [x] **PROCESS (done 2026-09-10)** — Enforce `docs/guides/pre-merge-checklist.md` before marking any feature complete. All [GATE] items must be satisfied; all [FILE] items must have schedule entries. Added a "Pre-Merge Checklist" section to CLAUDE.md referencing the guide.

### FlowRun State Model Validation

- [x] **VALIDATION (done 2026-09-10)** — Validate `internal/controller/flowrun_controller.go` against `docs/architecture/flowrun-state-model.md`. Use the State Transition Correctness review template from `docs/guides/code-review-strategy.md#8`. File any violations as P0 bugs. Scope: all `.Phase =` assignment sites in `flowrun_controller.go`. **Findings filed and one P0 fixed — see §28.**
- [x] **TESTING (substantially complete 2026-09-11)** — Added unit tests for the FlowRun phase transitions from `docs/architecture/flowrun-state-model.md` that had no guard test yet. Existing coverage (verified by survey, not rewritten): `""→Pending→Running`, `Running→Succeeded`, `Running→Failed`, `Running→Cancelled` (both DeletionTimestamp and `kubezap.io/cancel` annotation paths, §28), stuck-orphan `Running→Failed` recovery, step `Pending→Skipped` (CEL `when=false` and cascade-skip), step `Running→Waiting→Running` (wait re-entry). **New this pass**: `internal/controller/flowrun_controller_test.go` — a `DescribeTable` asserting a FlowRun already in `Succeeded`/`Failed`/`Cancelled` never re-enters `Running`, never dispatches a step, and its backend receives zero requests on reconcile (closes the FlowRun-level "Invalid / Dangerous Transitions" list); a second `DescribeTable` asserting a step already `Succeeded`/`Failed` is never re-dispatched (zero further backend calls, `Attempts` unchanged) even while the FlowRun itself is still `Running` with other steps pending — this is a stricter, per-step guard than the FlowRun-level one above. 217 controller tests pass (was 212). **Explicitly NOT covered, left open**: the "Empty phase → Succeeded/Failed (skipped Running)" invalid-transition row — per §25 item below (moved from an inline architecture note), the current implementation deliberately does *not* persist an observable `Running` step phase for steps that complete synchronously within one reconcile, so a literal test of that invariant would fail against intended, documented-as-acceptable behavior, not a bug. Writing that test is blocked on resolving that open question first, not on effort — see the item below.

### Spec Drift Detection

- [x] **DRIFT CHECK (done 2026-09-10)** — Run the full spec drift process from `docs/guides/spec-drift.md` across all current CRD types (`Trigger`, `Flow`, `FlowRun`, `Integration`). Produce a findings list; file P0/P1 items as new schedule entries. This is a one-time baseline check. **Findings in §31** — one item fixed live (`kubezap.io/cancel` annotation, previously fully documented with zero implementation), several doc corrections applied directly, and one large P0 (Flow parameters) filed for owner input rather than fixed inline.
- [x] **RECURRING (done 2026-09-11)** — Add spec drift check as a recurring gate: run Steps 1–4 from `docs/guides/spec-drift.md` before any OperatorHub submission or GA milestone. No `.github/PULL_REQUEST_TEMPLATE.md` exists in this repo (confirmed — `.github/` only has `workflows/`), and creating one would apply to every routine PR, not just submission/GA milestones. Instead, added the gate directly to §1's OperatorHub submission PR item — the closest thing this repo has to a "submission PR checklist" — distinguishing it explicitly from the lighter per-PR spec-drift spot-check already in `docs/guides/pre-merge-checklist.md` §6.

---

## 26. Live Slack E2E Findings — 2026-09-10

> Discovered while validating real-Slack HMAC support (`hmac.provider: slack`) against a live workspace.

### P0 — Fix before public release

- [x] **BUG (fixed 2026-09-10)** — `internal/controller/flowrun_controller.go:1931-1951` (`substituteVars`): `$(trigger.body.<field>)` resolution only works when `triggerData.Body` is valid JSON (`json.Unmarshal`). Form-urlencoded bodies (`Content-Type: application/x-www-form-urlencoded`, e.g. Slack slash commands: `command=%2Fkubezap&text=deploy+staging&...`) fail to unmarshal, so the substitution block is silently skipped and `$(trigger.body.text)` is left as a literal unresolved string in step results. Downstream CEL conditions then evaluate against the literal placeholder instead of the real value, so branching on form-encoded fields never works. Confirmed live: real Slack request to `examples/slack-router/` authenticated correctly but routed to `handle-unknown` instead of `handle-deploy` for `/kubezap deploy staging`. This contradicts `examples/slack-router/README.md` and the Flow docs, which document form-decoding into `trigger.body.*` as a supported feature — it has never actually worked. Affects every form-urlencoded webhook example, not just Slack. **Fix**: `TriggerData.ContentType` now selects the parser — `application/x-www-form-urlencoded` bodies are parsed into a flat key/value map via `url.ParseQuery` (top-level fields only) alongside the existing JSON path; nested/array dot-paths remain JSON-only. Re-verified live: `commandText` resolves to `"deploy staging"`, `handle-deploy` now correctly Succeeds.
- [x] **BUG (fixed 2026-09-10)** — `internal/controller/gateway_deployment.go` (`desiredWebhookGatewayRole`): the webhook gateway's Role never granted `secrets` access. Any Trigger auth type backed by a `secretRef` (hmac, bearer, basic, apiKey, header-equals) could never resolve its secret against a live cluster — `buildRouteEntry` failed with RBAC `forbidden` and the route was never registered (manifests as a 404 on the webhook path, not a 401). Fixed by adding a `secrets`/`get` rule. Unit test added: `internal/controller/gateway_deployment_test.go`.

### P2 — Nice to have

- [x] **SECURITY (fixed 2026-09-10)** — `internal/gateway/webhook/registry.go` (`Register`/`Deregister` logging) logs the full `RouteEntry` struct at INFO level, including the raw `HMACSecret` value in plaintext. Any webhook auth secret ends up in gateway pod logs on every route (re)registration. Redact secret-bearing fields before logging. **Fix**: added `RouteEntry.redactedForLog()`, called at both `Register` log sites; replaces `HMACSecret`/`BearerToken`/`BasicPassword`/`APIKey`/`HeaderEqualsValue` with `"[REDACTED]"` when set, without mutating the entry actually stored in the registry.
- [x] **TECH DEBT (verified resolved 2026-09-11)** — Live cluster's controller-manager RBAC (`ClusterRole kubezap-manager-role`) was found out of sync at the time this item was filed — missing `networkpolicies` create permission needed by `ExecutorReconciler`. Re-checked against the live cluster: `kubectl get clusterrole kubezap-manager-role -o yaml` now shows the full `networkpolicies` rule (`create;delete;get;list;patch;update;watch`) and every other rule matches `config/rbac/role.yaml`'s generated `manager-role` exactly, rule for rule. No `forbidden`/RBAC errors in the last 200 lines of `kubectl logs deployment/kubezap-controller-manager -n kubezap-system`. Resolved by a subsequent `make deploy` sometime after this item was filed (likely alongside the §30 RBAC-marker-placement fix, PR #133) — no further action needed.

---

## 27. Live Kafka E2E Findings — 2026-09-10

> Discovered while validating `kafka-enrichment` and `dlq-handler` against the real Strimzi cluster (`my-cluster` in `kafka` ns). Neither example had ever actually run successfully before — both were blocked by real bugs, not environment setup.

### P0 — Fix before public release

- [x] **BUG (fixed 2026-09-10)** — `cmd/kafka-gateway/main.go`: the binary never served `/healthz` on any port, but `desiredKafkaGatewayDeployment` (`internal/controller/integration_controller.go`) configures liveness/readiness probes against `:8090/healthz`. Every kafka-gateway Deployment crash-loops forever in a real cluster — `kafka-enrichment` and `dlq-handler` could never have passed E2E validation before this fix. Fix: added a `--health-port` flag (default 8090) and a minimal `/healthz` HTTP server, matching the pattern already used by `webhook-gateway`. **`cmd/amqp-gateway/main.go` and `cmd/nats-gateway/main.go` have the identical gap** (their Deployments — `desiredAmqpGatewayDeployment`/`desiredNatsGatewayDeployment` — also probe `:8090/healthz` with nothing listening there) — **not fixed here**, since neither is exercised by a live example in this session and both are beta. Filed as a separate P1 item below.
- [x] **BUG (fixed 2026-09-10)** — `examples/dlq-handler/flow.yaml`: the `escalate` step's CEL `when` expression used invalid pipe-filter syntax (`steps.extract_error.results.retryCount|int > 3`) — CEL has no `|` filter operator, only function-call casts. This is a compile error on every evaluation, so `escalate` always failed the FlowRun regardless of `retry_count`. Fixed to `int(steps.extract_error.results.retryCount) > 3`.
- [x] **BUG (fixed 2026-09-10)** — `examples/kafka-enrichment/mockoon.yaml` and `examples/dlq-handler/mockoon.yaml` used `image: mockoon/mockoon:latest` (does not exist on Docker Hub — `pull access denied, repository does not exist`) plus a nonexistent `--admin-api-port` CLI flag and HTTP `/health` probes against a port nothing serves. Same class of bug already fixed for `oidc-webhook`/`slack-router` in an earlier session (§19 2026-04-05) but not caught in these two. Fixed to match the working pattern (`mockoon/cli:latest`, no `--admin-api-port` flag, `tcpSocket` probes on the `mock` port) already used by `order-router`/`slack-router`.

### P1 — Fix before GA

- [x] **DOC FIX (fixed 2026-09-10)** — All 8 example READMEs referencing Mockoon's admin/log-inspection API (`order-router`, `kafka-enrichment`, `oidc-webhook`, `slack-router`, `nightly-export`, `dlq-handler`, `multi-tenant-fanout`, `k8s-pod-failure-ticket`) documented `wget http://localhost:3001/api/logs` — this endpoint doesn't exist. `mockoon/cli` (v9.6.1) serves its admin API at `/mockoon-admin/logs` on the **main** mock port (3000), not a separate `3001` port at all — the declared `admin` containerPort/servicePort is never actually listened on by the CLI. Fixed all 8 READMEs to the correct URL; verified live against the running Mockoon pod. The vestigial `admin` port declarations in each `mockoon.yaml` are harmless (unused, not referenced by any probe) but could be removed as a follow-up cleanup — not done here to keep this change doc-only.
- [x] **TECH DEBT (fixed 2026-09-11)** — `cmd/amqp-gateway/main.go` / `cmd/nats-gateway/main.go` were missing the `/healthz` server described in the P0 item above (and also never had a metrics server at all, unlike kafka-gateway). Fixed by mirroring `cmd/kafka-gateway/main.go`'s pattern exactly into both: `--health-port` (default 8090, matching the Deployment's hardcoded probe port in `desiredAmqpGatewayDeployment`/`desiredNatsGatewayDeployment`), `--metrics-port` (default 9090) with optional TLS, and graceful shutdown of both servers on `SIGINT`/`SIGTERM`. No Deployment/probe changes needed — both already probed port 8090, they just had nothing listening there. `go build`/`go vet`/`make test` all clean; no existing test exercised these binaries' `main()` so none needed updating.
- [x] **TECH DEBT (fixed 2026-09-10)** — `desiredKafkaGatewayDeployment`, `desiredAmqpGatewayDeployment`, `desiredNatsGatewayDeployment` (`internal/controller/integration_controller.go`) never set an explicit `ImagePullPolicy`, unlike `desiredWebhookGatewayDeployment` and the executor (`corev1.PullIfNotPresent`). Since all three default to the `:latest` tag, Kubernetes defaults this to `Always` — surfaced as the real reason `kubezap-kafka-gateway-customer-kafka` couldn't start locally (`ImagePullBackOff` against `ghcr.io/kubezap/kafka-gateway:latest`, which returns 403 — this image has never been published). Fixed by adding `ImagePullPolicy: corev1.PullIfNotPresent` to all three, matching the existing pattern. Plugin Deployments (`desiredPluginDeployment`) were deliberately left alone — third-party plugin images may reasonably want `Always` freshness, and Kubernetes' standard tag-based default already applies there.

---

## 28. FlowRun State Transition Correctness Review — 2026-09-10

> Findings from the §25 State Transition Correctness validation (`docs/guides/code-review-strategy.md#8` template) against `docs/architecture/flowrun-state-model.md`. Scope: all `.Phase =` and `Phase:` assignment sites in `internal/controller/flowrun_controller.go`.

### P0 — Fix before public release

- [x] **BUG (fixed 2026-09-10)** — The documented `Cancelled` FlowRun phase (`docs/architecture/flowrun-state-model.md`: "FlowRun was deleted while Running; controller cleaned up in-flight work") was never actually assigned anywhere in `flowrun_controller.go` — confirmed by grepping every `.Phase =`/`Phase:` site in the file. The one code path that should produce it (`Reconcile`, deletion-while-`Running`, line ~213) instead called `failFlowRun`, reporting an intentional deletion as `Phase: "Failed"` with reason `FlowRunFailed` — misrepresenting a cancellation as an error in status, conditions, and the `kubezap_flowrun_duration_seconds` metric (mislabeled `"Failed"`). An existing test (`flowrun_controller_test.go`, "DeletionTimestamp while running") had even locked in the wrong behavior by asserting `Phase == "Failed"`. **Fix**: refactored `failFlowRun` into a shared `finishFlowRun(phase, reason, msg)` helper; added `cancelFlowRun` (phase `Cancelled`, reason `FlowRunCancelled`) and switched the deletion-while-Running call site to use it. Updated the existing test to assert `Cancelled` and added condition-type assertions. Note: the `Cancelled` GC-eligibility check already added in §23 (`flowrun_controller.go:202`) was consequently dead code until this fix — it checked for a phase value nothing ever produced.

### P2 — Nice to have

- [x] **ARCHITECTURE NOTE (resolved 2026-09-11 — owner decided: document as-is)** — For steps that complete synchronously within one reconcile (the common case — `http`, `transform`, `publish`, and any wait step whose duration hasn't elapsed yet on first execution), the documented `Running` step phase (`docs/architecture/flowrun-state-model.md`: `Pending ──► Running ──► Succeeded/Failed`) is set only on an in-memory `StepRunStatus` value that gets overwritten with the terminal phase before any `Status().Update()` call — so it is never actually persisted or observable via `kubectl get flowrun`. The only step phases genuinely observable mid-execution are `Running` for a step retried after an executor transport error (correct — matches the model's retry semantics) and `Waiting` for an unfinished wait step. Not a bug — no invariant is violated and nothing double-executes. Owner chose to document the behavior rather than add an intermediate status write (which would need its own design record per `docs/guides/design-process.md`, since it changes reconciliation logic, for the sake of extra per-step API load with no invariant-safety benefit). **Fix**: added a "Step Phase Observability" section to `docs/architecture/flowrun-state-model.md` explaining the synchronous-execution behavior explicitly and clarifying the "Empty phase → Succeeded/Failed" row in the Invalid/Dangerous Transitions table is the expected shape for fast steps, not a bug signature. This unblocks full completion of §25's TESTING item (no code change was needed there — the test suite's existing behavior already matches the now-documented model).
- [x] **DOC GAP (fixed 2026-09-11)** — `docs/architecture/flowrun-state-model.md`'s step `Pending → Skipped` rule only documented two triggers ("`when` condition false, or all unsatisfied deps are `Failed` under `failurePolicy: Continue`"). The implementation (`flowrun_controller.go` `allDepsSkipped`) also cascades a step to `Skipped` when all its `runAfter` dependencies were themselves `Skipped` (not `Failed`) — a third, undocumented trigger. Behavior was already correct; fixed the model doc's transition diagram and rule list to name all three triggers.

---

## 29. Secret Rotation Detection Gap — 2026-09-10

> Raised by owner: does the operator detect a Secret rotated externally (e.g. by External Secrets Operator) without the referencing Trigger/Integration being touched?

- [x] **BUG (fixed 2026-09-11)** — Webhook Trigger auth secrets (HMAC/bearer/basic/apiKey/header-equals — `internal/gateway/webhook/watcher.go`) and Kafka/AMQP/NATS broker credentials (SASL/TLS — `internal/gateway/{kafka,amqp,nats}/watcher.go`) were read once when the gateway processed a Trigger/Integration add-or-update event, then cached in memory in the route/subscription entry for its lifetime, with no watch on `Secret` objects anywhere. Design record: **`docs/design/2026-09-11-secret-rotation-watches.md`**. **Also found while researching this fix, more severe**: the kafka/amqp/nats gateways' shared `kubezap-gateway` Role had **no `secrets` permission of any kind** — not even `get` — so `readSecretKey`'s live API call must already have been failing with `Forbidden` in any real cluster with SASL/TLS actually configured; this went unnoticed because every live E2E validation of `kafka-enrichment`/`dlq-handler` used a plaintext, unauthenticated Strimzi cluster. **Fix**: added a `corev1.Secret` informer to all four watchers (same `crcache.Cache` each already owns for its `Trigger` informer) with a new, small shared `internal/gateway/secretindex` package providing a thread-safe reverse index (Secret key → dependent Trigger keys), rebuilt every time a Trigger is reconciled so stale references clean up automatically. On a Secret change, every affected Trigger is reprocessed through the exact same code path a real Trigger spec change takes (`handleTrigger` for webhook, `reconcileTrigger` for kafka/amqp/nats) using each Trigger's most recently seen object — no extra API call. For kafka/amqp/nats, added a `credentialFingerprint` (SHA-256 of the resolved SASL/TLS/username material) to each `subscription`, since the existing "topic/consumerGroup/integrationName unchanged, skip restart" fast path would otherwise silently swallow a credential-only rotation. Fixed the prerequisite RBAC gap in the same change: `desiredWebhookGatewayRole` upgraded from `secrets: get` to `get/list/watch`; `desiredKafkaGatewayRole`/`desiredAmqpGatewayRole`/`desiredNatsGatewayRole` (previously no secrets rule at all) all gained `secrets: get/list/watch`. New tests: `internal/gateway/secretindex` (6 unit tests, 100% coverage), plus per-watcher tests for credential fingerprinting, index population, secret-rotation reprocessing, and delete cleanup across all 4 gateway packages, plus one `internal/controller` test asserting the RBAC fix. `go build`/`go vet`/`make test` all pass; no CRD/RBAC manifest drift (these Role rule sets are plain Go structs, not `+kubebuilder:rbac`-marker-derived).
- [x] **DECIDED, no code change (2026-09-11)** — Related and lower-severity, a different code path from the gateway watchers fixed above: the `type: publish` Kafka producer cache (`internal/controller/flowrun_controller.go`'s `kafkaProducers`, used when a Flow step publishes *to* Kafka) doesn't notice a SASL/TLS secret rotation — only `kafkaProducerIdleTTL` (10 minutes) eventually forces a reconnect. Unlike the gateway consume-side fix, this cache is hit on every `type: publish` step execution (potentially per-message), not just on rare Trigger/Secret CRD events — a fingerprint check here means reading the SASL/TLS secrets on every publish call, not just occasionally. Owner decided: **accept the 10-minute bound as-is** rather than add a per-call secret-read cost to the publish hot path — a rotated Kafka publish credential takes up to `kafkaProducerIdleTTL` to be picked up, the same order of magnitude as most secret-rotation grace periods. No code change; this is a documented, intentional tradeoff, not an outstanding bug.
- [x] **CONFIRMED WORKING (no gap)** — Outbound HTTP step auth (Bearer/Basic/APIKey/SecretURL — `internal/controller/flowrun_controller.go` `fetchSecretValue`) is fetched fresh on every step execution, not cached across FlowRuns. Executor mTLS certs have their own independent rotation cycle. Neither needs a fix.

---

## 30. RBAC Markers Silently Dropped (misplaced package-scoped markers) — 2026-09-10

> Discovered while investigating §26's RBAC drift item. A KubeZap source bug (marker placement), not an environment or controller-gen fault — the initial diagnosis in this section was wrong and has been corrected below. Dangerous because it fails **silently**: `make manifests` exits 0 and simply omits the rules.

- [x] **BUG (root-caused and fixed 2026-09-10)** — `+kubebuilder:rbac` is a **package-scoped** marker. In `executor_reconciler.go` the four RBAC markers were written inside the doc comment attached directly to `type ExecutorReconciler struct` (prose, then `//`, then the markers, with no blank line before the type). controller-gen only collects package-scoped markers from **free-floating** comment groups — when they appear in a declaration's doc comment it **silently ignores them**, with no warning and no error. Result: `networking.k8s.io/networkpolicies` and the `secrets` write verbs never reached `config/rbac/role.yaml`, so the operator shipped without permissions its own code required — the cause of the recurring `Failed to reconcile executor NetworkPolicy` errors in §26. **Fix**: moved the marker block above the doc comment, separated by a blank line (matching the pattern already used correctly by the other seven marker-bearing files), then re-ran `make manifests` — the rules now generate correctly, and the manual patch previously applied to `role.yaml` was removed as no longer needed. Audited every other `+kubebuilder:rbac` block in the repo: all seven others were already correctly free-floating; `executor_reconciler.go` was the only one affected. Added a guard note to `CLAUDE.md` Coding Conventions, since the failure mode is completely silent.

  <details><summary>Why this took so long to spot (kept for future debugging reference)</summary>

  The silence sent the earlier investigation down a wrong path — it looked like an environment/tooling fault rather than a source bug, because: adding a canary marker to a brand-new file also produced nothing (that canary was itself attached to a `var` declaration, so it hit the exact same rule); a deliberately broken Go file produced no error either (controller-gen tolerates it); and upgrading controller-gen v0.18.0 → v0.22.0 changed nothing (correct — the behavior is by design, not a version bug). `GOCACHE`, path style, `replace` directives, `vendor/`, `GOFLAGS`/`GOWORK` were all ruled out and all were red herrings. The decisive test was comparing the comment-group *structure* of a working marker block against the broken one, not the marker text (whose bytes were byte-for-byte clean ASCII).
  </details>


---

## 31. Spec Drift Baseline Findings — 2026-09-10

> Full run of `docs/guides/spec-drift.md` across `Trigger`, `Flow`, `FlowRun`, `Integration`. Step 1 (regenerate/diff manifests) and Step 4 (sample CR dry-run) were clean — no findings. Steps 2/3/5 (types vs docs vs controller behavior) turned up real drift, listed below.

### P0 — Fix before public release

- [x] **BUG (fixed and live-verified 2026-09-10)** — `docs/api/flowrun.md`'s kubectl cheat sheet documented `kubectl annotate flowrun <name> -n <ns> kubezap.io/cancel=true` as the way to "Cancel a running FlowRun" — this annotation was never read anywhere in the codebase (confirmed by grep; contrast with `kubezap.io/retain`, which is real). A user following the docs to cancel a runaway FlowRun would see nothing happen, silently. **Fix**: implemented in `internal/controller/flowrun_controller.go` — reuses the `cancelFlowRun` helper from §28, checked right after the existing deletion-while-Running path. Only takes effect while `Phase == "Running"`, matching the documented scope. Added two Ginkgo tests (cancels while Running; no-op before Running) and live-verified against the real cluster: a FlowRun mid-way through a 5-minute wait step transitioned to `Cancelled` within one reconcile of the annotation being applied, with the correct condition and message, without deleting the object.
- [x] **PHANTOM FEATURE — decision made 2026-09-10: implement.** Flow parameters (`FlowSpec.params`/`ParamDeclaration`, `FlowRunSpec.params`/`ParamValue`, `$(params.<name>)`) are defined in the Go API and documented at length in `docs/api/flow.md` — used as the primary trigger→flow data-passing pattern in most of that doc's worked examples (Examples 2–7, ~200+ lines) — but nothing reads or resolves any of it; every `$(params.x)` today is a silently-dropped literal placeholder. Owner decided to implement rather than deprecate: the shape of the types (`required`/`default` on the declaration, `FlowRunSpec.Params` as a channel independent of `TriggerData`) reflects real, still-wanted design intent — trigger-derived input already works via `$(trigger.body.*)`, but there's no validation/default mechanism and no supported way to invoke a Flow with explicit inputs independent of a Trigger event. Design record: **`docs/design/2026-09-10-flow-parameters.md`** (per `docs/guides/design-process.md`). Summary of the approach: resolve entirely in `flowrun_controller.go` at FlowRun start, no gateway or CRD changes — explicit `FlowRunSpec.Params` entry wins, else auto-derive from a same-named top-level `trigger.body` field (JSON or form-urlencoded, reusing existing body-parsing helpers), else declared `default`, else fail fast if `required`, else empty string. Thread the resolved map into `substituteVars` (`$(params.x)`) and the CEL `when:` activation map (`params` variable). `FlowStep.params` stays out of scope — separate follow-up item below.

- [x] **IMPLEMENTATION (done and live-verified 2026-09-10)** — Built the design in `docs/design/2026-09-10-flow-parameters.md`. All 8 steps complete:
  1. [x] `resolveFlowParams` added to `internal/controller/flowrun_controller.go`, implementing the 5-step resolution order (explicit `FlowRunSpec.Params` > auto-derived from a same-named top-level `trigger.body` field, JSON or form-urlencoded > declared `default` > fail if `required` > empty string). Called once in `Reconcile()` right after `stepResults` is rebuilt, before any step is dispatched.
  2. [x] Fails via `failFlowRun` when a `required` param has no value and no default; message names the missing parameter.
  3. [x] `substituteVars` threads a new `params map[string]string` arg and resolves `$(params.<name>)` (all call sites updated: `substituteVarsWithSecrets`, `executeHTTPStep`, `executePublishStep`, `applyHTTPIntegration`, the `transform` step case).
  4. [x] `evaluateWhen`'s CEL activation gained a `params` variable (`cel.MapType(cel.StringType, cel.DynType)`, matching `trigger`/`steps`); all 4 `cel.NewEnv(...)` call sites (1 production, 3 test) updated in lockstep.
  5. [x] Ginkgo coverage added: `flow_params_test.go` (10 unit tests directly against `resolveFlowParams`), `substitute_vars_test.go` (+4), `evaluate_when_test.go` (+3), plus 2 full-Reconcile integration tests in `flowrun_controller_test.go` (required-and-missing fails before any step runs; `$(params.x)` resolves from trigger body into a step result). 205 controller tests pass (up from 186 baseline).
  6. [x] Live-verified against the real k3s cluster: real webhook Trigger + Flow + 3 real HTTP requests proved auto-derivation-from-body, default-fallback, explicit-override-beats-default, and required-param-fail-fast all work through the actual reconcile loop. Test resources cleaned up afterward.
  7. [x] Docs updated: removed the top-of-page banner and the `ParamDeclaration`/`ParamValue` not-implemented warnings in `docs/api/flow.md`, documented the real resolution order in both `ParamDeclaration` and the CEL/interpolation quick references, added `params.<name>` back into both quick-reference tables/lists as working, and rewrote `docs/api/flowrun.md`'s `params` field row. `ParamValue`'s doc now explicitly distinguishes `FlowRunSpec.params` (implemented) from `FlowStep.params` (still out of scope, see follow-up below).
  8. [x] Re-checked the worked examples in `flow.md`: Examples 2 and 3 (the two that declare `params:`) already follow the same-name auto-derivation convention and needed no changes; fixed one stale example under "Parameter Interpolation" that showed a `FlowRunSpec.Params`-shaped (`name`/`value`) block nested under `Flow.spec.params` (which is actually `[]ParamDeclaration`) — replaced with a `required: true` declaration plus a note on how a `FlowRun` would override it explicitly.

- [x] **FOLLOW-UP DECISION — decided and removed 2026-09-11** — `FlowStep.params` (`[]ParamValue`, distinct from `FlowRunSpec.params`): purpose was unclear from docs or code (one-line description, no example or test used it, not addressed by the Flow-parameters design record, explicitly out of scope for that implementation). No `$(stepParams.x)`-style mechanism exists anywhere it could have fed — a step can already reference `$(params.x)`, `$(steps.y.results.z)`, and `$(trigger.body.w)` directly inline. Owner decided: remove outright, same reasoning and same pre-public no-deprecation-needed basis as the `HTTPAction.BodyFrom` removal above. Removed the field from `api/v1alpha1/flow_types.go` (`ParamValue` itself stays — still used by `FlowRunSpec.Params` — its doc comment was updated to describe only that remaining usage), regenerated `config/crd/bases/` via `make generate && make manifests`, and mirrored the identical schema removal into `charts/kubezap/crds/` and `bundle/manifests/`. Removed the `params` row from `docs/api/flow.md`'s `FlowStep` table and simplified the `ParamValue` section (no longer needs to distinguish two usages, since only one remains). While mirroring the CRD diff, found `charts/kubezap/crds/` and `bundle/manifests/` are significantly stale relative to `config/crd/bases/` for reasons unrelated to this change (missing `HTTPAction.integrationRef`, missing resource-trigger fields, stale trigger-type enum still listing `pubsub` instead of `kafka`/`amqp`/`nats`/`resource`, missing `observedGeneration`, and more) — filed as a new tech-debt item below rather than fixed here, since resyncing it fully is a much larger, unrelated task.
- [x] **TECH DEBT (fixed 2026-09-11)** — `charts/kubezap-operator/crds/` and `bundle/manifests/` (the Helm chart and OLM bundle copies of the CRD schemas) were significantly out of sync with `config/crd/bases/`, the source of truth regenerated by `make manifests` — confirmed across all 4 CRDs (`Trigger`, `Flow`, `FlowRun`, `Integration`), not just the two originally spot-checked. Fixed: ran `make bundle VERSION=0.3.0` (matching the CSV's actual current version — the Makefile's `VERSION` default of `0.0.1` would have silently regressed the release version if run bare) to regenerate `bundle/manifests/`'s CRDs correctly, and manually copied `config/crd/bases/*.yaml` into `charts/kubezap-operator/crds/` (no generator exists for the Helm chart destination). Added a `make sync-crds` target, wired into `make manifests`, so the Helm chart copy can no longer silently drift — `bundle/manifests/` still requires an explicit `make bundle` (unchanged workflow, already documented in `docs/releasing.md`), not auto-wired, since bundle regeneration touches the CSV (see follow-up below). `helm lint charts/kubezap-operator` passes; `operator-sdk bundle validate ./bundle` passes.
- [ ] **BUG — found while fixing the item above, NOT FIXED** — `make bundle` regenerates `bundle/manifests/kubezap.clusterserviceversion.yaml` from `config/manifests/bases/kubezap.clusterserviceversion.yaml`, which has a genuinely empty OLM catalog icon (`base64data: ""`) and no `specDescriptors`/`resources` entries for any of the 4 owned CRDs (confirmed: zero `+operator-sdk:csv:...` markers exist anywhere in `api/v1alpha1/*.go`, so operator-sdk has nothing to derive them from). The currently-committed `bundle/manifests/kubezap.clusterserviceversion.yaml` has real icon data and hand-curated `specDescriptors` (plus a "Resource Trigger RBAC Note" paragraph) that exist **only** in that committed file — they were added by hand at some point and never fed back into `config/manifests/bases/`. Running `make bundle` today silently wipes all of it. Not fixed here — this pass deliberately reverted those regressions (`git checkout -- bundle/manifests/kubezap.clusterserviceversion.yaml`) to keep this fix scoped to CRD schemas only. **Fix**: add the icon `base64data` and `+operator-sdk:csv:customresourcedefinitions:type=spec,...` markers (or the equivalent hand-edit) to `config/manifests/bases/kubezap.clusterserviceversion.yaml` so `make bundle` becomes safe to run without a manual diff-and-revert dance every time.
- [x] **PHANTOM FIELD — decided and removed 2026-09-10** — `HTTPAction.BodyFrom` was never read anywhere in the controller (confirmed via grep: zero matches for `.BodyFrom` outside its type definition), and its documented purpose ("populate the body from a step result: `$(steps.name.results.field)`") was already fully covered by `body: "$(steps.name.results.field)"` via existing interpolation — it added no capability `body` didn't already have. Owner decided: remove outright rather than deprecate, since the project is pre-public with no external API consumers yet — the usual v1alpha1 no-removal-without-deprecation constraint doesn't apply until there's an actual consumer to break. Removed the field from `api/v1alpha1/flow_types.go`, regenerated `config/crd/bases/` via `make generate && make manifests`, and manually mirrored the identical schema removal into `charts/kubezap/crds/` and `bundle/manifests/` (not auto-synced by any Makefile target). No sample or example referenced the field. Removed the field's row from `docs/api/flow.md`'s `HTTPAction` table.
- [x] **BUG (fixed and unit-verified 2026-09-10, Kafka path only)** — `PublishAction.Topic` is documented as supporting `$(...)` interpolation, but for Kafka-type Integrations, `executePublishStep` passed `step.Action.Publish.Topic` through raw into `r.publishToKafka(...)`, never via `substituteVars`. A topic like `orders.$(trigger.body.region)` would have silently published to a literal, unresolved topic name. Fixed by resolving `topic := substituteVars(step.Action.Publish.Topic, stepResults, triggerData, params)` before use (deliberately `substituteVars`, not `substituteVarsWithSecrets` — a topic name pulled from a Secret would leak the secret value into Kafka broker metadata visible to any consumer). New test: `mockSyncProducer` now records the last `sarama.ProducerMessage` it received; a new unit test asserts the message's `Topic` is the interpolated value, not the raw template (206 controller tests, up from 205).
- [x] **BUG (fixed and unit-verified 2026-09-10)** — the **plugin-type** publish path (`integration.Spec.Plugin != nil` branch of `executePublishStep` / `doPluginPublish`) didn't send `Topic` at all, interpolated or not, and didn't send the documented JSON envelope in any form — it POSTed the raw `body` string as the HTTP request body with message `headers` set as literal HTTP request headers, discarding the response entirely. Owner decided: design record first (`docs/design/2026-09-10-plugin-publish-envelope.md`), then implement — this touches an external dependency's wire contract, per `docs/guides/design-process.md`'s trigger list. Fixed by having `doPluginPublish` build and send the documented `{integration, namespace, destination, headers, body}` JSON envelope (with `Content-Type: application/json` on the request; message headers now live inside the envelope, not as literal HTTP headers on the `/publish` call itself), with `destination` resolved via `substituteVars` over `Topic` (same as the Kafka-path fix above) and `body`/`headers` resolved via the existing `substituteVarsWithSecrets` calls. Response parsing added: a `2xx` with `{"messageId": "..."}` surfaces `messageId` as a step result (parity with the Kafka path's `partition`/`offset`); a non-`2xx` with `{"error": "..."}` uses that message verbatim, falling back to the raw body otherwise. 6 new unit tests against `doPluginPublish` via a real `httptest.Server` (envelope shape, header separation, messageId success, empty-body success, JSON error parsing, non-JSON error fallback) — 212 controller tests pass (was 206).
- [x] **DOC FIX (fixed 2026-09-10)** — `docs/api/integration.md` `KafkaSASLSpec`: documented a plain-text `username` field that doesn't exist in the Go type (`KafkaSASLConfig` only has `Mechanism`, `UsernameSecretRef`, `PasswordSecretRef` — no bare `Username`). Also `usernameSecretRef` was documented as not required (`No`) when the Go field has no `omitempty`/`+optional` marker and is genuinely required whenever `sasl:` is set. Fixed both; no example or sample used the phantom field.
- [x] **DOC FIX (fixed 2026-09-10)** — `docs/api/flow.md`: the CEL `when:` Conditions table listed `params`, `trigger.name`, and `trigger.type` as available CEL variables (none exist in the actual activation map built in `evaluateWhen`) and `trigger.payload` as a `map<string, dyn>` (the real field is `trigger.body`, a plain string — never parsed into a map for CEL). The worked example directly below the table (`trigger.payload.environment == "production"`) would have failed at evaluation time. Fixed the table to list the real activation map (`trigger.body`, `.topic`, `.partition`, `.offset`, `.scheduledTime`, `.headers`, `steps.*.status`, `steps.*.results`) and replaced the broken example with a working extract-then-branch pattern (matches what `examples/kafka-enrichment` and `examples/dlq-handler` actually do, validated live in §27).
- [x] **DOC FIX (fixed 2026-09-10)** — `docs/api/flow.md`'s `$(...)` Interpolation Quick Reference had the same `trigger.payload`/`trigger.name`/`trigger.type`/`params` issues as the CEL table, plus `trigger.header.<name>` (singular — the real placeholder is `trigger.headers.<name>`, plural) and two fully phantom entries, `$(configmaps.<name>.<key>)` and `$(env.<VAR_NAME>)`, neither of which exists anywhere in `substituteVars`. Fixed the table and the "when to use each credential source" list; the CEL Quick Reference examples further down the page had the identical `params.*`/`trigger.payload.*` pattern and were fixed the same way.

### P1 — Fix before GA

- [x] **DOC FIX (fixed 2026-09-10)** — `docs/api/trigger.md`: `WebhookRateLimit` (referenced from the `WebhookTrigger.rateLimit` field row) had no dedicated field-table section anywhere in the doc — `maxRequests` and `window` were undiscoverable except by reading the Go type or `examples/*/trigger.yaml`. Added a `### WebhookRateLimit` section with both fields and an example.
- [x] **DOC FIX (fixed 2026-09-10)** — `docs/api/flow.md`'s Step Action Types summary table was missing `publish` entirely, despite it being fully implemented and having its own `### PublishAction` field-table section lower on the same page, and being exercised live in `examples/kafka-enrichment` and `examples/dlq-handler` (§27). Also, both this table and the `### TransformAction` field table described `transform` mappings as using "CEL expressions" — they actually use `$(...)` string interpolation (`substituteVars`), a different mechanism from the CEL `when:` evaluator. Fixed both.

---

## 32. Helm Chart Rename — 2026-09-11

- [x] **CHORE (done 2026-09-11)** — Renamed the Helm chart from `kubezap` to `kubezap-operator`, per owner request. `charts/kubezap/` → `charts/kubezap-operator/` (directory rename via `git mv`, preserving history); `Chart.yaml`'s `name:` field updated to match. Updated every forward-looking reference to the old path/OCI artifact name: `.github/workflows/release.yml` (package + OCI push), `docs/releasing.md` (release runbook commands and checklist), `docs/overview.md` (7 `helm install`/values-file references), `README.md`, `docs/guides/dashboard.md`, and the `[Unreleased]` entry in `CHANGELOG.md`. Left historical, dated references alone (`CHANGELOG.md`'s `v0.3.0` entry; `docs/schedule.md` §31's completed-work entries describing the old path as it existed at the time) — updated only the one still-open §31 item that names the path going forward. `helm lint charts/kubezap-operator` passes. Internal template helper names (`_helpers.tpl`'s `kubezap.name`/`kubezap.fullname`/etc.) and resource-name prefixes generated by the chart (e.g. `kubezap-controller-manager`) were deliberately left alone — out of scope for "rename the chart," and changing generated resource names would be a distinct, bigger decision.

---

## 33. Web UI Removal — 2026-09-11

> Owner decision: both README.md and docs/overview.md's own tagline says "no web UI, no proprietary runtime, no vendor lock-in" — directly contradicted by the shipped `--enable-ui` dashboard. Rather than rewrite the tagline, owner chose to remove the dashboard entirely, since it's a completely separate feature from the `kubezap` CLI (which stays). Decision: **no deprecation cycle, no removal note anywhere** — delete the code and docs outright, and also strip historical records (CHANGELOG v0.3.0 entry, this schedule's own §15 section) rather than leave a paper trail. This is a decision, not a scoping guess — see the two bullets below for exactly what "strip historical records" means.

- [x] **REMOVAL (done 2026-09-11)** — Deleted the web dashboard entirely. Everything from the original scope list was removed as planned, plus additional scope found only once execution started (the original investigation missed the actual Vue frontend source, not just its built output):
  - `internal/ui/` — entire package (`server.go`, `api.go`, `sse.go`, `runnable.go`, their three `_test.go` files, and the built `dist/` assets).
  - `ui/` — **found during execution, not in the original scope**: the actual Vue frontend *source* (`src/`, `package.json`, `vite.config.ts`, etc.), compiled by `make ui` into `internal/ui/dist/`. Deleted entirely, along with its gitignored `node_modules/`.
  - `Makefile` — **found during execution**: removed the `.PHONY: ui` target (`cd ui && npm ci && npm run build`) and its dependency in `build: ui manifests generate fmt vet`.
  - `.github/workflows/ci.yml` — **found during execution**: removed the `actions/setup-node@v4` step in the `build` job — it existed only to support the now-deleted frontend build.
  - `.gitignore` — **found during execution**: removed the now-meaningless `ui/node_modules/` and `internal/ui/dist/` entries.
  - `cmd/main.go` (hot file) — removed the `internal/ui` import, `enableUI`/`uiPort`/`uiBearerToken` vars and their three `flag.*Var` registrations, and the `if enableUI { ... }` block.
  - `config/dev/manager_dev_patch.yaml` — removed the `--enable-ui=true` arg.
  - `docs/guides/dashboard.md`, `docs/design/dashboard.md` — deleted both files.
  - `README.md`, `docs/overview.md` (2 mentions: architecture paragraph + shipped-features checklist), `docs/contributing.md` (2 whole sections: "Enabling the UI when deploying with `make deploy`", "Running the UI locally") — all dashboard/`--enable-ui` mentions removed.
  - `docs/docs-rewrite-plan.md` — removed all 6 `dashboard.md`-related rows/mentions (classification table x2, directory-tree sketch x2, move-mapping list, per-file review section, getting-started rewrite brief, remaining-guides polish list); left the rest of the file untouched as noted was fine.
  - No Helm chart or RBAC changes were needed, confirmed — the dashboard reused the controller's existing manager client/cache with no dedicated ServiceAccount, port, or extra permission.
  - **Not touched, confirmed correct**: the `kubezap` CLI (`internal/cli/`) — doesn't import `internal/ui`, fully independent.
  - `go build ./...`, `go vet ./...`, and `make test` all pass with `internal/ui` no longer appearing in the package list at all.
- [x] **REMOVAL — historical records (done 2026-09-11)** — Per owner decision, also removed the records that described the dashboard as already-shipped, rather than leaving them as historical record:
  - `CHANGELOG.md`'s `[v0.3.0]` entry — removed the `**Web dashboard**` and `**--enable-ui / --ui-port**` bullets, and its file-list bullet's `docs/guides/dashboard.md` mention.
  - `docs/overview.md`'s shipped-features checklist — removed the `Web dashboard (... --enable-ui flag)` line (found during execution; not in the original two-item scope, but the same category of "describes the dashboard as shipped").
  - `docs/schedule.md` §15 ("Dashboard / Monitoring UI") — removed the section entirely (its "Complete" note and 4 Phase-3 `FUTURE` sub-items).
  - This remains a deliberate, one-off exception to this file's normal practice of treating completed-work log entries as an immutable historical record (§31's entries, describing superseded paths, were correctly left alone during the chart-rename and CRD-drift-resync work) — applied here only because the owner explicitly asked for it for this specific feature.

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
