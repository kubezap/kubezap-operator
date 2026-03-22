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

### P1 — Should fix before public

- [ ] **SECURITY** — `internal/gateway/webhook/handler.go`: bearer token and API key comparisons use plain string `!=` (not constant-time). HMAC already uses `hmac.Equal`. Fix: use `subtle.ConstantTimeCompare` for `bearer` and `apiKey` auth types. Must be fixed before the WebhookAuth struct restructure below, as both touch the same handler.
- [ ] **DOCS/API** — `docs/api/trigger.md` + `docs/guides/webhook-security.md` + `api/v1alpha1/trigger_types.go` + `internal/gateway/webhook/handler.go`: **Decision made (2026-03-22):** Restructure Go types to use nested structs (`HMACConfig`, `BearerConfig`, `OIDCConfig`, `APIKeyConfig`, `IPAllowlistConfig`, `MTLSConfig`) inside `WebhookAuth`. Update handler to read from new nested fields. Update all YAML examples in docs to match new nested structure. Run `make generate && make manifests` after type changes.
- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. Do this after e2e tests are green.
- [ ] **DOCS** — Improve `docs/contributing.md`: add architecture orientation section (binary layout, reconciler entry points, gateway entry points, key packages); add "first contribution" guide (good-first-issue labels, how to run a single test, how to add a new step action type); add section on running the UI locally (`make ui && go run cmd/main.go --enable-ui`); add section on generating and validating OLM bundle.
- [ ] **DOCS** — **User-facing documentation rewrite (Opus research task).** Current `docs/` tree is development-oriented. Commission an Opus-model deep-dive to: (1) audit every doc file and classify as user-facing vs. internal/dev-only; (2) design a two-tier structure (`docs/` user-facing, `docs/dev/` internal); (3) draft a rewrite plan for each user-facing doc; (4) produce a priority-ordered implementation list. Research only — implementation is a separate task. **Prompt:** `/research` with scope: "audit all docs for user-facing vs internal classification, design a two-tier doc structure, and produce a rewrite plan ordered by user impact."
- [ ] **DOCS** — `docs/guides/observability.md` metrics table: lists `kubezap_webhook_requests_total` and `kubezap_webhook_request_body_bytes` which do not exist in `internal/metrics/metrics.go`. Reconcile the entire table against actual registered metrics.
- [ ] **DOCS** — `pubsub` terminology appears in 13+ doc files: `flow.md`, `integration.md`, `plugin-contract.md`, `amqp-setup.md`, `nats-setup.md`, `observability.md`, `troubleshooting.md`, `using-the-cli.md`. Global grep-and-replace pass needed.
- [ ] **DOCS** — `README.md`: "Helm chart _(coming in v0.3)_" and "OperatorHub _(coming in v0.3)_" are stale (both exist). Features section missing AMQP, NATS, resource trigger, web dashboard, CLI. Update to reflect current feature set.
- [ ] **DOCS** — `docs/api/flowrun.md`: resource trigger creator still shows "_(planned)_" — implemented (alpha). Fix label and verify FlowRun naming pattern matches current `resource_watcher.go`.
- [ ] **BRANDING** — CSV `bundle/manifests/kubezap.clusterserviceversion.yaml`: version is `v0.0.1`, icon is a 1×1 placeholder PNG. Update version to match actual release; create a real icon (≥64×64).
- [ ] **DOCS** — `docs/overview.md` line ~493: claims `resultMappings` supports XPath for XML. No XPath parser exists in the codebase. Remove claim; document JSONPath only.
- [ ] **BUG** — `internal/controller/resource_watcher.go` line ~113: naive pluralization (`strings.ToLower(kind) + "s"`) fails silently for irregular plurals (`Ingress` → `ingresss`, `NetworkPolicy` → `networkpolicys`). Fix: use discovery API to resolve correct plural form; fall back to naive `+s` with a warning log if discovery fails. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- [ ] **BUG** — `internal/controller/resource_watcher.go` line ~207: FlowRun names use Unix timestamp at second precision with no random suffix — two events for the same resource+eventtype within the same second collide silently (second FlowRun is dropped). Add a short random suffix. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.

### P2 — Nice to have before public

- [ ] **DOCS** — `docs/architecture.md`: Component Overview table and ASCII diagram list only 3 components; AMQP and NATS gateways are missing. Add them.
- [ ] **DOCS** — `docs/architecture.md` line 321: "KEDA integration planned for v0.3" — v0.3 is complete. Change to "KEDA is recommended as an external HPA replacement for Kafka gateways."
- [ ] **CLEANUP** — Remove Kubebuilder scaffold boilerplate: `// TODO(user): If you enable certManager...` comment in `cmd/main.go`; `// EDIT THIS FILE! THIS IS SCAFFOLDING FOR YOU TO OWN!` in `api/v1alpha1/trigger_types.go`.
- [ ] **DOCS** — `docs/overview.md` Getting Started link uses `../examples/order-router/` — relative path may break on hosted doc sites. Verify or use absolute GitHub link.
- [ ] **BUG** — `internal/controller/resource_watcher.go`: no retry when informer cache sync fails (transient RBAC issue or API server blip exits the watcher goroutine permanently). Trigger stays "registered" but is dead until reconciler re-registers on next Trigger touch. Add retry with backoff. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- [ ] **BUG** — `internal/controller/resource_watcher.go`: no cooldown mechanism. Rapidly-updated resources (e.g., Pod status churn) with no `watchFields` filter create a FlowRun on every update. Add `maxInvocations`/`window` rate-limiting consistent with other trigger types. Evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- [ ] **TECH DEBT** — `internal/controller/integration_controller.go`: Kafka producer pool (`kafkaProducers` map) has no TTL or health check; stale connections not detected until next publish attempt fails. Add periodic health check or TTL eviction. Evidenced by `docs/review-latest.md`.
- [ ] **TECH DEBT** — `internal/controller/flowrun_controller.go`: CEL environment init failure cached forever via `sync.Once` — a transient failure permanently disables CEL evaluation for the pod lifetime. Replace with retriable init that resets on failure. Evidenced by `docs/review-latest.md`.

---

## 10. Future / Backlog

- [x] Kubernetes resource-event trigger type (`type: resource`) — implemented (alpha). Four known bugs tracked in §16 P1/P2. Example 6 (K8s ITSM) is blocked on pluralization fix.
- [ ] `Step` CRD for reusable step definitions
- [ ] Multi-namespace flows (cross-namespace FlowRun)
- [ ] Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- [ ] Plugin catalog / marketplace in `docs/plugins/` with community registry and maturity levels
- [ ] Reference plugin implementation in `docs/plugins/example-plugin/`
- [ ] OpenLineage support
- [ ] Multi-region HA support
- [ ] S3/Git event trigger source
