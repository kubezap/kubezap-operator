# STORY-009: `WebhookGatewayConfig` CRD types + controller (HPA reconciliation)

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Backlog — blocked on STORY-008, not yet groomed
**Size:** unknown — cannot size until STORY-008's design record settles the CRD shape

## Description

Define the `WebhookGatewayConfig` CRD types and reconcile HPA min/max/target-CPU from its spec instead of `internal/controller/gateway_deployment.go`'s current hardcoded constants (`desiredWebhookGatewayHPA`).

## Acceptance Criteria

Cannot be written yet — depends on STORY-008's design record (exact field names/types, defaulting behavior, singleton enforcement mechanism).

## File / Module Footprint

Cannot be fully footprinted yet — per `/groom-backlog`'s own rule, a story without a real footprint isn't groomed. Likely candidates once STORY-008 lands:
- `api/v1alpha1/webhookgatewayconfig_types.go` (new)
- `api/v1alpha1/groupversion_info.go` — **hot file** (new type registration); per `CLAUDE.md`'s Parallel Agent Guidelines, this must be a sequential wiring-pass edit, not something a parallel worker touches directly
- `internal/controller/webhookgatewayconfig_controller.go` (new) or an extension of `internal/controller/gateway_deployment.go` — which, exactly, is a STORY-008 decision
- `config/crd/bases/*.yaml`, `config/rbac/role.yaml` (regenerated via `make manifests` — also a hot file per the parallel guidelines)
- `cmd/main.go` — **hot file** (new reconciler registration)

## Dependencies

- Depends on: STORY-008 (design record must be `Approved` first)
- Blocks: none

## Notes

Do not start implementation until STORY-008 is `Approved`. Re-groom this story once that happens — footprint above is a reasonable guess, not a commitment.
