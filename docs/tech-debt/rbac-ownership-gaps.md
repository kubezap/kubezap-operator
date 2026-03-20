# Tech Debt: RBAC Ownership and Cleanup Gaps

> Identified: 2026-03-18 (codebase review)
> **RESOLVED: All issues fixed. See resolution notes below.**
> Severity: HIGH (owner ref gap) / MEDIUM (delete verb) — resolved
> Affects: `internal/controller/integration_controller.go`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`

---

## Summary

Two related gaps in RBAC lifecycle management that cause orphaned Kubernetes resources when Integrations or gateway Deployments are deleted:

1. Kafka gateway SA, Role, and RoleBinding are created without owner references on the Integration — they are never garbage-collected
2. The controller's own ClusterRole (and namespaced Role) is missing the `delete` verb on `roles` and `rolebindings`, so the controller cannot clean up gateway RBAC resources even when it tries to

---

## Issue 1: Missing Owner References on Kafka Gateway RBAC

### Location

`internal/controller/integration_controller.go` — `reconcileKafkaGateway` function, SA/Role/RoleBinding creation blocks (~lines 495–548)

### Problem

When the controller creates the Kafka gateway ServiceAccount, Role, and RoleBinding, it does NOT call `ctrl.SetControllerReference`:

```go
// Current code — no owner reference set
sa := &corev1.ServiceAccount{...}
if err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
    // no SetControllerReference here
    return nil
}); err != nil { ... }
```

**Compare** with the plugin RBAC path (`reconcilePluginRBAC`) which correctly sets owner references on all three resources, causing them to be garbage-collected when the Integration is deleted.

**Consequence:** When a Kafka Integration is deleted, the controller's reconciler stops running for it, but:
- The `kubezap-kafka-gateway-<name>` ServiceAccount remains
- The `kubezap-kafka-gateway-<name>` Role remains
- The `kubezap-kafka-gateway-<name>` RoleBinding remains

These accumulate over the lifetime of the cluster. In namespaces where many Integrations have come and gone, this creates RBAC noise that is invisible to the operator but visible to anyone with `kubectl get roles`.

### Fix

Add `ctrl.SetControllerReference(integration, sa, r.Scheme)`, `ctrl.SetControllerReference(integration, role, r.Scheme)`, and `ctrl.SetControllerReference(integration, rb, r.Scheme)` inside the respective `CreateOrUpdate` mutate functions, mirroring the plugin RBAC path.

Note: The Kafka gateway *Deployment* is already cleaned up by the controller (the reconciler deletes it when no matching Triggers remain), but the RBAC resources are not.

---

## Issue 2: Controller ClusterRole Missing `delete` Verb on RBAC Resources

### Location

| File | Resource | Lines |
|------|----------|-------|
| `config/rbac/role.yaml` | ClusterRole | ~100–110 |
| `config/rbac/namespaced_role.yaml` | Role (OwnNamespace mode) | ~104–114 |

### Problem

The controller's ClusterRole grants:
```yaml
- apiGroups: [rbac.authorization.k8s.io]
  resources: [roles, rolebindings]
  verbs: [get, list, watch, create, update, patch]
  # missing: delete
```

The `delete` verb is absent for `roles`, `rolebindings`, and `serviceaccounts`. This means:

1. Even if owner references are added (Issue 1 fix), Kubernetes garbage collection of RBAC resources requires the controller to have `delete` permission — otherwise GC is silently blocked
2. The controller cannot proactively clean up orphaned RBAC resources when an Integration is removed (the `reconcileKafkaGateway` cleanup path cannot delete SA/Role/RoleBinding it created)
3. This affects both AllNamespaces mode (`role.yaml` → ClusterRole) and OwnNamespace mode (`namespaced_role.yaml` → Role)

### Fix

Add `delete` to the verbs list for `serviceaccounts`, `roles`, and `rolebindings` in both RBAC files. Then run `make manifests` to regenerate. The kubebuilder markers in the reconciler should also be updated to match:

```go
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
```

---

## Resolution Order

Fix Issue 2 first (it is a prerequisite for Issue 1 to work correctly with Kubernetes GC), then Issue 1.

Steps:
1. Update kubebuilder RBAC markers in `integration_controller.go` and `trigger_controller.go` to add `delete`
2. Run `make manifests` to regenerate `role.yaml` and `namespaced_role.yaml`
3. ~~Add `ctrl.SetControllerReference` calls in `reconcileKafkaGateway` for SA, Role, RoleBinding~~ — **corrected**: shared SA/Role/RoleBinding must NOT be owned by a single Integration (see Issue 3 below)

---

## Issue 3: Kafka Gateway Owner Ref on Shared SA/Role/RoleBinding (fixed 2026-03-20)

The fix applied in Issue 1 incorrectly called `ctrl.SetControllerReference(integration, sa, r.Scheme)` on the `kubezap-gateway` SA, Role, and RoleBinding. These resources are **shared** across all broker-type integrations in a namespace (kafka, amqp, nats all use the same `kubezap-gateway` SA/Role/RoleBinding). Setting an owner reference to a single Kafka Integration would cause Kubernetes GC to delete the shared SA when the Kafka Integration is deleted, even if AMQP or NATS integrations still exist in the same namespace and still need the SA.

**Fix (2026-03-20):** Removed `ctrl.SetControllerReference` from the `kubezap-gateway` SA, Role, and RoleBinding in all three gateway reconcilers (kafka, amqp, nats). The gateway Deployments retain their owner references (each Deployment is owned by its specific Integration and is correctly GC'd). The shared RBAC resources persist as namespace-level resources; they are not GC'd automatically when integrations are deleted (acceptable — they are idempotent to recreate and the same permissions are needed regardless of which broker type is active).

Also fixed in the same pass:
- AMQP and NATS gateway `reconcileAmqpGateway`/`reconcileNatsGateway`: converted Role management from manual Get/Create/Update to `CreateOrUpdate` (matching the Kafka gateway pattern), ensuring idempotent rule updates without the read-modify-write race.

---

## Related Schedule Items

- Original issue tracked in schedule.md section 8 (now cleaned up — all resolved)
- Issue 3 was introduced by the Issue 1 fix and corrected 2026-03-20
