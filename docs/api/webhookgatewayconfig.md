# WebhookGatewayConfig CRD

A `WebhookGatewayConfig` configures the shared webhook gateway Deployment for a single
namespace — its inbound TLS/mTLS termination, `HorizontalPodAutoscaler` behavior, and
`PodDisruptionBudget`. At most one `WebhookGatewayConfig` may exist per namespace; the object
must be named `default`, and Kubernetes' own name-uniqueness rejects a second one.

---

## Contents

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

There is no dedicated `WebhookGatewayConfig` reconciler: the object is read synchronously,
as plain configuration, from inside the Trigger reconciler's `ensureWebhookGateway` step
(`internal/controller/trigger_controller.go`). That step also writes a single `Ready`
condition on every reconcile, validating that `spec.tls.serverSecretRef`/`clientCASecretRef`
(if set) resolve to Secrets containing the expected keys (`tls.crt`/`tls.key`/`ca.crt`):

| Reason                     | Status  | Meaning                                                          |
| -------------------------- | ------- | ----------------------------------------------------------------- |
| `NoTLSConfigured`          | `True`  | `spec.tls` is not set; the gateway serves plain HTTP.             |
| `WebhookGatewayConfigReady`| `True`  | `spec.tls` Secret references resolved successfully.               |
| `ServerSecretNotFound`     | `False` | `spec.tls.serverSecretRef` names a Secret that doesn't exist.     |
| `ServerSecretMissingKeys`  | `False` | The server Secret is missing `tls.crt` and/or `tls.key`.          |
| `ClientCASecretNotFound`   | `False` | `spec.tls.clientCASecretRef` names a Secret that doesn't exist.   |
| `ClientCASecretMissingKey` | `False` | The CA Secret is missing `ca.crt`.                                |

This condition is only refreshed when a Trigger in the namespace reconciles (see
[When Changes Take Effect](#when-changes-take-effect)) and is best-effort — a failed status
update is logged, not retried or surfaced as a reconcile error, so it does not block
`ensureWebhookGateway`'s Deployment/Service/HPA work. It does not flag
`clientCASecretRef` set without `serverSecretRef` — that combination is a documented no-op,
not an error (see [Limitations](#limitations)).

---

## Singleton Enforcement

The object must be named `default` — a CRD-level CEL validation rule
(`+kubebuilder:validation:XValidation` on the `WebhookGatewayConfig` type) rejects any other
name. Combined with Kubernetes' own per-`(namespace, name)` uniqueness, this makes "at most
one `WebhookGatewayConfig` per namespace" hold unconditionally, enforced by the API server
itself rather than by an admission webhook.

A wrong name is rejected at the schema-validation level:

```
$ kubectl apply -f webhookgatewayconfig.yaml
WebhookGatewayConfig.automation.kubezap.io "webhook-gateway-config" is invalid: <nil>:
Invalid value: the only valid name for a WebhookGatewayConfig is 'default'
```

A second object literally named `default` in a namespace that already has one is rejected by
etcd's native name-uniqueness, the same as any other Kubernetes object:

```
$ kubectl create -f webhookgatewayconfig.yaml
webhookgatewayconfigs.automation.kubezap.io "default" already exists
```

To change configuration, edit the existing object (`kubectl edit webhookgatewayconfig default
-n <namespace>`) rather than creating a new one.

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

---

## Example: Custom TLS, Wider HPA Range, and a PodDisruptionBudget

A higher-traffic namespace that terminates TLS (with mTLS client verification), widens the
autoscaling range beyond the 1–10 default, and guarantees at least 2 gateway Pods stay up
during a voluntary disruption (e.g. a node drain or rolling upgrade):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: WebhookGatewayConfig
metadata:
  name: default
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
  name: default
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
- **`status.conditions` only reflects Secret-reference validity, not deployment health.**
  A `Ready: True` condition means `spec.tls`'s Secret references resolved — it does not mean
  the gateway Deployment is actually up or that the mounted cert is valid PEM. Check
  `kubectl describe deployment kubezap-webhook-gateway -n <namespace>` and the gateway pod's
  logs/events for deployment-level or cert-content problems.
- **Not watched directly.** Changes take effect on the next Trigger reconcile in the
  namespace, not immediately — see [When Changes Take Effect](#when-changes-take-effect).
- **One config per namespace, not per Trigger.** All webhook Triggers in a namespace share
  the same gateway Deployment and therefore the same TLS/HPA/PDB configuration.
- **No cluster-scoped or cross-namespace default.** There is no way to set an
  organization-wide default HPA range or require TLS across all namespaces from a single
  object; each namespace needs its own `WebhookGatewayConfig` to opt out of the operator's
  built-in defaults.
