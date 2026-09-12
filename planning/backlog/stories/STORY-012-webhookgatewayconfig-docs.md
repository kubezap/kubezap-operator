# STORY-012: Docs for `WebhookGatewayConfig`

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Backlog — blocked on STORY-009/010/011 (design is now settled via STORY-008, but this documents shipped behavior, not the design)
**Size:** S

## Description

A dedicated `docs/api/webhookgatewayconfig.md` page (purpose, spec fields, status fields, examples, limitations — per `CLAUDE.md`'s Documentation Standards). The CRD shape is now known (`docs/design/2026-09-12-webhookgatewayconfig-crd.md`): `spec.tls.{serverSecretRef,clientCASecretRef}`, `spec.hpa.{minReplicas,maxReplicas,targetCPUUtilization}`, `spec.podDisruptionBudget.minAvailable`. What's still blocked is documenting it *as shipped* (exact field behavior, defaulting, the singleton-rejection error message users will actually see) rather than as designed.

## Acceptance Criteria

- [ ] `docs/api/webhookgatewayconfig.md`: purpose, full spec field reference, a worked example (custom TLS + wider HPA range + PDB), and a documented example of the singleton-rejection error.
- [ ] `docs/guides/webhook-security.md` and `docs/overview.md`'s TLS sections updated to describe the CRD (if STORY-011 didn't already do this).
- [ ] Explicit migration note for anyone reading the old annotation docs: the annotations are gone (hard cutover, not deprecated) — link to the CHANGELOG entry from STORY-011.

## File / Module Footprint

- `docs/api/webhookgatewayconfig.md` (new)
- `docs/guides/webhook-security.md`, `docs/overview.md` (updates, unless STORY-011 already made them)

## Dependencies

- Depends on: STORY-008, STORY-009, STORY-010, STORY-011 (documents the shipped result, not a design)
- Blocks: none

## Notes

Deliberately last in this epic's story order — matches this project's documentation-driven-development principle in spirit (docs before implementation) for the *design*, but a docs page describing a CRD's final behavior has to follow the implementation, not precede it. Re-groom once STORY-011 is `Done`.
