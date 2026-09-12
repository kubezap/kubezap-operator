# Backlog

Master index of every epic and its status. This table is the source of truth for status — keep it in sync with the individual Epic/Story files.

The `PI` column is the source of truth for what's actually committed — an epic with a PI value has been through `/plan-pi`; one without (`—`) is still a **candidate**, not a commitment. Turn a candidate into a real epic file with `/new-epic` before it can be considered in a `/plan-pi` session.

## Epics

| ID | Title | Status | PI | File |
|---|---|---|---|---|
| EPIC-001 | Open Source Release Readiness | In Progress | PI-1 | [epics/EPIC-001-open-source-release-readiness.md](epics/EPIC-001-open-source-release-readiness.md) |
| EPIC-002 | Post-Release Hardening & Feature Backlog | Backlog | — | not yet created — see Backlog Candidates below |
| EPIC-003 | WebhookGatewayConfig CRD | Backlog | PI-1 | [epics/EPIC-003-webhook-gateway-config-crd.md](epics/EPIC-003-webhook-gateway-config-crd.md) |
| EPIC-004 | Execution Latency Benchmarking (RPC Executor vs. Pod-per-Step) | Backlog | PI-1 | [epics/EPIC-004-execution-latency-benchmarking.md](epics/EPIC-004-execution-latency-benchmarking.md) |

## Stories

| ID | Title | Epic | Status | Size |
|---|---|---|---|---|
| STORY-001 | [Final public-facing docs cleanup pass](stories/STORY-001-docs-cleanup-pass.md) | EPIC-001 | Done (PR #181) | M |
| STORY-002 | [Clean up `docs/contributing.md`](stories/STORY-002-contributing-docs-cleanup.md) | EPIC-001 | Done (PR #179) | S |
| STORY-003 | [Test suite value review](stories/STORY-003-test-suite-value-review.md) | EPIC-001 | Groomed — partially done | L |
| STORY-004 | [Product/docs website via GitHub Pages](stories/STORY-004-github-pages-website.md) | EPIC-001 | Planned (Batch 2) | S |
| STORY-005 | [`CODE_OF_CONDUCT.md` + issue/PR templates](stories/STORY-005-community-health-files.md) | EPIC-001 | Done (PR #180) | S |
| STORY-006 | [Support/community channel decision](stories/STORY-006-support-channel-decision.md) | EPIC-001 | Planned (Batch 2) | XS |
| STORY-007 | [Final release validation and OperatorHub submission](stories/STORY-007-final-release-validation.md) | EPIC-001 | Groomed | M |

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
- **Docs describe outbound TLS annotations that don't exist in code.** `docs/api/trigger.md`'s "TLS and mTLS Annotations" section (mirrored in `docs/overview.md`) documents `kubezap.io/tls-ca-secret`, `kubezap.io/tls-client-cert-secret`, and `kubezap.io/tls-insecure-skip-verify` as Trigger-level annotations controlling outbound TLS verification. None of the three are read anywhere in the Go code (verified by grepping for the literal annotation strings and for any plausible backing identifier). The only real outbound CA/client-cert mechanism is the structured `Integration.spec.{kafka,amqp,nats}.tls.{caSecretRef,clientCertSecretRef}` fields — Integration-scoped, not Trigger-scoped, and limited to those three broker types (HTTP steps have their own separate `--allow-tls-skip-verify` executor flag + per-request `tlsSkipVerify` field, with no CA/client-cert override at all). The two annotations in the same doc table that *are* real — `kubezap.io/webhook-tls-secret` and `webhook-mtls-ca-secret` — are Namespace-scoped and control the webhook gateway's *inbound* TLS serving, a third, unrelated mechanism. Needs a decision: implement the documented outbound annotations for real (design record required — this is user-facing security config), or delete the fictional sections and document the actual `Integration`-field-based mechanism instead. Until resolved, a user following these docs to trust a private CA or present a client cert for an HTTP step's outbound call gets no error and no effect — the call is just made with default system-root TLS verification.

**Speculative features:**
- Support ConfigMap-sourced CA bundles for outbound TLS verification (not just Secret-sourced) — contingent on the above doc/code mismatch being resolved first, since there is currently no working Secret-based outbound-CA annotation to extend. If the resolution is "implement `Integration`-style CA refs for HTTP steps too," a `configMapKeyRef` alternative alongside `secretKeyRef` (a CA bundle is public data, not a secret) should be considered in that same design pass rather than bolted on afterward.
- `Step` CRD for reusable step definitions
- Multi-namespace flows — deferred to v1beta1; requires a FlowGrant CRD (like Gateway API ReferenceGrant) for cross-namespace authorization
- Additional message brokers: GCP Pub/Sub, Solace (non-AMQP), TIBCO EMS (via plugin model)
- Plugin catalog / marketplace with community registry and maturity levels
- Reference plugin implementation
- OpenLineage support
- Multi-region HA support
- S3/Git event trigger source
