# NATS Gateway Setup

This guide walks through connecting KubeZap to a NATS cluster for event-driven workflow automation. KubeZap supports both NATS Core (plain subject subscriptions, at-most-once delivery) and NATS JetStream (durable consumers with at-least-once delivery and deduplication).

---

## Contents

- [Prerequisites](#prerequisites)
- [Creating a NATS Integration](#creating-a-nats-integration)
  - [NATS Core Integration](#nats-core-integration)
  - [NATS JetStream Integration](#nats-jetstream-integration)
  - [Verifying the Integration](#verifying-the-integration)
- [Creating a NATS Trigger](#creating-a-nats-trigger)
  - [NATS Core Trigger](#nats-core-trigger)
  - [NATS JetStream Trigger](#nats-jetstream-trigger)
  - [Verifying the Gateway Deployment](#verifying-the-gateway-deployment)
- [Publishing a Test Message and Watching the FlowRun](#publishing-a-test-message-and-watching-the-flowrun)
  - [NATS Core](#nats-core)
  - [NATS JetStream](#nats-jetstream)
- [JetStream Dedup Keys and At-Least-Once Delivery](#jetstream-dedup-keys-and-at-least-once-delivery)
  - [FlowRun Naming and Deduplication](#flowrun-naming-and-deduplication)
  - [Redelivery Behavior](#redelivery-behavior)
  - [Consumer Ack Behavior](#consumer-ack-behavior)
- [TLS and Credential Options](#tls-and-credential-options)
  - [TLS with a Custom CA](#tls-with-a-custom-ca)
  - [NKey / User JWT Credentials](#nkey--user-jwt-credentials)
  - [Username and Password](#username-and-password)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

Before you begin, ensure the following are in place:

1. **NATS cluster running** -- either [NATS Server](https://nats.io/) (for Core) or NATS with JetStream enabled. The [nats Helm chart](https://github.com/nats-io/k8s/tree/main/helm/charts/nats) is the recommended way to deploy NATS on Kubernetes:

   ```bash
   helm repo add nats https://nats-io.github.io/k8s/helm/charts/
   helm repo update

   # Core NATS only:
   helm install nats nats/nats -n infra --create-namespace

   # NATS with JetStream enabled:
   helm install nats nats/nats -n infra --create-namespace \
     --set config.jetstream.enabled=true \
     --set config.jetstream.fileStore.pvc.size=10Gi
   ```

2. **KubeZap operator installed** in the `kubezap-system` namespace. See the [installation guide](../overview.md) for details.

3. **User namespace created** where your Triggers and Flows will live:

   ```bash
   kubectl create namespace default
   ```

4. **(Optional) NATS CLI** for publishing test messages:

   ```bash
   # macOS
   brew install nats-io/nats-tools/nats

   # Or download from https://github.com/nats-io/natscli/releases
   ```

---

## Creating a NATS Integration

An `Integration` with `type: nats` stores the NATS connection details and credentials separately from your Triggers. This allows multiple Triggers to share the same NATS cluster connection and simplifies credential rotation.

See [Integration CRD -- NATS section](../api/integration.md#nats-type-nats-beta) for the full spec reference.

### NATS Core Integration

For plain NATS with no persistence or durable consumers:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-core
  namespace: default
spec:
  type: nats
  nats:
    # NATS server URLs. List multiple for cluster failover.
    servers:
      - nats://nats.infra.svc.cluster.local:4222
```

Apply it:

```bash
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-core
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats.infra.svc.cluster.local:4222
EOF
```

### NATS JetStream Integration

For durable, at-least-once delivery with deduplication, enable `jetStream: true`:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-jetstream
  namespace: default
spec:
  type: nats
  nats:
    # Multiple servers for cluster failover
    servers:
      - nats://nats-0.nats.infra.svc.cluster.local:4222
      - nats://nats-1.nats.infra.svc.cluster.local:4222
      - nats://nats-2.nats.infra.svc.cluster.local:4222
    # Enable JetStream for durable consumers
    jetStream: true
    # NKey or User JWT credentials file (optional -- see TLS section below)
    credentialsSecretRef:
      name: nats-kubezap-creds   # must contain key: nats.creds
```

> **Note:** The NATS server must have JetStream enabled (`jetstream: enabled` in its config or the Helm `config.jetstream.enabled=true` value). If JetStream is not enabled on the server, the gateway will fail to create a JetStream context and log an error.

Apply it:

```bash
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-jetstream
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats-0.nats.infra.svc.cluster.local:4222
      - nats://nats-1.nats.infra.svc.cluster.local:4222
      - nats://nats-2.nats.infra.svc.cluster.local:4222
    jetStream: true
EOF
```

### Verifying the Integration

Check that the Integration is ready:

```bash
kubectl get integration -n default
```

Expected output:

```
NAME             TYPE   READY   AGE
nats-core        nats   True    30s
nats-jetstream   nats   True    15s
```

For detailed status:

```bash
kubectl describe integration nats-jetstream -n default
```

---

## Creating a NATS Trigger

A Trigger with `type: nats` subscribes to NATS subjects and creates a `FlowRun` for each message received.

See [Trigger CRD -- NatsTrigger](../api/trigger.md#natstrigger) for the full spec reference.

### NATS Core Trigger

Subscribe to a plain NATS subject. Supports wildcards (`orders.*`, `events.>`):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events
  namespace: default
spec:
  type: nats
  nats:
    # Reference to the Integration with connection details
    integrationRef:
      name: nats-core
    # NATS subject to subscribe to (supports wildcards)
    subject: "orders.created"
    # topic is also accepted as a fallback if subject is not set
  flowRef:
    name: process-order
```

Apply:

```bash
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events
  namespace: default
spec:
  type: nats
  nats:
    integrationRef:
      name: nats-core
    subject: "orders.created"
  flowRef:
    name: process-order
EOF
```

> **Note:** The `subject` field is NATS-specific. If omitted, the gateway falls back to the generic `topic` field. For clarity, prefer `subject` when using NATS.

### NATS JetStream Trigger

For JetStream, the gateway creates a durable consumer with explicit acknowledgment. The durable name is automatically derived from the Trigger name (`kubezap-<trigger-name>`, truncated to 32 characters):

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events-js
  namespace: default
spec:
  type: nats
  nats:
    integrationRef:
      name: nats-jetstream
    # Subject must match a subject bound to an existing JetStream stream
    subject: "orders.created"
  flowRef:
    name: process-order
```

> **Note:** You must create the JetStream stream before the Trigger. The gateway subscribes to the subject within the stream; it does not create streams. Use the NATS CLI or your NATS management tooling to create streams:
>
> ```bash
> nats stream add ORDERS \
>   --subjects "orders.*" \
>   --retention limits \
>   --max-msgs=-1 \
>   --max-bytes=-1 \
>   --max-age=72h \
>   --storage file \
>   --replicas 3 \
>   --server nats://nats.infra.svc.cluster.local:4222
> ```

Apply:

```bash
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events-js
  namespace: default
spec:
  type: nats
  nats:
    integrationRef:
      name: nats-jetstream
    subject: "orders.created"
  flowRef:
    name: process-order
EOF
```

### Verifying the Gateway Deployment

The KubeZap controller creates a NATS gateway Deployment for each (namespace, NATS Integration) pair. Verify it is running:

```bash
kubectl get deployments -n default -l kubezap.io/component=nats-gateway
```

Expected output:

```
NAME                              READY   UP-TO-DATE   AVAILABLE   AGE
kubezap-nats-gateway-nats-core    1/1     1            1           45s
```

Check the gateway pod logs:

```bash
kubectl logs -n default -l kubezap.io/component=nats-gateway --tail=20
```

You should see lines like:

```
{"level":"info","msg":"nats watcher started","namespace":"default"}
{"level":"info","msg":"nats subscription started","trigger":"default/order-events","subject":"orders.created","jetStream":false}
```

Check the Trigger status:

```bash
kubectl get trigger order-events -n default -o wide
```

---

## Publishing a Test Message and Watching the FlowRun

### NATS Core

Open two terminal windows.

**Terminal 1** -- watch for FlowRuns:

```bash
kubectl get flowruns -n default -w
```

**Terminal 2** -- publish a test message using the NATS CLI (port-forward if needed):

```bash
# Port-forward to the NATS service
kubectl port-forward -n infra svc/nats 4222:4222 &

# Publish a message
nats pub orders.created '{"orderId": "ord-123", "amount": 49.99}' \
  --server nats://localhost:4222
```

In Terminal 1, you should see a FlowRun appear:

```
NAME                                      FLOW            STATUS    AGE
order-events-1711036800000000000-a1b2c3d4 process-order   Pending   0s
```

### NATS JetStream

The process is the same -- publish to a subject that is bound to a JetStream stream:

**Terminal 1** -- watch for FlowRuns:

```bash
kubectl get flowruns -n default -w
```

**Terminal 2** -- publish a message (the stream must already exist and capture the subject):

```bash
kubectl port-forward -n infra svc/nats 4222:4222 &

nats pub orders.created '{"orderId": "ord-456", "amount": 99.99}' \
  --server nats://localhost:4222
```

In Terminal 1, the FlowRun name includes the JetStream consumer sequence number:

```
NAME                         FLOW            STATUS    AGE
order-events-js-seq-1        process-order   Pending   0s
```

Publish another message and observe the sequence increment:

```
NAME                         FLOW            STATUS    AGE
order-events-js-seq-1        process-order   Running   10s
order-events-js-seq-2        process-order   Pending   0s
```

---

## JetStream Dedup Keys and At-Least-Once Delivery

### FlowRun Naming and Deduplication

The NATS gateway uses different FlowRun naming schemes depending on the delivery mode:

| Mode      | FlowRun Name Pattern                       | Deterministic | Dedup |
| --------- | ------------------------------------------ | ------------- | ----- |
| Core NATS | `<trigger>-<timestamp-nanos>-<random-hex>` | No            | No    |
| JetStream | `<trigger>-seq-<consumer-sequence-number>` | Yes           | Yes   |

For JetStream, the consumer sequence number is a monotonically increasing integer assigned by the NATS server. Because the FlowRun name is deterministic (derived from the trigger name and sequence), Kubernetes rejects `AlreadyExists` errors on duplicate creation attempts. This provides natural deduplication: if the same message is redelivered, the gateway attempts to create a FlowRun with the same name, the API server returns `AlreadyExists`, and the gateway treats it as a no-op.

For Core NATS, names include a random component and timestamp, so there is no deduplication guarantee. Each message delivery produces a new FlowRun.

### Redelivery Behavior

When a JetStream message is redelivered (because the consumer did not acknowledge it in time), the gateway:

1. Receives the message again with the same consumer sequence number
2. Attempts to create a FlowRun with the same deterministic name (e.g., `order-events-js-seq-42`)
3. If the FlowRun already exists, the gateway logs a debug message and moves on
4. If the original FlowRun was deleted (e.g., by TTL garbage collection), a new FlowRun is created and the Flow executes again

> **Note:** Design your Flows to be idempotent. While JetStream dedup prevents duplicate FlowRuns from being created simultaneously, edge cases such as FlowRun GC followed by redelivery can cause re-execution.

### Consumer Ack Behavior

The NATS gateway uses explicit acknowledgment (`AckExplicit`) for JetStream consumers:

- **Ack**: Sent after the FlowRun CRD is successfully created (or if it already exists). This tells the NATS server the message has been processed.
- **Nak**: Sent if the FlowRun creation fails for any reason other than `AlreadyExists` (e.g., API server unreachable). The NATS server will redeliver the message after the consumer's `AckWait` period.

Core NATS subscriptions have no acknowledgment mechanism -- messages are fire-and-forget.

---

## TLS and Credential Options

### TLS with a Custom CA

If your NATS cluster uses TLS with an internal or private CA, create a Secret containing the CA certificate and reference it in the Integration:

```bash
kubectl create secret generic nats-ca-cert \
  --from-file=ca.crt=/path/to/ca.pem \
  -n default
```

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-tls
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats.infra.svc.cluster.local:4222
    jetStream: true
    tls:
      # Reference to the Secret containing the CA certificate
      caSecretRef:
        name: nats-ca-cert
        key: ca.crt
```

For mTLS (mutual TLS), add a client certificate Secret:

```bash
kubectl create secret tls nats-client-cert \
  --cert=/path/to/client.crt \
  --key=/path/to/client.key \
  -n default
```

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-mtls
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats.infra.svc.cluster.local:4222
    jetStream: true
    tls:
      caSecretRef:
        name: nats-ca-cert
        key: ca.crt
      clientCertSecretRef:
        name: nats-client-cert   # must contain tls.crt and tls.key
```

> **Note:** For development only, you can disable TLS verification with `tls.insecureSkipVerify: true`. Never use this in production.

### NKey / User JWT Credentials

NATS supports [NKey](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/nkey_auth) and [User JWT](https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/jwt) credential files. These are stored as a single `.creds` file and referenced via `credentialsSecretRef`.

Create the credentials Secret (the key **must** be `nats.creds`):

```bash
kubectl create secret generic nats-kubezap-creds \
  --from-file=nats.creds=/path/to/kubezap-user.creds \
  -n default
```

Reference it in the Integration:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-nkey
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats.infra.svc.cluster.local:4222
    jetStream: true
    # The Secret must contain the key "nats.creds"
    credentialsSecretRef:
      name: nats-kubezap-creds
```

The gateway writes the credentials to a temporary file at startup and removes it when the subscription is stopped. The credentials file is never written to a predictable path.

> **Note:** Credential rotation requires recreating the Secret and restarting the gateway pod (or deleting and re-creating the Trigger to force a subscription restart). Automatic credential rotation is planned.

### Username and Password

For simple username/password authentication (not recommended for production):

```bash
kubectl create secret generic nats-username \
  --from-literal=username=kubezap-user \
  -n default

kubectl create secret generic nats-password \
  --from-literal=password=s3cret \
  -n default
```

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: nats-basic-auth
  namespace: default
spec:
  type: nats
  nats:
    servers:
      - nats://nats.infra.svc.cluster.local:4222
    usernameSecretRef:
      name: nats-username
      key: username
    passwordSecretRef:
      name: nats-password
      key: password
```

> **Note:** Both `usernameSecretRef` and `passwordSecretRef` must be set together. Setting only one will cause the gateway to fail with an error. Prefer NKey or JWT credentials for production deployments.

---

## Troubleshooting

### Gateway not connecting to NATS

**Symptoms:** Gateway pod is running but logs show repeated connection errors.

**Check the gateway logs:**

```bash
kubectl logs -n default -l kubezap.io/component=nats-gateway --tail=50
```

**Common causes:**

- **Wrong server URL**: Verify the `servers` list in the Integration matches your NATS deployment. Use the full in-cluster DNS name (e.g., `nats://nats.infra.svc.cluster.local:4222`).
- **TLS mismatch**: If the NATS server requires TLS but the Integration does not configure `tls`, the connection will be refused. Add a `tls` block with the appropriate CA certificate.
- **Credentials invalid**: If using `credentialsSecretRef`, verify the Secret exists and contains the key `nats.creds`:
  ```bash
  kubectl get secret nats-kubezap-creds -n default -o jsonpath='{.data}' | jq 'keys'
  ```
- **Network policy blocking traffic**: Ensure pods in the user namespace can reach the NATS service in the `infra` namespace on port 4222.

The gateway uses `MaxReconnects(-1)` and a 5-second reconnect wait, so it will retry indefinitely after the initial connection is established.

### Consumer not created (JetStream)

**Symptoms:** Gateway connects to NATS but the JetStream consumer does not appear in `nats consumer ls`.

**Check:**

1. **JetStream is enabled on the server**: `nats server info --server nats://localhost:4222` should show `JetStream: true`.
2. **The Integration has `jetStream: true`**: Without this, the gateway uses Core NATS subscriptions (no JetStream consumer is created).
3. **The stream exists and captures the subject**: The gateway subscribes to a subject within an existing stream. If no stream captures the subject, the subscription will fail:
   ```bash
   nats stream ls --server nats://localhost:4222
   nats stream info ORDERS --server nats://localhost:4222
   ```
   Verify the stream's `Subjects` list includes the Trigger's `subject` (or a wildcard that matches it).

**Durable name**: The gateway creates a durable consumer named `kubezap-<trigger-name>` (truncated to 32 characters). Check if it exists:

```bash
nats consumer info ORDERS kubezap-order-events-js --server nats://localhost:4222
```

### Message not triggering a FlowRun

**Symptoms:** Messages are published to the subject but no FlowRun is created.

**Check:**

1. **Trigger is enabled**: Verify `spec.enabled` is not set to `false`:
   ```bash
   kubectl get trigger order-events -n default -o jsonpath='{.spec.enabled}'
   ```
2. **Subject matches**: The Trigger's `subject` must match what you are publishing to. NATS wildcards (`*` for single token, `>` for multi-token) are supported in the subscription.
3. **FlowRef exists**: The referenced Flow must exist. Check:
   ```bash
   kubectl get flow process-order -n default
   ```
4. **Gateway logs**: Look for `"created FlowRun"` or error messages:
   ```bash
   kubectl logs -n default -l kubezap.io/component=nats-gateway --tail=50
   ```
5. **FlowRun may have been garbage collected**: If `ttlAfterFinished` is very short, the FlowRun may have been created and cleaned up before you checked. Watch in real-time:
   ```bash
   kubectl get flowruns -n default -w
   ```

### JetStream stream not found

**Symptoms:** Gateway logs show `"jetstream subscribe ... : stream not found"` or similar error.

**Fix:** Create the stream before creating the Trigger. Streams are not managed by KubeZap -- they must be created out-of-band using the NATS CLI, Terraform, or your NATS management tooling:

```bash
nats stream add ORDERS \
  --subjects "orders.*" \
  --retention limits \
  --max-msgs=-1 \
  --max-bytes=-1 \
  --max-age=72h \
  --storage file \
  --replicas 3 \
  --server nats://nats.infra.svc.cluster.local:4222
```

After the stream is created, delete and re-create the Trigger (or wait for the next reconciliation cycle) to retry the subscription.

---

**Next steps:**

- [Integration CRD Reference](../api/integration.md) -- full spec for all Integration types
- [Trigger CRD Reference](../api/trigger.md) -- full spec for all Trigger types including PubSubTrigger fields
- [Kafka Integration Reference](../api/integration.md#kafka-type-kafka) -- if you also need Kafka integration
