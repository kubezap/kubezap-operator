# Design Records

One file per non-obvious technical decision, created per `planning/process/design-process.md`. Filenames are `docs/design/<YYYY-MM-DD>-<slug>.md`; numbering is by date, not sequential IDs — dates are stable and never reused, so there's nothing to renumber.

Every record starts with a header block right after its title:

```
> Status: Draft | Review | Approved | Superseded by docs/design/<file>.md
> Related: <files, docs, or backlog items this decision touches>
```

Status values match `planning/process/design-process.md`'s Lifecycle table. When a decision is later reversed, don't edit or delete the old record — write a new one and mark the old one's Status as `Superseded by docs/design/<file>.md`.

## Index

| Date | Title | Status | Summary |
|---|---|---|---|
| 2026-09-10 | [Flow Parameters](2026-09-10-flow-parameters.md) | Approved | `$(params.*)` resolution order (explicit `FlowRunSpec.Params` → auto-derived from trigger body → declared default → fail-if-required) and CEL/interpolation wiring. |
| 2026-09-10 | [Plugin Publish Envelope](2026-09-10-plugin-publish-envelope.md) | Approved | The `{integration, namespace, destination, headers, body}` JSON envelope a plugin's `POST /publish` endpoint actually receives, plus response-parsing contract (`messageId`/`error`). |
| 2026-09-11 | [CEL Access to Nested Trigger Body Fields](2026-09-11-cel-trigger-body-fields.md) | Approved | `trigger.bodyFields` CEL activation-map entry — parsed JSON/form-urlencoded body reachable from `when:` conditions, independent of `trigger.body`'s raw-string form. |
| 2026-09-11 | [HTTP Executor Egress NetworkPolicy](2026-09-11-executor-egress-networkpolicy.md) | Approved | Network-layer SSRF defense-in-depth (egress CIDR blocklist on the auto-created executor `NetworkPolicy`), closing the software blocklist's DNS-rebinding TOCTOU gap. Addendum (2026-09-12): ingress-selector and egress-port/dev-bypass corrections found during live validation. |
| 2026-09-11 | [Secret Rotation Detection for Gateway Watchers](2026-09-11-secret-rotation-watches.md) | Approved | `Secret` informer + reverse index (`internal/gateway/secretindex`) so a rotated webhook/broker credential is picked up without touching the owning Trigger/Integration. |
| 2026-09-11 | [Single-Pass Template Interpolation](2026-09-11-single-pass-interpolation.md) | Approved | `interpolateTemplate` — resolves each `$(...)` token once from the original text, fixing a remote-DoS (self-referential placeholder hang) and a secret-exfiltration bug in the prior scan-and-`ReplaceAll` implementation. |
| 2026-09-11 | [Typed Phase/FailurePolicy Enums](2026-09-11-typed-phase-enums.md) | Approved | Four distinct named string types (`FlowRunPhase`, `StepPhase`, `FailurePolicy`, `OnFailureAction`) replacing bare `string` fields, kept intentionally separate rather than shared. |
| 2026-09-11 | [Webhook Gateway Trust Boundary Hardening](2026-09-11-webhook-gateway-trust-boundary.md) | Approved | `--trusted-proxy-cidrs`-gated `X-Forwarded-For`/`X-Real-IP` trust — closes the `ipAllowlist` auth bypass where any direct caller could spoof its source IP. |
| 2026-09-12 | [WebhookGatewayConfig CRD](2026-09-12-webhookgatewayconfig-crd.md) | Approved | New namespaced, admission-enforced-singleton CRD replacing the `webhook-tls-secret`/`webhook-mtls-ca-secret` Namespace annotations and exposing per-namespace HPA min/max/target-CPU and `PodDisruptionBudget` `minAvailable`. Hard cutover (no annotation deprecation window); plain-passthrough `minReplicas` (no HA minimum enforced). |
| 2026-09-13 | [FlowRun Step-Timing Precision](2026-09-13-flowrun-step-timing-precision.md) | Draft | Additive `DurationMillis *int64` on both `StepRunStatus` and `FlowRunStatus`, populated directly from the `time.Duration` already computed for the `kubezap_step_duration_seconds`/`kubezap_flowrun_duration_seconds` histograms — fixes the whole-second `metav1.Time` quantization that made 594/600 step observations in STORY-015's benchmark read as `0.000s`. No `v1alpha2` bump (purely additive `+optional` fields). |
| 2026-09-13 | [HTTP-Step Outbound TLS/CA Support](2026-09-13-http-step-outbound-tls.md) | Approved | Additive `HttpIntegrationSpec.TLS` field (`CABundleConfigMapRef`, `ClientCertSecretRef`) closing the gap where HTTP steps have no way to trust a private CA. `ConfigMap`-sourced CA bundle (public data, supports multiple concatenated PEM certs, added to — not replacing — the system root pool) vs. `Secret`-sourced client cert; resolved controller-side and forwarded inline over the executor RPC, preserving the executor's zero-Secret/ConfigMap-RBAC design. No watcher needed — resolved fresh per `FlowRun` reconcile, same as existing auth-secret resolution. No `v1alpha2` bump. |

## Pre-process documents

These predate `planning/process/design-process.md` and don't follow the 6-section format or header convention — left as-is, not part of the index above:

- [`cli.md`](cli.md) — `kubezap` CLI design overview.
- [`scale-limitations.md`](scale-limitations.md) — documented scale limitations and the decision to accept them.
- [`2026-03-24-security-review.md`](2026-03-24-security-review.md) — the original security design review that produced the SSRF blocklist + separate `http-executor` binary architecture (still in production use), the OwnNamespace-default RBAC model, and the webhook admission-warning-on-unauthenticated-Trigger behavior. Multi-finding format (Critical/High findings, then a resolved-decisions table), not the single-decision 6-section format — kept as historical record rather than retrofitted.
