# STORY-009: `WebhookGatewayConfig` CRD types + controller (HPA reconciliation + singleton webhook)

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Implemented, PR #190 open (not yet merged) — includes a wiring-pass commit (`cmd/main.go` webhook registration + the real `ensureWebhookGateway` call site) on top of the dispatched agent's own commit, plus the reconcile-level test proving the two are actually connected
**Size:** M

## Description

Define the `WebhookGatewayConfig` CRD (per `docs/design/2026-09-12-webhookgatewayconfig-crd.md`) and reconcile HPA min/max/target-CPU from its spec instead of `internal/controller/gateway_deployment.go`'s current hardcoded constants (`desiredWebhookGatewayHPA`). Includes the admission webhook that enforces at most one `WebhookGatewayConfig` per namespace.

## Acceptance Criteria

- [x] `WebhookGatewayConfigSpec` with `TLS`/`HPA`/`PodDisruptionBudget` sub-structs — done (PDB is a field-shape stub, STORY-010 fills in its reconciliation behavior).
- [x] `MinReplicas` rejects `<= 0` via `+kubebuilder:validation:Minimum=1` — no HA-minimum enforcement, per the design record.
- [x] `desiredWebhookGatewayHPAFromConfig` reads from the namespace's `WebhookGatewayConfig` when present, falling back per-field to today's hardcoded values when absent — proven both by unit tests (`gateway_deployment_test.go`) and, after the wiring-pass commit, by a reconcile-level envtest that the real `Reconcile` path actually applies a config's HPA fields to the live `HorizontalPodAutoscaler` (`trigger_controller_test.go`).
- [x] Singleton-enforcement validating webhook (mirroring `flowrun_webhook.go`'s rejecting-webhook pattern, not `trigger_webhook.go`'s warn-only one, which was the wrong template) rejects a second `create` in the same namespace — registered in `cmd/main.go` by the wiring-pass commit.
- [x] `make generate && make manifests` run once; RBAC landing confirmed by grep in both `role.yaml` and (hand-verified as not actually auto-regenerated) `namespaced_role.yaml`.

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
