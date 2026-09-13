# EPIC-005: HTTP Step Outbound TLS/CA Support

**Status:** Done — STORY-025/026/027 all merged (PR #213, #214, #216)
**PI:** PI-1 — committed 2026-09-13, launch-blocking alongside EPIC-001 (see `planning/roadmap/pi-plan.md`'s Revisions)

## Problem

HTTP steps (the `type: http` Flow action) have no way to trust a private/internal CA bundle (potentially more than one trusted certificate) or present a client certificate for an outbound call. The only outbound TLS control that exists today is a boolean skip-verify (`tlsSkipVerify` in the step body, honored only when the executor is started with `--allow-tls-skip-verify`, dev-only) — so an HTTP step targeting a service behind an internal/private CA either fails TLS verification with no fix, or the operator has to disable TLS verification cluster-wide via the dev-only flag, defeating the purpose. This gap was previously masked by docs that fictionally claimed Trigger-level annotations (`kubezap.io/tls-ca-secret`, `kubezap.io/tls-client-cert-secret`) already solved it — confirmed not to exist anywhere in code, removed via STORY-023. Meanwhile Kafka/AMQP/NATS `Integration`s already have a real, working CA/client-cert mechanism (`Integration.spec.{kafka,amqp,nats}.tls.{caSecretRef,clientCertSecretRef}`) — HTTP steps are the one place this pattern doesn't reach.

## Goal

An HTTP step can specify a **CA bundle** — potentially multiple trusted CA certificates (e.g. a private root plus intermediates, or more than one trusted root), not a field restricted to exactly one certificate — and optionally a client certificate, for outbound TLS verification. Owner requirements stated explicitly (2026-09-13):
- "It should support a ca bundle also not just a single CA."
- "Probably configmap for ca bundle not secret" — a CA bundle is public data (no private key material), so it should be sourced via `ConfigMapKeyRef`, not `SecretKeyRef`, unlike the client certificate (which does contain a private key and must stay Secret-sourced). This diverges from the existing Kafka/AMQP/NATS `Integration.spec.*.tls.caSecretRef` pattern, which is Secret-only today — see `backlog.md`'s "Speculative features" list, which already flagged this exact ConfigMap-vs-Secret question as contingent on this epic.

Whatever the final shape, no client-certificate private-key material is ever written to etcd — same invariant the RPC-based credential-forwarding architecture already applies to other secrets (see `CLAUDE.md`'s Runtime Architecture: "credentials never written to etcd; they are resolved in-process and forwarded via internal RPC only"). A CA bundle, being public, isn't subject to that same constraint — worth the design record explicitly noting the two follow different rules rather than treating "TLS material" as one undifferentiated category.

## Success Metric

An HTTP step targeting a service secured by a private/internal CA succeeds without `--allow-tls-skip-verify`, verified end-to-end on a real cluster (kind/k3s, a private CA, a step configured to trust it) — not just via unit tests on the resolution logic in isolation.

## Related Design Docs

- [`2026-03-24-security-review.md`](../../../docs/design/2026-03-24-security-review.md) — original design that produced the separate `http-executor` binary and the "credentials resolved in-process, forwarded via RPC only, never in etcd" model this epic must preserve.
- [`2026-09-11-executor-egress-networkpolicy.md`](../../../docs/design/2026-09-11-executor-egress-networkpolicy.md) — network-layer defense-in-depth on the same binary/RPC channel this epic touches.
- [`2026-09-11-secret-rotation-watches.md`](../../../docs/design/2026-09-11-secret-rotation-watches.md) — the `internal/gateway/secretindex` rotation-detection pattern, likely reusable if a CA/client-cert Secret needs rotation handling.
- `api/v1alpha1/integration_types.go`'s `KafkaTLSConfig`/`AmqpTLSConfig`/`NatsTLSConfig` — the existing `caSecretRef`/`clientCertSecretRef` pattern this epic should extend to HTTP steps, not reinvent.
- `api/v1alpha1/integration_types.go`'s `HttpIntegrationSpec` (`type: http` — `baseUrl`/`auth`/`defaultHeaders`, zero TLS fields today) — found during STORY-007's spec-drift pass (2026-09-13). Extending this existing type with CA-bundle/client-cert fields may be more natural than introducing a new `Integration` type; the design record should weigh it against `Trigger`/step-level alternatives rather than defaulting to it.
- Found during STORY-023 (2026-09-13): `internal/controller/flowrun_controller.go:887` never actually sets `TLSSkipVerify: true` on the executor RPC request regardless of the executor's `--allow-tls-skip-verify` flag — there's no Trigger/Flow field wired to it at all today. The flag exists in the internal RPC contract but isn't reachable through the standard API surface. Relevant baseline for the design record: the "existing" skip-verify escape hatch is even narrower than it looks from the executor code alone.
- `docs/architecture.md`'s "Why Separate Gateway Pods" section and the `http-executor` row in `CLAUDE.md`'s Runtime Architecture table — describes the controller → executor `POST /execute` RPC this epic's credential material must flow through.
- [`2026-09-13-http-step-outbound-tls.md`](../../../docs/design/2026-09-13-http-step-outbound-tls.md) — **Approved.** This epic's own design record: additive `HttpIntegrationSpec.TLS` field (`CABundleConfigMapRef`/`ClientCertSecretRef`), CA bundle via `ConfigMap` and additive to system roots (deliberately diverging from Kafka/AMQP/NATS's Secret-only, replace-system-trust pattern — see the record's Rejected Alternatives for why migrating the brokers to match was considered and rejected), resolved controller-side and forwarded inline over the executor RPC, no rotation watcher needed.

## Candidate Stories

Rough list, not yet sized/footprinted:

- [x] [STORY-025](../stories/STORY-025-http-outbound-tls-design-record.md) — Design record: HTTP-step outbound CA-bundle/client-cert support. Done — `docs/design/2026-09-13-http-step-outbound-tls.md` (Approved), PR #213, merged.
- [x] [STORY-026](../stories/STORY-026-http-outbound-tls-impl.md) — Implementation: CRD/type changes, controller-side resolution, and HTTP executor TLS-client changes, per the design record's decision. Done — PR #214, merged. Live e2e validated (trust succeeds with the CA configured, fails with `x509: certificate signed by unknown authority` without it).
- [x] [STORY-027](../stories/STORY-027-http-outbound-tls-docs.md) — Docs: `docs/api/integration.md` reference + fixing the now-stale "no CA override for HTTP steps" claim in `docs/overview.md`/`docs/api/trigger.md`/`docs/guides/troubleshooting.md` (written by STORY-023 before STORY-026 existed). Done — PR #216, merged.

## Dependencies

- Depends on: none (STORY-023 removes the fictional docs first, but doesn't block this epic's design work — they can proceed independently)
- Blocks: none

## Notes

Sourced directly from a follow-up filed during STORY-023's grooming (2026-09-13, `planning/backlog/follow-ups.md`) — owner confirmed live, in-session: "I do need support for setting CA somehow for outbound http requests." Committed to PI-1 the same session, launch-blocking alongside `EPIC-001` — owner decision, stated directly ("Prioritize epic 5 before release"). Still needs `/groom-backlog` (starting with the design record) before any story here is dispatchable.
