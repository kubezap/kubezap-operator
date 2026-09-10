# Multi-Tenant Webhook Fan-Out

This example demonstrates a single inbound webhook that triggers parallel HTTP
calls to three tenant-specific endpoints, each authenticated with its own
bearer token via a dedicated `Integration` resource.

---

## What you'll build

```
Webhook: POST /hooks/inbound-event
        |
        v
  +----------------+
  | normalize-     |  transform: extract eventId + type from body
  | payload        |
  +-------+--------+
          |
    +-----+-----+-----------+
    v           v            v
notify-     notify-      notify-       <-- all three run in parallel
tenant-a    tenant-b     tenant-c          (runAfter: [normalize-payload])
    |           |            |
    v           v            v
Mockoon     Mockoon      Mockoon
/events/a   /events/b    /events/c
```

Key properties:
- **Parallel fan-out**: all three tenant notification steps share
  `runAfter: [normalize-payload]`, so they execute concurrently.
- **Per-tenant credentials**: each step references a different `Integration`
  with its own bearer token Secret.
- **Failure isolation**: `failurePolicy: Continue` on the Flow means a failure
  for one tenant does not block the others. The FlowRun status shows all three
  outcomes independently.
- **Retries**: each notification step retries up to 2 times with exponential
  backoff (500ms initial, 5s max).
- **Self-contained**: no external services needed -- all three tenant endpoints
  are Mockoon stubs running in-cluster.

---

## Prerequisites

- A running Kubernetes cluster with KubeZap installed
- `kubectl` configured with access to the `default` namespace

No external services are required. The example deploys its own Mockoon mock
server to simulate the three tenant APIs.

---

## Setup

```bash
kubectl apply -k examples/multi-tenant-fanout/
```

This creates:
- Three `Secret` resources with placeholder bearer tokens
- Three `Integration` resources (one per tenant) pointing at the Mockoon mock
- A `Deployment` + `Service` for Mockoon (three stub routes)
- A `Flow` named `fan-out-to-tenants` with parallel notification steps
- A `Trigger` named `inbound-event` exposing the webhook endpoint

Wait for Mockoon to be ready:

```bash
kubectl rollout status deployment/mockoon -n default
```

---

## Sending a test event

Port-forward to the webhook gateway and send a request:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

```bash
curl -X POST http://localhost:8080/hooks/inbound-event \
  -H "Content-Type: application/json" \
  -d '{"eventId":"evt-001","type":"user.signup"}'
```

---

## Watching parallel execution

Watch FlowRuns as they appear:

```bash
kubectl get flowruns -n default -w
```

Once a FlowRun appears, inspect the step-level results:

```bash
kubectl describe flowrun <name> -n default
```

You should see all three notification steps ran in parallel with
`phase: Succeeded`:

```
normalize-payload   Succeeded
notify-tenant-a     Succeeded
notify-tenant-b     Succeeded
notify-tenant-c     Succeeded
```

---

## Simulating a tenant failure

To see `failurePolicy: Continue` in action, temporarily break one tenant's
credentials:

```bash
# Patch tenant-b's secret to an invalid token
kubectl patch secret tenant-b-secret -n default \
  -p '{"data":{"token":"aW52YWxpZA=="}}'
```

> `aW52YWxpZA==` is base64 for "invalid".

Now re-send the event:

```bash
curl -X POST http://localhost:8080/hooks/inbound-event \
  -H "Content-Type: application/json" \
  -d '{"eventId":"evt-002","type":"order.created"}'
```

Inspect the new FlowRun:

```bash
kubectl get flowruns -n default
kubectl describe flowrun <new-name> -n default
```

Because the Flow uses `failurePolicy: Continue`, the FlowRun completes even
though tenant B's step failed. You will see:

```
normalize-payload   Succeeded
notify-tenant-a     Succeeded
notify-tenant-b     Failed      (after 2 retries)
notify-tenant-c     Succeeded
```

Restore the original token when done:

```bash
kubectl patch secret tenant-b-secret -n default \
  -p '{"data":{"token":"bW9jay10b2tlbi1i"}}'
```

---

## Inspecting Mockoon captured requests

View all requests Mockoon received via the admin API:

```bash
kubectl exec -n default \
  $(kubectl get pod -n default -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3000/mockoon-admin/logs | jq .
```

You should see three POST requests (one per tenant endpoint), each carrying the
`Authorization: Bearer mock-token-*` header injected by the Integration.

---

## Cleaning up

```bash
kubectl delete -k examples/multi-tenant-fanout/
```

The controller will also remove the `kubezap-webhook-gateway` Deployment once
no webhook Triggers remain in the namespace.

---

## Production hardening

Before using this pattern in production:

- **Replace mock tokens**: use real credentials managed by an external secret
  operator (e.g., External Secrets Operator, HashiCorp Vault). Never commit
  real tokens to version control.
- **Add webhook authentication**: uncomment the `auth` section in `trigger.yaml`
  and configure HMAC or bearer token validation. See the
  [Webhook Security guide](../../docs/guides/webhook-security.md).
- **Replace Mockoon endpoints**: point each Integration's `baseUrl` at the real
  tenant API. Remove the Mockoon Deployment.
- **Add timeouts**: consider adding `timeoutSeconds` to each HTTP step action
  for explicit per-call deadlines.
- **Monitor**: set up Prometheus alerting on `kubezap_step_outcome_total`
  filtered by `step=notify-tenant-*` and `outcome=Failed` to catch tenant
  delivery failures early.
