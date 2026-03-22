# Integration CRD

An `Integration` stores the connection details and credentials for an external system — a Kafka cluster, a message broker, a plugin-managed service — separately from the `Triggers` and `Flows` that use it. This keeps sensitive configuration reusable and out of individual Flow specs, and lets you update connection details in one place.

---

## Contents

- [Integration CRD](#integration-crd)
  - [Contents](#contents)
  - [Overview](#overview)
  - [Roles: Subscriber and Publisher](#roles-subscriber-and-publisher)
    - [Subscriber role](#subscriber-role)
    - [Publisher role](#publisher-role)
  - [Built-in Integration Types](#built-in-integration-types)
    - [Kafka (`type: kafka`)](#kafka-type-kafka)
      - [Scaling with KEDA](#scaling-with-keda)
    - [AMQP (`type: amqp`) _(beta)_](#amqp-type-amqp-beta)
    - [NATS (`type: nats`) _(beta)_](#nats-type-nats-beta)
    - [HTTP (`type: http`)](#http-type-http)
  - [Plugin Integration Type](#plugin-integration-type)
    - [How it works](#how-it-works)
  - [Plugin Protocol Specification](#plugin-protocol-specification)
    - [Subscriber contract](#subscriber-contract)
    - [Publisher contract](#publisher-contract)
    - [Plugin environment](#plugin-environment)
    - [Plugin health check](#plugin-health-check)
  - [Spec Reference](#spec-reference)
    - [IntegrationSpec](#integrationspec)
    - [AmqpIntegrationSpec](#amqpintegrationspec)
    - [AmqpTLSConfig](#amqptlsconfig)
    - [NatsIntegrationSpec](#natsintegrationspec)
    - [NatsTLSConfig](#natstlsconfig)
    - [KafkaIntegrationSpec](#kafkaintegrationspec)
    - [KafkaTLSSpec](#kafkatlsspec)
    - [KafkaSASLSpec](#kafkasaslspec)
    - [SecretKeyRef](#secretkeyref)
    - [PluginIntegrationSpec](#pluginintegrationspec)
    - [PluginSecretRef](#pluginsecretref)
    - [HttpIntegrationSpec](#httpintegrationspec)
    - [HttpAuthSpec](#httpauthspec)
    - [HttpBearerAuth](#httpbearerauth)
    - [HttpBasicAuth](#httpbasicauth)
    - [HttpAPIKeyAuth](#httpapikeyauth)
    - [HttpSecretURLAuth](#httpsecreturlauth)
  - [Status Reference](#status-reference)
    - [IntegrationStatus](#integrationstatus)
    - [Conditions](#conditions)
    - [Printer Columns](#printer-columns)
  - [Examples](#examples)
    - [Example 1: Kafka Integration](#example-1-kafka-integration)
    - [Example 2: Kafka with mTLS](#example-2-kafka-with-mtls)
    - [Example 3: Kafka with SASL/SCRAM](#example-3-kafka-with-saslscram)
    - [Example 4: AMQP Integration (RabbitMQ)](#example-4-amqp-integration-rabbitmq)
    - [Example 5: AMQP 1.0 Integration (ActiveMQ Artemis)](#example-5-amqp-10-integration-activemq-artemis)
    - [Example 6: NATS JetStream Integration](#example-6-nats-jetstream-integration)
    - [Example 7: Community Plugin Integration](#example-7-community-plugin-integration)
    - [Example 8: Using an Integration in a Trigger](#example-8-using-an-integration-in-a-trigger)
    - [Example 9: Using an Integration as a Publisher in a Flow](#example-9-using-an-integration-as-a-publisher-in-a-flow)
    - [Example 10: HTTP Integration (GitHub API)](#example-10-http-integration-github-api)
    - [Example 11: HTTP Integration (Slack Webhook — secretUrl)](#example-11-http-integration-slack-webhook--secreturl)
  - [Community Plugin Graduation](#community-plugin-graduation)
    - [Graduation criteria](#graduation-criteria)
    - [What graduation changes](#what-graduation-changes)
    - [Plugin catalog](#plugin-catalog)
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

KubeZap ships first-party gateway binaries for the protocols below. The operator manages the gateway Deployment lifecycle; you supply only the connection config.

The type taxonomy is intentionally **protocol-level, not broker-level**. A single `type: amqp` gateway covers RabbitMQ, ActiveMQ Artemis, Solace, Azure Service Bus, and IBM MQ AMQP — regardless of which broker is behind the connection. This avoids an unbounded list of broker-specific types. For brokers without a matching protocol type, use `type: plugin` with a community or vendor-supplied image.

### Kafka (`type: kafka`)

Uses the `kubezap/kafka-gateway` image with the [IBM/sarama](https://github.com/IBM/sarama) client.

- SASL/PLAIN, SASL/SCRAM-SHA-256, SASL/SCRAM-SHA-512
- TLS with custom CAs and mutual TLS
- Configurable consumer group prefix
- Offset-based dedup key in FlowRun names (`<trigger>-p<partition>-offset-<offset>`)
- One gateway Deployment per Integration per namespace

Kafka has dedicated first-party support rather than being absorbed into a generic AMQP type because its offset/partition semantics, dedup model, and scaling story (KEDA partition-bounded HPA) are fundamentally different from queue-based protocols.

#### Scaling with KEDA

KubeZap creates a KEDA `ScaledObject` alongside each Kafka gateway Deployment when KEDA is installed in the cluster. The `ScaledObject` uses the `kafka` trigger type and scales the gateway based on consumer group lag, bounded by the number of partitions in the topic:

```
minReplicaCount: 1
maxReplicaCount: <number of partitions>
trigger:
  type: kafka
  metadata:
    bootstrapServers: <from Integration.spec.kafka.bootstrapServers>
    consumerGroup: <kubezap-<trigger-name>>
    topic: <from Trigger.spec.pubsub.topic>
    lagThreshold: "50"
    offsetResetPolicy: latest
```

**Prerequisites:** KEDA must be installed in the cluster (`keda-operator` pod running in the `keda` namespace or similar). KubeZap detects KEDA availability by checking for the `ScaledObject` CRD at startup. If KEDA is not installed, the gateway Deployment is created with a static replica count of 1 and no `ScaledObject` is created.

**Lag threshold:** The default lag threshold is 50 messages per replica. This is intentionally conservative — tune it down for latency-sensitive flows or up for high-throughput batch scenarios. Future: expose `spec.kafka.kedaLagThreshold` on the `Integration` to control this per-integration.

**Partition-bounded scaling:** KEDA will not scale the gateway beyond the number of partitions in the topic, since there is no benefit to having more consumers than partitions. Ensure your topics have enough partitions for the concurrency you expect.

### AMQP (`type: amqp`) _(beta)_

> **Beta stability definition:** The AMQP gateway is fully implemented and included in production examples. The CRD API is stable — no breaking field changes are planned. "Beta" indicates the following known limitations that may affect some deployments:
>
> - **Consumer flow control:** The gateway consumes messages at full speed regardless of downstream FlowRun processing rate. High-volume queues may accumulate in-flight FlowRuns faster than the controller processes them. Mitigate with a smaller `spec.amqp.prefetchCount` (not yet exposed — tracked for a future release).
> - **Azure Service Bus long-lived connections:** The AMQP 1.0 gateway establishes a persistent connection. Azure Service Bus issues short-lived SAS tokens; token refresh on connections older than ~1 hour is not yet implemented. Workaround: set a short reconnect interval or use a Managed Identity credential provider via a custom plugin.
> - **IBM MQ:** Listed as supported (AMQP 1.0 wire protocol) but not validated in CI against a live IBM MQ instance. RabbitMQ and ActiveMQ Artemis are the primary tested targets.
> - **No publisher confirm mode:** The gateway uses auto-ack; there is no AMQP publisher confirm / mandatory flag for outbound publish steps.

Uses the `kubezap/amqp-gateway` image. Covers:

| Broker            | Protocol version     |
| ----------------- | -------------------- |
| RabbitMQ          | AMQP 0-9-1 (default) |
| ActiveMQ Classic  | AMQP 0-9-1           |
| ActiveMQ Artemis  | AMQP 1.0             |
| Solace PubSub+    | AMQP 1.0             |
| Azure Service Bus | AMQP 1.0             |
| IBM MQ            | AMQP 1.0             |

Select the wire protocol via `spec.amqp.version: "0-9-1"` or `"1.0"` (default: `"0-9-1"`). The gateway dispatches to the appropriate client library at startup.

> **Note on JMS:** JMS is a Java API layer, not a wire protocol. For brokers typically accessed via JMS in Java environments, use the AMQP type with the appropriate version. ActiveMQ Artemis and IBM MQ both support AMQP 1.0 natively. TIBCO EMS and other JMS-only brokers have no AMQP support — use `type: plugin` with the vendor's Go SDK.

### NATS (`type: nats`) _(beta)_

> **Beta stability definition:** The NATS gateway is fully implemented and included in production examples. The CRD API is stable. "Beta" indicates the following known limitations:
>
> - **Core NATS (no JetStream):** Without `spec.nats.jetStream: true`, the gateway uses Core NATS (at-most-once delivery). There is no dedup key guarantee — a FlowRun is created for every message, with no replay protection on controller restart.
> - **Single subject per Trigger:** Each Trigger subscribes to exactly one subject (or wildcard pattern). To fan out across multiple distinct subjects, use multiple Triggers.
> - **NATS Server version:** Tested with NATS Server 2.10+. Older servers are not validated. JetStream requires NATS Server 2.2+.
> - **No flow control:** The gateway delivers messages to FlowRun creation without back-pressure against downstream controller throughput. For high-volume subjects, tune `--max-concurrent-flowruns` on the controller.

Uses the `kubezap/nats-gateway` image with the official [nats.go](https://github.com/nats-io/nats.go) client.

- NATS Core (at-most-once) and JetStream (durable, at-least-once)
- NKey and User JWT credential files
- TLS and mTLS
- Multiple server URLs for cluster failover

Enable JetStream with `spec.nats.jetStream: true`. Without JetStream, the gateway uses Core NATS subjects (no persistence, no dedup key guarantee).

### HTTP (`type: http`)

Provides a reusable base URL, authentication, and default headers for Flow steps that call external HTTP APIs. No gateway Deployment is created — the controller resolves auth at step execution time.

Supported auth types:

| Auth type   | How it works                                                                                     |
| ----------- | ------------------------------------------------------------------------------------------------ |
| `bearer`    | Adds `Authorization: Bearer <token>` header from a Secret                                        |
| `basic`     | Adds `Authorization: Basic <b64(user:pass)>` header from two Secrets                             |
| `apiKey`    | Adds a custom header (e.g. `X-Api-Key`) with value from a Secret                                 |
| `secretUrl` | Replaces the step URL entirely with a URL stored in a Secret (for Slack incoming webhooks, etc.) |

When a Flow step sets `http.integrationRef`, the controller:
1. Fetches the Integration
2. Merges `defaultHeaders` (step-level headers override integration defaults)
3. Prepends `baseUrl` to the step URL if the step URL is a relative path
4. Injects auth (overrides any step-level `Authorization` header)

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

| Variable                   | Description                               |
| -------------------------- | ----------------------------------------- |
| `KUBEZAP_NAMESPACE`        | The namespace this plugin instance serves |
| `KUBEZAP_INTEGRATION_NAME` | Name of the Integration CRD               |
| `KUBEZAP_PUBLISHER_PORT`   | Port to listen on for publisher calls     |
| `KUBEZAP_LOG_LEVEL`        | `debug`, `info`, `warn`, `error`          |

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

| Field    | Type                  | Required | Default | Description                                                   |
| -------- | --------------------- | -------- | ------- | ------------------------------------------------------------- |
| `type`   | string                | **Yes**  | —       | `kafka`, `amqp`, `nats`, `plugin`, or `http`                  |
| `kafka`  | KafkaIntegrationSpec  | No       | —       | Kafka connection details. Required when `type: kafka`.        |
| `amqp`   | AmqpIntegrationSpec   | No       | —       | AMQP connection details. Required when `type: amqp`. _(beta)_ |
| `nats`   | NatsIntegrationSpec   | No       | —       | NATS connection details. Required when `type: nats`. _(beta)_ |
| `plugin` | PluginIntegrationSpec | No       | —       | Plugin configuration. Required when `type: plugin`.           |
| `http`   | HttpIntegrationSpec   | No       | —       | HTTP endpoint config. Required when `type: http`.             |

### AmqpIntegrationSpec

| Field               | Type          | Required | Default   | Description                                                         |
| ------------------- | ------------- | -------- | --------- | ------------------------------------------------------------------- |
| `url`               | string        | **Yes**  | —         | AMQP broker URL, e.g. `amqp://rabbitmq:5672/vhost` or `amqps://...` |
| `version`           | string        | No       | `"0-9-1"` | Wire protocol: `"0-9-1"` or `"1.0"`                                 |
| `tls`               | AmqpTLSConfig | No       | —         | TLS configuration. Inferred from `amqps://` URL if omitted.         |
| `usernameSecretRef` | SecretKeyRef  | No       | —         | Secret key containing AMQP username                                 |
| `passwordSecretRef` | SecretKeyRef  | No       | —         | Secret key containing AMQP password                                 |

### AmqpTLSConfig

| Field                 | Type                 | Required | Default | Description                                                |
| --------------------- | -------------------- | -------- | ------- | ---------------------------------------------------------- |
| `enabled`             | boolean              | No       | `false` | Enable TLS (auto-enabled for `amqps://` URLs)              |
| `insecureSkipVerify`  | boolean              | No       | `false` | Disable certificate verification. Development only.        |
| `caSecretRef`         | SecretKeyRef         | No       | —       | Custom CA certificate                                      |
| `clientCertSecretRef` | LocalObjectReference | No       | —       | Client certificate secret for mTLS (`tls.crt` + `tls.key`) |

### NatsIntegrationSpec

| Field                  | Type                 | Required | Default | Description                                                   |
| ---------------------- | -------------------- | -------- | ------- | ------------------------------------------------------------- |
| `servers`              | []string             | **Yes**  | —       | NATS server URLs. Multiple entries used for cluster failover. |
| `tls`                  | NatsTLSConfig        | No       | —       | TLS configuration                                             |
| `credentialsSecretRef` | LocalObjectReference | No       | —       | Secret containing `nats.creds` (NKey or User JWT credentials) |
| `usernameSecretRef`    | SecretKeyRef         | No       | —       | Username for basic auth (not recommended for production)      |
| `passwordSecretRef`    | SecretKeyRef         | No       | —       | Password for basic auth                                       |
| `jetStream`            | boolean              | No       | `false` | Enable NATS JetStream for durable delivery                    |

### NatsTLSConfig

| Field                 | Type                 | Required | Default | Description                                         |
| --------------------- | -------------------- | -------- | ------- | --------------------------------------------------- |
| `insecureSkipVerify`  | boolean              | No       | `false` | Disable certificate verification. Development only. |
| `caSecretRef`         | SecretKeyRef         | No       | —       | Custom CA certificate                               |
| `clientCertSecretRef` | LocalObjectReference | No       | —       | Client certificate secret for mTLS                  |

### KafkaIntegrationSpec

| Field                 | Type              | Required | Default   | Description                                                                                      |
| --------------------- | ----------------- | -------- | --------- | ------------------------------------------------------------------------------------------------ |
| `bootstrapServers`    | []string          | **Yes**  | —         | Kafka bootstrap broker addresses (e.g., `kafka.infra:9092`)                                      |
| `tls`                 | KafkaTLSSpec      | No       | disabled  | TLS configuration                                                                                |
| `sasl`                | KafkaSASLSpec     | No       | disabled  | SASL authentication configuration                                                                |
| `consumerGroupPrefix` | string            | No       | `kubezap` | Prefix for consumer group names. Final group: `<prefix>-<triggerName>-<consumerGroup>`           |
| `producerConfig`      | map[string]string | No       | —         | Additional Kafka producer configuration key/value pairs (passed directly to the producer client) |
| `consumerConfig`      | map[string]string | No       | —         | Additional Kafka consumer configuration key/value pairs                                          |

### KafkaTLSSpec

| Field                 | Type                 | Required | Default | Description                                           |
| --------------------- | -------------------- | -------- | ------- | ----------------------------------------------------- |
| `enabled`             | boolean              | No       | `false` | Enable TLS                                            |
| `caSecretRef`         | SecretKeyRef         | No       | —       | Secret containing `ca.crt` for custom CA verification |
| `clientCertSecretRef` | LocalObjectReference | No       | —       | Secret containing `tls.crt` and `tls.key` for mTLS    |
| `insecureSkipVerify`  | boolean              | No       | `false` | Disable certificate verification. Development only.   |

### KafkaSASLSpec

| Field               | Type         | Required | Default | Description                                                              |
| ------------------- | ------------ | -------- | ------- | ------------------------------------------------------------------------ |
| `mechanism`         | string       | **Yes**  | —       | `PLAIN`, `SCRAM-SHA-256`, or `SCRAM-SHA-512`                             |
| `username`          | string       | No       | —       | SASL username (plain text; use `usernameSecretRef` for sensitive values) |
| `usernameSecretRef` | SecretKeyRef | No       | —       | Reference to a Secret key containing the SASL username                   |
| `passwordSecretRef` | SecretKeyRef | **Yes**  | —       | Reference to a Secret key containing the SASL password                   |

### SecretKeyRef

| Field  | Type   | Description           |
| ------ | ------ | --------------------- |
| `name` | string | Name of the Secret    |
| `key`  | string | Key within the Secret |

### PluginIntegrationSpec

| Field              | Type                   | Required | Default | Description                                                                                                      |
| ------------------ | ---------------------- | -------- | ------- | ---------------------------------------------------------------------------------------------------------------- |
| `image`            | string                 | **Yes**  | —       | Container image implementing the plugin protocol                                                                 |
| `publisherPort`    | integer                | No       | `8090`  | Port the plugin listens on for publisher calls from the controller                                               |
| `replicas`         | integer                | No       | `1`     | Number of plugin pod replicas. For subscriber plugins, ensure your deduplication key handles multiple consumers. |
| `resources`        | ResourceRequirements   | No       | —       | CPU/memory requests and limits for the plugin container                                                          |
| `config`           | map[string]string      | No       | —       | Non-sensitive configuration passed to the plugin as environment variables                                        |
| `secretRefs`       | []PluginSecretRef      | No       | —       | Secrets mounted as environment variables in the plugin container                                                 |
| `imagePullSecrets` | []LocalObjectReference | No       | —       | Image pull secrets for private registries                                                                        |

### PluginSecretRef

| Field            | Type              | Description                                                                       |
| ---------------- | ----------------- | --------------------------------------------------------------------------------- |
| `secretName`     | string            | Name of the Kubernetes Secret                                                     |
| `envVarMappings` | map[string]string | Maps Secret keys to environment variable names: `{ "api-key": "PLUGIN_API_KEY" }` |

### HttpIntegrationSpec

| Field            | Type              | Required | Default | Description                                                                   |
| ---------------- | ----------------- | -------- | ------- | ----------------------------------------------------------------------------- |
| `baseUrl`        | string            | No       | —       | Base URL prepended to step URLs. Ignored if the step URL is already absolute. |
| `auth`           | HttpAuthSpec      | No       | —       | Authentication configuration                                                  |
| `defaultHeaders` | map[string]string | No       | —       | Default headers merged into every request. Step-level headers override these. |

### HttpAuthSpec

| Field       | Type              | Required | Default | Description                                        |
| ----------- | ----------------- | -------- | ------- | -------------------------------------------------- |
| `type`      | string            | **Yes**  | —       | `bearer`, `basic`, `apiKey`, or `secretUrl`        |
| `bearer`    | HttpBearerAuth    | No       | —       | Bearer token config. Required when `type=bearer`.  |
| `basic`     | HttpBasicAuth     | No       | —       | Basic auth config. Required when `type=basic`.     |
| `apiKey`    | HttpAPIKeyAuth    | No       | —       | API key config. Required when `type=apiKey`.       |
| `secretUrl` | HttpSecretURLAuth | No       | —       | Secret URL config. Required when `type=secretUrl`. |

### HttpBearerAuth

| Field            | Type         | Required | Default | Description                                  |
| ---------------- | ------------ | -------- | ------- | -------------------------------------------- |
| `tokenSecretRef` | SecretKeyRef | **Yes**  | —       | Secret key containing the bearer token value |

### HttpBasicAuth

| Field               | Type         | Required | Default | Description                        |
| ------------------- | ------------ | -------- | ------- | ---------------------------------- |
| `usernameSecretRef` | SecretKeyRef | **Yes**  | —       | Secret key containing the username |
| `passwordSecretRef` | SecretKeyRef | **Yes**  | —       | Secret key containing the password |

### HttpAPIKeyAuth

| Field            | Type         | Required | Default | Description                                |
| ---------------- | ------------ | -------- | ------- | ------------------------------------------ |
| `headerName`     | string       | **Yes**  | —       | HTTP header name to set (e.g. `X-Api-Key`) |
| `valueSecretRef` | SecretKeyRef | **Yes**  | —       | Secret key containing the API key value    |

### HttpSecretURLAuth

| Field          | Type         | Required | Default | Description                                                         |
| -------------- | ------------ | -------- | ------- | ------------------------------------------------------------------- |
| `urlSecretRef` | SecretKeyRef | **Yes**  | —       | Secret key containing the full URL (including embedded credentials) |

---

## Status Reference

### IntegrationStatus

| Field                | Type                   | Description                                                              |
| -------------------- | ---------------------- | ------------------------------------------------------------------------ |
| `conditions`         | []Condition            | Standard `Ready` condition                                               |
| `phase`              | string                 | `Ready`, `Degraded`, `Failed`                                            |
| `gatewayDeployments` | []GatewayDeploymentRef | Names of gateway Deployments managed for this Integration, per namespace |
| `connectedTriggers`  | integer                | Number of Triggers currently referencing this Integration                |

### Conditions

| Type               | Status  | Meaning                                                            |
| ------------------ | ------- | ------------------------------------------------------------------ |
| `Ready`            | `True`  | Integration is configured and gateways are running                 |
| `Ready`            | `False` | Configuration error or gateway pod failed to start. See `message`. |
| `GatewayAvailable` | `True`  | At least one gateway replica is running and healthy                |
| `GatewayAvailable` | `False` | No gateway replicas available                                      |

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

### Example 4: AMQP Integration (RabbitMQ)

RabbitMQ using AMQP 0-9-1 (the default version):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: rabbitmq-prod
  namespace: automation
spec:
  type: amqp
  amqp:
    url: amqps://rabbitmq.infra.svc.cluster.local:5671/production
    version: "0-9-1"
    usernameSecretRef:
      name: rabbitmq-credentials
      key: username
    passwordSecretRef:
      name: rabbitmq-credentials
      key: password
```

---

### Example 5: AMQP 1.0 Integration (ActiveMQ Artemis)

ActiveMQ Artemis with AMQP 1.0 and mTLS:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: artemis-prod
  namespace: automation
spec:
  type: amqp
  amqp:
    url: amqps://artemis.infra.svc.cluster.local:5671
    version: "1.0"
    tls:
      caSecretRef:
        name: artemis-ca
        key: ca.crt
      clientCertSecretRef:
        name: kubezap-artemis-client-cert
```

---

### Example 6: NATS JetStream Integration

NATS cluster with JetStream enabled and NKey credentials:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-cluster
  namespace: automation
spec:
  type: nats
  nats:
    servers:
      - nats://nats-0.nats.infra.svc.cluster.local:4222
      - nats://nats-1.nats.infra.svc.cluster.local:4222
      - nats://nats-2.nats.infra.svc.cluster.local:4222
    jetStream: true
    credentialsSecretRef:
      name: nats-kubezap-creds   # must contain key: nats.creds
```

---

### Example 7: Community Plugin Integration

A community plugin for a system without a built-in first-party type:

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

### Example 8: Using an Integration in a Trigger

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

### Example 9: Using an Integration as a Publisher in a Flow

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

### Example 10: HTTP Integration (GitHub API)

A `type: http` Integration providing GitHub API base URL, bearer token auth, and default headers. A Flow step references it via `integrationRef` — no inline credentials:

```yaml
# Integration
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: github-api
  namespace: automation
spec:
  type: http
  http:
    baseUrl: "https://api.github.com"
    auth:
      type: bearer
      bearer:
        tokenSecretRef:
          name: github-api-token
          key: token
    defaultHeaders:
      Content-Type: "application/vnd.github+json"
      X-GitHub-Api-Version: "2022-11-28"
---
# Flow step using the integration
- name: apply-label
  action:
    type: http
    http:
      integrationRef:
        name: github-api
      url: "/repos/acme/platform/issues/$(steps.extract_pr.results.prNumber)/labels"
      method: POST
      body: '{"labels":["needs-review"]}'
```

---

### Example 11: HTTP Integration (Slack Webhook — secretUrl)

For services where the URL itself is the credential (e.g. Slack incoming webhooks), use `type: secretUrl`. The controller replaces the step URL entirely with the secret value:

```yaml
# Integration — URL is the credential
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: slack-webhook
  namespace: automation
spec:
  type: http
  http:
    auth:
      type: secretUrl
      secretUrl:
        urlSecretRef:
          name: slack-webhook-secret
          key: url
    defaultHeaders:
      Content-Type: "application/json"
---
# Secret
apiVersion: v1
kind: Secret
metadata:
  name: slack-webhook-secret
  namespace: automation
type: Opaque
stringData:
  url: "https://hooks.slack.com/services/T.../B.../..."
---
# Flow step
- name: notify-slack
  action:
    type: http
    http:
      integrationRef:
        name: slack-webhook
      url: ""          # ignored — URL comes from the Integration secret
      method: POST
      body: '{"text":"$(steps.export_data.results.status)"}'
```

---

## Community Plugin Graduation

Community plugins can graduate to first-party built-in types. The lifecycle is:

```
type: plugin  (community image, BYO lifecycle)
    ↓  adoption signal + stable wire protocol + Go client
type: <protocol>  (first-party gateway binary, operator-managed)
```

### Graduation criteria

A `type: plugin` integration is a graduation candidate when it meets all of:

1. **Stable wire protocol** — a well-defined open protocol (AMQP, STOMP, NATS, etc.) rather than a vendor-proprietary SDK
2. **Permissive Go client** — a Go client library exists with Apache 2.0 or MIT license
3. **Full contract compliance** — implements the complete [plugin contract](./plugin-contract.md) including dedup keys, W3C traceparent propagation, and structured logging
4. **Integration tests** — test suite contributions to the KubeZap repository covering the subscribe and publish paths
5. **Assigned maintainer** — at least one person willing to own the first-party gateway long-term

### What graduation changes

When a plugin graduates:
- A new `type: <protocol>` value is added to the enum (non-breaking addition)
- A first-party `kubezap/<protocol>-gateway` image is built and published
- `type: plugin` with the community image continues to work indefinitely — existing Integration CRs do not need to migrate

### Plugin catalog

The community plugin catalog lives at `docs/plugins/` (forthcoming). Each catalog entry declares the supported broker, required secrets schema, supported trigger types, and maturity level (`community` / `verified` / `core`). See that directory for contribution guidelines.

---

## kubectl Reference

| Command                                          | Description                                                             |
| ------------------------------------------------ | ----------------------------------------------------------------------- |
| `kubectl get integrations -n <ns>`               | List all Integrations with phase and trigger count                      |
| `kubectl get integration <name> -n <ns> -o yaml` | Full spec and status                                                    |
| `kubectl describe integration <name> -n <ns>`    | Human-readable summary including conditions                             |
| `kubectl delete integration <name> -n <ns>`      | Remove the Integration (gateways are deleted; Triggers become degraded) |

---

## Limitations

- **Namespace-scoped references**: A Trigger and the Integration it references must be in the same namespace. Cross-namespace Integration references are not supported.
- **Plugin RBAC is namespace-scoped**: Plugin pods are granted Role (not ClusterRole) permissions to watch Triggers and create FlowRuns only in their own namespace. This is intentional for security and OpenShift SCC compliance.
- **Plugin image trust**: KubeZap does not verify plugin images. Only use plugin images from sources you trust, as they run inside your cluster with Kubernetes API access.
- **AMQP and NATS gateways**: `type: amqp` and `type: nats` are implemented (beta). Known limitations: the AMQP and NATS gateway watchers currently use a 30-second polling interval to detect Trigger changes (reaction latency up to 30 s); informer-based watch is planned for the next stabilization sprint. See `docs/tech-debt/gateway-shutdown-correctness.md` for details.
- **One gateway Deployment per Integration per namespace**: KubeZap does not share a single Kafka gateway pod across multiple Integrations. Each Integration gets its own gateway Deployment in each namespace where it is used.
