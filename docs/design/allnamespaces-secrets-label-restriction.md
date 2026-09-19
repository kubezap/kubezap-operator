# AllNamespaces-Mode Secrets Access Restricted to `kubezap.io/managed=true`

> Status: Approved
> Date: 2026-09-19
> Related: `internal/controller/flowrun_controller.go`, `internal/controller/gateway_deployment.go`, `internal/gateway/webhook/watcher.go`, `internal/gateway/kafka/watcher.go`, `internal/gateway/amqp/watcher.go`, `internal/gateway/nats/watcher.go`, `docs/design/security-architecture.md`, `docs/architecture.md`, `docs/guides/security-checklist.md`, `planning/backlog/follow-ups.md`

## Problem

`docs/design/security-architecture.md`, `docs/architecture.md`, `docs/guides/security-checklist.md`, and a comment in `cmd/main.go` all claim that in AllNamespaces mode (`WATCH_NAMESPACES=*`), "secrets RBAC is restricted to namespaces labeled `kubezap.io/managed=true`." **This restriction does not exist anywhere in code.** Native Kubernetes RBAC cannot express "restricted to labeled namespaces" — `Role`/`ClusterRole` rules match only `apiGroup`/`resource`/`verb`/`resourceNames`, never a namespace's own labels — so the documented behavior was never achievable via RBAC alone, and no application-level equivalent was ever built either. Today, in AllNamespaces mode, the operator (and each gateway process) can read the value of any Secret referenced by a `secretRef` in any namespace, full stop, undermining the stated least-privilege story for multi-tenant clusters using AllNamespaces mode.

