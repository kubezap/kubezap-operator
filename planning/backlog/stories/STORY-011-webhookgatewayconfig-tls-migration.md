# STORY-011: Cut over webhook TLS annotations to `WebhookGatewayConfig` (hard cutover)

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Groomed
**Size:** S

## Description

Per `docs/design/2026-09-12-webhookgatewayconfig-crd.md`'s hard-cutover decision: remove `internal/controller/trigger_controller.go`'s reads of the `kubezap.io/webhook-tls-secret` / `kubezap.io/webhook-mtls-ca-secret` Namespace annotations entirely, replacing them with reads from `WebhookGatewayConfig.spec.tls.{serverSecretRef,clientCASecretRef}`. No transition window, no dual-read, no deprecation warning — the annotations simply stop being consulted the moment this ships.

## Acceptance Criteria

- [ ] `internal/controller/trigger_controller.go`'s `ns.Annotations["kubezap.io/webhook-tls-secret"]` / `ns.Annotations["kubezap.io/webhook-mtls-ca-secret"]` lookups are removed, replaced with reads from the namespace's `WebhookGatewayConfig` (absent config → no TLS cert mounted, matching today's behavior for a namespace with neither annotation set).
- [ ] A namespace that had one or both annotations set before this ships, but has no `WebhookGatewayConfig` object, gets **no TLS cert mounted post-upgrade** (the annotations are simply no longer read) — this is the intended, documented hard-cutover behavior, not a bug. Confirm this is called out prominently in the CHANGELOG as a required migration action, mirroring how `docs/design/2026-09-11-webhook-gateway-trust-boundary.md`'s trust-proxy-cidrs change was flagged.
- [ ] `docs/guides/webhook-security.md` and `docs/overview.md`'s TLS annotation sections updated to describe the CRD instead of the annotations (this could also be left to STORY-012 if the docs story is dispatched immediately after — decide at dispatch time based on how much overlap there'd be).

## File / Module Footprint

- `internal/controller/trigger_controller.go`
- `docs/guides/webhook-security.md`, `docs/overview.md` (unless deferred to STORY-012 — see above)

## Dependencies

- Depends on: STORY-008 (design record), STORY-009 (the CRD must exist and be readable before anything can read from it instead of annotations)
- Blocks: STORY-012 (docs, if not already covered by this story)

## Notes

This is the story that most directly changes runtime behavior for any namespace that was actually using the annotations before this lands — the CHANGELOG-visibility acceptance criterion above is not optional polish, it's the difference between "hard cutover" being a deliberate, documented decision versus a silent breaking change.
