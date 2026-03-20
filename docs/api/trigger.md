# Trigger CRD

A `Trigger` defines an event source that starts a `Flow`. It listens for an event — an incoming HTTP request, a Kafka message, or a scheduled time — and invokes the referenced Flow when that event occurs.

---

## Contents

- [Overview](#overview)
- [Spec Reference](#spec-reference)
- [Status Reference](#status-reference)
- [Trigger Types](#trigger-types)
  - [Kubernetes Resource Events (planned)](#kubernetes-resource-events-planned)
- [Exposing Webhook Triggers](#exposing-webhook-triggers)
- [TLS and mTLS Annotations](#tls-and-mtls-annotations)
- [Examples](#examples)
  - [Webhook Trigger](#example-1-webhook-trigger)
  - [Cron Trigger](#example-2-cron-trigger)
  - [Kafka Trigger](#example-3-kafka-trigger)
  - [With Cooldown Policy](#example-4-with-cooldown-policy)
  - [Inline Action (No Flow)](#example-5-inline-action-no-flow)
- [Rate Limiting](#rate-limiting)
- [Status Conditions](#status-conditions)
- [Limitations](#limitations)

---

## Overview

Each `Trigger` has a `type` (webhook, cron, or pubsub) and a `flowRef` pointing to the `Flow` to execute. When the event fires, the operator resolves the Flow, populates its parameters from the event payload, and executes the steps.

A `Trigger` can also define an inline `action` instead of a `flowRef` for simple one-step use cases such as forwarding a webhook to another URL.

---

## Spec Reference

### TriggerSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | enum | **Yes** | — | Trigger source type: `webhook`, `cron`, or `pubsub` |
| `enabled` | boolean | No | `true` | Whether this trigger is active. Set to `false` to pause without deleting. |
| `webhook` | WebhookTrigger | Conditional | — | Required when `type: webhook` |
| `cron` | CronTrigger | Conditional | — | Required when `type: cron` |
| `pubsub` | PubSubTrigger | Conditional | — | Required when `type: pubsub` |
| `flowRef` | FlowReference | Conditional | — | Reference to the Flow to execute. Required unless `action` is set. |
| `action` | ActionDefinition | Conditional | — | Inline action. Used instead of `flowRef` for simple single-step responses. |
| `cooldown` | CooldownPolicy | No | — | Rate limiting policy to prevent trigger storms |
| `target` | TargetResource | No | — | Cluster resource to watch (reserved for future resource-based triggers) |
| `event` | string | No | — | Resource event type for resource-based triggers: `create`, `update`, or `delete` |

### WebhookTrigger

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | **Yes** | — | HTTP path exposed by the operator (e.g., `/hooks/my-trigger`) |
| `method` | enum | No | `POST` | Accepted HTTP method: `POST` or `PUT` |
| `auth` | WebhookAuth | No | — | Authentication policy for this endpoint. See [Securing Webhook Triggers](../guides/webhook-security.md). |

The full URL of the webhook endpoint is: `http://<operator-service>:<port><path>`

For authentication configuration examples and security guidance see [Securing Webhook Triggers](../guides/webhook-security.md).

### WebhookAuth

Configures authentication for a webhook trigger endpoint. If omitted, the endpoint accepts requests from any caller — always set `auth` in production.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | enum | **Yes** | — | Authentication method: `hmac`, `bearer`, `oidc`, `basic`, `apiKey`, or `ipAllowlist` |
| `hmacSecretRef` | SecretKeySelector | Conditional | — | Reference to the Secret key containing the HMAC shared secret. Required when `type: hmac`. |
| `bearerTokenSecretRef` | SecretKeySelector | Conditional | — | Reference to the Secret key containing the expected bearer token. Required when `type: bearer`. |
| `oidcIssuer` | string | Conditional | — | OIDC/JWT issuer URL (e.g., `https://accounts.google.com`). Required when `type: oidc`. |
| `oidcAudience` | string | No | — | Expected `aud` claim value. When omitted, audience validation is skipped. Used when `type: oidc`. |
| `basic` | WebhookBasicAuth | Conditional | — | Basic auth configuration. Required when `type: basic`. |
| `apiKeySecretRef` | SecretKeySelector | Conditional | — | Reference to the Secret key containing the expected API key value. Required when `type: apiKey`. |
| `apiKeyHeader` | string | No | `X-Api-Key` | Header name to check for the API key. Used when `type: apiKey`. |
| `ipAllowlist` | []string | Conditional | — | List of CIDR blocks allowed to call this endpoint (e.g., `["10.0.0.0/8", "192.168.1.0/24"]`). Required when `type: ipAllowlist`. |

> **Note — mTLS**: Client-certificate authentication is not configured via `WebhookAuth`. It is enforced at the TLS termination layer using the `kubezap.io/webhook-mtls-ca-secret` annotation. See [TLS and mTLS Annotations](#tls-and-mtls-annotations) for details.

### WebhookBasicAuth

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `secretRef` | LocalObjectReference | **Yes** | — | Name of the Secret containing the username and password. |
| `usernameKey` | string | No | `username` | Key within the Secret that holds the username. |
| `passwordKey` | string | No | `password` | Key within the Secret that holds the password. |

### CronTrigger

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `schedule` | string | **Yes** | — | Cron expression in standard five-field format: `minute hour day-of-month month day-of-week`. Also supports `robfig/cron` extended syntax: `@daily`, `@hourly`, `@every 5m`, etc. |
| `timezone` | string | No | `UTC` | IANA timezone name for the schedule (e.g. `America/New_York`, `Europe/Berlin`). Defaults to UTC when omitted. |

**Example schedules:**

| Schedule | Meaning |
|---|---|
| `0 * * * *` | Every hour at minute 0 |
| `*/15 * * * *` | Every 15 minutes |
| `0 2 * * *` | Daily at 2:00 AM |
| `0 9 * * 1` | Every Monday at 9:00 AM |

### PubSubTrigger

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | enum | **Yes** | — | Message broker type: `kafka`, `amqp`, or `nats` |
| `integrationRef` | IntegrationReference | **Yes** | — | Reference to an `Integration` CR with broker connection details |
| `topic` | string | **Yes** | — | Topic name to consume from (Kafka and AMQP exchange name) |
| `consumerGroup` | string | No | `kubezap-<trigger-name>` | Kafka consumer group ID. Ignored for AMQP and NATS. |
| `routingKey` | string | No | — | AMQP routing key or binding pattern. Used when `type: amqp` only. |
| `subject` | string | No | — | NATS subject to subscribe to. Supports wildcards (e.g. `orders.*`, `events.>`). Used when `type: nats` only. |

### FlowReference

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | **Yes** | — | Name of the Flow CR to execute |
| `namespace` | string | No | Trigger's namespace | Namespace of the Flow CR |

See [Flow CRD](flow.md) for the full specification of what a Flow contains and how it processes the trigger payload.

### ActionDefinition

An inline action for simple use cases that do not require a full Flow. Currently supports `webhook` actions only.

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | enum | **Yes** | Action type: `webhook` |
| `webhook` | WebhookAction | Conditional | Required when `type: webhook` |

### WebhookAction

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `url` | string | **Yes** | — | URL to call when the trigger fires |
| `method` | enum | No | `POST` | HTTP method: `POST`, `PUT`, `PATCH`, `GET` |
| `headers` | map[string]string | No | — | HTTP headers to include |
| `body` | string | No | — | Request body (supports Go template syntax) |

### CooldownPolicy

Prevents a trigger from firing more than a set number of times in a given window. Excess firings are silently dropped and recorded as `RateLimited` in the status.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `maxInvocations` | integer | **Yes** | — | Maximum number of firings allowed within `window` |
| `window` | duration | No | `60s` | Time window for counting invocations (e.g., `60s`, `5m`) |

### TargetResource

Reserved for future use with resource-based triggers (watching Kubernetes resources).

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | No | Kind of the resource to watch (e.g., `Pod`, `ConfigMap`) |
| `namespace` | string | No | Namespace of the resource |
| `name` | string | No | Name of a specific resource |
| `labelSelector` | LabelSelector | No | Select resources by label |

---

## Status Reference

### TriggerStatus

| Field | Type | Description |
|---|---|---|
| `conditions` | []Condition | Standard Kubernetes conditions. See condition types below. |
| `lastTriggeredTime` | timestamp | Timestamp of the last successful trigger firing |
| `lastResult` | string | Outcome of the last trigger attempt |
| `lastError` | string | Error message from the last failed attempt |
| `currentInvocationCount` | integer | Number of invocations in the current cooldown window |

### `lastResult` Values

| Value | Meaning |
|---|---|
| `Succeeded` | The trigger fired and the flow completed successfully |
| `Failed` | The trigger fired but the flow or inline action failed |
| `Skipped` | The trigger fired but was skipped (e.g., trigger is disabled) |
| `RateLimited` | The trigger fired but was suppressed by the cooldown policy |

### Condition Types

| Type | Status | Meaning |
|---|---|---|
| `Ready` | `True` | The trigger is configured correctly and actively listening for events |
| `Ready` | `False` | The trigger has a configuration error. See `message` for details. |
| `Ready` | `Unknown` | The trigger is being reconciled |

---

## Trigger Types

### Webhook

The operator exposes a dedicated HTTP endpoint for each webhook trigger at the configured `path`. Incoming requests on that path fire the trigger.

The request body is parsed according to the `Content-Type` header and made available as `$(trigger.payload.<field>)`. HTTP headers are available as `$(trigger.header.<name>)`. See [Payload Formats](../overview.md#payload-formats) for supported content types.

**Exposing the endpoint**: In-cluster services can call the operator's webhook Service directly. For external access, see [Exposing Webhook Triggers](#exposing-webhook-triggers) below.

### Cron

Cron triggers fire on the schedule defined by a standard cron expression. The operator runs a scheduler and fires the trigger at each scheduled time.

The Flow receives timing metadata as parameters:
- `$(trigger.payload.scheduledTime)` — the scheduled fire time (RFC3339)
- `$(trigger.payload.actualTime)` — the actual fire time (RFC3339)

### Pub/Sub — Kafka, AMQP, NATS

The operator creates a gateway consumer for each `pubsub` trigger. Each message consumed from the topic fires the trigger once. Three broker types are supported:

- **`kafka`** — built-in Kafka gateway (`kubezap-kafka-gateway`), one Deployment per Kafka cluster per namespace
- **`amqp`** — built-in AMQP gateway (`kubezap-amqp-gateway`), supports AMQP 0-9-1 (RabbitMQ) and AMQP 1.0 (ActiveMQ Artemis)
- **`nats`** — built-in NATS gateway (`kubezap-nats-gateway`), supports NATS Core and JetStream

The Flow receives the message contents:
- `$(trigger.payload.value)` — the message value (JSON-decoded if valid JSON, otherwise raw string)
- `$(trigger.payload.key)` — the message key (Kafka)
- `$(trigger.payload.topic)` — the topic/queue name
- `$(trigger.payload.partition)` — the partition number (Kafka only)
- `$(trigger.payload.offset)` — the message offset (Kafka only)
- `$(trigger.payload.headers.<name>)` — a message header value

### Kubernetes Resource Events _(planned)_

Watch any Kubernetes resource type and fire the trigger when resources matching a label selector are created, updated, or deleted. Unlike webhook and Kafka triggers, resource event triggers run inside the controller — no separate gateway pod is needed.

See [Architecture → Kubernetes Resource Event Triggers](../architecture.md#kubernetes-resource-event-triggers) for the planned spec and payload structure.

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

| `termination` | Description |
|---|---|
| `edge` | TLS terminates at the router. Traffic to the pod is plain HTTP. |
| `reencrypt` | TLS terminates at the router and is re-encrypted to the pod using a separate certificate. |
| `passthrough` | TLS passes through the router to the pod. The operator handles TLS directly (required for inbound mTLS). |

---

## TLS and mTLS Annotations

TLS behavior is configured via annotations rather than spec fields, keeping the CRD schema focused on functional configuration.

### Custom Certificate Authority

When the operator needs to call services that use a private or internal CA (for outbound connections from inline actions or when accepting webhook calls from clients using an internal CA):

```yaml
metadata:
  annotations:
    kubezap.io/tls-ca-secret: "my-internal-ca"
```

The referenced Secret must exist in the same namespace as the Trigger and contain a `ca.crt` key with a PEM-encoded certificate bundle.

### Mutual TLS (mTLS) for Outbound Connections

Provide a client certificate for outbound connections requiring mTLS:

```yaml
metadata:
  annotations:
    kubezap.io/tls-ca-secret: "my-internal-ca"
    kubezap.io/tls-client-cert-secret: "my-client-cert"
```

The client certificate Secret must contain `tls.crt` and `tls.key` keys (standard Kubernetes TLS secret format). Use [cert-manager](https://cert-manager.io) to issue and rotate client certificates automatically.

### Inbound Webhook mTLS

Require callers to present a valid client certificate when calling this trigger's webhook endpoint:

```yaml
metadata:
  annotations:
    kubezap.io/webhook-mtls-ca-secret: "webhook-client-ca"
```

The operator will verify that the client certificate is signed by the CA in the specified Secret. Requests without a valid client certificate are rejected with HTTP 401. Note: inbound mTLS requires TLS passthrough at the Ingress/Route layer.

### Skip TLS Verification (development only)

```yaml
metadata:
  annotations:
    kubezap.io/tls-insecure-skip-verify: "true"
```

> Disables certificate validation for outbound connections. **Never use in production.**

### Annotation Reference

| Annotation | Value | Description |
|---|---|---|
| `kubezap.io/tls-ca-secret` | Secret name | PEM CA bundle (`ca.crt`) for outbound TLS verification |
| `kubezap.io/tls-client-cert-secret` | Secret name | Client certificate (`tls.crt`, `tls.key`) for outbound mTLS |
| `kubezap.io/webhook-mtls-ca-secret` | Secret name | CA to verify inbound webhook client certificates |
| `kubezap.io/tls-insecure-skip-verify` | `"true"` | Skip outbound TLS verification (dev only) |

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
  type: pubsub
  pubsub:
    type: kafka
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
- **Single topic per trigger**: Each `pubsub` trigger subscribes to one topic. Create multiple triggers to consume from multiple topics.
- **Cron timezone**: Schedules default to UTC. Set `spec.cron.timezone` to an IANA timezone name (e.g. `America/New_York`) to use a different timezone.
