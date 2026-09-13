# STORY-027: Docs for HTTP-step outbound TLS/CA support

**Epic:** EPIC-005 — HTTP Step Outbound TLS/CA Support
**Status:** Done (PR #216, merged)
**Size:** S

## Description

STORY-026 shipped `HttpIntegrationSpec.TLS` (`spec.http.tls.{caBundleConfigMapRef,clientCertSecretRef}`), but no docs describe it — `docs/api/integration.md` has zero mention of the field, and three other doc locations (written by STORY-023, before STORY-026 existed) now make a **stale, false claim**: that HTTP steps have "no CA-bundle override today... a known, currently-unaddressed gap." That gap is closed; the docs don't know it yet. Found during a fresh readiness check (2026-09-13) — this is the exact class of doc/code mismatch STORY-023 itself was created to fix, now reintroduced by the next story in the same epic.

## Acceptance Criteria

- [x] `docs/api/integration.md`: add a `tls` row to the `HttpIntegrationSpec` field table (`HttpTLSSpec`, optional, "Outbound TLS configuration: a private CA bundle to trust and/or a client certificate for mTLS"). Add a new `### HttpTLSSpec` reference section (matching the existing `AmqpTLSConfig`/`NatsTLSConfig`/`KafkaTLSSpec` style) documenting `caBundleConfigMapRef` (ConfigMapKeySelector, PEM CA bundle — note explicitly it's ConfigMap-sourced, not Secret, and why: public data) and `clientCertSecretRef` (LocalObjectReference, `tls.crt`+`tls.key`). Add `HttpTLSSpec` to the doc's TOC. Add a short worked example (a `type: http` Integration with `tls.caBundleConfigMapRef` set, referenced by a step) — reuse the existing "Kafka with mTLS" example's structure/tone.
- [x] `docs/overview.md:296`, `docs/api/trigger.md:516`, `docs/guides/troubleshooting.md:232`: rewrite the "HTTP steps have no CA-bundle or client-certificate override... known, currently-unaddressed gap" claim to describe the real, shipped mechanism (`Integration.spec.http.tls.{caBundleConfigMapRef,clientCertSecretRef}`), consistent with how the Kafka/AMQP/NATS TLS mechanism is already described in the same locations. Keep the still-true parts (the `tlsSkipVerify`/`--allow-tls-skip-verify` internal knob is still not exposed through any Trigger/Flow field) — only the "no CA override, unaddressed gap" framing is now false.
- [x] Grep confirms no other doc file repeats the stale claim (`grep -rn "no CA-bundle\|currently-unaddressed gap\|unaddressed gap" docs/`).

## File / Module Footprint

- `docs/api/integration.md`
- `docs/overview.md`
- `docs/api/trigger.md`
- `docs/guides/troubleshooting.md`

Docs-only — no code touched.

## Dependencies

- Depends on: STORY-026 (Done, PR #214 — this story documents its shipped shape)
- Blocks: STORY-007 (final release validation — EPIC-005 is launch-blocking per PI-1, and this is its last open story)

## Notes

Source: found directly during a "are we ready to go public" readiness check (2026-09-13), not from `follow-ups.md` — the gap was live and blocking, not deferred. No design record needed (docs-only, no behavior change).
