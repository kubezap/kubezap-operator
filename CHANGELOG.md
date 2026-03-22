# Changelog

All notable changes to KubeZap are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### Added
- GitHub Actions CI workflow: unified lint, test, build, docker-build on every push and PR
- GitHub Actions release workflow: GoReleaser CLI binaries + container image push to GHCR on tag
- GitHub Actions publish-latest workflow: `:latest` image push on merge to `main`
- Helm chart published to OCI registry (`ghcr.io/kubezap/charts/kubezap`)
- `docs/releasing.md` — internal release runbook

### Fixed
- All container image references migrated from `docker.io/kubezap/` to `ghcr.io/kubezap/`
- Stale `github.com/Borfswitch/kubezap` issue URL replaced with `kubezap/kubezap-operator`
- Wrong `kubezap.io/` container registry domain in OLM CSV base manifest

---

## [v0.3.0] — 2026-03-21

### Added
- **Helm chart** (`charts/kubezap`) — configurable namespace mode, gateway image overrides, RBAC
- **GoReleaser** — multi-platform CLI binaries (`kubezap` + `kubectl-kubezap`) for linux/darwin/windows amd64+arm64
- **AMQP gateway** (`kubezap/amqp-gateway`) — RabbitMQ, ActiveMQ Artemis, Azure Service Bus, IBM MQ (beta)
- **NATS gateway** (`kubezap/nats-gateway`) — NATS JetStream (beta)
- **`kubezap watch`** CLI subcommand — live FlowRun execution timeline with per-step status badges and elapsed time
- **`kubezap history`**, **`kubezap triggers`**, **`kubezap flows`** CLI subcommands
- **Web dashboard** — read-only Vue 3 SPA embedded in operator binary; enable with `--enable-ui`; SSE live updates
- **`type: http` Integration** — centralized HTTP credentials (base URL, bearer token, TLS config) for `type: http` flow steps
- **`type: header-equals` webhook auth** — additional per-Trigger auth type alongside HMAC, bearer, OIDC, API-key, IP-allowlist, mTLS
- **`--disable-cel-cache`** operator flag — escape hatch for CEL expression cache debugging
- **`--enable-ui` / `--ui-port`** operator flags — replaces previous `--ui-port`-only design
- OLM bundle validated (`operator-sdk bundle validate` + scorecard; both `basic` and `olm` suites pass)
- Multi-namespace support: all four OLM install modes (AllNamespaces, SingleNamespace, MultiNamespace, OwnNamespace)
- KEDA recommended for Kafka gateway scaling (partition-bounded consumer); documented in architecture guide
- Structured access logs with source IP (webhook gateway); source IP excluded from Prometheus label values (cardinality)
- `docs/guides/dashboard.md`, `docs/guides/cron-triggers.md`, `docs/guides/troubleshooting.md`
- `docs/guides/amqp-setup.md`, `docs/guides/nats-setup.md`, `docs/guides/using-the-cli.md`
- Kubernetes resource-event trigger (`type: resource`) — alpha; dynamic informers in controller (known bugs tracked)

### Changed
- Go module path renamed to `github.com/kubezap/kubezap-operator` (GitHub org: `kubezap`)
- `type: pubsub` trigger renamed to `type: kafka` / `type: amqp` / `type: nats` at API level
- Metrics port normalized to `:9090` HTTP across all components
- `MockEndpoint` CRD removed; replaced by Mockoon (see `docs/guides/mocking-http-endpoints.md`)

### Fixed
- CEL dot-path variable resolution (`$(trigger.body.nested.field)`) — full depth now supported
- `docs/api/flow.md` stale "top-level JSON fields only" limitation removed

---

## [v0.2.0] — 2026-02-15

### Added
- **CEL `when` expression evaluation** — per-step conditional execution using Google CEL
- **Skipped step phase** with downstream cascade (skipped step skips all dependents)
- **Step output data passing** — `$(steps.<name>.results.<key>)` variable substitution between steps
- **`type: transform` step** — JSONPath/CEL data transformation between steps
- **Retry policies** — per-step `retryPolicy` with exponential, linear, and fixed backoff strategies
- **Flow-level and per-step timeouts** — `spec.timeout` and per-step `timeout` with requeueing
- **`type: publish` step** — routes events to Kafka topics or plugin `/publish` endpoints via `integrationRef`
- **`type: wait` step** — blocking pause with restart-safe `ResumeAfter` timestamp in FlowRun status
- Parallel step execution — steps with the same `runAfter` set execute concurrently
- CEL expression cache (`sync.Map`) for compiled program reuse across reconcile loops

### Fixed
- FlowRun GC respects `spec.ttlAfterFinished`, operator-level `--flowrun-ttl-succeeded` / `--flowrun-ttl-failed` flags, and `spec.maxFlowRuns` on Trigger
- `kubezap.io/retain=true` annotation exempts FlowRuns from GC

---

## [v0.1.0] — 2026-01-20

### Added
- **`Trigger` CRD** — webhook, cron, Kafka trigger sources with per-trigger webhook auth
- **`Flow` CRD** — ordered DAG steps with `runAfter` dependencies and `type: http` actions
- **`FlowRun` CRD** — execution history, per-step status, phase lifecycle (`Pending` → `Running` → `Succeeded`/`Failed`)
- **`Integration` CRD** — Kafka built-in integration; plugin protocol for custom integrations
- **Webhook gateway** — HTTP server with dynamic route registration; one Deployment per namespace managed by operator
- **Cron scheduler** — `robfig/cron` v3-based; creates FlowRuns on schedule
- **Kafka gateway** — Sarama consumer group; one Deployment per (namespace × Kafka Integration)
- **Webhook auth** — HMAC-SHA256, bearer token, OIDC/JWT, API-key header, IP allowlist, mTLS; all per-Trigger
- **Observability** — Prometheus metrics (`/metrics`), OpenTelemetry traces, structured JSON access logs
- **Multi-namespace** — `WATCH_NAMESPACES` env var controls scope; Role vs. ClusterRole per mode
- **FlowRun naming** — deterministic per trigger type (webhook: `<trigger>-<ts>-<rand>`, kafka: `<trigger>-p<partition>-offset-<offset>`, cron: `<trigger>-<scheduled-time>`)
- OLM bundle scaffolded via Operator SDK
- `config/samples/` — example CRs for all CRD types
- Kubebuilder-scaffolded project with distroless/static:nonroot base images

[Unreleased]: https://github.com/kubezap/kubezap-operator/compare/v0.3.0...HEAD
[v0.3.0]: https://github.com/kubezap/kubezap-operator/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/kubezap/kubezap-operator/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/kubezap/kubezap-operator/releases/tag/v0.1.0
