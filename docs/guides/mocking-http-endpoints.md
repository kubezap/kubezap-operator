# Mocking HTTP Endpoints

This guide explains how to stand up in-cluster HTTP mock servers for KubeZap development and testing. It describes the recommended approach using [Mockoon](https://mockoon.com).

---

## Contents

- [Mocking HTTP Endpoints](#mocking-http-endpoints)
  - [Contents](#contents)
  - [Mockoon: The Recommended Replacement](#mockoon-the-recommended-replacement)
  - [In-Cluster Deployment](#in-cluster-deployment)
    - [Quick Start](#quick-start)
    - [Reusable Deployment YAML](#reusable-deployment-yaml)
  - [Defining Stub Responses](#defining-stub-responses)
    - [Environment File Format](#environment-file-format)
    - [Static Response](#static-response)
    - [Dynamic Response with Templating](#dynamic-response-with-templating)
    - [Simulating Failures and Rate Limits](#simulating-failures-and-rate-limits)
    - [Response Sequences](#response-sequences)
  - [Inspecting Captured Requests](#inspecting-captured-requests)
    - [Admin API — Request Log](#admin-api--request-log)
    - [Streaming Logs from the Pod](#streaming-logs-from-the-pod)
    - [Port-Forwarding the Admin API Locally](#port-forwarding-the-admin-api-locally)
  - [URL Switching with ConfigMaps](#url-switching-with-configmaps)
  - [Examples That Use Mockoon](#examples-that-use-mockoon)
    - [order-router](#order-router)
    - [kafka-enrichment](#kafka-enrichment)
    - [slack-router](#slack-router)
  - [Updating Stub Responses Without Redeploying](#updating-stub-responses-without-redeploying)
  - [Limitations](#limitations)

---

## Mockoon: The Recommended Replacement

[Mockoon](https://mockoon.com) is an open-source HTTP mock server available as a Docker image (`mockoon/mockoon:latest`). It reads a JSON environment file that describes routes and responses, serves them on a configurable port, and exposes a REST admin API for inspecting request history.

Key properties relevant to KubeZap development:

| Feature          | Details                                                                                                                           |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Docker image     | `mockoon/mockoon:latest`                                                                                                          |
| Mock server port | `3000` (configurable via `--port`)                                                                                                |
| Admin API port   | `3001` (configurable via `--admin-api-port`)                                                                                      |
| Configuration    | JSON environment file — see [Mockoon data file format](https://mockoon.com/docs/latest/mockoon-data-files/data-storage-location/) |
| Request history  | Available via `GET /api/logs` on the admin API                                                                                    |
| Templating       | Handlebars syntax in response bodies                                                                                              |
| Health check     | `GET /health` on the admin API → `200 OK`                                                                                         |

---

## In-Cluster Deployment

### Quick Start

Apply the reusable Deployment, Service, and ConfigMap from the repository:

```bash
kubectl apply -f docs/guides/mockoon-deployment.yaml -n <your-namespace>
```

Verify the pod is running:

```bash
kubectl get pods -l app=mockoon -n <your-namespace>
# Expected: mockoon-<hash>   1/1   Running
```

The mock server is now reachable in-cluster at:

```
http://mockoon.<your-namespace>.svc.cluster.local:3000
```

The admin API is at:

```
http://mockoon.<your-namespace>.svc.cluster.local:3001
```

### Reusable Deployment YAML

The full Kubernetes manifests are maintained at:

```
docs/guides/mockoon-deployment.yaml
```

The file contains three resources:

1. **ConfigMap** (`mockoon-env`) — Mockoon environment JSON with stub routes. Edit this to add or modify routes.
2. **Deployment** (`mockoon`) — runs the `mockoon/mockoon:latest` container with the ConfigMap mounted at `/config/environment.json`. Includes OpenShift-compatible security context (non-root, read-only root filesystem, all capabilities dropped).
3. **Service** (`mockoon`) — exposes port `3000` (mock) and port `3001` (admin) as ClusterIP.

Change the `namespace: default` field in the file to match your target namespace before applying.

The sample ConfigMap in the file ships with four routes that match the services used in the KubeZap examples:

| Route               | Method | Used by                                                      |
| ------------------- | ------ | ------------------------------------------------------------ |
| `/notify-express`   | POST   | [order-router example](../../examples/order-router/)         |
| `/notify-standard`  | POST   | [order-router example](../../examples/order-router/)         |
| `/customer-profile` | GET    | [kafka-enrichment example](../../examples/kafka-enrichment/) |
| `/deploy-sink`      | POST   | [slack-router example](../../examples/slack-router/)         |

---

## Defining Stub Responses

All routes are defined in the Mockoon environment JSON stored in the `mockoon-env` ConfigMap. The format is standard Mockoon — you can generate this file from the [Mockoon desktop app](https://mockoon.com/download/) using **File → Export current environment**.

### Environment File Format

The top-level structure of the environment JSON:

```json
{
  "uuid": "<unique-id>",
  "name": "My Dev Mocks",
  "port": 3000,
  "routes": [ ... ],
  "rootChildren": [ { "type": "route", "uuid": "<route-uuid>" } ]
}
```

Each entry in `routes` defines one HTTP endpoint.

### Static Response

A route that always returns the same JSON body:

```json
{
  "uuid": "r0001-slack-webhook",
  "type": "http",
  "documentation": "Captures Slack webhook calls",
  "method": "post",
  "endpoint": "hooks/slack",
  "responses": [
    {
      "uuid": "resp-slack-200",
      "body": "{\"ok\": true}",
      "statusCode": 200,
      "headers": [
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": true
    }
  ],
  "enabled": true
}
```

This route responds to `POST /hooks/slack` with `{"ok": true}`.

### Dynamic Response with Templating

Mockoon supports [Handlebars templating](https://mockoon.com/docs/latest/templating/mockoon-helpers/) in response bodies. Use `{{body 'field' 'default'}}` to echo request body fields back in the response:

```json
{
  "uuid": "r0002-orders-api",
  "type": "http",
  "method": "get",
  "endpoint": "orders/:orderId",
  "responses": [
    {
      "uuid": "resp-order-200",
      "body": "{\n  \"id\": \"{{urlParam 'orderId'}}\",\n  \"status\": \"confirmed\",\n  \"customerName\": \"Test User\"\n}",
      "statusCode": 200,
      "headers": [
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": true
    }
  ],
  "enabled": true
}
```

`GET /orders/ORD-001` returns:

```json
{
  "id": "ORD-001",
  "status": "confirmed",
  "customerName": "Test User"
}
```

### Simulating Failures and Rate Limits

To test Flow retry policies, configure a response with a non-2xx status code:

```json
{
  "uuid": "r0003-failing-api",
  "type": "http",
  "method": "post",
  "endpoint": "payments",
  "responses": [
    {
      "uuid": "resp-payments-503",
      "body": "{\"error\": \"service unavailable\"}",
      "statusCode": 503,
      "headers": [
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": true
    }
  ],
  "enabled": true
}
```

To simulate artificial latency and test step timeout behavior, add a `latency` value in milliseconds to the response object:

```json
{
  "uuid": "resp-slow-200",
  "body": "{\"ok\": true}",
  "statusCode": 200,
  "latency": 35000
}
```

A 35-second latency will cause a step with `timeoutSeconds: 30` to time out.

### Response Sequences

Mockoon's [response rules](https://mockoon.com/docs/latest/route-responses/multiple-responses/) and [sequential mode](https://mockoon.com/docs/latest/route-responses/multiple-responses/#sequential-responses) let you cycle through a list of responses in order.

Set `responseMode` to `"SEQUENTIAL"` in the route to cycle through responses:

```json
{
  "uuid": "r0004-flaky-api",
  "type": "http",
  "method": "post",
  "endpoint": "payment",
  "responseMode": "SEQUENTIAL",
  "responses": [
    {
      "uuid": "resp-429-1",
      "body": "{\"error\": \"too many requests\"}",
      "statusCode": 429,
      "headers": [
        { "key": "Retry-After", "value": "2" },
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": false
    },
    {
      "uuid": "resp-429-2",
      "body": "{\"error\": \"too many requests\"}",
      "statusCode": 429,
      "headers": [
        { "key": "Retry-After", "value": "2" },
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": false
    },
    {
      "uuid": "resp-200-ok",
      "body": "{\"transactionId\": \"mock-txn-001\", \"status\": \"approved\"}",
      "statusCode": 200,
      "headers": [
        { "key": "Content-Type", "value": "application/json" }
      ],
      "default": true
    }
  ],
  "enabled": true
}
```

The first two requests to `POST /payment` return 429; the third and all subsequent requests return 200. This is the canonical setup for validating `retryPolicy` in a Flow step.

---

## Inspecting Captured Requests

Mockoon does not write request history to Kubernetes resources. Instead, it exposes a REST admin API and structured logs.

### Admin API — Request Log

Retrieve the request log for all routes from the admin API:

```
GET http://mockoon.<namespace>.svc.cluster.local:3001/api/logs
```

Example using `kubectl exec`:

```bash
kubectl exec -n <namespace> \
  $(kubectl get pod -n <namespace> -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3001/api/logs | jq .
```

Example response:

```json
[
  {
    "UUID": "a1b2c3d4",
    "timestamp": "2026-03-21T10:32:15.123Z",
    "method": "POST",
    "url": "/notify-express",
    "headers": {
      "content-type": "application/json"
    },
    "body": "{\"customerId\":\"cust-001\",\"message\":\"Express order dispatched\"}",
    "proxied": false,
    "response": {
      "status": 200,
      "headers": {
        "content-type": "application/json"
      },
      "body": "{\"notified\":true,\"tier\":\"express\"}"
    }
  }
]
```

To filter to a specific route path (requires `jq`):

```bash
kubectl exec -n <namespace> \
  $(kubectl get pod -n <namespace> -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3001/api/logs \
  | jq '[.[] | select(.url == "/notify-express")]'
```

### Streaming Logs from the Pod

Mockoon writes one structured log line per request to stdout. Stream them directly:

```bash
kubectl logs -n <namespace> -l app=mockoon -f
```

Example output:

```
{"level":"info","method":"POST","url":"/notify-express","status":200,"duration":2}
{"level":"info","method":"GET","url":"/customer-profile","status":200,"duration":1}
```

### Port-Forwarding the Admin API Locally

For interactive inspection during development, port-forward the admin port to your local machine:

```bash
kubectl port-forward -n <namespace> svc/mockoon 3001:3001
```

Then query from your workstation:

```bash
curl -s http://localhost:3001/api/logs | jq '.[0]'
```

Or open the [Mockoon desktop app](https://mockoon.com/download/) and use its built-in request log viewer to browse captured requests with full detail.

---

## URL Switching with ConfigMaps

The recommended pattern is to store external service URLs in a `ConfigMap` and reference them with `$(configmaps.name.key)` in your Flow. Swap the ConfigMap to switch between real and mock URLs — the Flow spec is identical in both environments.

**Development ConfigMap (Mockoon):**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "http://mockoon.automation.svc.cluster.local:3000/hooks/slack"
  orders-api: "http://mockoon.automation.svc.cluster.local:3000/orders"
  notifications-api: "http://mockoon.automation.svc.cluster.local:3000/notifications"
```

**Production ConfigMap:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  orders-api: "https://orders.internal/api/v1"
  notifications-api: "https://notify.internal/v2"
```

**Flow (unchanged between environments):**

```yaml
steps:
  - name: notify-slack
    action:
      type: http
      http:
        url: "$(configmaps.integration-urls.slack-webhook)"
        method: POST
        body: '{"text": "Order $(params.orderId) processed"}'
```

This pattern also composes with base URL references:

```yaml
url: "$(configmaps.integration-urls.orders-api)/orders/$(params.orderId)"
```

---

## Examples That Use Mockoon

The following KubeZap examples use the Mockoon deployment to capture requests during development and testing.

### order-router

**Location:** [`examples/order-router/`](../../examples/order-router/)

Uses Mockoon routes `/notify-express` and `/notify-standard` to capture conditional notification output, and `/customer-profile` to stand in for the enrichment API with a sequential response that cycles between express and standard tiers.

The example demonstrates:
- Conditional branching (`when:` CEL expressions)
- Step result passing between the enrich and notify steps
- Skipped steps (`Skipped` phase when `when` condition is false)
- Inspecting captured requests via the Mockoon admin API

### kafka-enrichment

**Location:** [`examples/kafka-enrichment/`](../../examples/kafka-enrichment/)

Uses Mockoon's `/customer-profile` route to simulate the customer enrichment API. The route returns a dynamic JSON response using Handlebars templating, and supports switching tiers by updating the stub response body.

The example demonstrates:
- Kafka-triggered FlowRun creation with partition/offset dedup keys
- Retry policy with exponential backoff tested against a `503` response
- CEL routing across three tier branches (`enterprise`, `standard`, `trial`)
- Publish step writing enriched events back to a Kafka output topic

### slack-router

**Location:** [`examples/slack-router/`](../../examples/slack-router/)

Uses Mockoon's `/deploy-sink`, `/status-sink`, and `/fallback-sink` routes to capture the output of each slash-command branch. No external Slack webhook is needed during development.

The example demonstrates:
- HMAC signature verification + IP allowlist in a single auth block
- `application/x-www-form-urlencoded` payload parsing
- Multi-branch routing with exactly one active branch per invocation
- Fire-and-forget gateway response pattern (Slack's 3-second timeout)

---

## Updating Stub Responses Without Redeploying

Edit the `mockoon-env` ConfigMap and apply the change:

```bash
kubectl edit configmap mockoon-env -n <namespace>
# or
kubectl apply -f docs/guides/mockoon-deployment.yaml -n <namespace>
```

Then restart the Mockoon pod to pick up the new environment file:

```bash
kubectl rollout restart deployment/mockoon -n <namespace>
```

Mockoon reads the environment file once at startup. A pod restart is required when the ConfigMap changes. The rollout completes in a few seconds and the mock server is ready once the readiness probe passes.

> **Tip**: for rapid iteration during local development, consider using the [Mockoon CLI](https://mockoon.com/cli/) directly on your workstation and pointing Flow steps at `host.docker.internal` (Docker Desktop) or the host IP. Reserve the in-cluster Deployment for shared dev/test environments.

---

## Limitations

- **Pod restart required for config changes**: Mockoon loads the environment JSON at startup. Updating the ConfigMap requires a pod rollout. For environments where zero downtime is required, run two Mockoon pods behind a Service and use a rolling update strategy.
- **Admin API is unauthenticated**: the Mockoon admin API has no authentication. Do not expose it outside the cluster. The `mockoon` Service in `docs/guides/mockoon-deployment.yaml` is of type `ClusterIP` for this reason.
- **Single environment file per pod**: each Mockoon pod serves one environment JSON. If you need logical separation between groups of mock routes, run multiple Mockoon Deployments with different ConfigMaps and Services.
- **Request log size**: the Mockoon admin API retains the most recent 100 requests in memory by default. Logs older than this are not accessible through the API, though they remain in the pod's stdout log stream.
- **Development use only**: Mockoon is a development and testing aid. Do not deploy it in production namespaces.
