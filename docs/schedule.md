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

### P1 — Should fix before public

- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. Do this after e2e tests are green. **Run after all §17 P0 items are complete** — validation before the security refactors (HTTP executor split, OwnNamespace default, FlowRef removal) would need to be re-run after, so sequence this last among P1 items.

---

## 17. Security Hardening — 2026-03-24 Review

> Items from the security design review (`docs/security-review-2026-03-24.md`). All items complete as of 2026-03-27.

---

## 18. Code Quality — 2026-03-27 Review

> Items from the periodic health review. Ordered P0 → P1 → P2.

### P1 — Should fix before public

- [ ] **BUG** — `internal/controller/flowrun_controller.go` line ~442: `StepRunStatus.Attempts` hardcoded to 1 regardless of actual retry count. Thread attempt count through `executeHTTPStep` and publish step return path. Known TODO(T7). **Owner input needed** — see `docs/tech-debt/pending-input-required.md` §2026-03-27.
- [ ] **DOCS** — `docs/api/trigger.md`: add alpha-stability callout to the Resource Trigger section (prominent warning, not just in overview.md). Users reading API docs without reading overview may not realise `type: resource` is alpha-quality.
- [ ] **DOCS** — Write `docs/guides/security-checklist.md`: single operator-facing page linking all security considerations (webhook auth, SSRF, RBAC, NetworkPolicy, mTLS, OwnNamespace default, plugin image digest, CEL cost limits). Required before OperatorHub submission — evaluators expect a runnable security checklist.

### P2 — Nice to have before public

- [ ] **TECH DEBT** — `internal/controller/resource_watcher.go`: missing kubebuilder RBAC markers for dynamic informer watches. Document dynamic watch requirements; add to RBAC generation. (Code agent finding from 2026-03-27 review.)
- [ ] **TECH DEBT** — `internal/controller/executor_reconciler.go`: executor Deployment missing `terminationGracePeriodSeconds`. Set to 30s or the configured step timeout, whichever is larger. Prevents in-flight HTTP requests from being interrupted on rolling updates.
- [ ] **TECH DEBT** — `internal/controller/flowrun_controller.go`: executor RPC transport failures (pod unavailable) should requeue with exponential backoff rather than immediately failing the step. **Owner input needed** — see `docs/tech-debt/pending-input-required.md` §2026-03-27.
- [ ] **OBSERVABILITY** — `internal/controller/flowrun_controller.go` Kafka producer pool: add debug-level log events for cache hits, cache misses, evictions, and creation. Needed for production diagnostics.
- [ ] **TESTING** — `internal/controller/executor_mtls.go`: add test for concurrent bundle access during the 23h rotation window. Low probability race but not currently covered.
- [ ] **TESTING** — `internal/controller/flowrun_controller.go` Kafka producer cache: add unit test for idle TTL eviction (10m). Currently untested.

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
