# Example: Kubernetes Pod Failure -> ITSM Ticket

> **This example requires `type: resource` trigger which is not yet implemented.**
> The manifests document the intended pattern. Apply them only for reference --
> the Trigger will not function until the feature is available.

## Overview

This example demonstrates how KubeZap can watch for Kubernetes resource events
and trigger automated workflows in response. When a Pod transitions to the
`Failed` phase, KubeZap opens an ITSM ticket containing the pod name, namespace,
failure reason, and a deduplication key derived from the pod UID.

## What it demonstrates

Once `type: resource` triggers are implemented, this example will show:

- **Kubernetes resource-event trigger** -- watching Pods for `phase == "Failed"`
- **Event metadata extraction** -- pulling pod name, namespace, UID, and failure
  reason from the resource event body via a transform step
- **HTTP action with retry** -- POSTing a ticket to an ITSM system with
  exponential backoff (2 retries, 1s initial delay, 10s max)
- **Deduplication via pod UID** -- the `dedupKeyExpression` on the Trigger
  ensures that re-processing the same pod failure does not create duplicate
  FlowRuns

## Prerequisites

- KubeZap operator installed and running
- `type: resource` trigger implemented (future -- not yet available)

## Intended usage (once implemented)

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

   A FlowRun should be created with the pod UID as part of its name.

4. **Verify deduplication:**

   Re-running the same pod failure should NOT create a second FlowRun because
   the `dedupKeyExpression` uses the pod UID as the dedup key.

5. **Verify Mockoon captured the ticket creation request:**

   ```bash
   kubectl exec -n default \
     $(kubectl get pod -n default -l app=mockoon-kpft -o jsonpath='{.items[0].metadata.name}') \
     -- wget -q -O - http://localhost:3001/api/logs | jq .
   ```

   The log should show a POST to `/tickets` with the pod metadata in the body.

6. **Cleanup:**

   ```bash
   kubectl delete -k examples/k8s-pod-failure-ticket/
   kubectl delete pod fail-test --ignore-not-found
   ```

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
