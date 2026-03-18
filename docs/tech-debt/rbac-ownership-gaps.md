# Tech Debt: RBAC Ownership and Cleanup Gaps

> Identified: 2026-03-18 (codebase review)
> Severity: HIGH (owner ref gap) / MEDIUM (delete verb)
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
3. Add `ctrl.SetControllerReference` calls in `reconcileKafkaGateway` for SA, Role, RoleBinding

After these changes, deleting a Kafka Integration will cascade-delete all associated gateway RBAC resources via Kubernetes garbage collection.

---

## Related Schedule Items

- `docs/schedule.md` section 8 backlog line 262 (partially covers this — mentions Role/RoleBinding accumulation and missing owner references on Kafka gateway RBAC)
- Issue 1 (owner references) is **new** — not previously tracked in schedule.md
- Issue 2 (delete verb) was noted in backlog as affecting `config/rbac/role.yaml` but `namespaced_role.yaml` was not mentioned
