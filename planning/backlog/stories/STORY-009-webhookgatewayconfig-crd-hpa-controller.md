# STORY-009: `WebhookGatewayConfig` CRD types + controller (HPA reconciliation + singleton webhook)

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Planned (WP-1 — see `planning/checkpoints/checkpoint-2026-09-12/work-packages-2.md`)
**Size:** M

## Description

Define the `WebhookGatewayConfig` CRD (per `docs/design/2026-09-12-webhookgatewayconfig-crd.md`) and reconcile HPA min/max/target-CPU from its spec instead of `internal/controller/gateway_deployment.go`'s current hardcoded constants (`desiredWebhookGatewayHPA`). Includes the admission webhook that enforces at most one `WebhookGatewayConfig` per namespace.

## Acceptance Criteria

- [ ] `WebhookGatewayConfigSpec` with `TLS *WebhookGatewayTLSSpec`, `HPA *WebhookGatewayHPASpec`, `PodDisruptionBudget *WebhookGatewayPDBSpec` sub-structs (PDB struct itself can be a stub in this story — STORY-010 fills in its behavior). `HPA` has `MinReplicas`, `MaxReplicas`, `TargetCPUUtilization` (all `*int32`, `+optional`).
- [ ] `MinReplicas` rejects `<= 0` via kubebuilder validation marker (`+kubebuilder:validation:Minimum=1`) — no HA-minimum enforcement, per the design record.
- [ ] `desiredWebhookGatewayHPA` (or its replacement) reads from the namespace's `WebhookGatewayConfig` when present, falling back to today's exact hardcoded values (min=1, max=10, target=70%) when absent — verify via a reconcile-level test that an absent config produces byte-identical HPA spec to today's code.
- [ ] New validating webhook (mirroring `internal/webhook/trigger_webhook.go`'s registration pattern) rejects a `create` when the namespace already has a `WebhookGatewayConfig` (any name).
- [ ] `make generate && make manifests` run once, after all other changes in this story land — not per-edit.

## File / Module Footprint

- `api/v1alpha1/webhookgatewayconfig_types.go` (new)
- `api/v1alpha1/groupversion_info.go` — **hot file**, sequential wiring-pass edit only (register the new type)
- `internal/webhook/webhookgatewayconfig_webhook.go` (new) — singleton-enforcement admission webhook
- `internal/controller/webhookgatewayconfig_controller.go` (new) or extends `internal/controller/gateway_deployment.go` — prefer a new controller file unless the reconcile is trivially small; decide at implementation time based on how much of `gateway_deployment.go`'s existing logic needs to read the new CRD
- `config/crd/bases/*.yaml`, `config/webhook/*.yaml`, `config/rbac/role.yaml` — regenerated via `make manifests`, **hot files**
- `cmd/main.go` — **hot file** (new reconciler + webhook registration)
- `config/samples/` — a sample `WebhookGatewayConfig` CR

## Dependencies

- Depends on: STORY-008 (design record, now `Approved`)
- Blocks: STORY-010 (PDB spec may extend the same types file), STORY-011 (TLS annotation migration reads from this CRD), STORY-012 (docs)

## Notes

Per `CLAUDE.md`'s Parallel Agent Guidelines, this story's two hot-file touches (`groupversion_info.go`, `cmd/main.go`) mean it needs a sequential wiring pass if ever dispatched alongside another story touching those files — not a candidate for naive `/plan-parallel` alongside anything else that registers a new controller/type.
