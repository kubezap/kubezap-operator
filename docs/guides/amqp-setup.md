# AMQP Gateway Setup

This guide walks through setting up KubeZap's built-in AMQP gateway to consume messages from RabbitMQ (AMQP 0-9-1) or ActiveMQ Artemis (AMQP 1.0) and trigger Flows automatically. By the end, you will have a working Integration, a Trigger subscribed to a queue, and a FlowRun created from a test message.

For the full field reference, see the [Integration CRD spec](../api/integration.md#amqpintegrationspec) and the [Trigger CRD spec](../api/trigger.md#pubsubtrigger).

---

## Contents

- [Prerequisites](#prerequisites)
- [Creating an AMQP Integration](#creating-an-amqp-integration)
  - [RabbitMQ (AMQP 0-9-1)](#rabbitmq-amqp-0-9-1)
  - [ActiveMQ Artemis (AMQP 1.0)](#activemq-artemis-amqp-10)
  - [Verifying the Integration](#verifying-the-integration)
- [Creating an AMQP Trigger](#creating-an-amqp-trigger)
  - [Verifying the Gateway Deployment](#verifying-the-gateway-deployment)
- [Producing a Test Message and Watching the FlowRun](#producing-a-test-message-and-watching-the-flowrun)
  - [RabbitMQ](#rabbitmq)
  - [ActiveMQ Artemis](#activemq-artemis)
- [TLS and Authentication Options](#tls-and-authentication-options)
  - [TLS with a Custom CA](#tls-with-a-custom-ca)
  - [Mutual TLS (mTLS)](#mutual-tls-mtls)
  - [SASL Authentication (AMQP 1.0)](#sasl-authentication-amqp-10)
  - [Disabling TLS Verification (Development Only)](#disabling-tls-verification-development-only)
- [AMQP 0-9-1 vs AMQP 1.0 Differences](#amqp-0-9-1-vs-amqp-1-0-differences)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

Before starting, ensure you have:

1. **A running AMQP broker** -- either RabbitMQ (AMQP 0-9-1) or ActiveMQ Artemis (AMQP 1.0), reachable from your cluster. If you need a quick test broker:
   - RabbitMQ: `helm install rabbitmq bitnami/rabbitmq -n default`
   - Artemis: deploy the [ArtemisCloud operator](https://artemiscloud.io/) or use a standalone container
2. **KubeZap installed** in your cluster with the controller running in the `kubezap-system` namespace
3. **A namespace** for your workloads (this guide uses `default`)
4. **kubectl** configured to talk to your cluster
5. **Broker credentials** (username/password) if your broker requires authentication

> **Note:** The AMQP gateway image (`kubezap/amqp-gateway`) must be accessible from your cluster. If you are running in an air-gapped environment, mirror the image to your internal registry first.

---

## Creating an AMQP Integration

An `Integration` CRD stores the broker connection details -- URL, protocol version, credentials, and TLS settings. You create one Integration per broker, then reference it from one or more Triggers.

### RabbitMQ (AMQP 0-9-1)

First, create a Secret with the RabbitMQ credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: rabbitmq-credentials
  namespace: default
type: Opaque
stringData:
  username: kubezap
  password: changeme
```

```bash
kubectl apply -f rabbitmq-credentials.yaml
```

Then create the Integration:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: rabbitmq
  namespace: default
spec:
  type: amqp
  amqp:
    # Broker URL. Use amqp:// for plaintext, amqps:// for TLS.
    # Append the vhost after the port (e.g., /production).
    url: amqp://rabbitmq.default.svc.cluster.local:5672/

    # Wire protocol version. "0-9-1" is the default and correct for RabbitMQ.
    version: "0-9-1"

    # Credentials stored in a Secret. The gateway reads these at connection time.
    usernameSecretRef:
      name: rabbitmq-credentials
      key: username
    passwordSecretRef:
      name: rabbitmq-credentials
      key: password
```

```bash
kubectl apply -f rabbitmq-integration.yaml
```

### ActiveMQ Artemis (AMQP 1.0)

Create a Secret with the Artemis credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: artemis-credentials
  namespace: default
type: Opaque
stringData:
  username: kubezap
  password: changeme
```

```bash
kubectl apply -f artemis-credentials.yaml
```

Then create the Integration:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: artemis
  namespace: default
spec:
  type: amqp
  amqp:
    # Artemis AMQP endpoint. Port 5672 is the default AMQP acceptor.
    url: amqp://artemis.default.svc.cluster.local:5672

    # AMQP 1.0 is required for Artemis, Solace, Azure Service Bus, and IBM MQ.
    version: "1.0"

    # Credentials via SASL PLAIN (injected into the AMQP 1.0 connection).
    usernameSecretRef:
      name: artemis-credentials
      key: username
    passwordSecretRef:
      name: artemis-credentials
      key: password
```

```bash
kubectl apply -f artemis-integration.yaml
```

### Verifying the Integration

Check that the Integration reaches the `Ready` phase:

```bash
kubectl get integration -n default
```

Expected output:

```
NAME       TYPE   PHASE   TRIGGERS   AGE
rabbitmq   amqp   Ready   0          30s
```

If the phase shows `Failed` or `Degraded`, inspect the conditions:

```bash
kubectl describe integration rabbitmq -n default
```

Look at `Status.Conditions` for details. Common issues include unreachable broker URLs and incorrect Secret references.

---

## Creating an AMQP Trigger

A `Trigger` with `type: pubsub` and `pubsub.type: amqp` tells KubeZap to consume messages from a queue and create a FlowRun for each one.

> **Note:** You need an existing Flow for the Trigger to reference. If you do not have one yet, create a simple echo Flow for testing. See the [Flow CRD docs](../api/flow.md) for details.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events
  namespace: default
spec:
  type: pubsub
  pubsub:
    # Must match the Integration type.
    type: amqp

    # Reference to the Integration created above.
    integrationRef:
      name: rabbitmq

    # Queue name to consume from.
    # For AMQP 0-9-1: this is the queue name (declared automatically as durable).
    # For AMQP 1.0: this is the address name.
    topic: orders.created

    # Optional: AMQP 0-9-1 routing key for exchange bindings.
    # Omit if consuming directly from a named queue.
    # routingKey: "order.new"

  # Flow to execute for each message.
  flowRef:
    name: process-order
```

```bash
kubectl apply -f order-events-trigger.yaml
```

### Verifying the Gateway Deployment

When the controller reconciles the Trigger, it creates (or updates) an AMQP gateway Deployment in the same namespace. One gateway Deployment is shared across all AMQP Triggers that reference the same Integration in the same namespace.

```bash
kubectl get deployment -n default -l kubezap.io/component=amqp-gateway
```

Expected output:

```
NAME                              READY   UP-TO-DATE   AVAILABLE   AGE
kubezap-amqp-gateway-rabbitmq     1/1     1            1           15s
```

Check the gateway pod logs to confirm it connected and is consuming:

```bash
kubectl logs -n default -l kubezap.io/component=amqp-gateway --tail=20
```

You should see log lines like:

```
{"level":"info","msg":"amqp 0-9-1 consuming","trigger":"default/order-events","topic":"orders.created"}
```

Check the Trigger status to confirm it is ready:

```bash
kubectl get trigger order-events -n default
```

---

## Producing a Test Message and Watching the FlowRun

Open a second terminal to watch for FlowRuns:

```bash
kubectl get flowruns -n default -w
```

### RabbitMQ

If you have `rabbitmqadmin` available (installed with the RabbitMQ management plugin), publish a test message:

```bash
# Port-forward the RabbitMQ management API if needed
kubectl port-forward svc/rabbitmq 15672:15672 -n default &

rabbitmqadmin publish \
  exchange="" \
  routing_key="orders.created" \
  payload='{"orderId": "ord-001", "customer": "acme-corp", "total": 99.95}' \
  properties='{"content_type": "application/json"}'
```

Alternatively, use the RabbitMQ management UI at `http://localhost:15672` to publish a message to the `orders.created` queue.

### ActiveMQ Artemis

Use the Artemis CLI to produce a test message:

```bash
# Exec into the Artemis broker pod
kubectl exec -it -n default deploy/artemis -- /bin/bash

# Produce a single message to the address
./bin/artemis producer \
  --url tcp://localhost:61616 \
  --destination orders.created \
  --message-count 1 \
  --message '{"orderId": "ord-001", "customer": "acme-corp", "total": 99.95}'
```

Or use any AMQP 1.0 client (e.g., `qpid-proton`) from a pod with network access to the broker.

### Observing the FlowRun

In your watch terminal, you should see a new FlowRun appear:

```
NAME                                    FLOW            PHASE     AGE
order-events-1711036800-a1b2c           process-order   Running   2s
order-events-1711036800-a1b2c           process-order   Succeeded 5s
```

Inspect the FlowRun to see the message payload passed as trigger data:

```bash
kubectl describe flowrun order-events-1711036800-a1b2c -n default
```

The message body is available to the Flow as `$(trigger.payload.value)`. If the message was valid JSON, the fields are also accessible individually -- for example, `$(trigger.payload.value.orderId)`.

---

## TLS and Authentication Options

### TLS with a Custom CA

If your broker uses a certificate signed by an internal CA, provide the CA bundle in a Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: amqp-ca
  namespace: default
type: Opaque
stringData:
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    <your CA certificate PEM>
    -----END CERTIFICATE-----
```

Reference it in the Integration:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: rabbitmq-tls
  namespace: default
spec:
  type: amqp
  amqp:
    url: amqps://rabbitmq.default.svc.cluster.local:5671/
    version: "0-9-1"
    tls:
      enabled: true
      caSecretRef:
        name: amqp-ca
        key: ca.crt
    usernameSecretRef:
      name: rabbitmq-credentials
      key: username
    passwordSecretRef:
      name: rabbitmq-credentials
      key: password
```

> **Note:** When the URL uses the `amqps://` scheme, TLS is enabled automatically. Setting `tls.enabled: true` explicitly is optional in that case but recommended for clarity.

### Mutual TLS (mTLS)

For brokers that require client certificate authentication, provide a standard Kubernetes TLS Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: amqp-client-cert
  namespace: default
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded client certificate>
  tls.key: <base64-encoded client private key>
```

> **Note:** Use [cert-manager](https://cert-manager.io) to issue and automatically rotate client certificates.

Reference both the CA and client certificate in the Integration:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: artemis-mtls
  namespace: default
spec:
  type: amqp
  amqp:
    url: amqps://artemis.default.svc.cluster.local:5671
    version: "1.0"
    tls:
      enabled: true
      caSecretRef:
        name: amqp-ca
        key: ca.crt
      clientCertSecretRef:
        name: amqp-client-cert
    # Username/password can be omitted if the broker authenticates
    # solely via client certificate.
```

### SASL Authentication (AMQP 1.0)

For AMQP 1.0 connections, credentials are sent via SASL PLAIN during the connection handshake. This is the standard authentication mechanism for Artemis, Solace, Azure Service Bus, and IBM MQ over AMQP 1.0.

Configure credentials using `usernameSecretRef` and `passwordSecretRef` as shown in the [ActiveMQ Artemis example](#activemq-artemis-amqp-10) above.

For AMQP 0-9-1 (RabbitMQ), credentials are embedded into the connection URL by the gateway. The same `usernameSecretRef`/`passwordSecretRef` fields are used -- the gateway handles the protocol difference internally.

### Disabling TLS Verification (Development Only)

For local development with self-signed certificates, you can skip TLS verification:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: rabbitmq-dev
  namespace: default
spec:
  type: amqp
  amqp:
    url: amqps://rabbitmq.local:5671/
    version: "0-9-1"
    tls:
      enabled: true
      insecureSkipVerify: true
    usernameSecretRef:
      name: rabbitmq-credentials
      key: username
    passwordSecretRef:
      name: rabbitmq-credentials
      key: password
```

> **Note:** Never use `insecureSkipVerify: true` in production. It disables all certificate validation, making the connection vulnerable to man-in-the-middle attacks. Use a proper CA certificate instead.

---

## AMQP 0-9-1 vs AMQP 1.0 Differences

Despite sharing the AMQP name, these are fundamentally different wire protocols. The KubeZap AMQP gateway uses a different client library for each.

| Aspect                    | AMQP 0-9-1                                                                                                                                           | AMQP 1.0                                                                                                                          |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| **Routing model**         | Exchange + binding key + queue. Messages are published to an exchange and routed to queues via binding rules.                                        | Address-based. Messages are sent to a named address; the broker decides how to route them.                                        |
| **Brokers**               | RabbitMQ, ActiveMQ Classic                                                                                                                           | ActiveMQ Artemis, Solace PubSub+, Azure Service Bus, IBM MQ                                                                       |
| **`spec.amqp.version`**   | `"0-9-1"` (default)                                                                                                                                  | `"1.0"`                                                                                                                           |
| **`topic` field meaning** | Queue name. The gateway declares this as a durable queue and consumes from it.                                                                       | Address name. The gateway creates a receiver link on this address.                                                                |
| **`routingKey` field**    | Used as the binding key when the queue is bound to an exchange. Omit if consuming from a named queue directly.                                       | Not applicable. Ignored if set.                                                                                                   |
| **Authentication**        | Credentials embedded in the AMQP URL (`amqp://user:pass@host`). The gateway handles this automatically from `usernameSecretRef`/`passwordSecretRef`. | SASL PLAIN sent during the AMQP 1.0 connection handshake. Configured via the same `usernameSecretRef`/`passwordSecretRef` fields. |
| **Client library**        | [rabbitmq/amqp091-go](https://github.com/rabbitmq/amqp091-go)                                                                                        | [Azure/go-amqp](https://github.com/Azure/go-amqp)                                                                                 |
| **Delivery guarantee**    | At-least-once (manual ack after FlowRun creation)                                                                                                    | At-least-once (message accepted after FlowRun creation)                                                                           |

**Choosing the right version:**

- If your broker is **RabbitMQ**, use `version: "0-9-1"` (the default). RabbitMQ does support AMQP 1.0 via a plugin, but its native protocol is 0-9-1.
- If your broker is **ActiveMQ Artemis, Solace, Azure Service Bus, or IBM MQ**, use `version: "1.0"`.
- If you are unsure, check your broker's documentation for which AMQP version it supports natively.

---

## Troubleshooting

### Gateway Pod Not Starting

**Symptom:** `kubectl get deployment -n default -l kubezap.io/component=amqp-gateway` shows 0/1 ready replicas.

1. Check the pod events:
   ```bash
   kubectl describe pod -n default -l kubezap.io/component=amqp-gateway
   ```
2. Common causes:
   - **ImagePullBackOff**: The `kubezap/amqp-gateway` image is not accessible. Check your image registry and pull secrets.
   - **CrashLoopBackOff**: The gateway is starting but failing to connect. Check the logs (see below).
   - **Pending**: Insufficient cluster resources. Check node capacity with `kubectl describe nodes`.

### Authentication Failures

**Symptom:** Gateway logs show `403 ACCESS_REFUSED` (RabbitMQ) or `sasl negotiation failed` (AMQP 1.0).

1. Verify the Secret exists and contains the correct keys:
   ```bash
   kubectl get secret rabbitmq-credentials -n default -o jsonpath='{.data.username}' | base64 -d
   ```
2. Confirm the credentials work by connecting manually:
   ```bash
   # RabbitMQ
   kubectl run --rm -it amqp-test --image=rabbitmq:3-management --restart=Never -- \
     rabbitmqadmin -H rabbitmq.default.svc.cluster.local -u kubezap -p changeme list queues

   # Artemis
   kubectl run --rm -it amqp-test --image=quay.io/artemiscloud/activemq-artemis-broker:latest --restart=Never -- \
     ./bin/artemis check node --url tcp://artemis.default.svc.cluster.local:61616 --user kubezap --password changeme
   ```
3. For AMQP 0-9-1, ensure the user has permission on the vhost specified in the URL (the path component after the port, e.g., `/production`).

### Message Not Triggering a FlowRun

**Symptom:** Messages are published to the queue but no FlowRun appears.

1. **Check the gateway logs** for errors:
   ```bash
   kubectl logs -n default -l kubezap.io/component=amqp-gateway --tail=50
   ```
   Look for lines containing `"error"` or `"failed"`.

2. **Verify the Trigger is enabled and ready**:
   ```bash
   kubectl get trigger order-events -n default -o wide
   ```
   If the `Ready` condition is `False`, the gateway will not subscribe.

3. **Confirm the topic/queue name matches** between the Trigger and where you are publishing. For AMQP 0-9-1, the `topic` field is the queue name, not an exchange name. If you are publishing to an exchange, ensure a binding exists that routes to the queue name in the Trigger.

4. **Check that the Flow exists** and is referenced correctly:
   ```bash
   kubectl get flow process-order -n default
   ```
   If the Flow does not exist, the gateway will create the FlowRun but it will fail during execution.

5. **Verify the Integration is not `Failed`**:
   ```bash
   kubectl describe integration rabbitmq -n default
   ```

### Connection Drops and Reconnection

The AMQP gateway uses exponential backoff for reconnection (starting at 5 seconds, maxing at 60 seconds). If you see repeated connection errors in the logs:

1. Check broker health and network connectivity from the gateway pod:
   ```bash
   kubectl exec -n default deploy/kubezap-amqp-gateway-rabbitmq -- nc -zv rabbitmq.default.svc.cluster.local 5672
   ```
2. If using TLS, verify the CA certificate has not expired:
   ```bash
   kubectl get secret amqp-ca -n default -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -dates
   ```

### Dead-Letter Handling

KubeZap does not configure dead-letter queues (DLQs) automatically. If a message fails processing and the FlowRun enters a `Failed` phase, the message has already been acknowledged (to avoid infinite redelivery loops). To handle failures:

1. **Configure DLQ on the broker side.** For RabbitMQ, set `x-dead-letter-exchange` and `x-dead-letter-routing-key` on the queue. For Artemis, configure a `dead-letter-address` in `broker.xml`.
2. **Use Flow-level error handling** to catch step failures and route them to an error-handling Flow or publish to a DLQ via a publish step.
3. **Monitor FlowRun failures** with Prometheus metrics or by watching FlowRun status:
   ```bash
   kubectl get flowruns -n default --field-selector status.phase=Failed
   ```

> **Note:** Design your Flows to be idempotent. AMQP provides at-least-once delivery, so a message may be delivered more than once if the gateway restarts between consuming the message and acknowledging it.
