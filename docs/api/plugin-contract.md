# KubeZap Plugin Contract

**Contract version: v1alpha1**

This document defines the interface between the KubeZap operator and a plugin container. A plugin is any container image deployed via `Integration type: plugin`. Both community plugins and vendor-supplied images must implement this contract to interoperate with KubeZap.

The contract is versioned. Breaking changes will increment the version and be announced with a deprecation window. The current version is **v1alpha1** (pre-stable; breaking changes may occur before v1).

---

## Contents

- [Overview](#overview)
- [Subscriber Contract](#subscriber-contract)
- [Publisher Contract](#publisher-contract)
- [Health Check](#health-check)
- [Environment](#environment)
- [FlowRun Schema](#flowrun-schema)
- [Dedup Key Requirements](#dedup-key-requirements)
- [Observability](#observability)
- [Security Considerations](#security-considerations)
- [Graduation Path](#graduation-path)
- [Reference Implementation](#reference-implementation)

---

## Overview

A plugin serves up to two roles:

- **Subscriber**: watches Trigger CRDs, connects to an external system, and creates `FlowRun` resources when events arrive
- **Publisher**: exposes an HTTP endpoint that the controller calls when a Flow step executes a `type: publish` action

A plugin may implement one or both roles. A subscriber-only plugin (e.g., an inbound webhook relay) does not need to expose a publisher port. A publisher-only plugin (e.g., a notification gateway) does not need to watch Triggers.

```
┌─────────────────────────────────────────────────────────────────┐
│ Plugin Pod                                                       │
│                                                                  │
│  Subscriber goroutine          Publisher HTTP server             │
│  ─────────────────────         ──────────────────────           │
│  watches Trigger CRDs          POST /publish                     │
│  connects to ext system        called by controller              │
│  creates FlowRun CRDs          returns messageId or error        │
└─────────────────────────────────────────────────────────────────┘
        │  k8s API                       ▲
        ▼                                │
  FlowRun created             controller routes publish call
        │                                │
        ▼                          Flow step executes
  controller picks up
  executes Flow
```

---

## Subscriber Contract

### Trigger selection

The plugin must watch `Trigger` resources in its namespace (`KUBEZAP_NAMESPACE`) and process only those matching:

```
spec.type == "pubsub"
  AND spec.pubsub.type == <your-integration-type>  // e.g. "rabbitmq", "tibco"
  AND spec.pubsub.integrationRef.name == KUBEZAP_INTEGRATION_NAME
  AND spec.enabled == true  (or spec.enabled is absent)
```

The plugin must **not** process Triggers in other namespaces.

### FlowRun creation

For each received event, the plugin creates a `FlowRun` in the same namespace:

```go
FlowRun{
    ObjectMeta: metav1.ObjectMeta{
        Name:      "<trigger-name>-<dedup-key>",    // see Dedup Key Requirements
        Namespace: trigger.Namespace,
        Labels: map[string]string{
            "kubezap.io/trigger":      trigger.Name,
            "kubezap.io/trigger-type": "pubsub",
            "kubezap.io/flow":         trigger.Spec.FlowRef.Name,
        },
        Annotations: map[string]string{
            "kubezap.io/traceparent": "<W3C traceparent>",  // if available; see Observability
        },
    },
    Spec: FlowRunSpec{
        FlowRef:    trigger.Spec.FlowRef,
        TriggerRef: LocalObjectReference{Name: trigger.Name},
        Params: []ParamValue{
            // extract from message body using trigger.spec.pubsub.payloadMappings if set
        },
        TriggerData: TriggerData{
            Source: "pubsub",
            Body:   messageBody,  // raw message payload as string
            Headers: map[string]string{
                // message headers/properties as key-value pairs
            },
        },
    },
}
```

### Handling duplicates

The Kubernetes API will reject a FlowRun creation with `409 Conflict` if the name already exists. The plugin **must** treat `409 Conflict` as a success and not retry. This is the primary deduplication mechanism.

### Offset/cursor commit

The plugin must commit the message offset or cursor to the external system **only after** the FlowRun is confirmed created (200 Created or 409 Conflict). Committing before creation risks message loss if the plugin crashes between commit and FlowRun creation.

### Dynamic subscription management

When a Trigger is added, modified, or deleted, the plugin must update its subscriptions accordingly without restarting:

- **Added / enabled**: establish subscription
- **Disabled (`spec.enabled: false`)**: cancel subscription, do not process further messages
- **Deleted**: cancel subscription
- **Modified** (topic/consumerGroup changed): cancel old subscription, establish new one

---

## Publisher Contract

The plugin must expose an HTTP server on `KUBEZAP_PUBLISHER_PORT` (default: `8090`).

### `POST /publish`

**Request:**

```
POST /publish
Content-Type: application/json
```

```json
{
    "integration": "<integration-name>",
    "namespace":   "<namespace>",
    "destination": "<topic, queue, or exchange name>",
    "headers":     { "<key>": "<value>" },
    "body":        "<message body string>"
}
```

**Response on success:**

```
HTTP 200
Content-Type: application/json
```

```json
{
    "messageId": "<broker-assigned message ID, or empty string>"
}
```

**Response on failure:**

```
HTTP 4xx or 5xx
Content-Type: application/json
```

```json
{
    "error": "<human-readable error message>"
}
```

The controller applies the step's `retryPolicy` on any non-200 response. The plugin should return `5xx` for transient broker failures (network, timeout) and `4xx` for permanent configuration errors (invalid destination, auth failure).

### Idempotency

The controller may retry a publish call if the step is re-executed (e.g., after FlowRun failover). If the external system supports idempotent publishing via a caller-supplied message ID, the plugin should accept an optional `idempotencyKey` field in the request body and forward it to the broker.

---

## Health Check

The plugin must implement:

```
GET /healthz  →  HTTP 200  body: "ok"
```

The operator uses this as the Deployment readiness probe. The plugin should return `200` only when it has successfully connected to the external system and is ready to receive publish calls. Return `5xx` if the broker connection is unavailable; the pod will be removed from the ready set and the operator will not route publish calls to it.

---

## Environment

The operator injects these environment variables into the plugin container:

| Variable | Description |
|---|---|
| `KUBEZAP_NAMESPACE` | The namespace this plugin instance serves |
| `KUBEZAP_INTEGRATION_NAME` | Name of the `Integration` CRD |
| `KUBEZAP_PUBLISHER_PORT` | Port to listen on for publisher calls (default: `8090`) |
| `KUBEZAP_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` |

Secrets referenced in `spec.plugin.secretRefs` are injected as the environment variable names you define in `envVarMappings`. Non-sensitive config values in `spec.plugin.config` are also injected directly as environment variables.

### In-cluster Kubernetes access

The operator creates a `ServiceAccount`, `Role`, and `RoleBinding` in the plugin's namespace. The Role grants:

```yaml
rules:
  - apiGroups: ["automation.kubezap.io"]
    resources: ["triggers"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["automation.kubezap.io"]
    resources: ["flowruns"]
    verbs: ["get", "list", "create", "update", "patch"]
```

The plugin must use in-cluster config (`rest.InClusterConfig()`) to access the Kubernetes API. No additional RBAC grants are needed for the subscriber role.

---

## FlowRun Schema

Reference the full FlowRun spec in [docs/api/flowrun.md](./flowrun.md). Key fields for plugin authors:

| Field | Type | Description |
|---|---|---|
| `spec.flowRef.name` | string | Name of the Flow to execute. Copy from `trigger.spec.flowRef.name`. |
| `spec.triggerRef.name` | string | Name of the Trigger that caused this FlowRun. |
| `spec.params` | []ParamValue | Input parameters extracted from the message payload. |
| `spec.triggerData.source` | string | Set to `"pubsub"` for all plugin-created FlowRuns. |
| `spec.triggerData.body` | string | Raw message body. |
| `spec.triggerData.headers` | map[string]string | Message headers or properties. |
| `spec.ttlAfterFinished` | Duration | Optional. Override the operator-level TTL for this FlowRun. |

---

## Dedup Key Requirements

The FlowRun name is the dedup key. Requirements:

- Must be **unique per message** within the trigger's namespace
- Must be **deterministic** — the same message must always produce the same FlowRun name
- Must be a valid Kubernetes name: lowercase alphanumeric and `-`, max 253 characters

Recommended naming patterns:

| System type | Recommended key | Example |
|---|---|---|
| Offset-based (Kafka-style) | partition + offset | `my-trigger-p3-offset-1042` |
| Message ID | message ID | `my-trigger-msg-abc123def` |
| Content-addressed | SHA-256 prefix of body | `my-trigger-sha-a3f9b2` |
| No natural key available | timestamp + random | `my-trigger-1741046400-xk9z` (last resort) |

Timestamp + random is acceptable but provides no dedup guarantee on retry. Prefer deterministic keys wherever the external system provides them.

---

## Observability

### Logging

The plugin must emit structured JSON logs to stdout. Required fields on every log line:

```json
{
    "level":       "info",
    "time":        "2026-03-15T10:00:00Z",
    "msg":         "flowrun created",
    "integration": "my-integration",
    "trigger":     "order-events",
    "flowrun":     "order-events-p0-offset-1042",
    "namespace":   "automation"
}
```

Use `KUBEZAP_LOG_LEVEL` to control verbosity.

### Trace context propagation

If the external message carries a W3C `traceparent` header (or equivalent), the plugin must propagate it to the FlowRun annotation `kubezap.io/traceparent`. The controller reads this annotation to continue the distributed trace across the broker boundary.

```go
annotations["kubezap.io/traceparent"] = msg.Headers["traceparent"]
```

If no traceparent is present in the message, omit the annotation. Do not generate a new traceparent in the plugin — the controller creates the FlowRun root span.

---

## Security Considerations

**Plugin images run inside your cluster with Kubernetes API access.** Before deploying a community plugin image, consider:

- The plugin can read all `Trigger` and `FlowRun` resources in its namespace
- The plugin can create `FlowRun` resources, which trigger Flow execution
- The plugin has network access to all services in the cluster unless restricted by NetworkPolicy
- KubeZap **does not verify plugin images** — use images only from sources you trust

Mitigations:
- Use NetworkPolicy to restrict the plugin pod to only the broker it needs to reach
- Pin plugin images to specific digest (`image@sha256:...`) rather than mutable tags
- Run periodic vulnerability scans on plugin images (Trivy, Grype)
- For high-security environments, build plugin images from source in your own CI pipeline

---

## Graduation Path

A community plugin may graduate to a first-party built-in Integration type. See [integration.md — Community Plugin Graduation](./integration.md#community-plugin-graduation) for criteria and process. The plugin contract is the primary artifact for evaluating graduation readiness: a plugin that fully implements this document and passes the KubeZap integration test suite is a strong graduation candidate.

---

## Reference Implementation

A minimal Go reference implementation is forthcoming at `docs/plugins/example-plugin/`. Until then, the Kafka gateway source at `cmd/kafka-gateway/` and `internal/gateway/kafka/` demonstrates the subscriber pattern against the Kubernetes API, and the `internal/controller/flowrun_controller.go` publish path demonstrates the publisher call sequence.
