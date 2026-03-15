# Getting Started with KubeZap

This guide walks you through a complete working example that demonstrates the core KubeZap
feature set: a webhook trigger, multi-step flow with data transformation, conditional branching,
and a mock notification endpoint for development testing.

By the end you will have:

- A webhook endpoint that accepts order events
- A flow that inspects the order type and routes to different notification paths
- A MockEndpoint that captures the notification so you can verify the result without a real Slack

---

## Prerequisites

- A running Kubernetes cluster (k3s, kind, or any cluster with CRD support)
- `kubectl` configured to reach the cluster
- KubeZap operator deployed (see [Installation](../overview.md#installation))
- The webhook gateway accessible — either via `kubectl port-forward` or a Service of type LoadBalancer

---

## The Demo Scenario

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

## Step 1: Create the MockEndpoints

MockEndpoints act as stand-ins for real downstream services. They respond with a fixed
payload and record what they receive.

```yaml
# config/samples/demo/mockendpoints.yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: notify-express
  namespace: default
spec:
  path: notify-express
  response:
    statusCode: 200
    body: '{"notified":true,"tier":"express"}'
    headers:
      Content-Type: application/json
  maxRequestHistory: 20
---
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: notify-standard
  namespace: default
spec:
  path: notify-standard
  response:
    statusCode: 200
    body: '{"notified":true,"tier":"standard"}'
    headers:
      Content-Type: application/json
  maxRequestHistory: 20
```

```bash
kubectl apply -f config/samples/demo/mockendpoints.yaml
```

The webhook gateway will register `/mock/notify-express` and `/mock/notify-standard`
automatically. Check the gateway logs to confirm:

```bash
kubectl logs -l app=kubezap-webhook-gateway -n default | grep mock
```

---

## Step 2: Create the Flow

The flow has four steps:

1. `extract-type` — transform step that pulls `orderType` from the trigger body
2. `enrich-order` — http step that fetches order details (using the mock enrich endpoint)
3. `notify-express` — http step that fires only when `orderType == "express"`
4. `notify-standard` — http step that fires only when `orderType == "standard"`

```yaml
# config/samples/demo/flow.yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: order-router
  namespace: default
spec:
  timeout: 30s
  steps:
    - name: extract-type
      action:
        type: transform
        transform:
          mappings:
            orderType: "$(trigger.body)"   # replaced with CEL/jq extraction once implemented;
                                           # for now pass the raw field from a structured body

    - name: enrich-order
      runAfter:
        - extract-type
      action:
        type: http
        http:
          url: "http://kubezap-webhook-gateway.default.svc.cluster.local:8080/mock/enrich-order"
          method: POST
          body: '{"orderId":"$(trigger.headers.X-Order-Id)"}'
          headers:
            Content-Type: application/json
          timeoutSeconds: 10
          resultMappings:
            customerId: "$.customerId"
            tier: "$.tier"

    - name: notify-express
      runAfter:
        - enrich-order
      when:
        - expression: 'steps.enrich_order.results.tier == "express"'
      action:
        type: http
        http:
          url: "http://kubezap-webhook-gateway.default.svc.cluster.local:8080/mock/notify-express"
          method: POST
          body: '{"customerId":"$(steps.enrich_order.results.customerId)","message":"Express order dispatched"}'
          headers:
            Content-Type: application/json

    - name: notify-standard
      runAfter:
        - enrich-order
      when:
        - expression: 'steps.enrich_order.results.tier == "standard"'
      action:
        type: http
        http:
          url: "http://kubezap-webhook-gateway.default.svc.cluster.local:8080/mock/notify-standard"
          method: POST
          body: '{"customerId":"$(steps.enrich_order.results.customerId)","message":"Standard order queued"}'
          headers:
            Content-Type: application/json
```

> **Note:** The `when` field uses CEL expressions. CEL evaluation is implemented in the
> FlowRun reconciler using the `google/cel-go` library. See [Flow API → Conditions and CEL](../api/flow.md#conditions-and-cel)
> for the full variable reference.

Also add a MockEndpoint for the enrich step:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: enrich-order
  namespace: default
spec:
  path: enrich-order
  responseSequence:
    - statusCode: 200
      body: '{"customerId":"cust-001","tier":"express"}'
      headers:
        Content-Type: application/json
    - statusCode: 200
      body: '{"customerId":"cust-002","tier":"standard"}'
      headers:
        Content-Type: application/json
  maxRequestHistory: 20
```

The `responseSequence` cycles on each call — so the first webhook fires an express path
and the second fires a standard path, letting you see both branches in one demo session.

```bash
kubectl apply -f config/samples/demo/flow.yaml
kubectl apply -f config/samples/demo/enrich-mockendpoint.yaml

# Confirm Flow is valid
kubectl get flow order-router -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# Expected: True
```

---

## Step 3: Create the Trigger

```yaml
# config/samples/demo/trigger.yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-placed
  namespace: default
spec:
  type: webhook
  enabled: true
  webhook:
    path: /hooks/order-placed
    method: POST
  flowRef:
    name: order-router
```

```bash
kubectl apply -f config/samples/demo/trigger.yaml

# Check the trigger is accepted
kubectl get trigger order-placed -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

---

## Step 4: Send a Test Request

Forward the webhook gateway port if not already externally accessible:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Fire the first request (will route to express path via the mock's responseSequence):

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

Once it completes:

```bash
# See which steps ran and which were skipped
kubectl get flowrun <name> -o jsonpath='{.status.steps[*].name}' | tr ' ' '\n'
kubectl get flowrun <name> -o jsonpath='{.status.steps[*].phase}' | tr ' ' '\n'
```

Expected output:
```
extract-type   → Succeeded
enrich-order   → Succeeded
notify-express → Succeeded
notify-standard → Skipped   # when condition was false
```

---

## Step 5: Inspect Captured Requests

The MockEndpoints record everything they receive:

```bash
kubectl get mockendpoint notify-express -o jsonpath='{.status.recentRequests}' | jq .
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

Fire a second request — the enrich mock will now return `tier: standard`, so `notify-standard`
runs instead:

```bash
curl -X POST http://localhost:8080/hooks/order-placed \
  -H "Content-Type: application/json" \
  -H "X-Order-Id: ord-002" \
  -d '{"event":"order.placed","orderId":"ord-002"}'
```

---

## What This Demonstrates

| Feature | How it shows up |
|---|---|
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
kubectl auth can-i patch mockendpoints/status --as=system:serviceaccount:default:kubezap-webhook-gateway
```

If not, see [Architecture → Gateway RBAC](../architecture.md#gateway-serviceaccount-and-rbac).

---

## Next Steps

- Add [HMAC authentication](webhook-security.md) to the trigger
- Add a cron trigger that runs the flow on a schedule
- Replace the MockEndpoints with real downstream services
- Set up [Prometheus metrics](observability.md) to track FlowRun durations
