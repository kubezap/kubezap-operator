# MockEndpoint CRD

A `MockEndpoint` registers a local HTTP endpoint on the KubeZap operator's webhook server that captures and logs incoming requests instead of forwarding them anywhere. Use it during development and testing to replace real external services (Slack, PagerDuty, downstream APIs) without making actual calls.

---

## Contents

- [MockEndpoint CRD](#mockendpoint-crd)
  - [Contents](#contents)
  - [Overview](#overview)
  - [How It Works](#how-it-works)
  - [URL Switching with ConfigMaps](#url-switching-with-configmaps)
  - [Spec Reference](#spec-reference)
    - [MockEndpointSpec](#mockendpointspec)
    - [MockResponse](#mockresponse)
  - [Status Reference](#status-reference)
    - [MockEndpointStatus](#mockendpointstatus)
    - [CapturedRequest](#capturedrequest)
    - [Printer Columns](#printer-columns)
  - [Inspecting Captured Requests](#inspecting-captured-requests)
    - [View recent requests](#view-recent-requests)
    - [View just the request bodies](#view-just-the-request-bodies)
    - [Clear request history](#clear-request-history)
    - [Watch for new requests](#watch-for-new-requests)
  - [Response Scenarios](#response-scenarios)
    - [Simulating rate limits and transient failures](#simulating-rate-limits-and-transient-failures)
    - [Simulating timeouts](#simulating-timeouts)
  - [Examples](#examples)
    - [Example 1: Basic Mock](#example-1-basic-mock)
    - [Example 2: Custom Response Body](#example-2-custom-response-body)
    - [Example 3: Simulating Failures](#example-3-simulating-failures)
    - [Example 4: Response Sequence](#example-4-response-sequence)
    - [Example 5: Full Dev/Prod URL Switching Pattern](#example-5-full-devprod-url-switching-pattern)
  - [kubectl Reference](#kubectl-reference)
  - [Limitations](#limitations)

---

## Overview

Without a mock, testing a Flow that calls Slack means either spamming a real Slack channel, setting up a test workspace, or commenting out the step. A `MockEndpoint` gives you a zero-config alternative: create the CRD, point your Flow at its URL, and every captured request appears in the CRD status where you can inspect it with `kubectl`.

```
  Trigger fires
       │
       ▼
  Flow Engine
       │  url: $(configmaps.integration-urls.slack-webhook)
       ▼
  ConfigMap lookup
       │
       ├── production  ──►  https://hooks.slack.com/...
       │                    (real notification sent)
       │
       └── development ──►  /mock/slack  (MockEndpoint)
                            captures request, logs to CRD status
```

---

## How It Works

The KubeZap operator's HTTP server handles two path prefixes:

- `/hooks/*` — inbound webhook triggers (production)
- `/mock/*` — mock endpoints (development and testing)

When you create a `MockEndpoint`, the reconciler registers the configured path with the mock server. Incoming requests to that path are:

1. Logged to `status.recentRequests` (capped at `maxRequestHistory`)
2. Responded to immediately with the configured response (default: `200 OK`)
3. Not forwarded anywhere

The in-cluster URL for any `MockEndpoint` is:

```
http://kubezap-webhook-service.<operator-namespace>.svc.cluster.local:8080/mock/<path>
```

This URL is also surfaced in `status.url` for easy copy-paste.

---

## URL Switching with ConfigMaps

The recommended pattern is to store external service URLs in a `ConfigMap` and reference them with `$(configmaps.name.key)` in your Flow. Swap the ConfigMap to switch between real and mock URLs — the Flow spec stays identical in both environments.

**Production ConfigMap:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  orders-api: "https://orders.internal/api"
  notifications-api: "https://notify.internal/v2"
```

**Development ConfigMap:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "http://kubezap-webhook-service.kubezap-system.svc/mock/slack"
  orders-api: "http://kubezap-webhook-service.kubezap-system.svc/mock/orders-api"
  notifications-api: "http://kubezap-webhook-service.kubezap-system.svc/mock/notifications"
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

This pattern also works for base URLs:

```yaml
url: "$(configmaps.integration-urls.orders-api)/orders/$(params.orderId)"
```

---

## Spec Reference

### MockEndpointSpec

| Field               | Type           | Required | Default            | Description                                                                                                                          |
| ------------------- | -------------- | -------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| `path`              | string         | **Yes**  | —                  | Path suffix after `/mock/`. Must start without a leading slash (e.g., `slack`, `orders-api`).                                        |
| `response`          | MockResponse   | No       | 200 OK, empty body | Static response returned for every request. Mutually exclusive with `responseSequence`.                                              |
| `responseSequence`  | []MockResponse | No       | —                  | Ordered list of responses. Each request advances the sequence. After the last entry, it repeats. Mutually exclusive with `response`. |
| `maxRequestHistory` | integer        | No       | `10`               | Number of recent requests to retain in `status.recentRequests`. Set to `0` to disable history.                                       |
| `enabled`           | boolean        | No       | `true`             | Set to `false` to temporarily disable the mock (returns 503 to callers).                                                             |

### MockResponse

| Field        | Type              | Required | Default | Description                                                                                       |
| ------------ | ----------------- | -------- | ------- | ------------------------------------------------------------------------------------------------- |
| `statusCode` | integer           | No       | `200`   | HTTP status code to return                                                                        |
| `body`       | string            | No       | `""`    | Response body                                                                                     |
| `headers`    | map[string]string | No       | —       | Additional response headers                                                                       |
| `delayMs`    | integer           | No       | `0`     | Artificial delay in milliseconds before responding. Use to test step timeouts and retry behavior. |

---

## Status Reference

### MockEndpointStatus

| Field                  | Type              | Description                                                             |
| ---------------------- | ----------------- | ----------------------------------------------------------------------- |
| `conditions`           | []Condition       | Standard `Ready` condition                                              |
| `url`                  | string            | Full in-cluster URL for this mock endpoint                              |
| `requestCount`         | integer           | Total requests received since the MockEndpoint was created              |
| `lastRequestTime`      | timestamp         | Timestamp of the most recent request                                    |
| `recentRequests`       | []CapturedRequest | The last `maxRequestHistory` captured requests, newest first            |
| `currentResponseIndex` | integer           | Current position in `responseSequence` (only set when using a sequence) |

When `spec.enabled` is set to `false`, the `Ready` condition remains `True` but the endpoint returns `503 Service Unavailable` to all callers. A `Paused` condition is also set:

| Type     | Status  | Meaning                                                                            |
| -------- | ------- | ---------------------------------------------------------------------------------- |
| `Ready`  | `True`  | The mock endpoint is registered and accepting requests                             |
| `Ready`  | `False` | The mock endpoint failed to register. See `message` for details.                   |
| `Paused` | `True`  | The mock endpoint is registered but `spec.enabled: false` — returns 503 to callers |

### CapturedRequest

| Field            | Type              | Description                                                              |
| ---------------- | ----------------- | ------------------------------------------------------------------------ |
| `timestamp`      | timestamp         | When the request was received                                            |
| `method`         | string            | HTTP method (POST, GET, etc.)                                            |
| `headers`        | map[string]string | Request headers (sensitive headers such as `Authorization` are redacted) |
| `body`           | string            | Request body (truncated to 4KB; see `bodyTruncated`)                     |
| `bodyTruncated`  | boolean           | `true` if the body exceeded 4KB and was truncated                        |
| `responseStatus` | integer           | HTTP status code that was returned to the caller                         |

### Printer Columns

```bash
kubectl get mockendpoints -n automation
```

```
NAME            PATH               REQUESTS   LAST REQUEST   READY
slack-mock      /mock/slack        14         2m ago         True
orders-api      /mock/orders-api   3          12s ago        True
payment-fail    /mock/payment      0          <none>         True
```

---

## Inspecting Captured Requests

### View recent requests

```bash
kubectl get mockendpoint slack-mock -n automation \
  -o jsonpath='{.status.recentRequests}' | jq .
```

Example output:
```json
[
  {
    "timestamp": "2026-03-14T10:32:15Z",
    "method": "POST",
    "headers": {
      "Content-Type": "application/json"
    },
    "body": "{\"text\": \"Order ORD-9921 processed for Acme Corp\"}",
    "bodyTruncated": false,
    "responseStatus": 200
  }
]
```

### View just the request bodies

```bash
kubectl get mockendpoint slack-mock -n automation \
  -o jsonpath='{range .status.recentRequests[*]}{.body}{"\n"}{end}'
```

### Clear request history

Annotate the resource to trigger a history reset:

```bash
kubectl annotate mockendpoint slack-mock -n automation \
  kubezap.io/clear-requests=true --overwrite
```

The operator clears `status.recentRequests` and resets `requestCount` to `0` on the next reconcile, then removes the annotation.

### Watch for new requests

Use `kubectl get -w` to watch for status changes as requests arrive:

```bash
kubectl get mockendpoint slack-mock -n automation -w
```

---

## Response Scenarios

### Simulating rate limits and transient failures

Use `responseSequence` to return a failure on the first attempt and success on retry. This is useful for validating that `retryPolicy` in your Flow is working correctly.

```yaml
spec:
  responseSequence:
    - statusCode: 429
      body: '{"error": "rate limited"}'
      headers:
        Retry-After: "1"
    - statusCode: 429
      body: '{"error": "rate limited"}'
    - statusCode: 200
      body: '{"ok": true}'
```

With this configuration, the first two requests get a 429, and the third (and all subsequent) get 200.

### Simulating timeouts

Use `delayMs` to cause the step to hit its `timeoutSeconds` limit:

```yaml
spec:
  response:
    statusCode: 200
    body: '{"ok": true}'
    delayMs: 35000  # 35 seconds — exceeds the step's 30s timeout
```

---

## Examples

### Example 1: Basic Mock

Replace a Slack webhook URL with a mock that logs received notifications.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: slack-mock
  namespace: automation
spec:
  path: slack
```

After creation, check `status.url` for the endpoint address:

```bash
kubectl get mockendpoint slack-mock -n automation -o jsonpath='{.status.url}'
# http://kubezap-webhook-service.kubezap-system.svc/mock/slack
```

---

### Example 2: Custom Response Body

Return a response body that looks like the real Slack API response, so Flow steps that check the response work correctly.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: slack-mock
  namespace: automation
spec:
  path: slack
  response:
    statusCode: 200
    body: '{"ok": true}'
    headers:
      Content-Type: "application/json"
```

---

### Example 3: Simulating Failures

Test that your Flow's `retryPolicy` and `onFailure` handling works correctly.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: failing-api
  namespace: automation
spec:
  path: orders-api
  response:
    statusCode: 503
    body: '{"error": "service unavailable"}'
    headers:
      Content-Type: "application/json"
```

---

### Example 4: Response Sequence

Return a 429 twice, then succeed — validates exponential backoff retry logic.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: flaky-api
  namespace: automation
spec:
  path: payment-api
  maxRequestHistory: 20
  responseSequence:
    - statusCode: 429
      body: '{"error": "too many requests"}'
      headers:
        Content-Type: "application/json"
        Retry-After: "2"
    - statusCode: 429
      body: '{"error": "too many requests"}'
      headers:
        Content-Type: "application/json"
        Retry-After: "2"
    - statusCode: 200
      body: '{"transactionId": "mock-txn-001", "status": "approved"}'
      headers:
        Content-Type: "application/json"
```

---

### Example 5: Full Dev/Prod URL Switching Pattern

Complete example showing the ConfigMap URL switching pattern with three services.

**Step 1 — Define mock endpoints for dev:**

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: mock-slack
  namespace: automation
spec:
  path: slack
  response:
    statusCode: 200
    body: '{"ok": true}'
    headers:
      Content-Type: "application/json"
---
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: mock-orders-api
  namespace: automation
spec:
  path: orders-api
  response:
    statusCode: 200
    body: '{"id": "ORD-MOCK-001", "status": "confirmed", "customerName": "Test User"}'
    headers:
      Content-Type: "application/json"
```

**Step 2 — Create environment-specific ConfigMaps:**

```yaml
# development — apply to dev cluster or dev namespace
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "http://kubezap-webhook-service.kubezap-system.svc/mock/slack"
  orders-api-base: "http://kubezap-webhook-service.kubezap-system.svc/mock/orders-api"
---
# production — apply to prod cluster or prod namespace
apiVersion: v1
kind: ConfigMap
metadata:
  name: integration-urls
  namespace: automation
data:
  slack-webhook: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
  orders-api-base: "https://orders.internal/api/v1"
```

**Step 3 — Flow uses ConfigMap references throughout:**

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
    - name: fetch-order
      action:
        type: http
        http:
          url: "$(configmaps.integration-urls.orders-api-base)/orders/$(params.orderId)"
          method: GET
          headers:
            Authorization: "Bearer $(secrets.orders-api-creds.token)"
          resultMappings:
            customerName: "$.customerName"
            status: "$.status"
      results:
        - name: customerName
        - name: status

    - name: notify-slack
      runAfter: [fetch-order]
      when:
        - expression: 'steps.fetch_order.results.status == "confirmed"'
      action:
        type: http
        http:
          url: "$(configmaps.integration-urls.slack-webhook)"
          method: POST
          headers:
            Content-Type: "application/json"
          body: |
            {"text": "Order $(params.orderId) confirmed for $(steps.fetch_order.results.customerName)"}
```

**Step 4 — Fire the trigger and inspect the mock:**

```bash
# Fire the trigger manually
curl -X POST http://kubezap-webhook-service.kubezap-system.svc/hooks/order-received \
  -H "Content-Type: application/json" \
  -d '{"orderId": "ORD-TEST-001"}'

# Check what the flow sent to the mock Slack endpoint
kubectl get mockendpoint mock-slack -n automation \
  -o jsonpath='{.status.recentRequests[0].body}' | jq .
```

Expected output:
```json
{"text": "Order ORD-TEST-001 confirmed for Test User"}
```

---

## kubectl Reference

| Command                                                                                   | Description                         |
| ----------------------------------------------------------------------------------------- | ----------------------------------- |
| `kubectl get mockendpoints -n <ns>`                                                       | List all mock endpoints with status |
| `kubectl get mockendpoint <name> -n <ns> -o yaml`                                         | Full spec and status                |
| `kubectl get mockendpoint <name> -n <ns> -o jsonpath='{.status.recentRequests}' \| jq .`  | Pretty-print captured requests      |
| `kubectl get mockendpoint <name> -n <ns> -o jsonpath='{.status.requestCount}'`            | Total request count                 |
| `kubectl annotate mockendpoint <name> -n <ns> kubezap.io/clear-requests=true --overwrite` | Clear request history               |
| `kubectl delete mockendpoint <name> -n <ns>`                                              | Remove the mock endpoint            |

---

## Limitations

- **Development use only**: `MockEndpoint` resources are intended for development and testing. They are disabled in clusters where the operator is running with `--mock-endpoints=false` (the recommended production flag).
- **Request body size**: Captured request bodies are truncated at 4KB. Use `bodyTruncated: true` in status to detect truncation.
- **In-memory registration**: Mock endpoint registrations are held in the operator process. If the operator pod restarts, the MockEndpoint reconciler re-registers all endpoints automatically — no request history is lost from the status (it is persisted in etcd via the CRD status subresource).
- **No request matching**: All requests to a mock path receive the same response (or the next in sequence). Request-conditional responses are not supported.
- **Single operator deployment**: Mock endpoints are served by the same operator pod handling webhook triggers. In multi-replica deployments, requests may be handled by different replicas, and request history in status reflects only what that replica received.
