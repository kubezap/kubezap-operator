# Trigger CRD

A `Trigger` defines an event source that starts a `Flow`. It listens for an event — an incoming HTTP request, a Kafka message, or a scheduled time — and invokes the referenced Flow when that event occurs.

---

## Contents

- [Trigger CRD](#trigger-crd)
  - [Contents](#contents)
  - [Overview](#overview)
  - [Spec Reference](#spec-reference)
    - [TriggerSpec](#triggerspec)
    - [WebhookTrigger](#webhooktrigger)
    - [WebhookAuth](#webhookauth)
    - [WebhookBasicAuth](#webhookbasicauth)
    - [CronTrigger](#crontrigger)
    - [KafkaTrigger](#kafkatrigger)
    - [AmqpTrigger](#amqptrigger)
    - [NatsTrigger](#natstrigger)
    - [FlowReference](#flowreference)
    - [ActionDefinition](#actiondefinition)
    - [WebhookAction](#webhookaction)
    - [CooldownPolicy](#cooldownpolicy)
    - [ResourceTrigger](#resourcetrigger)
  - [Status Reference](#status-reference)
    - [TriggerStatus](#triggerstatus)
    - [`lastResult` Values](#lastresult-values)
    - [Condition Types](#condition-types)
  - [Trigger Types](#trigger-types)
    - [Webhook](#webhook)
    - [Cron](#cron)
    - [Kafka, AMQP, NATS (Broker Triggers)](#kafka-amqp-nats-broker-triggers)
    - [Kubernetes Resource Events](#kubernetes-resource-events)
  - [Exposing Webhook Triggers](#exposing-webhook-triggers)
    - [Kubernetes Ingress](#kubernetes-ingress)
    - [Kubernetes Gateway API (recommended for Kubernetes 1.28+)](#kubernetes-gateway-api-recommended-for-kubernetes-128)
    - [OpenShift Route](#openshift-route)
  - [TLS and mTLS](#tls-and-mtls)
    - [Outbound TLS](#outbound-tls)
    - [Inbound Webhook mTLS](#inbound-webhook-mtls)
  - [Examples](#examples)
    - [Example 1: Webhook Trigger](#example-1-webhook-trigger)
    - [Example 2: Cron Trigger](#example-2-cron-trigger)
    - [Example 3: Kafka Trigger](#example-3-kafka-trigger)
    - [Example 4: With Cooldown Policy](#example-4-with-cooldown-policy)
    - [Example 5: Inline Action (No Flow)](#example-5-inline-action-no-flow)
    - [Example 6: Resource Trigger (Pod Failure Watcher)](#example-6-resource-trigger-pod-failure-watcher)
  - [Rate Limiting](#rate-limiting)
  - [Status Conditions](#status-conditions)
  - [Limitations](#limitations)

---

## Overview

Each `Trigger` has a `type` (webhook, cron, kafka, amqp, nats, or resource) and a `flowRef` pointing to the `Flow` to execute. When the event fires, the operator resolves the Flow, populates its parameters from the event payload, and executes the steps.

A `Trigger` can also define an inline `action` instead of a `flowRef` for simple one-step use cases such as forwarding a webhook to another URL.

---

## Spec Reference

### TriggerSpec

| Field      | Type             | Required    | Default | Description                                                                      |
| ---------- | ---------------- | ----------- | ------- | -------------------------------------------------------------------------------- |
| `type`     | enum             | **Yes**     | —       | Trigger source type: `webhook`, `cron`, `kafka`, `amqp`, `nats`, or `resource`   |
| `enabled`  | boolean          | No          | `true`  | Whether this trigger is active. Set to `false` to pause without deleting.        |
| `webhook`  | WebhookTrigger   | Conditional | —       | Required when `type: webhook`                                                    |
| `cron`     | CronTrigger      | Conditional | —       | Required when `type: cron`                                                       |
| `kafka`    | KafkaTrigger     | Conditional | —       | Required when `type: kafka`                                                      |
| `amqp`     | AmqpTrigger      | Conditional | —       | Required when `type: amqp`                                                       |
| `nats`     | NatsTrigger      | Conditional | —       | Required when `type: nats`                                                       |
| `resource` | ResourceTrigger  | Conditional | —       | Required when `type: resource`                                                   |
| `flowRef`  | FlowReference    | Conditional | —       | Reference to the Flow to execute. Required unless `action` is set.               |
| `action`   | ActionDefinition | Conditional | —       | Inline action. Used instead of `flowRef` for simple single-step responses.       |
| `cooldown` | CooldownPolicy   | No          | —       | Rate limiting policy to prevent trigger storms                                   |
| `flowRunGC`| FlowRunGCPolicy  | No          | —       | Per-trigger GC policy for completed FlowRuns. See [FlowRun GC](flowrun.md#garbage-collection). |
| `event`    | string           | No          | —       | Resource event type for resource-based triggers: `create`, `update`, or `delete` |

### WebhookTrigger

| Field    | Type        | Required | Default | Description                                                                                              |
| -------- | ----------- | -------- | ------- | -------------------------------------------------------------------------------------------------------- |
| `path`           | string           | **Yes**  | —       | HTTP path exposed by the operator (e.g., `/hooks/my-trigger`)                                            |
| `method`         | enum             | No       | `POST`  | Accepted HTTP method: `POST` or `PUT`                                                                    |
| `auth`           | WebhookAuth      | No       | —       | Authentication policy for this endpoint. See [Securing Webhook Triggers](../guides/webhook-security.md). |
| `rateLimit`      | WebhookRateLimit | No       | —       | Per-route request budget (in-memory sliding window per gateway replica).                                  |
| `redactHeaders`  | []string         | No       | —       | Additional header names to redact in FlowRun TriggerData (extends built-in list).                        |
| `redactBody`     | boolean          | No       | `false` | Replace stored request body with `[REDACTED]` in FlowRun TriggerData.                                   |

The full URL of the webhook endpoint is: `http://<operator-service>:<port><path>`

For authentication configuration examples and security guidance see [Securing Webhook Triggers](../guides/webhook-security.md).

### WebhookRateLimit

Per-route request budget enforced in-memory by the webhook gateway, using a sliding window. This is per gateway replica, not cluster-wide — with multiple replicas the effective limit is `maxRequests × replica count`. Excess requests are rejected and recorded as `RateLimited` in the Trigger's `status.lastResult`.

| Field         | Type     | Required | Default | Description                                            |
| ------------- | -------- | -------- | ------- | ------------------------------------------------------- |
| `maxRequests` | integer  | **Yes**  | —       | Maximum number of requests allowed within `window`. Must be greater than 0. |
| `window`      | duration | No       | `60s`   | Duration of the sliding window (e.g. `60s`, `1m`, `5m`) |

```yaml
spec:
  type: webhook
  webhook:
    path: /hooks/my-service
    rateLimit:
      maxRequests: 100
      window: "60s"
```

### WebhookAuth

Configures authentication for a webhook trigger endpoint. If omitted, the endpoint accepts requests from any caller — always set `auth` in production.

| Field          | Type               | Required    | Description                                                                                    |
| -------------- | ------------------ | ----------- | ---------------------------------------------------------------------------------------------- |
| `type`         | enum               | **Yes**     | Authentication method: `hmac`, `bearer`, `oidc`, `basic`, `apiKey`, `ipAllowlist`, `header-equals` |
| `hmac`         | HMACConfig         | Conditional | HMAC signature config. Required when `type: hmac`.                                             |
| `bearer`       | BearerConfig       | Conditional | Bearer token config. Required when `type: bearer`.                                             |
| `oidc`         | OIDCConfig         | Conditional | OIDC/JWT config. Required when `type: oidc`.                                                   |
| `basic`        | WebhookBasicAuth   | Conditional | Basic auth config. Required when `type: basic`.                                                |
| `apiKey`       | APIKeyConfig       | Conditional | API key config. Required when `type: apiKey`.                                                  |
| `ipAllowlist`  | IPAllowlistConfig  | Conditional | IP allowlist config. Required when `type: ipAllowlist`.                                        |
| `headerEquals` | HeaderEqualsConfig | Conditional | Exact header match config. Required when `type: header-equals`.                                |

> **Note — mTLS**: Client-certificate authentication is not configured via `WebhookAuth`. It is enforced at the TLS termination layer using the namespace's `WebhookGatewayConfig` object (`spec.tls.clientCASecretRef`). See [TLS and mTLS](#tls-and-mtls) for details.

### HMACConfig

| Field                      | Type              | Required | Default  | Description                                                             |
| --------------------------- | ----------------- | -------- | -------- | ------------------------------------------------------------------------ |
| `secretRef`                 | SecretKeySelector | **Yes**  | —        | Secret key containing the HMAC secret.                                   |
| `provider`                  | enum              | No       | `github` | `github` (X-Hub-Signature-256, `sha256=<hex>` over the body) or `slack` (X-Slack-Signature, `v0=<hex>` over `v0:<timestamp>:<body>`). See [webhook-security.md](../guides/webhook-security.md#hmac-signature-verification). |
| `timestampToleranceSeconds` | int32             | No       | `300`    | Replay-window tolerance for `provider: slack`; ignored otherwise.        |

### BearerConfig

| Field            | Type              | Required | Description                                       |
| ---------------- | ----------------- | -------- | ------------------------------------------------- |
| `tokenSecretRef` | SecretKeySelector | **Yes**  | Secret key containing the expected bearer token.  |

### OIDCConfig

| Field      | Type   | Required | Default | Description                                                                           |
| ---------- | ------ | -------- | ------- | ------------------------------------------------------------------------------------- |
| `issuer`   | string | **Yes**  | —       | OIDC/JWT issuer URL (e.g., `https://accounts.google.com`).                            |
| `audience` | string | No       | —       | Expected `aud` claim value. When omitted, audience validation is skipped.             |

### APIKeyConfig

| Field       | Type              | Required | Default     | Description                                                   |
| ----------- | ----------------- | -------- | ----------- | ------------------------------------------------------------- |
| `secretRef` | SecretKeySelector | **Yes**  | —           | Secret key containing the expected API key value.             |
| `header`    | string            | No       | `X-Api-Key` | Header name to check for the API key.                         |

### IPAllowlistConfig

| Field   | Type     | Required | Description                                                                                  |
| ------- | -------- | -------- | -------------------------------------------------------------------------------------------- |
| `cidrs` | []string | **Yes**  | List of CIDR blocks allowed to call this endpoint (e.g., `["10.0.0.0/8", "192.168.1.0/24"]`). |

### HeaderEqualsConfig

| Field       | Type              | Required | Description                                            |
| ----------- | ----------------- | -------- | ------------------------------------------------------ |
| `header`    | string            | **Yes**  | HTTP header name to check (e.g., `X-Gitlab-Token`).    |
| `secretRef` | SecretKeySelector | **Yes**  | Secret key containing the expected header value.       |

### WebhookBasicAuth

| Field         | Type                 | Required | Default    | Description                                              |
| ------------- | -------------------- | -------- | ---------- | -------------------------------------------------------- |
| `secretRef`   | LocalObjectReference | **Yes**  | —          | Name of the Secret containing the username and password. |
| `usernameKey` | string               | No       | `username` | Key within the Secret that holds the username.           |
| `passwordKey` | string               | No       | `password` | Key within the Secret that holds the password.           |

### CronTrigger

| Field      | Type   | Required | Default | Description                                                                                                                                                                      |
| ---------- | ------ | -------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `schedule` | string | **Yes**  | —       | Cron expression in standard five-field format: `minute hour day-of-month month day-of-week`. Also supports `robfig/cron` extended syntax: `@daily`, `@hourly`, `@every 5m`, etc. |
| `timezone` | string | No       | `UTC`   | IANA timezone name for the schedule (e.g. `America/New_York`, `Europe/Berlin`). Defaults to UTC when omitted.                                                                    |

**Example schedules:**

| Schedule       | Meaning                 |
| -------------- | ----------------------- |
| `0 * * * *`    | Every hour at minute 0  |
| `*/15 * * * *` | Every 15 minutes        |
| `0 2 * * *`    | Daily at 2:00 AM        |
| `0 9 * * 1`    | Every Monday at 9:00 AM |

### KafkaTrigger

| Field            | Type                 | Required | Default                  | Description                                                    |
| ---------------- | -------------------- | -------- | ------------------------ | -------------------------------------------------------------- |
| `integrationRef` | LocalObjectReference | **Yes**  | —                        | Reference to an `Integration` CR with Kafka connection details |
| `topic`          | string               | **Yes**  | —                        | Kafka topic to consume from                                    |
| `consumerGroup`  | string               | No       | `kubezap-<trigger-name>` | Consumer group ID                                              |

### AmqpTrigger

| Field            | Type                 | Required | Default | Description                                                   |
| ---------------- | -------------------- | -------- | ------- | ------------------------------------------------------------- |
| `integrationRef` | LocalObjectReference | **Yes**  | —       | Reference to an `Integration` CR with AMQP connection details |
| `topic`          | string               | **Yes**  | —       | Queue name to consume from                                    |
| `routingKey`     | string               | No       | —       | AMQP routing key or binding pattern                           |

### NatsTrigger

| Field            | Type                 | Required | Default | Description                                                                    |
| ---------------- | -------------------- | -------- | ------- | ------------------------------------------------------------------------------ |
| `integrationRef` | LocalObjectReference | **Yes**  | —       | Reference to an `Integration` CR with NATS connection details                  |
| `subject`        | string               | **Yes**  | —       | NATS subject to subscribe to. Supports wildcards (e.g. `orders.*`, `events.>`) |

### FlowReference

| Field  | Type   | Required | Description                    |
| ------ | ------ | -------- | ------------------------------ |
| `name` | string | **Yes**  | Name of the Flow CR to execute |

> **v1alpha1 restriction:** Cross-namespace FlowRefs are not supported. The Flow must be in the same namespace as the Trigger. A `flowRef.namespace` field was present in earlier pre-release builds but has been removed; an admission webhook rejects any FlowRun with a non-empty `flowRef.namespace`. Cross-namespace flows are deferred to v1beta1 via a `FlowGrant` CRD (analogous to Gateway API `ReferenceGrant`).

See [Flow CRD](flow.md) for the full specification of what a Flow contains and how it processes the trigger payload.

### ActionDefinition

An inline action for simple use cases that do not require a full Flow. Currently supports `webhook` actions only.

| Field     | Type          | Required    | Description                   |
| --------- | ------------- | ----------- | ----------------------------- |
| `type`    | enum          | **Yes**     | Action type: `webhook`        |
| `webhook` | WebhookAction | Conditional | Required when `type: webhook` |

### WebhookAction

| Field     | Type              | Required | Default | Description                                |
| --------- | ----------------- | -------- | ------- | ------------------------------------------ |
| `url`     | string            | **Yes**  | —       | URL to call when the trigger fires         |
| `method`  | enum              | No       | `POST`  | HTTP method: `POST`, `PUT`, `PATCH`, `GET` |
| `headers` | map[string]string | No       | —       | HTTP headers to include                    |
| `body`    | string            | No       | —       | Request body (supports Go template syntax) |

### CooldownPolicy

Prevents a trigger from firing more than a set number of times in a given window. Excess firings are silently dropped and recorded as `RateLimited` in the status.

| Field            | Type     | Required | Default | Description                                              |
| ---------------- | -------- | -------- | ------- | -------------------------------------------------------- |
| `maxInvocations` | integer  | No       | `0` (no limit) | Maximum number of firings allowed within `window`. If zero (the default), no limit is applied. |
| `window`         | duration | No       | `60s`   | Time window for counting invocations (e.g., `60s`, `5m`) |

### ResourceTrigger

> **Alpha feature — not recommended for production.**
>
> `type: resource` is alpha-stability. The API may change in future releases without a deprecation period.
>
> Known limitations:
> - **Naive pluralization fallback**: the controller derives the plural resource name by appending `s`. Irregular plurals (e.g. `policies`, `endpoints`, `statuses`) will fail to discover the resource. Work around this by confirming the plural form with `kubectl api-resources` and opening a bug if the fallback is wrong.
> - **Cooldown interaction**: rapidly-updated resources (e.g. Pods during a rollout) can generate bursts of `MODIFIED` events that saturate the cooldown window and cause legitimate events to be suppressed. Tune `spec.cooldown` conservatively or avoid using `watchFields` on high-churn fields in production.
>
> See [docs/overview.md](../overview.md) for the authoritative stability notice and the full list of alpha-stage features.

Watches a Kubernetes resource type for create, update, or delete events and fires the trigger when a matching event occurs. Resource triggers run inside the controller -- no separate gateway pod is needed.

| Field           | Type          | Required | Default             | Description                                                                                  |
| --------------- | ------------- | -------- | ------------------- | -------------------------------------------------------------------------------------------- |
| `apiVersion`    | string        | **Yes**  | --                  | API version of the resource (e.g. `v1`, `apps/v1`, `automation.kubezap.io/v1alpha1`)         |
| `kind`          | string        | **Yes**  | --                  | Kind of the resource (e.g. `Pod`, `ConfigMap`, `Deployment`)                                 |
| `namespace`     | string        | No       | Trigger's namespace | Namespace to watch. Defaults to the Trigger's own namespace.                                 |
| `labelSelector` | LabelSelector | No       | --                  | Only fire for resources matching these labels                                                |
| `events`        | []string      | No       | `[create]`          | Event types to watch: `create`, `update`, `delete`                                           |
| `watchFields`   | []string      | No       | --                  | Dot-notation paths (e.g. `.status.phase`). Only fire update events when these fields change. |
| `cooldown`      | duration       | No       | --                  | Minimum duration between FlowRun creations for the same resource and event type. Suppresses rapid-fire events on high-churn resources. |

**RBAC note:** The controller's ServiceAccount must have `get`, `list`, and `watch` permissions on the target resource type. KubeZap does not grant these automatically — the cluster administrator must create the appropriate Role or ClusterRole and bind it to the controller ServiceAccount.

The controller already has discovery API access (non-resource URLs `/api`, `/api/*`, `/apis`, `/apis/*`) needed to resolve plural resource names. What you must add is a `get;list;watch` rule for the specific resource type.

Example Role for watching Pods:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubezap-watch-pods
  namespace: <trigger-namespace>
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubezap-watch-pods
  namespace: <trigger-namespace>
subjects:
  - kind: ServiceAccount
    name: kubezap-controller-manager
    namespace: kubezap-system
roleRef:
  kind: Role
  name: kubezap-watch-pods
  apiGroup: rbac.authorization.k8s.io
```

For cross-namespace watches (when `spec.resource.namespace` differs from the Trigger's namespace), use a ClusterRole and ClusterRoleBinding scoped to the target namespace via a RoleBinding in that namespace, or create a ClusterRoleBinding if the resource is cluster-scoped.

---

## Status Reference

### TriggerStatus

| Field                    | Type        | Description                                                |
| ------------------------ | ----------- | ---------------------------------------------------------- |
| `conditions`             | []Condition | Standard Kubernetes conditions. See condition types below. |
| `lastTriggeredTime`      | timestamp   | Timestamp of the last successful trigger firing            |
| `lastResult`             | string      | Outcome of the last trigger attempt                        |
| `lastError`              | string      | Error message from the last failed attempt                 |
| `currentInvocationCount` | integer     | Number of invocations in the current cooldown window       |

### `lastResult` Values

| Value         | Meaning                                                       |
| ------------- | ------------------------------------------------------------- |
| `Succeeded`   | The trigger fired and the flow completed successfully         |
| `Failed`      | The trigger fired but the flow or inline action failed        |
| `Skipped`     | The trigger fired but was skipped (e.g., trigger is disabled) |
| `RateLimited` | The trigger fired but was suppressed by the cooldown policy   |

### Condition Types

| Type    | Status    | Meaning                                                               |
| ------- | --------- | --------------------------------------------------------------------- |
| `Ready` | `True`    | The trigger is configured correctly and actively listening for events |
| `Ready` | `False`   | The trigger has a configuration error. See `message` for details.     |
| `Ready` | `Unknown` | The trigger is being reconciled                                       |

---

## Trigger Types

### Webhook

The operator exposes a dedicated HTTP endpoint for each webhook trigger at the configured `path`. Incoming requests on that path fire the trigger.

The request body is parsed according to the `Content-Type` header and made available as `$(trigger.body)` (raw) or `$(trigger.body.<field>)` (JSON dot-path, or a flat top-level field for `application/x-www-form-urlencoded`). HTTP headers are available as `$(trigger.headers.<name>)` (plural, case-insensitive). See [Payload Formats](../overview.md#payload-formats) for supported content types and [Flow CRD → Parameter Interpolation](flow.md#parameter-interpolation) for the full `$(...)` reference.

**Exposing the endpoint**: In-cluster services can call the operator's webhook Service directly. For external access, see [Exposing Webhook Triggers](#exposing-webhook-triggers) below.

### Cron

Cron triggers fire on the schedule defined by a standard cron expression. The operator runs a scheduler and fires the trigger at each scheduled time.

The Flow receives the scheduled fire time via `$(trigger.scheduledTime)` (RFC3339). There is no separate "actual fire time" — the scheduler fires at the scheduled time and that is the only timestamp recorded.

### Kafka, AMQP, NATS (Broker Triggers)

The operator creates a gateway consumer for each broker trigger (`kafka`, `amqp`, or `nats`). Each message consumed from the topic fires the trigger once. Three broker types are supported:

- **`kafka`** — built-in Kafka gateway (`kubezap-kafka-gateway`), one Deployment per Kafka cluster per namespace
- **`amqp`** — built-in AMQP gateway (`kubezap-amqp-gateway`), supports AMQP 0-9-1 (RabbitMQ) and AMQP 1.0 (ActiveMQ Artemis)
- **`nats`** — built-in NATS gateway (`kubezap-nats-gateway`), supports NATS Core and JetStream

The Flow receives the message contents via the same `$(trigger.*)` placeholders as any other trigger type:
- `$(trigger.body)` / `$(trigger.body.<field>)` — the message value (JSON-decoded via dot-path if valid JSON, otherwise the raw string via `$(trigger.body)`)
- `$(trigger.topic)` — the topic/queue name
- `$(trigger.partition)` — the partition number (Kafka only; empty for AMQP/NATS)
- `$(trigger.offset)` — the message offset (Kafka only; empty for AMQP/NATS)

> **Not accessible from step interpolation**: the Kafka message key and any broker-specific message headers (Kafka record headers, AMQP/NATS message headers) are not exposed via `$(...)` syntax today — `$(trigger.headers.<name>)` only resolves HTTP headers from webhook-sourced triggers. If a Flow needs the message key or broker headers, there is currently no supported way to read them.

### Kubernetes Resource Events

> **Alpha feature — not recommended for production.** `type: resource` is alpha-stability and may change without a deprecation period. See [docs/overview.md](../overview.md) and the [ResourceTrigger spec reference](#resourcetrigger) for known limitations (naive pluralization fallback, cooldown interaction with high-churn resources).

Watch any Kubernetes resource type and fire the trigger when resources matching a label selector are created, updated, or deleted. Unlike webhook and Kafka triggers, resource event triggers run inside the controller -- no separate gateway pod is needed.

The controller sets up a dynamic informer for each `type: resource` Trigger. When a matching event occurs, a FlowRun is created with the full resource object in the trigger payload.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: pod-ready-handler
  namespace: automation
spec:
  type: resource
  resource:
    apiVersion: v1
    kind: Pod
    namespace: production
    labelSelector:
      matchLabels:
        app: my-service
    events: [create, update]
    watchFields:
      - ".status.phase"
  flowRef:
    name: handle-pod-ready
```

The full resource object JSON is available via `$(trigger.body)` / `$(trigger.body.<field>)`, the same as any other trigger type.

**Not accessible from step interpolation**: the event metadata (`eventType`, `resourceName`, `resourceNamespace`, `resourceKind`, `resourceAPIVersion`) is recorded on the FlowRun's `spec.triggerData` (see [FlowRun CRD → TriggerData](flowrun.md#triggerdata)), but there is no `$(trigger.eventType)`/`$(trigger.resourceName)`/etc. interpolation syntax exposing it to step fields or `when:` CEL conditions today. To branch on `eventType` today, you'd need to derive it from the resource object body itself if the information happens to be present there — the trigger-level metadata is not otherwise reachable.

FlowRun naming: `<trigger>-<resource-name>-<eventtype>-<timestamp>`

See [Architecture - Kubernetes Resource Event Triggers](../architecture.md#kubernetes-resource-event-triggers) for design context.

---

## Exposing Webhook Triggers

The KubeZap operator exposes all webhook trigger endpoints on a single internal `Service`. In-cluster callers can use the service DNS name directly. For external traffic, use one of the following:

### Kubernetes Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
  rules:
    - host: webhooks.example.com
      http:
        paths:
          - path: /hooks/
            pathType: Prefix
            backend:
              service:
                name: kubezap-webhook-service
                port:
                  number: 8080
  tls:
    - hosts: [webhooks.example.com]
      secretName: kubezap-webhook-tls
```

### Kubernetes Gateway API (recommended for Kubernetes 1.28+)

The [Gateway API](https://gateway-api.sigs.k8s.io/) is the successor to Ingress and is generally available as of Kubernetes 1.28. It provides more expressive routing rules, better multi-tenancy, and is the recommended approach for new deployments.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
  parentRefs:
    - name: my-gateway
      namespace: gateway-system
  hostnames: ["webhooks.example.com"]
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /hooks/
      backendRefs:
        - name: kubezap-webhook-service
          port: 8080
```

For TLS termination at the gateway, configure it on the `Gateway` resource rather than the `HTTPRoute`. Refer to your gateway controller's documentation (e.g., Envoy Gateway, Istio, NGINX Gateway Fabric).

### OpenShift Route

```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
  host: webhooks.apps.cluster.example.com
  path: /hooks/
  to:
    kind: Service
    name: kubezap-webhook-service
  port:
    targetPort: 8080
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
```

**TLS termination options for OpenShift Routes:**

| `termination` | Description                                                                                              |
| ------------- | -------------------------------------------------------------------------------------------------------- |
| `edge`        | TLS terminates at the router. Traffic to the pod is plain HTTP.                                          |
| `reencrypt`   | TLS terminates at the router and is re-encrypted to the pod using a separate certificate.                |
| `passthrough` | TLS passes through the router to the pod. The operator handles TLS directly (required for inbound mTLS). |

---

## TLS and mTLS

TLS covers two independent paths: **outbound** connections KubeZap makes to other systems, and **inbound** TLS for calls arriving at the webhook gateway. Outbound TLS is configured on the relevant `Integration`, and inbound TLS is configured on the namespace's `WebhookGatewayConfig`.

### Outbound TLS

- **Broker connections (Kafka, AMQP, NATS)**: a custom CA and/or client certificate for mTLS are configured on the `Integration` resource, via `spec.kafka.tls`, `spec.amqp.tls`, or `spec.nats.tls` — each accepts `caSecretRef` (a Secret containing `ca.crt`) and `clientCertSecretRef` (a Secret containing `tls.crt` + `tls.key`). See the [Integration CRD reference](integration.md) (`KafkaTLSSpec`, `AmqpTLSConfig`, `NatsTLSConfig`) for full field details and worked examples.
- **HTTP steps**: a `type: http` `Integration` can configure `spec.http.tls.caBundleConfigMapRef` (a `ConfigMap` holding a PEM CA bundle — one or more concatenated certificates, added to the system root pool rather than replacing it) and/or `spec.http.tls.clientCertSecretRef` (a `Secret` with `tls.crt`/`tls.key`, for mTLS). The CA bundle is `ConfigMap`-sourced rather than `Secret`-sourced, unlike the broker types above — it's public data, not a credential. See the [Integration CRD reference](integration.md#httptlsspec) (`HttpTLSSpec`) for full field details and a worked example. Separately, a boolean `tlsSkipVerify` exists on the internal controller-to-executor `/execute` request, honored only when the `http-executor` is started with the dev-only `--allow-tls-skip-verify` flag (off by default) — that flag is not exposed through any `Trigger`/`Flow` field and is unrelated to the CA-bundle mechanism above. See `docs/dev/http-executor.md` for the internal RPC contract.

### Inbound Webhook mTLS

Require callers to present a valid client certificate when calling this trigger's webhook endpoint. This is configured per-namespace, not per-Trigger, via a `WebhookGatewayConfig` object (the webhook gateway is one shared Deployment per namespace):

```yaml
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

The operator will verify that the client certificate is signed by the CA in the specified Secret. Requests without a valid client certificate are rejected with HTTP 401. Note: inbound mTLS requires TLS passthrough at the Ingress/Route layer. See `docs/guides/webhook-security.md` for the full reference.

---

## Examples

### Example 1: Webhook Trigger

Listen for POST requests on `/hooks/deploys` and execute a flow.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: deploy-webhook
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/deploys
    method: POST
  flowRef:
    name: handle-deploy
```

### Example 2: Cron Trigger

Run a cleanup flow every day at midnight.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: nightly-cleanup
  namespace: automation
spec:
  type: cron
  cron:
    schedule: "0 0 * * *"
  flowRef:
    name: cleanup-old-records
```

### Example 3: Kafka Trigger

Consume messages from a Kafka topic and process each one.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events
  namespace: automation
spec:
  type: kafka
  kafka:
    integrationRef:
      name: prod-kafka-cluster
    topic: orders.created
    consumerGroup: kubezap-order-processor
  flowRef:
    name: process-order
```

### Example 4: With Cooldown Policy

Allow a maximum of 5 firings per minute to protect a downstream service.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: rate-limited-webhook
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/events
    method: POST
  flowRef:
    name: process-event
  cooldown:
    maxInvocations: 5
    window: "60s"
```

### Example 5: Inline Action (No Flow)

For simple forwarding, use an inline `action` instead of a Flow.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: forward-to-slack
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/alerts
  action:
    type: webhook
    webhook:
      url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
      method: POST
      headers:
        Content-Type: "application/json"
      body: '{"text": "Alert received"}'
```

### Example 6: Resource Trigger (Pod Failure Watcher)

Watch for Pod status changes and fire a flow when a Pod's phase changes.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: pod-failure-watcher
  namespace: default
spec:
  type: resource
  resource:
    apiVersion: v1
    kind: Pod
    namespace: default
    labelSelector:
      matchLabels:
        app: my-service
    events: [create, update]
    watchFields:
      - ".status.phase"
  flowRef:
    name: handle-pod-event
```

---

## Rate Limiting

The `cooldown` policy limits how frequently a trigger can fire within a rolling time window. This protects downstream systems from event bursts.

When a trigger is rate-limited:
- The event is received and acknowledged (no data loss for webhook/Kafka sources)
- The flow is **not** executed
- `lastResult` is set to `RateLimited`
- `currentInvocationCount` is incremented

The `currentInvocationCount` resets at the start of each new window. The window is rolling, not fixed-interval.

---

## Status Conditions

Check trigger health with `kubectl get trigger <name> -o wide` or inspect `status.conditions`:

```bash
kubectl describe trigger deploy-webhook -n automation
```

Example output:
```
Status:
  Conditions:
    Last Transition Time:  2026-03-01T10:00:00Z
    Message:               Trigger is active and listening on /hooks/deploys
    Reason:                TriggerReady
    Status:                True
    Type:                  Ready
  Last Result:             Succeeded
  Last Triggered Time:     2026-03-01T14:32:11Z
  Current Invocation Count: 2
```

---

## Limitations

- **Webhook authentication**: Configure `spec.webhook.auth` to require authentication. Without it, any caller that can reach the endpoint can fire the trigger. See [Securing Webhook Triggers](../guides/webhook-security.md).
- **Webhook delivery guarantee**: Webhook triggers do not acknowledge or retry the source request. If the operator is unavailable when a request arrives, the event is lost.
- **Kafka exactly-once**: Kafka triggers provide at-least-once delivery semantics. Design flows to be idempotent.
- **Single topic per trigger**: Each broker trigger (`kafka`, `amqp`, `nats`) subscribes to one topic. Create multiple triggers to consume from multiple topics.
- **Cron timezone**: Schedules default to UTC. Set `spec.cron.timezone` to an IANA timezone name (e.g. `America/New_York`) to use a different timezone.
