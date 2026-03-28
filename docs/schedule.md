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

---

## 1. Deployment & Distribution

> **Unblocked 2026-03-21.** OperatorHub submission is now a target, but gated on §16 P1 VALIDATION and §18 P1 items. **Paused 2026-03-22 pending §16 completion.**
> GitHub org migration to `kubezap/kubezap-operator` complete (2026-03-22). Module path is `github.com/kubezap/kubezap-operator`.
> OLM bundle passes `bundle validate` and `scorecard` as of 2026-03-21.

- [ ] OperatorHub submission PR — gates on §16 P1 VALIDATION + §18 P1 items complete

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

- [ ] **TECH DEBT** — `internal/controller/resource_watcher.go`: missing kubebuilder RBAC markers for dynamic informer watches. Document dynamic watch requirements; add to RBAC generation.
- [ ] **TECH DEBT** — `internal/controller/executor_reconciler.go`: executor Deployment missing `terminationGracePeriodSeconds`. Set to 30s or the configured step timeout, whichever is larger.
- [ ] **TECH DEBT** — `internal/controller/flowrun_controller.go`: executor RPC transport failures should requeue with exponential backoff rather than immediately failing the step. **Owner input needed** — see `docs/tech-debt/pending-input-required.md` §2026-03-27.
- [ ] **OBSERVABILITY** — `internal/controller/flowrun_controller.go` Kafka producer pool: add debug-level log events for cache hits, cache misses, evictions, and creation.
- [ ] **TESTING** — `internal/controller/executor_mtls.go`: add test for concurrent bundle access during the 23h rotation window.
- [ ] **TESTING** — `internal/controller/flowrun_controller.go` Kafka producer cache: add unit test for idle TTL eviction (10m).

---

## 16. Pre-Public Readiness — 2026-03-22 Review

> OperatorHub submission is **paused** pending this section's completion. Run §18 P1 items first (see §18 header).

### P1 — Should fix before public

- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. **Run after all §18 P1 items are complete** — validation before the StepRunStatus fix and security checklist gives incomplete signal.

---

## 17. Security Hardening — 2026-03-24 Review

> All items complete as of 2026-03-27.

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
