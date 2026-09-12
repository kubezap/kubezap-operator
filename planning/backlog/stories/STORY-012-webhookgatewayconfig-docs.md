# STORY-012: Docs for `WebhookGatewayConfig`

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Backlog — blocked on STORY-008/009/010/011, not yet groomed
**Size:** unknown — cannot size until the CRD's actual final shape is known

## Description

A dedicated `docs/api/` page for the new CRD (purpose, spec fields, status fields, examples, limitations — per `CLAUDE.md`'s Documentation Standards), plus updates to `docs/guides/webhook-security.md` and `docs/overview.md`'s TLS sections to reflect whatever STORY-011 lands (migrated annotations, deprecation notices if applicable).

## Acceptance Criteria

Cannot be written yet — this story describes the *final*, fully-implemented CRD, so its content depends on STORY-009/010/011 actually being done, not just STORY-008's design record.

## File / Module Footprint

- `docs/api/webhookgatewayconfig.md` (new)
- `docs/guides/webhook-security.md`, `docs/overview.md` (updates)

## Dependencies

- Depends on: STORY-008, STORY-009, STORY-010, STORY-011 (documents the shipped result, not a design)
- Blocks: none

## Notes

Deliberately last in this epic's story order — matches this project's documentation-driven-development principle in spirit (docs before implementation) for the *design*, but a docs page describing a CRD's final behavior has to follow the implementation, not precede it. Re-groom once STORY-011 is `Done`.
