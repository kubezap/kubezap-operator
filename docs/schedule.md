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
8. **Public readiness (§16) gates OperatorHub submission (§8)** — all P0 items in §16 must be complete before the OperatorHub submission PR is opened. P1 items should be resolved first; P2 items are nice-to-have.

---

## 1. Deployment & Distribution

> **Unblocked 2026-03-21.** OperatorHub submission is now a target, but gated on §12 architecture blockers, §16 P0 items (pre-public readiness), and OLM readiness tasks below. **Paused 2026-03-22 pending §16 completion.**

- [ ] **INFRA** — Create GitHub org `kubezap`, repo `kubezap-operator`, push codebase, update git remote. Module path and all code references are already updated to `github.com/kubezap/kubezap-operator`.
- [ ] OperatorHub submission PR — gates on §12 completion, §16 P0 items, and OLM readiness tasks below

### Release Process (document and automate before first public release)

> Define the full release runbook so that cutting a release is a single documented procedure rather than ad-hoc steps.

- [ ] **RELEASE** — Write `docs/releasing.md` (internal): step-by-step release runbook covering: (1) version bump in `go.mod`, `Chart.yaml`, CSV `spec.version`/`spec.replaces`, `CHANGELOG.md`; (2) `git tag vX.Y.Z` + push tag; (3) GoReleaser publish (`goreleaser release --clean`) → GitHub Release + CLI binaries; (4) GHCR image push (via CI or manual `docker push`); (5) Helm chart publish (OCI push to `ghcr.io/kubezap/charts/kubezap` or GitHub Pages chart repo); (6) OLM bundle regeneration (`make bundle`) + OperatorHub PR update; (7) post-release smoke test checklist.
- [ ] **INFRA** — Add GoReleaser GitHub Actions workflow (`.github/workflows/release.yml`): triggers on `vX.Y.Z` tag push; builds CLI binaries for linux/darwin/windows amd64+arm64; builds and pushes all five container images to `ghcr.io/kubezap/*`; creates GitHub Release with changelog and binary attachments.
- [ ] **INFRA** — Publish Helm chart: decide on distribution mechanism (OCI registry at `ghcr.io/kubezap/charts/kubezap` vs. GitHub Pages `helm repo`); add chart publish step to release workflow; document `helm repo add` or `helm install --oci` install path in `docs/overview.md`.
- [ ] **INFRA** — Add `CHANGELOG.md` covering v0.1 → v0.3 milestones (required for enterprise evaluators and acquisition targets; also referenced from OperatorHub CSV `spec.replaces` chain). Follow Keep a Changelog format.
- [ ] **INFRA** — Automate GHCR image publishing on merge to `main` (`:latest` tag) in addition to version tags, so contributors can always pull a fresh build without building locally.

### OLM Readiness (required before submission)

- [x] **OLM** — Complete required CSV fields in `bundle/manifests/kubezap.clusterserviceversion.yaml`: `spec.description` (full feature overview), `spec.icon` (base64 PNG), `spec.maintainers`, `spec.provider.name`, `spec.maturity` (`alpha`), `spec.links` (docs, source). These are required for OperatorHub acceptance.
- [x] **OLM** — Run `operator-sdk bundle validate ./bundle` and fix all failures. Must pass before submission.
- [x] **OLM** — Run `operator-sdk scorecard ./bundle` against a live cluster and fix all failures. Both `basic` and `olm` suites must pass.
- [x] **OLM** — Add resource trigger RBAC caveat to CSV description: in AllNamespaces mode, user-configured `type: resource` triggers may require the controller SA to have broad watch permissions on target resource types. Users must grant these explicitly.

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
- [x] **VUE** — Implement `FlowRunList.vue`: fetches `/api/v1/:ns/flowruns`, `FilterBar.vue` (phase/trigger/flow dropdowns + since picker), `FlowRunTable.vue` + `FlowRunRow.vue`, `PhaseChip.vue` (colour-coded badge), `RelativeTime.vue` (updates every 10s), `DurationCell.vue`.
- [x] **VUE** — Add SSE to `FlowRunList`: `composables/useFlowRunEvents.ts` using `EventSource`. Merge incoming events into a reactive `Map<name, FlowRunSummary>` so in-flight updates appear without a full reload.

#### Step 5 — Vue: FlowRun detail

- [x] **VUE** — Implement `FlowRunDetail.vue`: fetches `/api/v1/:ns/flowruns/:name`, `FlowRunHeader.vue` (name, trigger→flow link, phase, elapsed), `StepTimeline.vue` + `StepRow.vue`, `StepBadge.vue` (✓ ✗ ● ○ - matching CLI watch badges). SSE on the detail page re-fetches the single FlowRun on each event matching the viewed name.

