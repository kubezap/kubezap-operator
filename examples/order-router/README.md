# Getting Started with KubeZap

This example walks you through a complete working scenario that demonstrates the
core KubeZap feature set: a webhook trigger, multi-step flow with data
transformation, conditional branching, and a mock notification endpoint for
development testing.

By the end you will have:

- A webhook endpoint that accepts order events
- A flow that inspects the order type and routes to different notification paths
- A MockEndpoint that captures the notification so you can verify the result without a real Slack

---

## Prerequisites

- A running Kubernetes cluster (k3s, kind, or any cluster with CRD support)
- `kubectl` configured to reach the cluster
- KubeZap operator deployed (see [Installation](../../docs/overview.md#installation))
- The webhook gateway accessible — either via `kubectl port-forward` or a Service of type LoadBalancer

---

## The Scenario

```
POST /hooks/order-placed
        │
        ▼
   [transform]  extract orderType from body
        │
        ▼
   [http]  enrich order from internal API (or mock)
        │
        ├── when orderType == "express" ──► POST /mock/notify-express
        │
        └── when orderType == "standard" ─► POST /mock/notify-standard
```

Two conditional branches, each verified via a MockEndpoint. The MockEndpoints capture
the request so you can inspect exactly what was sent.

---

## Step 1: Apply the manifests

```bash
kubectl apply -k examples/order-router/
```

This creates:
- `MockEndpoint/enrich-order` — simulates the enrichment API with a `responseSequence` that alternates between express and standard responses
- `MockEndpoint/notify-express` and `MockEndpoint/notify-standard` — capture routed notifications
- `Flow/order-router` — the four-step workflow
- `Trigger/order-placed` — the webhook trigger on `/hooks/order-placed`

The MockEndpoints register routes on the webhook gateway automatically. Check the gateway logs to confirm:

```bash
kubectl logs -l app=kubezap-webhook-gateway -n default | grep mock
```

---

## Step 2: Verify the Trigger is accepted

```bash
kubectl get trigger order-placed \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

If the status is not `True` after a few seconds, check the controller logs:

```bash
kubectl logs -l control-plane=controller-manager -n kubezap-system
```

---

## Step 3: Send a test request

Forward the webhook gateway port if not already externally accessible:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Fire the first request (the enrich mock's `responseSequence` will return `tier: express`
on the first call, routing to the express path):

```bash
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-001" \
  -d '{"event":"order.placed","orderId":"ord-001"}'
```

Watch the FlowRun:

```bash
kubectl get flowruns -w
```

---

## Step 4: Inspect step results

Once the FlowRun completes:

```bash
# See which steps ran and which were skipped
kubectl get flowrun <name> \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
```

Expected output:

```
extract-type    Succeeded
enrich-order    Succeeded
notify-express  Succeeded
notify-standard Skipped   ← when condition was false
```

`notify-standard` is `Skipped` because the `when` expression (`tier == "standard"`)
was false. Skipped steps are not failures — the FlowRun phase is still `Succeeded`.

> **Note:** The `when` field uses CEL expressions evaluated by the FlowRun reconciler.
> See [Flow API → Conditions and CEL](../../docs/api/flow.md#conditions-and-cel)
> for the full variable reference.

---

## Step 5: Inspect captured MockEndpoint requests

The MockEndpoints record everything they receive:

```bash
kubectl get mockendpoint notify-express \
  -o jsonpath='{.status.recentRequests}' | jq .
```

Expected:

```json
[
  {
    "timestamp": "...",
    "method": "POST",
    "path": "/mock/notify-express",
    "body": "{\"customerId\":\"cust-001\",\"message\":\"Express order dispatched\"}",
    "responseStatusCode": 200
  }
]
```

Fire a second request — the enrich mock will now return `tier: standard`, so
`notify-standard` runs instead:

```bash
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-002" \
  -d '{"event":"order.placed","orderId":"ord-002"}'
```

---

## What this demonstrates

| Feature | How it shows up |
|---------|-----------------|
| Webhook trigger | `curl` to `/hooks/order-placed` creates a FlowRun |
| Transform step | `extract-type` reshapes the trigger body |
| Step result passing | `$(steps.enrich_order.results.*)` used in later steps |
| Conditional branching | `when: expression` routes to express vs standard |
| Skipped steps | One notify step is always `Skipped` |
| MockEndpoint capture | Inspect received payloads via `kubectl get mockendpoint` |
| responseSequence | Cycles through mock responses to demo both paths |

---

## Troubleshooting

**FlowRun stuck in `Running`**

Check the controller logs:

```bash
kubectl logs -l control-plane=controller-manager -n kubezap-system
```

**Gateway not registering routes**

Check the gateway logs and confirm the Trigger has `status.conditions[Accepted]=True`.

**MockEndpoint not receiving requests**

Verify the gateway ServiceAccount has permission to patch `mockendpoints/status`:

```bash
kubectl auth can-i patch mockendpoints/status \
  --as=system:serviceaccount:default:kubezap-webhook-gateway
```

If not, see [Architecture → Gateway RBAC](../../docs/architecture.md#gateway-serviceaccount-and-rbac).

---

## Cleanup

```bash
kubectl delete -k examples/order-router/
```

---

## Next steps

- Add [HMAC authentication](../../docs/guides/webhook-security.md) to the trigger
- Add a cron trigger that runs the flow on a schedule
- Replace the MockEndpoints with real downstream services
- Set up [Prometheus metrics](../../docs/guides/observability.md) to track FlowRun durations
- Explore the [Kafka enrichment example](../kafka-enrichment/) for a message-broker-driven pipeline
