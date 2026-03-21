# Example: Kubernetes Pod Failure -> ITSM Ticket

> **Alpha feature:** `type: resource` triggers are implemented but alpha-quality.
> Known limitations apply — see [docs/tech-debt/](../../docs/tech-debt/) for details.
> Notable limitations: naive pluralization fallback for irregular resource kinds
> (e.g. `Ingress`, `NetworkPolicy`) and no guarantee of exactly-once FlowRun
> creation under very high event rates. Not recommended for production use.

## Overview

This example demonstrates how KubeZap can watch for Kubernetes resource events
and trigger automated workflows in response. When a Pod transitions to the
`Failed` phase, KubeZap opens an ITSM ticket containing the pod name, namespace,
failure reason, and a deduplication key derived from the pod UID.

## What it demonstrates

- **Kubernetes resource-event trigger** -- watching Pods for `phase == "Failed"`
- **Event metadata extraction** -- pulling pod name, namespace, UID, and failure
  reason from the resource event body via a transform step
- **HTTP action with retry** -- POSTing a ticket to an ITSM system with
  exponential backoff (2 retries, 1s initial delay, 10s max)
- **Deduplication via pod UID** -- the `dedupKeyExpression` on the Trigger
  ensures that re-processing the same pod failure does not create duplicate
  FlowRuns

## Prerequisites

- KubeZap operator installed and running (v0.4+)
- The controller's ServiceAccount must have `get/list/watch` on `pods` in the
  target namespace (resource triggers require explicit RBAC for each watched
  kind — see [docs/api/trigger.md](../../docs/api/trigger.md))
- Mockoon deployed as the ITSM stand-in (manifests included)

## Usage

1. **Apply the example manifests:**

   ```bash
   kubectl apply -k examples/k8s-pod-failure-ticket/
   ```

2. **Cause a pod failure:**

   ```bash
   kubectl run fail-test --image=busybox --restart=Never -- /bin/false
   ```

3. **Watch for FlowRuns:**

   ```bash
   kubectl get flowruns -n default -w
   ```

   A FlowRun should be created within a few seconds. The name contains the
   pod name, event type, and a timestamp.

4. **Verify Mockoon captured the ticket creation request:**

   ```bash
   kubectl exec -n default \
     $(kubectl get pod -n default -l app=mockoon-kpft -o jsonpath='{.items[0].metadata.name}') \
     -- wget -q -O - http://localhost:3001/api/logs | jq .
   ```

   The log should show a POST to `/tickets` with the pod metadata in the body.

5. **Cleanup:**

   ```bash
   kubectl delete -k examples/k8s-pod-failure-ticket/
   kubectl delete pod fail-test --ignore-not-found
   ```

## Known alpha limitations

- **Naive pluralization fallback**: The controller uses the Kubernetes discovery
  API to resolve canonical plural names (e.g. `Pod` → `pods`). If the discovery
  call fails, it falls back to appending `s` to the lowercased kind, which is
  incorrect for irregular plurals (`Ingress` → `ingresss`,
  `NetworkPolicy` → `networkpolicys`). Most core kinds work correctly.
- **No rate limiting**: High-churn resources can produce a FlowRun per event.
  Use `spec.resource.cooldown` (e.g. `"30s"`) to suppress bursts.
- **Cache sync retry**: If the informer fails to sync on startup (transient RBAC
  or API server issue), the watcher retries with exponential backoff (up to 5
  attempts, starting at 1s). If all attempts fail, the watcher deregisters and
  will be retried on the next Trigger reconcile.

## Implementation notes

The `type: resource` trigger design is documented in
[docs/architecture.md#kubernetes-resource-event-triggers](../../docs/architecture.md#kubernetes-resource-event-triggers).
Key design decisions:

- Resource triggers run inside the controller via dynamic informers (no separate
  gateway image)
- `filterExpression` uses CEL or a simple expression language to match resource
  state
- `dedupKeyExpression` extracts a dedup key from the resource object to prevent
  duplicate FlowRuns
- The full resource object is passed as `trigger.body` to the Flow