Surfaced during live Helm-install testing (see PR #257) when comparing the Helm chart's independently-narrowed `clusterrole.yaml` secrets rule (read-only, cluster-wide) against the raw manifests' (full CRUD, cluster-wide) — neither matches the documented per-namespace-label restriction.

## Constraints

- **Cross-namespace `secretRef`s are impossible by CRD type shape**, confirmed by code audit: every `secretRef` field across `Trigger`, `Integration`, and `WebhookGatewayConfig` uses `corev1.SecretKeySelector` or `corev1.LocalObjectReference`, neither of which has a `Namespace` field. A resolved secret's namespace is always identical to the referencing CR's own namespace. This significantly narrows the problem: the restriction is really "may the operator act on this namespace's secrets at all," not "which specific secret."
- Must apply consistently across **5 binaries**: the controller (`internal/controller/flowrun_controller.go`'s `fetchSecretValue`/`resolveHTTPTLS`, `internal/controller/gateway_deployment.go`'s `validateWebhookGatewayConfigTLS`) and all 4 gateway processes (webhook/kafka/amqp/nats watchers, each with their own `readSecretKey`).
- Must not meaningfully regress hot-path latency: webhook auth secret resolution happens per-inbound-request.
- Must be a no-op in OwnNamespace/SingleNamespace/MultiNamespace modes — those are already scope-limited by RBAC itself (a `Role` bound only in specific namespaces), so an additional label check there would be redundant complexity with no security benefit.
- Must not silently succeed nor silently skip a Trigger/Integration when its namespace isn't labeled — the resource should remain visible with a clear failure condition, not vanish or degrade unpredictably.
- No `ValidatingAdmissionPolicy` option: VAP only intercepts mutating verbs (create/update/delete/connect) at admission time — it never sees `GET`/`LIST`/`WATCH` requests, so it structurally cannot restrict reads. Confirmed before considering it further.

## Rejected Alternatives

- **Dynamic, self-managed per-namespace `Role`+`RoleBinding`** (operator watches `Namespace` label changes and creates/deletes a `get/list/watch` Secrets `RoleBinding` for its own ServiceAccount in each labeled namespace) — this would be genuine RBAC-level enforcement, matching the documented claim literally. Rejected for this pass: requires a brand-new `Namespace`-watching controller (confirmed none exists anywhere in the codebase today), reconciliation ordering/bootstrap complexity, and a new failure mode (a label removed while a FlowRun is mid-flight racing the RoleBinding's deletion). Meaningfully larger scope than the value justifies for a non-default mode. Noted as the "more correct" approach if AllNamespaces usage grows enough to justify it later — this design's Status should move to `Superseded` at that point, not be silently reopened.
- **Leave RBAC broad, document reality accurately, do nothing else** — rejected: this is a real, actionable security gap for anyone actually relying on the documented isolation story in a multi-tenant AllNamespaces deployment, not just a docs inaccuracy.
- **`resourceNames`-scoped RBAC per secret** — rejected: `resourceNames` requires knowing every secret name in advance; Trigger/Integration authors create secrets with arbitrary names, so this can't be pre-enumerated.

## Decision

Add a single shared helper, checked at each of the 7 existing secret-resolution choke points, active only when the caller is running in AllNamespaces mode (`WATCH_NAMESPACES=*`):

```go
// namespaceAllowsSecretAccess reports whether ns may have its Secrets read by
// the operator/gateways. Only meaningful in AllNamespaces mode — callers in
// any other watch mode skip this check entirely, since RBAC itself already
// scopes them to specific namespaces.
func namespaceAllowsSecretAccess(ctx context.Context, c client.Client, ns string) (bool, error) {
    var namespace corev1.Namespace
    if err := c.Get(ctx, client.ObjectKey{Name: ns}, &namespace); err != nil {
        return false, err
    }
    return namespace.Labels["kubezap.io/managed"] == "true", nil
}
```

Call this immediately before returning a resolved Secret's data, in: `fetchSecretValue` and `resolveHTTPTLS` (controller), `validateWebhookGatewayConfigTLS` (controller), and each gateway's own `readSecretKey` (webhook, kafka, amqp, nats). On a `false` result, return an error that the existing error paths already surface as a visible condition (`Trigger`'s `Ready` condition, `FlowRun`'s step failure message, etc.) — the Trigger/Integration/FlowRun itself stays visible and reconciled; only the credential resolution fails, with a message naming the missing label so an operator can fix it by labeling the namespace.

Each binary already performs a live `Get` for the Secret itself on every resolution (no caching today), so one additional `Get` for the Namespace is the same order of overhead as what already exists, not a new class of cost — acceptable for a mode that is opt-in and lower-traffic than the OwnNamespace default. Duplicate the helper as a small private function in each of the 5 binaries (controller + 4 gateways) rather than introducing a new shared module purely for a 4-line function; if this restriction later gains more logic (e.g. caching, the dynamic-RBAC alternative above), extract a shared package then.

**Resolves the entangled executor-mTLS-write gap noted in `follow-ups.md`**: since reads are now gated at the application layer regardless of what RBAC technically permits, RBAC no longer needs to *also* be narrowed for security — its job shifts to "what's technically possible," with this design's per-call check being the actual enforcement boundary. This means `clusterrole.yaml`'s secrets rule can safely go back to full CRUD (matching the canonical, source-generated `config/rbac/role.yaml`), restoring `reconcileExecutorCertSecret`'s ability to manage the executor's own mTLS cert in AllNamespaces mode — currently silently broken in the Helm chart today because that rule is read-only. Implement this RBAC restoration in the same change as the application-level check, not before it (restoring broad write access without the new read-time check first would briefly reopen the exact gap this design closes).

### Tradeoffs

This is real access control, but not RBAC-level — a cluster admin auditing "what can this ServiceAccount actually do" via `kubectl auth can-i` or RBAC-visualization tooling will still see the (necessarily broad) `get/list/watch` grant on secrets and won't see this narrower, code-enforced restriction. Document this explicitly in `SECURITY.md` and the architecture docs so it isn't mistaken for an RBAC guarantee. A future move to the dynamic-RBAC alternative would close this gap for real, at the cost noted above.
