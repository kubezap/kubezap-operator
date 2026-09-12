# STORY-011: Migrate webhook TLS annotations onto `WebhookGatewayConfig`

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Backlog — blocked on STORY-008, not yet groomed
**Size:** unknown — cannot size until STORY-008's design record settles the migration path

## Description

Migrate `kubezap.io/webhook-tls-secret` / `kubezap.io/webhook-mtls-ca-secret` Namespace annotations (currently read in `internal/controller/trigger_controller.go`) onto the new CRD, per whichever migration path STORY-008's design record chooses (deprecate-and-warn vs. hard cutover).

## Acceptance Criteria

Cannot be written yet — depends on STORY-008's design record (migration path decision directly determines whether old annotations must keep working, need a deprecation warning, or stop working immediately).

## File / Module Footprint

Cannot be fully footprinted yet. Likely candidates once STORY-008 lands:
- `internal/controller/trigger_controller.go` (currently reads the two annotations directly — see the `ns.Annotations[...]` lookups)
- `docs/guides/webhook-security.md`, `docs/overview.md` (TLS annotation docs — will need updating regardless of migration path chosen)

## Dependencies

- Depends on: STORY-008 (design record), STORY-009 (the CRD must exist before anything can read from it instead of annotations)
- Blocks: none

## Notes

This is the story most sensitive to STORY-008's decision — a deprecate-and-warn path and a hard-cutover path have almost entirely different implementations (dual-read-with-precedence-rules vs. simple replacement). Do not attempt to pre-guess which one before the design record lands.
