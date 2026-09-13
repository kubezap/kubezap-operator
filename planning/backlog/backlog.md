# Backlog

Master index of every epic and its status. This table is the source of truth for status — keep it in sync with the individual Epic/Story files.

The `PI` column is the source of truth for what's actually committed — an epic with a PI value has been through `/plan-pi`; one without (`—`) is still a **candidate**, not a commitment. Turn a candidate into a real epic file with `/new-epic` before it can be considered in a `/plan-pi` session.

## Epics

| ID | Title | Status | PI | File |
|---|---|---|---|---|
| EPIC-001 | Open Source Release Readiness | In Progress | PI-1 | [epics/EPIC-001-open-source-release-readiness.md](epics/EPIC-001-open-source-release-readiness.md) |
| EPIC-002 | Post-Release Hardening & Feature Backlog | Backlog | — | [epics/EPIC-002-post-release-hardening.md](epics/EPIC-002-post-release-hardening.md) |
| EPIC-003 | WebhookGatewayConfig CRD | Done | PI-1 | [epics/EPIC-003-webhook-gateway-config-crd.md](epics/EPIC-003-webhook-gateway-config-crd.md) |
| EPIC-004 | Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step) | Done | PI-1 | [epics/EPIC-004-execution-latency-benchmarking.md](epics/EPIC-004-execution-latency-benchmarking.md) |
| EPIC-005 | HTTP Step Outbound TLS/CA Support | In Progress | PI-1 | [epics/EPIC-005-http-step-outbound-tls.md](epics/EPIC-005-http-step-outbound-tls.md) |

## Stories

