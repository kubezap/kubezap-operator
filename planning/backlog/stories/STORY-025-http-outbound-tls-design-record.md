# STORY-025: Design record — HTTP-step outbound CA-bundle/client-cert support

**Epic:** EPIC-005 — HTTP Step Outbound TLS/CA Support
**Status:** Done (PR #213, `docs/design/2026-09-13-http-step-outbound-tls.md`, Approved)
**Size:** S — design record only, no implementation

## Description

HTTP steps have no way to trust a private/internal CA bundle or present a client certificate for an outbound call — the only control today is a boolean skip-verify, and it isn't even reachable through the standard API surface (`internal/controller/flowrun_controller.go` never sets `TLSSkipVerify: true` on the executor RPC request regardless of the executor's `--allow-tls-skip-verify` flag). This is a CRD/type change plus a credential-handling change to the controller → HTTP-executor internal RPC channel, so per `planning/process/design-process.md`'s trigger list it needs a design record before implementation — same as STORY-008 was for `WebhookGatewayConfig`.

## Acceptance Criteria

- [ ] Design record answering the shape decision: extend `Integration`'s existing `HttpIntegrationSpec` (`api/v1alpha1/integration_types.go`, `type: http` — `baseUrl`/`auth`/`defaultHeaders` today) with TLS fields, vs. introduce a new TLS-only `Integration` type, vs. a `Trigger`/step-level field. Recommend extending `HttpIntegrationSpec` unless the record surfaces a real reason not to — it already exists, already follows the `Integration`-scoped pattern used by Kafka/AMQP/NATS, and an HTTP step already references an `Integration` for other per-target config.
- [ ] Confirm the CA field is `ConfigMapKeyRef`-sourced (owner decision, 2026-09-13: "Probably configmap for ca bundle not secret" — a CA bundle is public data) and explicitly supports a **bundle** (multiple concatenated PEM certificates in one key, not a field restricted to one cert — owner decision, 2026-09-13: "It should support a ca bundle also not just a single CA"). Client certificate (private key material) stays `SecretKeyRef`.
- [ ] Decide how the resolved CA-bundle/client-cert material flows from the controller to the HTTP executor over the internal `POST /execute` RPC without ever putting the client-cert private key in etcd — consistent with the existing invariant (`CLAUDE.md`'s Runtime Architecture: "credentials never written to etcd; resolved in-process and forwarded via internal RPC only"). The CA bundle itself, being public, isn't under that same constraint — state this distinction explicitly rather than treating "TLS material" as one undifferentiated category.
- [ ] Decide the relationship to the existing `tlsSkipVerify` field / `--allow-tls-skip-verify` flag — does a configured CA bundle simply supplement the system root pool (added trust, not replacing it), and does `tlsSkipVerify` still work exactly as before (skip verification entirely) when both are present on the same step?
- [ ] Decide whether ConfigMap rotation needs a watcher — `internal/gateway/secretindex` exists for `Secret` rotation today but watches `Secret`s only; state whether a CA-bundle `ConfigMap` change needs equivalent reverse-index/informer handling or whether periodic reconciliation already covers it, and why.
- [ ] Confirm API-compatibility impact per `design-process.md`: additive `+optional` fields on `HttpIntegrationSpec` (or wherever the shape decision lands) should not require a `v1alpha2` bump — state this explicitly, don't just assume it.
- [ ] File under `docs/design/`, indexed in `docs/design/README.md`, per the standard six-section format (Problem Statement, Constraints, Invariants, Rejected Alternatives, Tradeoffs, Final Decision).

## File / Module Footprint

- `docs/design/YYYY-MM-DD-http-step-outbound-tls.md` (new)
- `docs/design/README.md` (index entry)

No code footprint — design record only.

## Dependencies

- Depends on: none
- Blocks: EPIC-005's implementation story (not yet groomed/footprinted — depends on this record's decisions, same pattern as STORY-008 → STORY-009)

## Notes

Source: `planning/backlog/epics/EPIC-005-http-step-outbound-tls.md`'s Candidate Stories list, itself sourced from a follow-up filed during STORY-023's grooming (2026-09-13, owner confirmed live: "I do need support for setting CA somehow for outbound http requests"). Committed to PI-1, launch-blocking alongside `EPIC-001` (owner decision, 2026-09-13: "Prioritize epic 5 before release").
