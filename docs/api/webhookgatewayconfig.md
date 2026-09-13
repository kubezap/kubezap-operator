# WebhookGatewayConfig CRD

A `WebhookGatewayConfig` configures the shared webhook gateway Deployment for a single
namespace — its inbound TLS/mTLS termination, `HorizontalPodAutoscaler` behavior, and
`PodDisruptionBudget`. At most one `WebhookGatewayConfig` may exist per namespace; the
operator's admission webhook rejects a second `create`.

---

## Contents

- [WebhookGatewayConfig CRD](#webhookgatewayconfig-crd)
  - [Contents](#contents)
  - [Overview](#overview)
  - [Spec Reference](#spec-reference)
    - [WebhookGatewayConfigSpec](#webhookgatewayconfigspec)
    - [WebhookGatewayTLSSpec](#webhookgatewaytlsspec)
    - [WebhookGatewayHPASpec](#webhookgatewayhpaspec)
    - [WebhookGatewayPDBSpec](#webhookgatewaypdbspec)
  - [Status Reference](#status-reference)
    - [WebhookGatewayConfigStatus](#webhookgatewayconfigstatus)
  - [Singleton Enforcement](#singleton-enforcement)
  - [When Changes Take Effect](#when-changes-take-effect)
  - [Migration from Namespace Annotations](#migration-from-namespace-annotations)
  - [Example: Custom TLS, Wider HPA Range, and a PodDisruptionBudget](#example-custom-tls-wider-hpa-range-and-a-poddisruptionbudget)
  - [Limitations](#limitations)

---

## Overview

The webhook gateway is a single shared Deployment per namespace (`kubezap-webhook-gateway`)
that serves every `type: webhook` Trigger in that namespace. Absent a `WebhookGatewayConfig`
object, the operator reconciles that Deployment with a fixed set of defaults: plain HTTP, an
HPA with `minReplicas: 1` / `maxReplicas: 10` / 70% target CPU utilization, and no
`PodDisruptionBudget`.

`WebhookGatewayConfig` lets you override those defaults on a per-namespace basis — enable
TLS or mutual TLS on the gateway's listener, widen or narrow its autoscaling range, and opt
in to a `PodDisruptionBudget` for rollout/drain protection. Every field is optional and every
field left unset keeps the operator's existing default for that field, so creating an empty
`WebhookGatewayConfig` (or omitting one entirely) is behaviorally identical.

`WebhookGatewayConfig` is **not** a Trigger-level setting — it applies to the one gateway
Deployment shared by every webhook Trigger in the namespace. There is no way to give two
different webhook Triggers in the same namespace different TLS or HPA behavior; that would
require separate namespaces (and separate gateway Deployments).

---

## Spec Reference

### WebhookGatewayConfigSpec

| Field                 | Type                     | Required | Default | Description                                                              |
| ---------------------- | ------------------------ | -------- | ------- | -------------------------------------------------------------------------------- |
| `tls`                  | WebhookGatewayTLSSpec     | No       | —       | Inbound TLS/mTLS termination for the gateway. Omitted means plain HTTP.          |
| `hpa`                  | WebhookGatewayHPASpec     | No       | —       | Overrides the gateway's `HorizontalPodAutoscaler`. Unset fields keep operator defaults. |
| `podDisruptionBudget`  | WebhookGatewayPDBSpec     | No       | —       | Configures a `PodDisruptionBudget` for the gateway. Omitted means none is created. |

### WebhookGatewayTLSSpec

| Field               | Type                            | Required    | Default | Description                                                                                          |
| ------------------- | -------------------------------- | ----------- | ------- | ------------------------------------------------------------------------------------------------------------ |
| `serverSecretRef`   | `corev1.LocalObjectReference`    | No          | —       | Secret in this namespace with `tls.crt` + `tls.key` (cert-manager compatible), terminated on the gateway's listener. Omitted means plain HTTP. |
| `clientCASecretRef` | `corev1.LocalObjectReference`    | Conditional | —       | Secret in this namespace with `ca.crt`, used to verify client certificates for mTLS. **Only effective when `serverSecretRef` is also set** — setting it alone has no effect. |

Both fields reference a Secret **in the same namespace** as the `WebhookGatewayConfig`
object — there is no cross-namespace Secret reference, matching the convention already used
by `Integration.spec.kafka.tls.{caSecretRef,clientCertSecretRef}`.

When `serverSecretRef` is set, the controller:
1. Mounts the Secret read-only at `/etc/webhook-tls` in the gateway pod
2. Starts the gateway with `--tls-cert-file=/etc/webhook-tls/tls.crt --tls-key-file=/etc/webhook-tls/tls.key`
3. Switches the liveness/readiness probe scheme to HTTPS and renames the Service port from `http` to `https`

When `clientCASecretRef` is additionally set, the gateway also mounts it at
`/etc/webhook-mtls-ca` and sets `tls.Config.ClientAuth = RequireAndVerifyClientCert` — callers
without a valid client certificate are rejected at the TLS handshake, before the webhook
handler runs. See [webhook-security.md → mTLS (Client Certificate)](../guides/webhook-security.md#mtls-client-certificate)
for the full walkthrough, including Ingress/Route passthrough requirements.

### WebhookGatewayHPASpec

| Field                    | Type    | Required | Default | Description                                                                                                    |
| ------------------------- | ------- | -------- | ------- | ---------------------------------------------------------------------------------------------------------------------- |
| `minReplicas`             | `*int32` | No       | `1`     | Floor of the HPA's replica range. Validated (`+kubebuilder:validation:Minimum=1`) to be a positive integer only — **no minimum-for-HA is enforced**; `1` remains a legitimate, non-HA choice for a low-traffic namespace. |
| `maxReplicas`              | `*int32` | No       | `10`    | Ceiling of the HPA's replica range. No cross-field validation against `minReplicas` — see [Limitations](#limitations). |
| `targetCPUUtilization`     | `*int32` | No       | `70`    | Target average CPU utilization percentage the HPA scales toward.                                                        |

Each field defaults independently — setting only `targetCPUUtilization: 60` keeps
`minReplicas: 1` / `maxReplicas: 10` from the operator's built-in defaults.

### WebhookGatewayPDBSpec

| Field           | Type                    | Required | Default | Description                                                                                  |
| --------------- | ------------------------ | -------- | ------- | ------------------------------------------------------------------------------------------------ |
| `minAvailable`  | `*intstr.IntOrString`    | No       | —       | Minimum number (or percentage, e.g. `"50%"`) of gateway Pods that must remain available during a voluntary disruption. Unset means **no `PodDisruptionBudget` is created** — setting it is the only way to opt in. |

The generated `PodDisruptionBudget` (named `kubezap-webhook-gateway`, same name as the
Deployment) targets the same pod selector as the gateway Deployment's pod template. If you
later clear `podDisruptionBudget.minAvailable` (or delete the `WebhookGatewayConfig`
entirely), the controller deletes the `PodDisruptionBudget` on the next reconcile — it does
not leave an orphaned object behind.

---

## Status Reference

### WebhookGatewayConfigStatus

| Field         | Type          | Description                                    |
| ------------- | ------------- | ----------------------------------------------------- |
| `conditions`  | `[]Condition` | Standard Kubernetes conditions.                        |

> **Currently unpopulated.** `status.conditions` is defined on the type but no controller
> writes to it today — the operator's RBAC for `webhookgatewayconfigs` grants only
> `get`/`list`/`watch` (no `update`/`patch`, and no status subresource access; see
> `config/rbac/role.yaml`). There is no dedicated `WebhookGatewayConfig` reconciler: the
> object is read synchronously, as plain configuration, from inside the Trigger reconciler's
> `ensureWebhookGateway` step (`internal/controller/trigger_controller.go`). A misconfigured
> or rejected field (e.g. a `serverSecretRef` pointing at a Secret that doesn't exist) does
> **not** surface as a `False` condition on this object — see [Limitations](#limitations).

---

## Singleton Enforcement

A validating admission webhook (`internal/webhook/webhookgatewayconfig_webhook.go`) rejects
any `create` of a second `WebhookGatewayConfig` in a namespace that already has one,
regardless of the new object's name. `update` and `delete` requests are always allowed — the
rule only guards the moment a second object would come into existence.

The rejection is a normal admission-webhook denial, so it surfaces to `kubectl apply`/`create`
as a standard API error. The exact message includes the namespace and the name of the
existing object:

```
$ kubectl apply -f second-webhookgatewayconfig.yaml
Error from server (Forbidden): error when creating "second-webhookgatewayconfig.yaml":
admission webhook "vwebhookgatewayconfig.kb.io" denied the request: namespace "team-a"
already has a WebhookGatewayConfig named "webhook-gateway-config" — at most one
WebhookGatewayConfig is allowed per namespace
```

To change configuration, edit the existing object (`kubectl edit webhookgatewayconfig
<name> -n <namespace>`) rather than creating a new one.

---

## When Changes Take Effect

`WebhookGatewayConfig` has no dedicated controller and the Trigger controller does not watch
it directly — it is read fresh, once, at the start of `ensureWebhookGateway` every time that
function runs. In practice that means a create/update/delete of a `WebhookGatewayConfig`
object takes effect **the next time any Trigger in that namespace is reconciled** (any
create, update, delete, or periodic resync of a Trigger in the namespace), not immediately on
its own. For a namespace with active webhook Triggers this is typically within the
controller's normal resync interval; for a namespace with no Triggers yet, the gateway
Deployment doesn't exist yet either, so the config takes effect the moment the first webhook
Trigger is created.

If you need the change applied immediately, touch any Trigger in the namespace (e.g. add or
remove a harmless annotation) to force a reconcile.

---

## Migration from Namespace Annotations

Before this CRD existed, gateway TLS/mTLS was configured via two `Namespace` annotations:
`kubezap.io/webhook-tls-secret` and `kubezap.io/webhook-mtls-ca-secret`. This was a **hard
cutover, not a deprecation** — as of this release those annotations are no longer read
anywhere in the operator (`internal/controller/trigger_controller.go`'s annotation lookups
were removed in the same change that added this CRD). There is no dual-read window and no
precedence rule between the old and new mechanism, because the old mechanism no longer exists
in the code at all.

**If you were relying on either annotation, create the equivalent `WebhookGatewayConfig`
object *before* upgrading the operator** — see the `## [Unreleased]` → `### Breaking` entry in
[`CHANGELOG.md`](../../CHANGELOG.md) for the required migration step and its exact wording.
Skipping this step does not produce an error: the namespace's gateway silently reverts to
plain HTTP with no client-certificate verification on upgrade.

```yaml
# Before (no longer read):
# metadata:
#   annotations:
#     kubezap.io/webhook-tls-secret: kubezap-webhook-tls
#     kubezap.io/webhook-mtls-ca-secret: webhook-client-ca

# After:
apiVersion: automation.kubezap.io/v1alpha1
kind: WebhookGatewayConfig
metadata:
  name: default
  namespace: <namespace>
spec:
  tls:
    serverSecretRef:
      name: kubezap-webhook-tls
    clientCASecretRef:
      name: webhook-client-ca
```

There is no hardcoded requirement that the object be named `default` — any name is valid, as
long as it's the only `WebhookGatewayConfig` in the namespace. `default` is used above only
because it matches the samples in `docs/overview.md` and `docs/guides/webhook-security.md`.

See also [design record: 2026-09-12-webhookgatewayconfig-crd.md](../design/2026-09-12-webhookgatewayconfig-crd.md)
for the full rationale behind the hard-cutover decision (Rejected Alternative B).

---

## Example: Custom TLS, Wider HPA Range, and a PodDisruptionBudget

A higher-traffic namespace that terminates TLS (with mTLS client verification), widens the
autoscaling range beyond the 1–10 default, and guarantees at least 2 gateway Pods stay up
during a voluntary disruption (e.g. a node drain or rolling upgrade):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: WebhookGatewayConfig
metadata:
  name: webhook-gateway-config
  namespace: payments
spec:
  tls:
    serverSecretRef:
      name: webhook-gateway-tls       # tls.crt + tls.key, e.g. from cert-manager
    clientCASecretRef:
      name: webhook-gateway-mtls-ca   # ca.crt used to verify caller client certs
  hpa:
    minReplicas: 3
    maxReplicas: 30
    targetCPUUtilization: 60
  podDisruptionBudget:
    minAvailable: 2
```

`minAvailable` also accepts a percentage string, e.g. `minAvailable: "50%"`.

Apply it, then confirm the singleton rule and current effective config:

```bash
kubectl apply -f webhook-gateway-config.yaml
kubectl get webhookgatewayconfig -n payments
kubectl describe deployment kubezap-webhook-gateway -n payments   # confirm replicas/TLS args
kubectl get hpa kubezap-webhook-gateway -n payments
kubectl get pdb kubezap-webhook-gateway -n payments
```

A minimal example that only widens the HPA range and leaves TLS and the PDB at their
defaults — every unset field keeps the operator's built-in default:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: WebhookGatewayConfig
metadata:
  name: webhook-gateway-config
  namespace: default
spec:
  hpa:
    minReplicas: 2
    maxReplicas: 20
```

---

## Limitations

- **No cross-field validation between `minReplicas` and `maxReplicas`.** Setting
  `minReplicas` higher than `maxReplicas` is accepted by the API server (both are validated
  independently) and produces whatever the underlying `HorizontalPodAutoscaler` does with an
  inverted range — validate your values before applying.
- **`status.conditions` is not populated.** There is no dedicated `WebhookGatewayConfig`
  reconciler; a bad reference (e.g. `serverSecretRef` naming a Secret that doesn't exist)
  does not surface as a condition on this object. Check `kubectl describe deployment
  kubezap-webhook-gateway -n <namespace>` and the gateway pod's logs/events to diagnose a
  TLS Secret that can't be mounted.
- **Not watched directly.** Changes take effect on the next Trigger reconcile in the
  namespace, not immediately — see [When Changes Take Effect](#when-changes-take-effect).
- **One config per namespace, not per Trigger.** All webhook Triggers in a namespace share
  the same gateway Deployment and therefore the same TLS/HPA/PDB configuration.
- **No cluster-scoped or cross-namespace default.** There is no way to set an
  organization-wide default HPA range or require TLS across all namespaces from a single
  object; each namespace needs its own `WebhookGatewayConfig` to opt out of the operator's
  built-in defaults.