| ID | Title | Epic | Status | Size |
|---|---|---|---|---|
| STORY-001 | [Final public-facing docs cleanup pass](stories/STORY-001-docs-cleanup-pass.md) | EPIC-001 | Done (PR #181) | M |
| STORY-002 | [Clean up `docs/contributing.md`](stories/STORY-002-contributing-docs-cleanup.md) | EPIC-001 | Done (PR #179) | S |
| STORY-003 | [Test suite value review](stories/STORY-003-test-suite-value-review.md) | EPIC-001 | Done (2026-09-13, all 3 AC complete) | L |
| STORY-004 | [Product/docs website via GitHub Pages](stories/STORY-004-github-pages-website.md) | EPIC-001 | Reverted (2026-09-13) — not publishing Pages, workflow removed | S |
| STORY-005 | [`CODE_OF_CONDUCT.md` + issue/PR templates](stories/STORY-005-community-health-files.md) | EPIC-001 | Done (PR #180) | S |
| STORY-006 | [Support/community channel decision](stories/STORY-006-support-channel-decision.md) | EPIC-001 | Done (PR #183) — Discussions not enabled, needs admin | XS |
| STORY-007 | [Final release validation and OperatorHub submission](stories/STORY-007-final-release-validation.md) | EPIC-001 | In Progress — blocker (STORY-024) merged; scorecard re-check to close this out not yet re-run | M |
| STORY-008 | [Design record: WebhookGatewayConfig CRD shape & migration path](stories/STORY-008-webhookgatewayconfig-design-record.md) | EPIC-003 | Done (`docs/design/2026-09-12-webhookgatewayconfig-crd.md`, Approved) | S |
| STORY-009 | [`WebhookGatewayConfig` CRD types + controller (HPA + singleton webhook)](stories/STORY-009-webhookgatewayconfig-crd-hpa-controller.md) | EPIC-003 | Done (PR #190) | M |
| STORY-010 | [PodDisruptionBudget reconciliation](stories/STORY-010-webhookgatewayconfig-pdb.md) | EPIC-003 | Done (PR #196) | S |
| STORY-011 | [Cut over webhook TLS annotations (hard cutover)](stories/STORY-011-webhookgatewayconfig-tls-migration.md) | EPIC-003 | Done (PR #197) | S |
| STORY-012 | [Docs for `WebhookGatewayConfig`](stories/STORY-012-webhookgatewayconfig-docs.md) | EPIC-003 | Done (PR #202) | S |
| STORY-013 | [Define benchmark methodology](stories/STORY-013-benchmark-methodology.md) | EPIC-004 | Done (PR #189) | S |
| STORY-014 | [Build and run the benchmark harness](stories/STORY-014-benchmark-harness.md) | EPIC-004 | Done (PR #195) | M |
| STORY-015 | [Write up results and a pursue/don't-pursue recommendation](stories/STORY-015-benchmark-writeup.md) | EPIC-004 | Done (PR #199) | S |
| STORY-016 | [Fix broken doc links/anchors surfaced by the MkDocs build](stories/STORY-016-docs-broken-links-cleanup.md) | EPIC-001 | Done (PR #193) | XS |
| STORY-017 | [`goconst` cleanup in production code](stories/STORY-017-goconst-cleanup.md) | EPIC-002 | Done (PR #198) | M |
| STORY-018 | [Fix silently-dropped `+kubebuilder:webhook` markers](stories/STORY-018-webhook-marker-fix.md) | EPIC-002 | Done (PR #194) | XS |
| STORY-019 | [Add missing `+kubebuilder:webhook` marker for FlowRun](stories/STORY-019-flowrun-webhook-marker.md) | EPIC-002 | Done (PR #203) | XS |
| STORY-020 | [Design record: sub-second FlowRun step-timing visibility](stories/STORY-020-flowrun-step-timing-precision-design.md) | EPIC-002 | Done (PR #204) | XS |
| STORY-021 | [Implement sub-second FlowRun step-timing visibility](stories/STORY-021-flowrun-step-timing-precision-impl.md) | EPIC-002 | Backlog — unblocked (STORY-020 Done), ready to groom | S |
| STORY-022 | [Functional test coverage for bearer/apiKey/basic/headerEquals webhook auth](stories/STORY-022-webhook-auth-test-coverage.md) | EPIC-002 | Done (PR #210) | S |
| STORY-023 | [Fix fictional outbound-TLS-annotation docs](stories/STORY-023-outbound-tls-annotation-docs-fix.md) | EPIC-001 | Done (PR #211) | S |
| STORY-024 | [Fix OLM scorecard descriptor/resource gaps in the CSV](stories/STORY-024-olm-csv-descriptors.md) | EPIC-001 | Done (PR #209) | S |
| STORY-025 | [Design record: HTTP-step outbound CA-bundle/client-cert support](stories/STORY-025-http-outbound-tls-design-record.md) | EPIC-005 | Done (PR #213) | S |
| STORY-026 | [Implement HTTP-step outbound TLS/CA support](stories/STORY-026-http-outbound-tls-impl.md) | EPIC-005 | Done (PR #214) | M |
| STORY-027 | [Docs for HTTP-step outbound TLS/CA support](stories/STORY-027-http-outbound-tls-docs.md) | EPIC-005 | In Progress — [PR #216](https://github.com/kubezap/kubezap-operator/pull/216) open | S |
| STORY-028 | [Container image CVE scanning + static analysis (CodeQL)](stories/STORY-028-container-image-and-sast-scanning.md) | EPIC-002 | Backlog — not yet footprinted | M |

## Backlog Candidates (not yet epics)

Carried over from the project's old task log, not yet scoped into Epics. Each needs `/new-epic` (or folding into an existing one) before it's real backlog work.

**Found bugs / gaps, already diagnosed:**
- Properly scaffold `config/webhook` + `config/certmanager` (standard kubebuilder shape: webhook Service, `ValidatingWebhookConfiguration`s with `cert-manager.io/inject-ca-from`, a self-signed `Issuer` + `Certificate`) so admission webhooks work via a real, rotating, cert-manager-issued cert in production — not just the self-signed dev/e2e workaround (`hack/gen-webhook-certs.sh`). Until this lands, every real (non-dev, non-e2e) deployment following `docs/contributing.md`'s `make deploy` workflow crash-loops on boot. Needs a design record (security posture / external dependency).
- Fix `config/manager/kustomization.yaml`'s image transformer: `name: controller` doesn't match `manager.yaml`'s actual image reference (`ghcr.io/kubezap/controller:latest`), so `make docker-build`/`make deploy`'s `IMG=` override silently no-ops. Likely fix: change the transformer's `name` to `ghcr.io/kubezap/controller`.
- Image signing/provenance (cosign/SLSA) for the 6 published container images — needs a decision on signing mechanism (keyless/Sigstore vs. KMS-backed) and whether OperatorHub/OLM has its own provenance expectations for certified operators.
- Single source of truth for the SSRF blocked-CIDR list — currently duplicated across `internal/executor/http/ssrf.go`, `internal/controller/executor_reconciler.go`, and `config/network-policy/http-executor-ingress.yaml`, kept in sync by code-comment convention only.
- Close the SSRF DNS-rebinding gap in the HTTP transport itself, not just via NetworkPolicy — the current fix (see `docs/design/2026-09-11-executor-egress-networkpolicy.md`) relies on the cluster's CNI enforcing NetworkPolicy egress; a non-enforcing CNI (plain Flannel) gets no protection. A transport-level fix (pin the validated IP, redial per-redirect, preserve SNI/Host) would close the gap independent of CNI, at meaningfully higher complexity.
- Multi-hop trusted-proxy chain for the webhook gateway — `--trusted-proxy-cidrs` (see `docs/design/2026-09-11-webhook-gateway-trust-boundary.md`) validates only the immediate TCP peer, not a chain of multiple trusted hops (CDN → WAF → ingress → gateway).
- Integration-object `secretRef`-change detection — the secret-rotation watches (`docs/design/2026-09-11-secret-rotation-watches.md`) detect a rotated Secret's *contents* changing, but not an Integration's `secretRef` field itself being repointed to a *different* Secret.
- Controller-side mTLS for the plugin publisher channel — plain HTTP between the controller and a plugin's `/publish` endpoint today (see `docs/guides/plugin-security.md`'s "Controller-Side mTLS (Roadmap)" section); a service mesh is the documented interim mitigation.
- No certificate revocation checking (CRL or OCSP) anywhere client certs are verified — notably `kubezap.io/webhook-mtls-ca-secret` (inbound webhook mTLS client certs). A compromised or otherwise-revoked client cert remains accepted until it naturally expires. Needs a decision on mechanism (CRL fetch/cache vs. OCSP, stapled or live) and whether it belongs in the webhook gateway itself or is documented as a service-mesh/ingress responsibility instead.
- **Observability guidance is untested against real tooling.** `docs/guides/observability.md` documents Prometheus metrics, structured access logs, and OTel traces, and states ServiceMonitor is intentionally not auto-created (user's own responsibility) — but none of this has been exercised end-to-end against actual Prometheus + a ServiceMonitor + Grafana + Loki. Needs a real-world validation pass: stand up Prometheus (ServiceMonitor scraping KubeZap's metrics endpoints), Grafana (build/import a dashboard against the real metric names), and Loki (ship the structured JSON access logs and confirm they're actually parseable/queryable as documented) — then confirm the guide's own instructions actually produce working dashboards/queries, not just that the metrics/logs are emitted in isolation (which `make test-e2e`'s metrics check already covers narrowly). Same motivation as STORY-003 (test suite value review) — untested documentation is a gap even when the underlying feature works.
- ~~**Docs describe outbound TLS annotations that don't exist in code.**~~ Resolved 2026-09-13 — docs fix routed to **STORY-023**; the real underlying gap (HTTP steps have no CA/client-cert override at all) routed to its own new epic, **EPIC-005** (HTTP Step Outbound TLS/CA Support), which also absorbs the ConfigMap-CA-bundle speculative item below.

**Speculative features:**
- `Step` CRD for reusable step definitions
- Multi-namespace flows — deferred to v1beta1; requires a FlowGrant CRD (like Gateway API ReferenceGrant) for cross-namespace authorization
- Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- Plugin catalog / marketplace with community registry and maturity levels
- Reference plugin implementation
- OpenLineage support
- Multi-region HA support
- S3/Git event trigger source
