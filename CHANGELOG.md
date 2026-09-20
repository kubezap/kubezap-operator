# Changelog

All notable changes to KubeZap are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/).

---

## [v0.2.1] - 2026-09-20

### Fixed

- Helm chart's `NOTES.txt` "create your first automation" command linked to `config/samples/automation_v1alpha1_trigger_webhook.yaml`, which doesn't exist — the exact command every new Helm user is shown 404'd. Repointed at the real sample file, and its `flowRef` (which itself pointed at a nonexistent Flow sample) fixed to reference the actual `handle-order` sample so it works end-to-end.
- OLM bundle's manager image was pinned to a floating `:latest` tag instead of the release version, in both the CSV's Deployment spec and its `containerImage` annotation — `operator-sdk bundle validate` flags this, and it defeats the reproducibility a versioned bundle is supposed to guarantee. Added `make bundle-pin-digest` for OperatorHub submissions that need digest-pinning instead of tag-pinning.
- `MultiNamespace` mode could crash a raw-manifest/kustomize install (`make deploy`) if `WATCH_NAMESPACES` didn't happen to include the operator's own namespace — the controller never automatically added it to its own cache scope (only the Helm chart's template helper did, since v0.2.0's STORY-063 fix). Fixed at the source in `cmd/main.go` so both install paths are covered.
- Discovered and fixed a related MultiNamespace deadlock while restoring e2e coverage for the above: a `Forbidden` RBAC response on any one watched namespace's informer blocks the controller's cache sync manager-wide, stalling reconciliation in *every* watched namespace, not just the one missing RBAC. Documented as an explicit ordering requirement (provision RBAC before the namespace is ever in `WATCH_NAMESPACES`) in `config/rbac/namespaced_role.yaml` and `docs/architecture.md`.
- Restored the `E2E Tests` GitHub Actions workflow, left broken since `v0.2.0`'s AllNamespaces-mode removal (it still set the now-rejected `WATCH_NAMESPACES=*`).

## [v0.2.0] - 2026-09-20

### Removed

- `WATCH_NAMESPACES=*` (AllNamespaces watch mode) — removed entirely. Native Kubernetes RBAC cannot express the documented "restricted to `kubezap.io/managed=true` namespaces" requirement, and every attempt to compensate for that gap produced disproportionate hardening work for a mode with zero real deployments. Rejected at startup (fail-fast) in the controller and all four gateway binaries. See `docs/design/namespace-scoped-watch-modes-only.md`.
- OLM CSV `AllNamespaces` install mode: now `supported: false`.
- The operator's own cluster-scoped `ClusterRole` variant — no watch mode needs cluster-scoped reconciler permissions anymore.

### Changed

- `MultiNamespace` mode now provisions a real `Role`+`RoleBinding` pair per watched namespace (Helm and raw manifests), instead of a `ClusterRole`.
- The alpha "Resource" trigger type's wildcard RBAC requirement is now an explicit opt-in (`config/samples/resource-trigger-rbac.yaml`, applied per namespace) rather than shipped by default.
- User-facing docs (`docs/architecture.md`, `docs/overview.md`, `README.md`, `docs/contributing.md`, security/troubleshooting guides) rewritten to describe the resulting two-mode model (OwnNamespace/SingleNamespace, MultiNamespace).

### Added

- CI: RBAC parity check comparing generated markers, kustomize, and Helm RBAC sources on every PR — catches the exact class of drift several of the fixes below had to be found by hand.
- `--health-port` on `http-executor` (default `8092`): a separate, always-plain-HTTP listener for liveness/readiness probes, independent of `--executor-mtls`.
- `executor.mtls` Helm values toggle for the existing `--executor-mtls` feature, which previously had no Helm-level way to enable.

### Fixed

