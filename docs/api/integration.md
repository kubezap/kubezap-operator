# Integration CRD

An `Integration` stores the connection details and credentials for an external system — a Kafka cluster, a message broker, a plugin-managed service — separately from the `Triggers` and `Flows` that use it. This keeps sensitive configuration reusable and out of individual Flow specs, and lets you update connection details in one place.

---

## Contents

- [Overview](#overview)
- [Roles: Subscriber and Publisher](#roles-subscriber-and-publisher)
- [Built-in Integration Types](#built-in-integration-types)
- [Plugin Integration Type](#plugin-integration-type)
- [Plugin Protocol Specification](#plugin-protocol-specification)
- [Spec Reference](#spec-reference)
- [Status Reference](#status-reference)
- [Examples](#examples)
  - [Kafka Integration](#example-1-kafka-integration)
  - [Kafka with mTLS](#example-2-kafka-with-mtls)
  - [Kafka with SASL/SCRAM](#example-3-kafka-with-saslscram)
  - [Community Plugin Integration](#example-4-community-plugin-integration)
  - [Using an Integration in a Trigger](#example-5-using-an-integration-in-a-trigger)
  - [Using an Integration as a Publisher in a Flow](#example-6-using-an-integration-as-a-publisher-in-a-flow)
- [kubectl Reference](#kubectl-reference)
- [Limitations](#limitations)

---

## Overview

Without an `Integration`, all connection details live in the resources that use them. That works for simple cases, but breaks down when:

- Multiple Triggers subscribe to the same Kafka cluster with the same credentials
- Rotating credentials requires updating every Flow that references them
- You want to centrally manage which external systems are allowed in a namespace

`Integration` solves all three: define the connection once, reference it by name, rotate credentials in the `Integration` (or the backing Secret), and every consumer picks up the change on next reconcile.

```
  Trigger ──► integrationRef: kafka-cluster ──► Integration (kafka-cluster)
                                                    │  bootstrapServers, SASL creds
                                                    │  TLS config
                                                    ▼
  Flow step ──► publish: kafka-cluster ──────► Integration (kafka-cluster)
                                                    │  same connection, reused
                                                    ▼
                                                Kafka Cluster
```

---

## Roles: Subscriber and Publisher

An Integration serves two roles depending on how it is referenced:

### Subscriber role

When referenced by a `Trigger`, the Integration acts as an event source. KubeZap creates a gateway (`kubezap-kafka-gateway` for built-in Kafka, or a plugin pod for community integrations) that subscribes to the external system and creates `FlowRun` resources when events arrive.

```yaml
# Trigger referencing an Integration as a subscriber
spec:
  type: pubsub
  pubsub:
    type: kafka
    integrationRef:
      name: kafka-cluster
    topic: orders.created
    consumerGroup: kubezap-order-processor
```

### Publisher role

When referenced by a `Flow` step action, the Integration acts as a publish target. The controller calls the Integration to send a message or call an external system as part of Flow execution.

```yaml
# Flow step using an Integration as a publisher
steps:
  - name: publish-result
    action:
      type: publish
      publish:
        integrationRef:
          name: kafka-cluster
        topic: orders.processed
        body: '{"orderId": "$(params.orderId)", "status": "confirmed"}'
```

---

## Built-in Integration Types

### Kafka (`type: kafka`)

First-class built-in support. Uses the `kubezap/kafka-gateway` image. Supports:

- SASL/PLAIN, SASL/SCRAM-SHA-256, SASL/SCRAM-SHA-512
- TLS with custom CAs
- Mutual TLS (client certificate authentication)
- Configurable consumer group prefix
- Per-namespace gateway Deployment (one Deployment per Integration per namespace)

### RabbitMQ (`type: rabbitmq`) _(planned)_

Planned for a future release. Will use a dedicated `kubezap/rabbitmq-gateway` image with AMQP 0-9-1 support, exchange/queue configuration, and the same subscriber/publisher model as Kafka.

---

## Plugin Integration Type

For external systems not supported natively, `type: plugin` runs a community or custom container image that implements the [plugin protocol](#plugin-protocol-specification). The KubeZap controller manages the plugin container's lifecycle — you provide the image.

### How it works

```
  Kubernetes API
       ▲   │  FlowRun create (subscriber)
       │   ▼
  ┌──────────────┐
  │ Plugin Pod   │◄── HTTP: /publish (publisher)
  │              │
  │  - watches   │     called by controller
  │    Triggers  │     when Flow step executes
  │  - creates   │
  │    FlowRuns  │
  └──────────────┘
       │
       ▼
  External System
  (RabbitMQ, ActiveMQ, NATS, custom...)
```

The operator:
1. Creates a Deployment for the plugin pod using the image you specify
2. Mounts any referenced Secrets as environment variables or volume mounts
3. Grants the plugin's ServiceAccount RBAC permission to watch Trigger CRDs and create FlowRun CRDs in its namespace
4. Routes publisher calls from the controller to the plugin pod's HTTP endpoint

---

## Plugin Protocol Specification

A plugin image must implement both the subscriber and publisher contracts. You may implement only one if the integration is subscriber-only or publisher-only.

### Subscriber contract

The plugin container must:

1. Connect to the Kubernetes API (in-cluster `ServiceAccount` is provided)
2. Watch `Trigger` resources in its namespace for `spec.type: pubsub` triggers that reference this Integration
3. Subscribe to the external system based on the Trigger spec
4. For each received event, create a `FlowRun` in the same namespace:

```go
FlowRun{
    ObjectMeta: {
        Name:      "<trigger-name>-<dedup-key>",
        Namespace: trigger.Namespace,
        Labels: {
            "kubezap.io/trigger":      trigger.Name,
            "kubezap.io/trigger-type": "pubsub",
            "kubezap.io/flow":         trigger.Spec.FlowRef.Name,
        },
    },
    Spec: {
        FlowRef:     trigger.Spec.FlowRef,
        Params:      []ParamValue{ ... }, // extracted from message
        TriggerRef:  { Name: trigger.Name, Type: "pubsub" },
        TriggerData: { Source: "plugin", Body: messageBody, ... },
    },
}
```

The dedup key must be unique per message. For ordered systems, use the message offset. For systems without offsets, use a message ID or content hash. Kubernetes will reject duplicate names with `409 Conflict`; the plugin must treat this as a success.

### Publisher contract

The plugin container must expose an HTTP server on `spec.plugin.publisherPort` (default: `8090`) implementing:

```
POST /publish
Content-Type: application/json

{
    "integration": "<integration-name>",
    "namespace":   "<namespace>",
    "destination": "<topic or queue name>",
    "headers":     { "<key>": "<value>", ... },
    "body":        "<message body string>"
}
```

Response on success:

```
HTTP 200
Content-Type: application/json

{
    "messageId": "<optional: broker-assigned message ID>"
}
```

Response on failure (the controller will apply the step's `retryPolicy`):

```
HTTP 4xx or 5xx
Content-Type: application/json

{
    "error": "<human readable error message>"
}
```

### Plugin environment

The operator injects these environment variables into the plugin container:

| Variable | Description |
|---|---|
| `KUBEZAP_NAMESPACE` | The namespace this plugin instance serves |
| `KUBEZAP_INTEGRATION_NAME` | Name of the Integration CRD |
| `KUBEZAP_PUBLISHER_PORT` | Port to listen on for publisher calls |
| `KUBEZAP_LOG_LEVEL` | `debug`, `info`, `warn`, `error` |

Secrets referenced in `spec.plugin.secretRefs` are injected as environment variables using the key mapping you define.

### Plugin health check

The plugin must implement:

```
GET /healthz  →  HTTP 200 with body "ok"
```

The operator uses this for the Deployment readiness probe.

---

## Spec Reference

### IntegrationSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | string | **Yes** | — | Integration type: `kafka`, `rabbitmq` _(planned)_, `plugin` |
| `kafka` | KafkaIntegrationSpec | No | — | Kafka connection details. Required when `type: kafka`. |
| `plugin` | PluginIntegrationSpec | No | — | Plugin configuration. Required when `type: plugin`. |

### KafkaIntegrationSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `bootstrapServers` | []string | **Yes** | — | Kafka bootstrap broker addresses (e.g., `kafka.infra:9092`) |
| `tls` | KafkaTLSSpec | No | disabled | TLS configuration |
| `sasl` | KafkaSASLSpec | No | disabled | SASL authentication configuration |
| `consumerGroupPrefix` | string | No | `kubezap` | Prefix for consumer group names. Final group: `<prefix>-<triggerName>-<consumerGroup>` |
| `producerConfig` | map[string]string | No | — | Additional Kafka producer configuration key/value pairs (passed directly to the producer client) |
| `consumerConfig` | map[string]string | No | — | Additional Kafka consumer configuration key/value pairs |

### KafkaTLSSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | boolean | No | `false` | Enable TLS |
| `caSecretRef` | SecretKeyRef | No | — | Secret containing `ca.crt` for custom CA verification |
| `clientCertSecretRef` | LocalObjectReference | No | — | Secret containing `tls.crt` and `tls.key` for mTLS |
| `insecureSkipVerify` | boolean | No | `false` | Disable certificate verification. Development only. |

### KafkaSASLSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `mechanism` | string | **Yes** | — | `PLAIN`, `SCRAM-SHA-256`, or `SCRAM-SHA-512` |
| `username` | string | No | — | SASL username (plain text; use `usernameSecretRef` for sensitive values) |
| `usernameSecretRef` | SecretKeyRef | No | — | Reference to a Secret key containing the SASL username |
| `passwordSecretRef` | SecretKeyRef | **Yes** | — | Reference to a Secret key containing the SASL password |

### SecretKeyRef

| Field | Type | Description |
|---|---|---|
| `name` | string | Name of the Secret |
| `key` | string | Key within the Secret |

### PluginIntegrationSpec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `image` | string | **Yes** | — | Container image implementing the plugin protocol |
| `publisherPort` | integer | No | `8090` | Port the plugin listens on for publisher calls from the controller |
| `replicas` | integer | No | `1` | Number of plugin pod replicas. For subscriber plugins, ensure your deduplication key handles multiple consumers. |
| `resources` | ResourceRequirements | No | — | CPU/memory requests and limits for the plugin container |
| `config` | map[string]string | No | — | Non-sensitive configuration passed to the plugin as environment variables |
| `secretRefs` | []PluginSecretRef | No | — | Secrets mounted as environment variables in the plugin container |
| `imagePullSecrets` | []LocalObjectReference | No | — | Image pull secrets for private registries |

### PluginSecretRef

| Field | Type | Description |
|---|---|---|
| `secretName` | string | Name of the Kubernetes Secret |
| `envVarMappings` | map[string]string | Maps Secret keys to environment variable names: `{ "api-key": "PLUGIN_API_KEY" }` |

---

## Status Reference

### IntegrationStatus

| Field | Type | Description |
|---|---|---|
| `conditions` | []Condition | Standard `Ready` condition |
| `phase` | string | `Ready`, `Degraded`, `Failed` |
| `gatewayDeployments` | []GatewayDeploymentRef | Names of gateway Deployments managed for this Integration, per namespace |
| `connectedTriggers` | integer | Number of Triggers currently referencing this Integration |

### Conditions

| Type | Status | Meaning |
|---|---|---|
| `Ready` | `True` | Integration is configured and gateways are running |
| `Ready` | `False` | Configuration error or gateway pod failed to start. See `message`. |
| `GatewayAvailable` | `True` | At least one gateway replica is running and healthy |
| `GatewayAvailable` | `False` | No gateway replicas available |

### Printer Columns

```bash
kubectl get integrations -n automation
```

```
NAME            TYPE     PHASE   TRIGGERS   AGE
kafka-cluster   kafka    Ready   3          2d
rabbitmq        plugin   Ready   1          6h
```

---

## Examples

### Example 1: Kafka Integration

Minimal Kafka integration with no authentication (suitable for development clusters without ACLs):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: kafka-cluster
  namespace: automation
spec:
  type: kafka
  kafka:
    bootstrapServers:
      - kafka.infra.svc.cluster.local:9092
```

---

### Example 2: Kafka with mTLS

Production Kafka cluster requiring mutual TLS authentication:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: kafka-prod
  namespace: automation
spec:
  type: kafka
  kafka:
    bootstrapServers:
      - kafka-broker-0.kafka.infra:9093
      - kafka-broker-1.kafka.infra:9093
      - kafka-broker-2.kafka.infra:9093
    tls:
      enabled: true
      caSecretRef:
        name: kafka-ca
        key: ca.crt
      clientCertSecretRef:
        name: kubezap-kafka-client-cert   # managed by cert-manager
```

The `kubezap-kafka-client-cert` Secret must contain `tls.crt` and `tls.key` in standard Kubernetes TLS Secret format. Use [cert-manager](https://cert-manager.io) to issue and rotate the client certificate.

---

### Example 3: Kafka with SASL/SCRAM

Kafka with SASL/SCRAM-SHA-512 authentication and TLS:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: kafka-msk
  namespace: automation
spec:
  type: kafka
  kafka:
    bootstrapServers:
      - b-1.msk-cluster.abc123.kafka.us-east-1.amazonaws.com:9096
      - b-2.msk-cluster.abc123.kafka.us-east-1.amazonaws.com:9096
    tls:
      enabled: true   # MSK uses public CA; no caSecretRef needed
    sasl:
      mechanism: SCRAM-SHA-512
      usernameSecretRef:
        name: kafka-msk-credentials
        key: username
      passwordSecretRef:
        name: kafka-msk-credentials
        key: password
```

The referenced Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kafka-msk-credentials
  namespace: automation
type: Opaque
stringData:
  username: kubezap-service-account
  password: <your-scram-password>
```

---

### Example 4: Community Plugin Integration

A community-maintained RabbitMQ plugin that implements the KubeZap plugin protocol:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: rabbitmq-prod
  namespace: automation
spec:
  type: plugin
  plugin:
    image: ghcr.io/kubezap-community/rabbitmq-plugin:v0.3.0
    publisherPort: 8090
    replicas: 2
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        cpu: 200m
        memory: 128Mi
    config:
      RABBITMQ_HOST: rabbitmq.infra.svc.cluster.local
      RABBITMQ_PORT: "5672"
      RABBITMQ_VHOST: "/production"
    secretRefs:
      - secretName: rabbitmq-credentials
        envVarMappings:
          username: RABBITMQ_USERNAME
          password: RABBITMQ_PASSWORD
```

---

### Example 5: Using an Integration in a Trigger

A Trigger referencing a Kafka Integration as a subscriber:

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
      name: kafka-prod          # references the Integration by name
    topic: orders.created
    consumerGroup: order-processor
  flowRef:
    name: process-order
  cooldown:
    maxInvocations: 100
    window: "60s"
```

---

### Example 6: Using an Integration as a Publisher in a Flow

A Flow step that publishes a message to Kafka on completion:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: process-order
  namespace: automation
spec:
  params:
    - name: orderId
      required: true
  steps:
    - name: process
      action:
        type: http
        http:
          url: "$(configmaps.service-urls.orders-api)/orders/$(params.orderId)/process"
          method: POST
          resultMappings:
            status: "$.status"
      results:
        - name: status

    - name: publish-result
      runAfter: [process]
      when:
        - expression: 'steps.process.results.status == "confirmed"'
      action:
        type: publish
        publish:
          integrationRef:
            name: kafka-prod      # references the Integration by name
          topic: orders.processed
          headers:
            X-Correlation-Id: "$(trigger.headers.X-Request-Id)"
          body: |
            {
              "orderId": "$(params.orderId)",
              "status": "$(steps.process.results.status)"
            }
```

---

## kubectl Reference

| Command | Description |
|---|---|
| `kubectl get integrations -n <ns>` | List all Integrations with phase and trigger count |
| `kubectl get integration <name> -n <ns> -o yaml` | Full spec and status |
| `kubectl describe integration <name> -n <ns>` | Human-readable summary including conditions |
| `kubectl delete integration <name> -n <ns>` | Remove the Integration (gateways are deleted; Triggers become degraded) |

---

## Limitations

- **Namespace-scoped references**: A Trigger and the Integration it references must be in the same namespace. Cross-namespace Integration references are not supported.
- **Plugin RBAC is namespace-scoped**: Plugin pods are granted Role (not ClusterRole) permissions to watch Triggers and create FlowRuns only in their own namespace. This is intentional for security and OpenShift SCC compliance.
- **Plugin image trust**: KubeZap does not verify plugin images. Only use plugin images from sources you trust, as they run inside your cluster with Kubernetes API access.
- **RabbitMQ native support**: Built-in RabbitMQ support (`type: rabbitmq`) is planned but not yet implemented. Use `type: plugin` with the community RabbitMQ plugin in the meantime.
- **One gateway Deployment per Integration per namespace**: KubeZap does not share a single Kafka gateway pod across multiple Integrations. Each Integration gets its own gateway Deployment in each namespace where it is used.
