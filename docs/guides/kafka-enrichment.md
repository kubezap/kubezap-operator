# Demo: Kafka Event Enrichment Pipeline

This guide walks through the **customer event enrichment** demo — a real-world
pattern where raw events arrive on a Kafka topic, are enriched by an API call,
conditionally routed by customer tier, and re-published to an output topic with
the enriched payload.

This is one of KubeZap's strongest differentiators: the entire pipeline is
declared as Kubernetes resources — no custom consumers, no Kafka Streams
topology, no separate enrichment service to operate.

---

## What you'll build

```
Kafka topic: customer-events
        │
        ▼
  ┌─────────────┐
  │ extract-    │  transform: pull customerId + eventType from message body
  │ customer    │
  └──────┬──────┘
         │
         ▼
  ┌─────────────┐
  │ enrich-     │  HTTP: call customer profile API → tier, region, accountManager
  │ profile     │  (retries with exponential backoff)
  └──────┬──────┘
         │
    ┌────┴────┐────────────┐
    ▼         ▼            ▼
enterprise  standard    trial     ← CEL when conditions, steps run in parallel
  sink       sink        sink
    └────┬────┘────────────┘
         │  (all three run; two are Skipped based on tier)
         ▼
  ┌─────────────┐
  │ publish-    │  type: publish → Kafka topic: customer-events-enriched
  │ enriched    │
  └─────────────┘
```

Key properties:
- **Dedup**: each Kafka message produces exactly one FlowRun named
  `customer-events-p<partition>-offset-<offset>`. Replaying the topic is safe.
- **Retry**: the profile API call retries up to 3× with exponential backoff.
- **Conditional routing**: CEL `when` expressions select the correct sink;
  unmatched branches are marked `Skipped` in the FlowRun status.
- **Fan-in**: `publish-enriched` waits for all three routing steps before
  publishing (skipped steps count as resolved).

---

## Prerequisites

- A running Kubernetes cluster with KubeZap installed
- A Kafka cluster accessible from within the cluster
- `kubectl` configured with access to the target namespace

This guide uses the namespace `default`. Change the `namespace:` field in the
manifests if you prefer a dedicated namespace.

---

## Step 1 — Apply the demo manifests

```bash
kubectl apply -k config/samples/demo/kafka-enrichment/
```

This creates:
- `Integration/customer-kafka` — points KubeZap at your Kafka cluster
- `MockEndpoint/customer-profile` — simulates the enrichment API
- `MockEndpoint/enterprise-sink`, `standard-sink`, `trial-sink` — capture
  routed events in CRD status (no external service needed)
- `Flow/enrich-customer-event` — the workflow definition
- `Trigger/customer-events` — subscribes to the `customer-events` Kafka topic

> **Point at a real Kafka cluster**: edit `integration.yaml` and replace
> `kafka.kafka.svc.cluster.local:9092` with your broker address. For TLS/SASL,
> see the commented-out fields in the manifest.

---

## Step 2 — Check the Trigger is accepted

```bash
kubectl get trigger customer-events -o jsonpath='{.status.conditions}'
```

Expected: condition `type: Accepted, status: True`.

The controller also creates a `kubezap-kafka-gateway` Deployment in the
namespace — one per (namespace × Kafka cluster). Check it's running:

```bash
kubectl get deployment kubezap-kafka-gateway
kubectl get pods -l app.kubernetes.io/component=kafka-gateway
```

---

## Step 3 — Produce a test message

```bash
# Using kafka-console-producer (adjust --bootstrap-server to match your cluster)
echo '{"customerId":"cust-001","eventType":"signed_up"}' | \
  kafka-console-producer.sh \
  --bootstrap-server kafka.kafka.svc.cluster.local:9092 \
  --topic customer-events
```

Or with `kcat`:

```bash
echo '{"customerId":"cust-001","eventType":"signed_up"}' | \
  kcat -P -b kafka.kafka.svc.cluster.local:9092 -t customer-events
```

---

## Step 4 — Watch the FlowRun

```bash
kubectl get flowruns -l kubezap.io/trigger=customer-events -w
```

Within a few seconds you should see a FlowRun appear with a name like:

```
customer-events-p0-offset-0
```

The name encodes the partition and offset — this is the **dedup key**. If the
message is replayed (same partition + offset), Kubernetes will reject the
duplicate `Create` call and the controller will reconcile the existing
FlowRun rather than create a new one.

Check the step-level results:

```bash
kubectl get flowrun customer-events-p0-offset-0 -o yaml
```

Look for `status.stepResults`:

