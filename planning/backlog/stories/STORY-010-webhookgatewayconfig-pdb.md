# STORY-010: PodDisruptionBudget reconciliation for the webhook gateway

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Backlog — blocked on STORY-008, not yet groomed
**Size:** unknown — cannot size until STORY-008's design record settles the CRD's PDB field

## Description

Add `PodDisruptionBudget` reconciliation for the webhook gateway Deployment — this doesn't exist today, which is the real HA gap the epic's Problem section describes (floor of 1 replica, no PDB, zero redundancy during a rollout/node drain). Gated on the CRD's `minAvailable` field being set (exact field name/behavior is a STORY-008 decision).

## Acceptance Criteria

Cannot be written yet — depends on STORY-008's design record (exact field shape, and whether a PDB is created unconditionally with a sane default or only when the field is explicitly set).

## File / Module Footprint

Cannot be fully footprinted yet. Likely candidates once STORY-008 lands:
- `internal/controller/gateway_deployment.go` (new `desiredWebhookGatewayPDB` function, alongside the existing `desiredWebhookGatewayHPA`) or the new controller file from STORY-009, depending on how that story lands
- `config/rbac/role.yaml` — **hot file** (new RBAC verbs for `poddisruptionbudgets`), regenerated via `make manifests`

## Dependencies

- Depends on: STORY-008 (design record); likely also STORY-009 (same controller/reconcile-loop territory — confirm at grooming time whether these two should actually be one story instead of two, once the design record clarifies the controller shape)
- Blocks: none

## Notes

Do not start implementation until STORY-008 is `Approved`. Re-groom this story once that happens, and reconsider whether it should be merged into STORY-009 rather than kept separate.
