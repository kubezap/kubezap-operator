# STORY-023: Fix fictional outbound-TLS-annotation docs

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #211)
**Size:** S

## Description

`docs/api/trigger.md`'s "TLS and mTLS Annotations" section (mirrored in `docs/overview.md`, and referenced once more in `docs/guides/troubleshooting.md`) documents three Trigger-level annotations — `kubezap.io/tls-ca-secret`, `kubezap.io/tls-client-cert-secret`, `kubezap.io/tls-insecure-skip-verify` — as controlling outbound TLS verification. **None of the three are read anywhere in the Go code** (confirmed by grep across the whole repo, 2026-09-13 grooming pass — zero matches outside `docs/`). A user following these docs to trust a private CA or present a client cert for an outbound call gets no error and no effect; the call is just made with default system-root TLS verification.

Owner decision (2026-09-13 checkpoint): **delete the fictional annotation docs and document the real mechanism instead**, rather than implementing the annotations for real. The real mechanism is narrower than what's documented: `Integration.spec.{kafka,amqp,nats}.tls.{caSecretRef,clientCertSecretRef}` covers CA/client-cert configuration for broker connections (Kafka/AMQP/NATS), but is `Integration`-scoped, not `Trigger`-scoped. **HTTP steps have no CA-bundle or client-cert override at all** — only a boolean skip-verify (`tlsSkipVerify` field in the step body, honored only when the HTTP executor is started with `--allow-tls-skip-verify`, dev-only). This gap (no way to trust a private CA for an HTTP-step outbound call) is real and stays open after this story — this story fixes the docs to state that accurately, it does not close the gap. (Implementing real CA/client-cert support for HTTP steps was considered and explicitly deferred — see Notes.)

Don't confuse this with the unrelated, already-accurate `kubezap.io/webhook-tls-secret` / `kubezap.io/webhook-mtls-ca-secret` mechanism referenced elsewhere in these same docs — that's `WebhookGatewayConfig`-based **inbound** webhook-serving TLS (STORY-011, already shipped correctly) and is out of this story's scope entirely.

## Acceptance Criteria

- [ ] `docs/api/trigger.md`'s "TLS and mTLS Annotations" section (`## TLS and mTLS Annotations`, roughly lines 512–583): remove the "Custom Certificate Authority", "Mutual TLS (mTLS) for Outbound Connections", and "Skip TLS Verification" subsections and their Annotation Reference table rows (`kubezap.io/tls-ca-secret`, `kubezap.io/tls-client-cert-secret`, `kubezap.io/tls-insecure-skip-verify`) — these three don't exist in code. Keep the "Inbound Webhook mTLS" subsection as-is (accurate, `WebhookGatewayConfig`-based). Add a short accurate replacement describing: broker outbound TLS via `Integration.spec.{kafka,amqp,nats}.tls.*` (link to `docs/api/integration.md`), and HTTP-step outbound TLS via the per-request `tlsSkipVerify` field gated by the executor's `--allow-tls-skip-verify` flag — explicitly stating there is no CA-bundle/client-cert override for HTTP steps today.
- [ ] `docs/overview.md`'s "TLS and mTLS" section (`## TLS and mTLS`, roughly lines 289–311 and the `tls-insecure-skip-verify` mention near line 399): same removal + replacement, consistent wording with `docs/api/trigger.md`.
- [ ] `docs/guides/troubleshooting.md:232`'s `TLS handshake failed` / `certificate signed by unknown authority` row: replace `Add kubezap.io/tls-ca-secret annotation` with accurate guidance — for broker Integrations, point at `Integration.spec.*.tls.caSecretRef`; for HTTP steps, state plainly that there's no fix today short of `--allow-tls-skip-verify` (dev only) or getting the target onto a publicly-trusted CA.
- [ ] `grep -rn "tls-ca-secret\|tls-client-cert-secret\|tls-insecure-skip-verify" docs/` after the edit returns zero matches (confirms no other doc file was missed).

## File / Module Footprint

- `docs/api/trigger.md`
- `docs/overview.md`
- `docs/guides/troubleshooting.md`

Docs-only — no Go code touched, no behavior change.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/backlog.md`'s Backlog Candidates list ("Found bugs / gaps, already diagnosed"), groomed at the 2026-09-13 (afternoon) checkpoint. No design record needed — this is a docs-only correction with no behavior or security-posture change, explicitly exempt per `planning/process/design-process.md`.

**Considered and deferred, but confirmed wanted**: implementing real outbound CA/client-cert support for HTTP steps (would require extending the HTTP executor's TLS client plus the credential-forwarding internal RPC path, which today intentionally carries no cert material) was discussed as the alternative to this story. Owner decision: too large for this pass (security-posture change, needs its own design record) — but explicitly confirmed as wanted, not just deferred indefinitely ("I do need support for setting CA somehow for outbound http requests," 2026-09-13). Logged in `planning/backlog/follow-ups.md` (2026-09-13) rather than scoped into this story; routed to its own new epic, **EPIC-005** (HTTP Step Outbound TLS/CA Support) — comparable in scope to `EPIC-003`/WebhookGatewayConfig. Owner also clarified the CA field must support a bundle (multiple trusted certificates), not just one.
