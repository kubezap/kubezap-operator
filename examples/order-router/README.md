# Getting Started with KubeZap

This example walks you through a complete working scenario that demonstrates the
core KubeZap feature set: a webhook trigger, multi-step flow with data
transformation, conditional branching, and a Mockoon mock server for development
testing.

By the end you will have:

- A webhook endpoint that accepts order events
- A flow that inspects the order type and routes to different notification paths
- A Mockoon in-cluster mock server that captures notifications so you can verify
  the result without a real downstream service

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
   [http]  enrich order from internal API (Mockoon /enrich-order)
        │
        ├── when tier == "express" ──► POST /notify-express
        │
        └── when tier == "standard" ─► POST /notify-standard
```

Two conditional branches, each captured by Mockoon. The `/enrich-order` route
cycles between express and standard responses (Mockoon's `SEQUENTIAL` mode),
so the first request routes to express and the second to standard.

---

## Step 1: Apply the manifests

```bash
kubectl apply -k examples/order-router/
```

This creates:
- `ConfigMap/mockoon-env` — Mockoon environment file with three stub routes
- `Deployment/mockoon` + `Service/mockoon` — in-cluster Mockoon mock server
- `Flow/order-router` — the four-step workflow
- `Trigger/order-placed` — the webhook trigger on `/hooks/order-placed`

Wait for the Mockoon pod to be ready:

```bash
kubectl get pods -l app=mockoon -n default
# Expected: mockoon-<hash>   1/1   Running
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

Fire the first request (the `/enrich-order` mock returns `tier: express` on the
first call, routing to the express path):

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

## Step 5: Inspect captured requests via Mockoon

The Mockoon admin API records all requests it receives:

```bash
kubectl exec -n default \
  $(kubectl get pod -n default -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3000/mockoon-admin/logs \
  | jq '[.[] | select(.url == "/notify-express")]'
```

Expected:

```json
[
  {
    "UUID": "...",
    "timestamp": "...",
    "method": "POST",
    "url": "/notify-express",
    "body": "{\"customerId\":\"cust-001\",\"message\":\"Express order dispatched\"}",
    "response": {
      "status": 200,
      "body": "{\"notified\":true,\"tier\":\"express\"}"
    }
  }
]
```

You can also stream Mockoon logs in real time:

```bash
kubectl logs -n default -l app=mockoon -f
```

Fire a second request — the enrich mock returns `tier: standard` on the second
call (SEQUENTIAL mode), so `notify-standard` runs instead:

```bash
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-002" \
  -d '{"event":"order.placed","orderId":"ord-002"}'
```

---

## What this demonstrates

| Feature                 | How it shows up                                                |
| ----------------------- | -------------------------------------------------------------- |
| Webhook trigger         | `curl` to `/hooks/order-placed` creates a FlowRun              |
| Transform step          | `extract-type` reshapes the trigger body                       |
| Step result passing     | `$(steps.enrich_order.results.*)` used in later steps          |
| Conditional branching   | `when: expression` routes to express vs standard               |
| Skipped steps           | One notify step is always `Skipped`                            |
| Mockoon request capture | Inspect received payloads via `GET /api/logs` on the admin API |
| SEQUENTIAL responses    | Cycles through mock responses to demo both paths               |

---

## Troubleshooting

**FlowRun stuck in `Running`**

Check the controller logs:

```bash
kubectl logs -l control-plane=controller-manager -n kubezap-system
```

**Gateway not registering routes**

Check the gateway logs and confirm the Trigger has `status.conditions[Accepted]=True`.

**Mockoon not receiving requests**

Verify the Mockoon pod is running and the route endpoint is correct:

```bash
kubectl get pods -l app=mockoon -n default
kubectl logs -l app=mockoon -n default
```

Common issue: Flow steps must use the path without a `/mock/` prefix.
The correct URL format is `http://mockoon.default.svc.cluster.local:3000/<endpoint>`.

See [Troubleshooting → Mockoon not receiving requests](../../docs/guides/troubleshooting.md#mockoon-not-receiving-requests)
and the [Mocking HTTP Endpoints guide](../../docs/guides/mocking-http-endpoints.md).

---

## Cleanup

```bash
kubectl delete -k examples/order-router/
```

---

## Next steps

- Add [HMAC authentication](../../docs/guides/webhook-security.md) to the trigger
- Add a cron trigger that runs the flow on a schedule
- Replace Mockoon with real downstream services using the [URL switching pattern](../../docs/guides/mocking-http-endpoints.md#url-switching-with-configmaps)
- Set up [Prometheus metrics](../../docs/guides/observability.md) to track FlowRun durations
- Explore the [Kafka enrichment example](../kafka-enrichment/) for a message-broker-driven pipeline
