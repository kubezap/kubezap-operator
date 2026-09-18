# WebhookGatewayConfig Singleton via Fixed Name

> Status: Approved
> Date: 2026-09-18
> Related: `api/v1alpha1/webhookgatewayconfig_types.go`, `internal/webhook/webhookgatewayconfig_webhook.go`, `internal/controller/gateway_deployment.go`, `config/webhook/manifests.yaml`, `docs/api/webhookgatewayconfig.md`, `docs/design/self-managed-webhook-certs.md`

## Problem

`WebhookGatewayConfig`'s "at most one object per namespace" invariant is enforced entirely by an admission webhook (`vwebhookgatewayconfig.kb.io`), which only actually enforces it when the webhook is deployed, its cert is valid, and the API server can reach it — none of which is guaranteed: the Helm chart deploys no `ValidatingWebhookConfiguration`/Service at all (so `getWebhookGatewayConfig` silently uses `list.Items[0]` with no error, event, or condition surfaced), and even where the webhook is deployed it depends on the self-managed cert mechanism successfully patching the shared `caBundle`, which itself needs cluster-scoped RBAC a namespace-restricted tenant may not have. User impact: depending on install path and RBAC posture, a user can end up with two `WebhookGatewayConfig` objects in one namespace with no indication which is actually governing the gateway.

## Constraints

- Must not depend on the operator's own webhook/cert infrastructure being deployed or healthy — the point is to make "at most one" hold even when that infrastructure isn't present.
- Must not change `WebhookGatewayConfigSpec`'s schema (`spec.tls`, `spec.hpa`, `spec.podDisruptionBudget`) — this changes how the singleton is enforced, not what's configurable.
- CEL validation (`x-kubernetes-validations`) requires Kubernetes 1.25+, already within this project's stated `minKubeVersion: 1.25.0`.
- No existing objects to migrate — pre-release, no installed base using a non-`default` name.
- `getWebhookGatewayConfig` is on the Trigger reconcile hot path — the lookup must stay a cheap, indexed call.
- At most one `WebhookGatewayConfig` must exist per namespace unconditionally, regardless of whether any admission webhook is deployed, reachable, or has a valid cert.
- A second `create` in the same namespace must be rejected by the API server itself, before etcd, with no dependency on controller-manager code running at all.
- A namespace with no `WebhookGatewayConfig` continues to reconcile with today's defaults (HPA min=1/max=10/target=70%, no PDB, no TLS), unchanged.
- `getWebhookGatewayConfig` returns not-found (`nil, nil`) for a namespace with no config, and never needs to reason about multiple items.

## Rejected Alternatives

- **Keep the admission webhook, change `failurePolicy` from `Fail` to `Ignore`, document the risk** — makes the webhook-healthy case strictly worse (silent accept on a genuine collision) while fixing nothing for the already-broken cases (Helm, RBAC-restricted tenants) that motivated this record.
- **Drop the webhook but keep arbitrary names, rely on `list.Items[0]` plus a warning Event/status condition when more than one object is found** — strictly better than today, but still lets a second object silently exist until someone notices the Event; a fixed name means the second `create` simply never succeeds, so this remains worth adding as a defensive backstop, not the primary guarantee.
- **Keep arbitrary-named CRD + webhook enforcement, but add the missing Helm webhook manifests and an RBAC-skip toggle for the cert mechanism** — more moving parts (Helm manifests, RBAC-skip toggle, Forbidden-handling) to reach a guarantee still conditional on all of them working together, versus a schema-level rule that's unconditional with less code; the RBAC-skip/Forbidden-handling work is still independently worth doing for `Trigger`/`FlowRun`'s webhooks, just not as the fix for this CRD.

## Decision

Replace admission-webhook singleton enforcement with a fixed required object name. Add `+kubebuilder:validation:XValidation:rule="self.metadata.name == 'default'",message="the only valid name for a WebhookGatewayConfig is 'default'"` at the root of the `WebhookGatewayConfig` Go type (not `WebhookGatewayConfigSpec`), so the rule sees `metadata` too. Combined with Kubernetes' native per-`(namespace, name)` uniqueness, "at most one per namespace" holds unconditionally — no webhook, cert, or cluster-scoped RBAC involved for this CRD. Remove the `vwebhookgatewayconfig.kb.io` entry from `config/webhook/manifests.yaml`, delete `WebhookGatewayConfigValidator` (`internal/webhook/webhookgatewayconfig_webhook.go`) and its `cmd/main.go` registration, and simplify `getWebhookGatewayConfig` to a `Get` for name `default` instead of a `List`. Update `docs/api/webhookgatewayconfig.md` (currently documents "any name is valid") and any sample manifests using a non-`default` name. `Trigger`/`FlowRun`'s webhook entries, and the operator's `internal/webhookcerts` cluster-scoped-RBAC dependency, are unaffected — that remains a separate, open concern.

- Every object must now be named exactly `default` — a new constraint, though existing docs/samples already use that name.
- The custom rejection message is replaced by the API server's generic CEL-violation message (wrong name) or a standard 409 (same-name double-create) — still immediate and unambiguous, just less custom-worded.
- This narrows but doesn't eliminate the cluster-scoped-RBAC dependency for admission webhooks generally, since `vflowrun.kb.io`/`vtrigger.kb.io` still need it.
- A defensive warning Event/status-condition on `getWebhookGatewayConfig` finding unexpected inconsistent state (e.g. a direct etcd/backup-restore edge case) is still worth adding, even though the fixed name makes it unreachable in the normal case.