- Helm install was effectively non-functional at v0.1.0: missing `POD_NAMESPACE` (Downward API) caused 403s managing the operator's own webhook cert Secret; `readOnlyRootFilesystem: true` with no writable volume broke writing that same cert; missing leader-election RBAC (`coordination.k8s.io/leases`, `events`) meant the operator could never win leader election; and RBAC had drifted from the canonical generated role (missing `configmaps`, `webhookgatewayconfigs`, `networkpolicies`, `poddisruptionbudgets`, and more).
- `--executor-port` was never actually wired to anything — `ExecutorReconciler.ExecutorPort` silently used a hardcoded default regardless of the flag or Helm value.
- `WebhookGatewayConfig` TLS Secret volumes mounted with `DefaultMode: 0400`, unreadable by the non-root container user — crash-looped any webhook gateway with `spec.tls` configured (server or client-CA), on both Helm and raw-manifest installs.
- `MultiNamespace` mode crash-looped on startup in Helm installs — the operator's own Role/RoleBinding was never provisioned in its own release namespace, where it still needs to manage its webhook cert Secret.
- Helm installs never granted leader-election `events` RBAC in any watch mode — non-fatal, but produced a rejected event on every leader-election cycle.
- KEDA-based Kafka gateway autoscaling never worked — the generated `ScaledObject` used the wrong metadata field name (`brokerList` instead of the KEDA Kafka scaler's actual `bootstrapServers`), so KEDA rejected it and no HPA was ever created.
- `--executor-mtls=true` broke HTTP step execution entirely — liveness/readiness probes targeted the mTLS-protected main port, which kubelet's cert-less probes can never authenticate against, so the executor Deployment never became `Ready` and the Service kept routing to a stale pod.
- `spec.amqp.routingKey` docs incorrectly described it as binding a queue to an exchange; the gateway never calls `QueueBind` — it's purely an internal subscription-dedup key. (Docs-only correction; runtime behavior was always as implemented.)

## [v0.1.0] - 2026-09-19

### Added

**Core CRDs and execution**
- `Trigger` CRD — webhook, cron, Kafka, AMQP, NATS, and Kubernetes resource-event (alpha) trigger sources
- `Flow` CRD — ordered DAG steps with `runAfter` dependencies, conditional (`when`, CEL) execution, and data transforms
- `FlowRun` CRD — execution history, per-step status, phase lifecycle (`Pending` → `Running` → `Succeeded`/`Failed`)
- `Integration` CRD — Kafka/AMQP/NATS built-in integrations, `type: http` (centralized HTTP credentials/base URL/TLS config), and a plugin protocol for custom integrations
- `WebhookGatewayConfig` CRD — per-namespace webhook gateway TLS/mTLS (`spec.tls.{serverSecretRef,clientCASecretRef}`), HPA, and PodDisruptionBudget configuration
- Step types: `http`, `transform` (JSONPath/CEL), `publish` (Kafka topic or plugin `/publish` endpoint), `wait` (restart-safe `ResumeAfter`)
- CEL `when` expression evaluation for per-step conditional execution, with a compiled-program cache (`--disable-cel-cache` escape hatch) and full dot-path variable resolution (`$(trigger.body.nested.field)`)
- Skipped step phase with downstream cascade (a skipped step skips all of its dependents)
- Step output data passing — `$(steps.<name>.results.<key>)` substitution between steps
- Per-step retry policies (exponential, linear, fixed backoff) and flow-level/per-step timeouts with requeueing
- Parallel step execution for steps sharing the same `runAfter` set
- FlowRun naming, deterministic per trigger type: webhook `<trigger>-<ts>-<rand>`, kafka `<trigger>-p<partition>-offset-<offset>`, cron `<trigger>-<scheduled-time>`
- FlowRun GC: `spec.ttlAfterFinished`, operator-level `--flowrun-ttl-succeeded`/`--flowrun-ttl-failed` flags, `spec.maxFlowRuns` on Trigger, and a `kubezap.io/retain=true` exemption annotation

**Gateways and triggers**
- Webhook gateway — HTTP server with dynamic route registration; one Deployment per namespace, managed by the operator
- Cron scheduler (`robfig/cron` v3) creating FlowRuns on schedule
- Kafka gateway (Sarama consumer group) — one Deployment per (namespace × Kafka Integration)
- AMQP gateway (`kubezap/amqp-gateway`, beta) — RabbitMQ, ActiveMQ Artemis, Azure Service Bus, IBM MQ
- NATS gateway (`kubezap/nats-gateway`, beta) — NATS JetStream
- KEDA recommended for Kafka gateway scaling (partition-bounded consumer); documented in the architecture guide

**Webhook authentication**
- HMAC-SHA256, bearer token, OIDC/JWT, Basic, API-key header, IP allowlist, mTLS, and header-equals auth types — all configured per-Trigger

**CLI**
- `kubezap watch` — live FlowRun execution timeline with per-step status badges and elapsed time
- `kubezap history`, `kubezap triggers`, `kubezap flows` subcommands
- GoReleaser-built CLI binaries (`kubezap` + `kubectl-kubezap`) for linux/darwin/windows, amd64+arm64

**Observability**
- Prometheus metrics (`/metrics`), OpenTelemetry traces, structured JSON access logs
- Source IP recorded in structured access logs only — excluded from Prometheus label values to avoid cardinality blowup

**Distribution and packaging**
- Self-managed admission webhook TLS: the controller generates and rotates its own self-signed CA/serving cert on boot and keeps the `ValidatingWebhookConfiguration`'s `caBundle` in sync — no cert-manager dependency, works identically across Helm, raw manifests, and OLM
- Helm chart (`charts/kubezap-operator`) — configurable namespace mode, gateway image overrides, RBAC
- OLM bundle scaffolded via Operator SDK; `operator-sdk bundle validate` and scorecard (`basic` + `olm` suites) both pass
- Multi-namespace support: all four OLM install modes (AllNamespaces, SingleNamespace, MultiNamespace, OwnNamespace); `WATCH_NAMESPACES` env var controls scope, with Role vs. ClusterRole chosen per mode
- `config/samples/` example CRs for every CRD type; Kubebuilder-scaffolded project on distroless/static:nonroot base images
- GitHub Actions CI workflow — unified lint, test, build, docker-build on every push and PR
- GitHub Actions release workflow — GoReleaser CLI binaries + versioned container image push (including `:latest`) to GHCR on tag
- Helm chart published to an OCI registry (`ghcr.io/kubezap/charts/kubezap-operator`)
- `docs/releasing.md` — internal release runbook

**Docs**
- `docs/guides/cron-triggers.md`, `docs/guides/troubleshooting.md`, `docs/guides/amqp-setup.md`, `docs/guides/nats-setup.md`, `docs/guides/using-the-cli.md`

**Kafka trigger data**
- Kafka record key captured on `TriggerData.Key`, exposed via `$(trigger.key)` step interpolation and `trigger.key`/`trigger.keyEncoding` in `when:` CEL expressions — encoded as UTF-8 or base64 depending on the key's content (`TriggerData.KeyEncoding`)

### Security

- Container images (`controller`, `webhook-gateway`, `kafka-gateway`, `amqp-gateway`, `nats-gateway`, `http-executor`) scanned for CRITICAL/HIGH CVEs (Trivy) on every push and PR, failing the build on a finding
- Container images signed with [cosign](https://docs.sigstore.dev/) using keyless signing (Sigstore/Fulcio/Rekor, tied to the release workflow's GitHub Actions OIDC identity) — see `SECURITY.md` for verification instructions
- All third-party GitHub Actions used in CI/release workflows pinned to commit SHAs rather than mutable tags
- SSRF blocked-CIDR list consolidated to a single source of truth shared between the executor and the controller
- `TriggerData.Body`/`.Key` no longer silently corrupt non-UTF-8 (binary) payloads when stored — `BodyEncoding`/`KeyEncoding` fields describe whether the value is literal UTF-8 or base64-encoded

### Fixed

- NATS trigger headers now populated on `TriggerData.Headers` (parity with webhook/Kafka/AMQP); the dead `KafkaHeaders` field was removed
- Webhook and Kafka gateway Prometheus metrics fixed (wrong metrics registry; no metrics port exposed on either gateway's Service)
- OpenTelemetry trace sampler wired up (`OTEL_TRACES_SAMPLER_ARG`); previously-undocumented spans added; trace-context propagation bugs fixed for the webhook and Kafka gateways
- Webhook access logs restructured to match the documented nested JSON schema
- `kubezap_trigger_firings_total` now incremented by all gateway types (webhook, Kafka, AMQP, NATS), not just the cron scheduler
- `bundle/manifests/` drift from `config/crd/bases/` is now caught by CI (`make bundle` output must match what's committed)

