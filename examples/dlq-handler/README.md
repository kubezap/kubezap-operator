# Dead-Letter Queue Handler

This example demonstrates an automated **DLQ re-delivery pipeline** — failed
messages that land on a dead-letter topic are automatically re-published to the
original topic, with an escalation path that fires when a message has been
retried too many times.

---

## What you'll build

```
Kafka topic: orders.dlq
        |
        v
  +---------------+
  | extract-error |  transform: pull orderId, retry_count, error from message
  +------+--------+
         |
         v
  +---------------+
  | republish-    |  type: publish -> Kafka topic: orders
  | order         |  (adds X-Retry-Attempt + X-DLQ-Reason headers)
  +------+--------+
         |
         v
  +---------------+
  | escalate      |  when: retryCount > 3
  |               |  HTTP POST to escalation endpoint (Mockoon mock)
  +---------------+  (retries 2x with exponential backoff)
```

Key properties:
- **Dedup**: each Kafka message produces exactly one FlowRun named
  `orders-dlq-trigger-p<partition>-offset-<offset>`. Replaying the topic is safe.
- **Re-delivery**: every DLQ message is re-published to the `orders` topic with
  retry metadata in headers, allowing downstream consumers to track replays.
- **Conditional escalation**: the escalate step only fires when `retry_count > 3`,
  preventing noise for messages on their first few retries.
- **Retry on escalation**: the HTTP escalation call retries up to 2 times with
  exponential backoff (1s initial, 10s max).

---

## Prerequisites

- A running Kubernetes cluster with KubeZap installed
- A Kafka cluster accessible from within the cluster (e.g., Strimzi)
- Two Kafka topics: `orders.dlq` and `orders`
- `kubectl` configured with access to the target namespace
- Optional: a Kubernetes Secret for SASL credentials (see commented fields in
  `integration.yaml`)

This example uses the namespace `default`. Change the `namespace:` field in the
manifests if you prefer a dedicated namespace.

---

## Setup

### Create the Kafka topics

If you are using Strimzi, create the topics via KafkaTopic CRs:

```yaml
apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaTopic
metadata:
  name: orders
  namespace: kafka
  labels:
    strimzi.io/cluster: my-cluster
spec:
  partitions: 3
  replicas: 1
---
apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaTopic
metadata:
  name: orders.dlq
  namespace: kafka
  labels:
    strimzi.io/cluster: my-cluster
spec:
  partitions: 3
  replicas: 1
```

Or via the CLI:

```bash
kafka-topics.sh --bootstrap-server my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
  --create --topic orders --partitions 3 --replication-factor 1

kafka-topics.sh --bootstrap-server my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
  --create --topic orders.dlq --partitions 3 --replication-factor 1
```

### Apply the manifests

```bash
kubectl apply -k examples/dlq-handler/
```

This creates:
- `Integration/orders-kafka` -- points KubeZap at your Kafka cluster
- Mockoon deployment (mock escalation endpoint) -- simulates a ticketing system
- `Flow/handle-dlq-message` -- the DLQ handler workflow
- `Trigger/orders-dlq-trigger` -- subscribes to the `orders.dlq` Kafka topic

> **Point at a real Kafka cluster**: edit `integration.yaml` and replace the
> bootstrap server address with your broker. For TLS/SASL, see the commented-out
> fields in the manifest.

---

## Sending a test message

Produce a poison message to the `orders.dlq` topic:

```bash
echo '{"orderId":"ord-fail-001","retry_count":1,"error":"payment-timeout"}' | \
  kafka-console-producer.sh \
  --bootstrap-server my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
  --topic orders.dlq
```

Or with `kcat`:

```bash
echo '{"orderId":"ord-fail-001","retry_count":1,"error":"payment-timeout"}' | \
  kcat -P -b my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 -t orders.dlq
```

---

## Watching execution

```bash
kubectl get flowruns -n default -w
```

Within a few seconds you should see a FlowRun appear with a name like:

```
orders-dlq-trigger-p0-offset-0
```

The name encodes the partition and offset -- this is the **dedup key**. If the
message is replayed (same partition + offset), Kubernetes will reject the
duplicate `Create` call and the controller will reconcile the existing FlowRun
rather than create a new one.

Check the step-level results:

```bash
kubectl get flowrun orders-dlq-trigger-p0-offset-0 -o yaml
```

Expected status (with `retry_count: 1`, the escalate step is skipped):

```yaml
status:
  phase: Succeeded
  stepResults:
    extract-error:
      orderId: ord-fail-001
      retryCount: 1
      errorReason: payment-timeout
  conditions:
    - type: Succeeded
      status: "True"
```

The `republish-order` step runs (re-publishes to `orders`), while the `escalate`
step is `Skipped` because `retryCount` (1) does not exceed 3.

---

## Testing the escalation path

Produce a message with a high retry count:

```bash
echo '{"orderId":"ord-fail-002","retry_count":5,"error":"payment-timeout"}' | \
  kafka-console-producer.sh \
  --bootstrap-server my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
  --topic orders.dlq
```

This time the `escalate` step fires because `5 > 3`. A new FlowRun
`orders-dlq-trigger-p0-offset-1` (or similar) will appear with all three steps
in `Succeeded` phase.

Inspect the Mockoon logs to confirm the escalation request was received:

```bash
kubectl exec -n default \
  $(kubectl get pod -n default -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3001/api/logs | jq .
```

You should see a `POST /escalate` request with the body:

```json
{
  "orderId": "ord-fail-002",
  "retryCount": 5,
  "reason": "payment-timeout"
}
```

---

## Verify re-publish

Consume from the `orders` topic to confirm the replayed message arrived:

```bash
kcat -C -b my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
  -t orders -e -q | jq .
```

Expected: the original DLQ message payload, with Kafka headers
`X-Retry-Attempt` and `X-DLQ-Reason` set by the `republish-order` step.

---

## Cleaning up

```bash
kubectl delete -k examples/dlq-handler/
```

The controller will also remove the `kubezap-kafka-gateway` Deployment once no
Triggers remain that reference the `orders-kafka` Integration.

---

## What's next

- **Add alerting**: replace the Mockoon escalation endpoint with a real ticketing
  system (PagerDuty, Jira, Slack).
- **Dead-letter the dead-letter**: add a final step that publishes to
  `orders.dlq.permanent` when retry_count exceeds a higher threshold (e.g., 10).
- **Add metrics dashboards**: use the `kubezap_flowrun_duration_seconds` and
  `kubezap_step_outcome_total` Prometheus metrics to build a DLQ health dashboard.
- Explore the [kafka-enrichment example](../kafka-enrichment/) for a full
  enrichment + conditional routing pipeline.