#### Step 6 — Vue: trigger + flow lists

- [x] **VUE** — Implement `TriggerList.vue` + `TriggerRow.vue` + `TriggerTypeChip.vue`: fetches `/api/v1/:ns/triggers`, shows type badge, ready status, last-fired time, active FlowRun count.
- [x] **VUE** — Implement `FlowList.vue` + `FlowRow.vue`: fetches `/api/v1/:ns/flows`, shows step count, ready status, last-used time.

#### Step 7 — Docs + service manifest

- [x] **DOCS** — Write `docs/guides/dashboard.md`: enabling `--ui-port`, port-forward access pattern, `--ui-bearer-token` for optional Ingress exposure, kube-rbac-proxy sidecar pattern for production auth.

### Phase 3 — Future (Tier 3, deferred)

> Design notes in `docs/design/dashboard.md` § "Phase 3 — Future Plans".

- [ ] **FUTURE** — Integration health page (`/api/v1/:ns/integrations`, `IntegrationList.vue`)
- [ ] **FUTURE** — Activity graph: FlowRun rate over time from in-process Prometheus registry
- [ ] **FUTURE** — Search: `?q=` substring filter on trigger/FlowRun name
- [ ] **FUTURE** — OIDC auth (`--ui-oidc-issuer` etc.) or document kube-rbac-proxy as the recommended production auth path

---

## 16. Pre-Public Readiness — 2026-03-22 Review

> Items from the Opus-model readiness audit before public availability. Ordered P0 → P1 → P2.
> OperatorHub submission is **paused** pending this section's completion.

### P0 — Blocks public release

