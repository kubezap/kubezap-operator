# STORY-008: Design record — WebhookGatewayConfig CRD shape & annotation migration path

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Groomed
**Size:** S

## Description

Produce the design record this epic's own Notes already call for, per `planning/process/design-process.md`'s trigger list (new CRD, new controller, and a migration touching security-relevant TLS config). Nothing else in this epic can be responsibly footprinted until the questions below are answered — the other 4 candidate stories are all downstream of these decisions.

## Acceptance Criteria

- [ ] A `docs/design/2026-09-12-webhookgatewayconfig-crd.md` record (via `/adr`) answering, at minimum:
  - **CRD shape**: exact field names/types for HPA (`minReplicas`, `maxReplicas`, `targetCPUUtilization`), PDB (`minAvailable`), and the two migrated TLS fields (secret refs replacing `webhook-tls-secret`/`webhook-mtls-ca-secret`). Kubebuilder validation markers and defaults for each.
  - **Singleton convention**: how "one per namespace" is enforced/represented — a fixed required name (e.g. `default`), an admission check rejecting a second object, or something else.
  - **Defaulting behavior**: exact fallback values when the CRD is absent from a namespace (must match today's hardcoded HPA behavior per the epic's Success Metric).
  - **Annotation migration/deprecation path**: deprecate-and-warn (both mechanisms work for some window, annotation wins or CRD wins on conflict?) vs. hard cutover (annotations stop working the moment this ships). Pick one and justify it.
  - **HA question** (raised by owner, not yet decided): does the CRD just pass through a raw `minReplicas` number, or does it validate/recommend `>= 2` for real HA? Pick one.
- [ ] Record reviewed and its Status moved to `Approved` (not left at `Draft`) before any implementation story below starts.

## File / Module Footprint

- `docs/design/2026-09-12-webhookgatewayconfig-crd.md` (new)
- `docs/design/README.md` (index entry)

## Dependencies

- Depends on: none
- Blocks: STORY-009, STORY-010, STORY-011, STORY-012 (all four are downstream of this record's decisions and are not yet groomed — see their placeholder files)

## Notes

This is the epic's own first candidate item, promoted to a real story because it's the one item in this epic that can be footprinted and sized with confidence right now. Everything else in the epic is genuinely not groomable until this lands — that's not a gap in this grooming pass, it's the correct outcome per `/groom-backlog`'s "don't guess a footprint" rule.
