# STORY-010: PodDisruptionBudget reconciliation for the webhook gateway

**Epic:** EPIC-003 — WebhookGatewayConfig CRD
**Status:** Done (PR #196)
**Size:** S

## Description

Add `PodDisruptionBudget` reconciliation for the webhook gateway Deployment — this doesn't exist today, which is the real HA gap the epic's Problem section describes (floor of 1 replica, no PDB, zero redundancy during a rollout/node drain). Gated on `spec.podDisruptionBudget.minAvailable` (per `docs/design/2026-09-12-webhookgatewayconfig-crd.md`): nil means no PDB is created at all, matching today's actual behavior.

## Acceptance Criteria

- [ ] `WebhookGatewayPDBSpec.MinAvailable *intstr.IntOrString` (`+optional`) added to the types from STORY-009.
- [ ] A `desiredWebhookGatewayPDB` function (alongside `desiredWebhookGatewayHPA` in `internal/controller/gateway_deployment.go`, or the new controller file from STORY-009 — match wherever HPA reconciliation landed) returns nil/no-op when `minAvailable` is unset, and a `policyv1.PodDisruptionBudget` targeting the gateway Deployment's pod selector when set.
- [ ] A namespace with `minAvailable` set actually gets a live `PodDisruptionBudget` object reconciled — verify with an envtest-level reconcile test, not just a unit test of the desired-object builder function.
- [ ] `config/rbac/role.yaml` gets `poddisruptionbudgets` create/get/list/watch/update/delete verbs (whichever the reconciler actually needs) via `+kubebuilder:rbac` markers, confirmed landed after `make manifests` (per `CLAUDE.md`'s reminder that misplaced markers are silently dropped).

## File / Module Footprint

- `internal/controller/gateway_deployment.go` or `internal/controller/webhookgatewayconfig_controller.go` (wherever STORY-009's HPA reconciliation landed — same file, same reconcile loop)
- `config/rbac/role.yaml` — **hot file**, regenerated via `make manifests`

## Dependencies

- Depends on: STORY-008 (design record), STORY-009 (CRD types + the controller/reconcile loop this extends — same reconcile loop, not a separate one)
- Blocks: STORY-012 (docs)

## Notes

Deliberately scoped as its own story rather than folded into STORY-009, since PDB reconciliation is logically separable (different desired-object builder, different RBAC verbs) even though it likely lands in the same controller file/reconcile pass. If implementation reveals they're trivially the same diff, that's fine — dispatch them together rather than force artificial separation.