- [ ] **TESTING** — E2E tests are failing. Triage failures (`make test-e2e`), determine if pre-existing or recent regressions, and fix. Must pass before public release.
- [ ] **REPO HYGIENE** — Gitignore all Claude-related files before public availability. Currently `.gitignore` excludes `.claude/*` but re-includes `settings.json`, hooks, skills, commands, agents, and agent-memory. Decide which (if any) to retain for contributors; for a clean first-public commit, exclude everything under `.claude/`.
- [x] **BRANDING** — `--ui-port` flag redesign: `--enable-ui` bool (default false) + `--ui-port` int (default 8082); auto-create `kubezap-ui` Service when enabled. Remove Zapier/Camunda references from README and overview. (PR #65)
- [x] **BRANDING** — Go module path renamed from `github.com/borfswitch/kubezap` to `github.com/kubezap/kubezap-operator`. New GitHub org: `kubezap`, repo: `kubezap-operator`. All `.go` imports, `go.mod`, `PROJECT`, `.goreleaser.yaml`, CSV `repository` field, and docs updated. Makefile CRD generator split to fix controller-gen v0.18.0 `paths="./..."` storage-version issue.
- [x] **DOCS** — `docs/api/flow.md` line 78: stale "top-level JSON fields only" limitation warning for `$(trigger.body.<field>)`. Full dot-path was implemented in §14; this contradicts `overview.md` and will confuse users immediately. Remove the limitation block.
- [x] **DOCS** — `docs/api/flowrun.md` + `docs/architecture.md`: multiple `type: pubsub` references remain after §12a API refactor. FlowRun Creator table, TriggerReference type enum, Kafka YAML examples all still say `pubsub`. Users following these docs write broken Trigger specs.
- [x] **DOCS/CODE** — `docs/guides/webhook-security.md` documents `type: header-equals` auth (lines 100, 301–302) but it is not in `WebhookAuth.Type` enum and not handled in `handler.go`. A user following the guide gets a CRD validation error. Either implement (trivial, ~10 lines) or remove from docs.
- [x] **REPO HYGIENE** — Verify `.claude/worktrees/` and `ui/node_modules/` are excluded by `.gitignore` and not tracked in git. Verified: all paths already correctly excluded by existing `.gitignore` rules; no changes needed.

### P1 — Should fix before public

- [ ] **INFRA** — Set up GHCR image publishing: add a GitHub Actions workflow (`.github/workflows/release.yml` or similar) that builds and pushes all five images (`controller`, `webhook-gateway`, `kafka-gateway`, `amqp-gateway`, `nats-gateway`) to `ghcr.io/kubezap/*` on tag push and/or merge to main. Update `contributing.md` with authenticated pull instructions once the packages are public.
- [ ] **VALIDATION** — Manual end-to-end pass: run through each example in `examples/`, exercise the `kubezap` CLI (watch, history, triggers, flows), and open the web dashboard (`--enable-ui`). Collect feedback and file follow-up tasks. Do this after e2e tests are green.
- [ ] **DOCS** — Improve `docs/contributing.md`: add architecture orientation section (binary layout, reconciler entry points, gateway entry points, key packages); add "first contribution" guide (good-first-issue labels, how to run a single test, how to add a new step action type); expand e2e section with AMQP/NATS skip vars and amqp/nats gateway build steps (already fixed in Building section); add section on running the UI locally (`make ui && go run cmd/main.go --enable-ui`); add section on generating and validating OLM bundle.
- [ ] **DOCS** — **User-facing documentation rewrite (Opus research task).** The current `docs/` tree is development-oriented (implementation specs, Claude context, internal design notes). Before public release, commission an Opus-model deep-dive to: (1) audit every doc file and classify as user-facing, internal/dev-only, or reference; (2) design a two-tier doc structure — `docs/` for user-facing content, `docs/dev/` (or `docs/internal/`) for internal/Claude context; (3) draft a rewrite plan for each user-facing doc to make it concise, task-oriented, and example-heavy rather than spec-heavy; (4) produce a priority-ordered implementation list. This task produces a plan only — implementation is a separate task. **Prompt:** `/research` with scope: "audit all docs for user-facing vs internal classification, design a two-tier doc structure, and produce a rewrite plan ordered by user impact."
- [ ] **DOCS** — `docs/guides/observability.md` metrics table: lists `kubezap_webhook_requests_total` and `kubezap_webhook_request_body_bytes` which do not exist in `internal/metrics/metrics.go`. Reconcile the entire table against actual registered metrics.
- [ ] **DOCS/API** — `docs/api/trigger.md` + `docs/guides/webhook-security.md`: WebhookAuth YAML examples show nested `hmac:` / `bearer:` / `oidc:` sub-keys, but Go types are flat fields (`HMACSecretRef`, `BearerTokenSecretRef`, `OIDCIssuer`). YAML in the docs doesn't match what the CRD accepts. Decide: restructure Go types to match docs (better UX) or update all examples to flat structure.
- [ ] **DOCS** — `pubsub` terminology appears in 13+ doc files: `flow.md`, `integration.md`, `plugin-contract.md`, `amqp-setup.md`, `nats-setup.md`, `observability.md`, `troubleshooting.md`, `using-the-cli.md`. Global grep-and-replace pass needed.
- [ ] **DOCS** — `README.md`: "Helm chart _(coming in v0.3)_" and "OperatorHub _(coming in v0.3)_" are stale (both exist). Features section missing AMQP, NATS, resource trigger, web dashboard, CLI. Update to reflect current feature set.
- [ ] **DOCS** — `docs/api/flowrun.md`: resource trigger creator still shows "_(planned)_" — implemented (alpha). Fix label and verify FlowRun naming pattern matches current `resource_watcher.go`.
- [ ] **SECURITY** — `internal/gateway/webhook/handler.go`: bearer token and API key comparisons use plain string `!=` (not constant-time). HMAC already uses `hmac.Equal`. Fix: use `subtle.ConstantTimeCompare` for `bearer` and `apiKey` auth types.
- [ ] **BRANDING** — CSV `bundle/manifests/kubezap.clusterserviceversion.yaml`: version is `v0.0.1`, icon is a 1×1 placeholder PNG. Update version to match actual release; create a real icon (≥64×64).
- [ ] **BRANDING** — No `CHANGELOG.md`. Enterprise evaluators and acquisition targets expect a changelog. Create covering v0.1 → v0.3 milestones.
- [ ] **DOCS** — `docs/overview.md` line ~493: claims `resultMappings` supports XPath for XML. No XPath parser exists in the codebase. Remove claim; document JSONPath only.

### P2 — Nice to have before public

- [ ] **DOCS** — `docs/architecture.md`: Component Overview table and ASCII diagram list only 3 components; AMQP and NATS gateways are missing. Add them.
- [ ] **DOCS** — `docs/architecture.md` line 321: "KEDA integration planned for v0.3" — v0.3 is complete. Change to "KEDA is recommended as an external HPA replacement for Kafka gateways."
- [ ] **CLEANUP** — Remove Kubebuilder scaffold boilerplate: `// TODO(user): If you enable certManager...` comment in `cmd/main.go`; `// EDIT THIS FILE! THIS IS SCAFFOLDING FOR YOU TO OWN!` in `api/v1alpha1/trigger_types.go`.
- [ ] **DOCS** — `docs/overview.md` Getting Started link uses `../examples/order-router/` — relative path may break on hosted doc sites. Verify or use absolute GitHub link.

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