```yaml
status:
  phase: Succeeded
  stepResults:
    extract-customer:
      customerId: cust-001
      eventType: signed_up
    enrich-profile:
      tier: enterprise
      region: us-east-1
      accountManager: alice@example.com
  conditions:
    - type: Succeeded
      status: "True"
```

---

## Step 5 — Inspect routed steps

Because the mock profile API returns `tier: enterprise`, the
`route-enterprise` step runs and `route-standard` / `route-trial` are skipped:

```bash
kubectl get flowrun customer-events-p0-offset-0 \
  -o jsonpath='{.status.steps[*]}'
```

```
route-enterprise → Succeeded
route-standard   → Skipped  (when: tier == "standard" was false)
route-trial      → Skipped  (when: tier == "trial" was false)
publish-enriched → Succeeded
```

Check what the enterprise sink captured:

```bash
kubectl get mockendpoint enterprise-sink \
  -o jsonpath='{.status.requests[-1:]}' | jq .
```

---

## Step 6 — Verify the enriched event on the output topic

```bash
kcat -C -b kafka.kafka.svc.cluster.local:9092 \
  -t customer-events-enriched -e -q | jq .
```

Expected:

```json
{
  "customerId": "cust-001",
  "eventType": "signed_up",
  "tier": "enterprise",
  "region": "us-east-1",
  "accountManager": "alice@example.com"
}
```

Every downstream consumer reading `customer-events-enriched` gets a
guaranteed-enriched event without needing to call the profile API themselves.

---

## Trying other tiers

The mock profile endpoint always returns `enterprise`. To test the other
branches, override the mock response by editing the MockEndpoint:

```bash
# Test standard tier
kubectl patch mockendpoint customer-profile --type=merge -p '
spec:
  response:
    body: |
      {"customerId":"{{.Body.customerId}}","tier":"standard","region":"eu-west-1","accountManager":"bob@example.com"}'

# Produce another message
echo '{"customerId":"cust-002","eventType":"upgraded"}' | \
  kafka-console-producer.sh \
  --bootstrap-server kafka.kafka.svc.cluster.local:9092 \
  --topic customer-events
```

A new FlowRun `customer-events-p0-offset-1` will appear with `route-standard`
as the active branch.

---

## Retry behaviour

The `enrich-profile` step has a retry policy:

```yaml
retryPolicy:
  maxRetries: 3
  backoffType: Exponential
  initialDelay: 500ms
  maxDelay: 10s
```

To see it in action, temporarily make the mock return a 503:

```bash
kubectl patch mockendpoint customer-profile --type=merge -p '
spec:
  response:
    status: 503
    body: "unavailable"'
```

Produce a message and watch the FlowRun status — the `enrich-profile` step
will show retry attempts in `status.steps[enrich-profile].attempts` before
eventually failing.

Restore the mock to recover:

```bash
kubectl patch mockendpoint customer-profile --type=merge -p '
spec:
  response:
    status: 200
    body: |
      {"customerId":"{{.Body.customerId}}","tier":"enterprise","region":"us-east-1","accountManager":"alice@example.com"}'
```

---

## Observability

### Prometheus metrics

The `kubezap_flowrun_duration_seconds` histogram shows end-to-end pipeline
latency. The `kubezap_step_outcome_total` counter shows per-step success/skip/fail
counts.

```bash
# Port-forward to the controller metrics endpoint
kubectl port-forward -n kubezap-system svc/kubezap-controller-manager-metrics-service 8443:8443

curl -sk https://localhost:8443/metrics | grep kubezap_step_outcome
```

### OTel traces

Each FlowRun produces a root span `flowrun.execute` with child spans per step.
If you have Jaeger or a compatible backend configured via `OTEL_EXPORTER_OTLP_ENDPOINT`,
you can search by `flowrun.name = customer-events-p0-offset-0` to see the full trace.

See [Observability guide](observability.md) for full setup.

---

## Cleaning up

```bash
kubectl delete -k config/samples/demo/kafka-enrichment/
```

The controller will also remove the `kubezap-kafka-gateway` Deployment once no
Triggers remain that reference the `customer-kafka` Integration.

---

## What's next

- **Add a dead-letter step**: add a final step with `when: steps.enrich_profile.status == "Failed"` that publishes to a `customer-events-dlq` topic.
- **Replace mocks with real services**: swap MockEndpoint URLs for your actual enrichment API and routing targets.
- **Replace mock sinks with publish steps**: route-enterprise could use `type: publish` to write directly to an `enterprise-events` Kafka topic.
- **[GitOps deployment gate demo](gitops-deploy-gate.md)** ← coming soon
