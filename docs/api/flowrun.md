# FlowRun CRD

A `FlowRun` is an execution instance of a `Flow`. Gateways create a `FlowRun` each time a trigger fires; the KubeZap controller watches for new `FlowRun` resources and executes the referenced `Flow`. Every execution is a persistent Kubernetes resource — you can inspect it with `kubectl` while it is running and after it completes.

---

## Contents

- [FlowRun CRD](#flowrun-crd)
  - [Contents](#contents)
  - [Overview](#overview)
  - [Lifecycle](#lifecycle)
    - [Step Lifecycle](#step-lifecycle)
  - [Who Creates FlowRuns](#who-creates-flowruns)
  - [Spec Reference](#spec-reference)
    - [FlowRunSpec](#flowrunspec)
    - [FlowReference](#flowreference)
    - [TriggerReference](#triggerreference)
    - [TriggerData](#triggerdata)
    - [ParamValue](#paramvalue)
  - [Cross-namespace flows](#cross-namespace-flows)
  - [Status Reference](#status-reference)
    - [FlowRunStatus](#flowrunstatus)
    - [Conditions](#conditions)
    - [StepRunStatus](#steprunstatus)
    - [ResultValue](#resultvalue)
    - [Printer Columns](#printer-columns)
  - [Garbage Collection](#garbage-collection)
    - [TTL-Based GC (time-to-live)](#ttl-based-gc-time-to-live)
    - [Count-Based GC (history limit)](#count-based-gc-history-limit)
    - [Active FlowRuns are exempt](#active-flowruns-are-exempt)
    - [Retain annotation](#retain-annotation)
  - [Deduplication](#deduplication)
    - [Kafka (at-least-once delivery)](#kafka-at-least-once-delivery)
    - [Webhook](#webhook)
  - [Examples](#examples)
    - [Example 1: Webhook-triggered FlowRun](#example-1-webhook-triggered-flowrun)
    - [Example 2: Kafka-triggered FlowRun](#example-2-kafka-triggered-flowrun)
    - [Example 3: Cron-triggered FlowRun](#example-3-cron-triggered-flowrun)
  - [kubectl Reference](#kubectl-reference)
    - [Watch a FlowRun execute in real time](#watch-a-flowrun-execute-in-real-time)
    - [Get all step results](#get-all-step-results)
    - [Find the most recent FlowRun for a trigger](#find-the-most-recent-flowrun-for-a-trigger)

---

## Overview

`FlowRun` is the decoupling mechanism between the event-receiving gateways and the flow-executing controller. This separation means:

- **Gateways are stateless routers** — they receive events and write FlowRuns, then move on
- **Every execution is auditable** — FlowRun persists in etcd with full trigger metadata, step results, and timing
- **Scaling is independent** — webhook gateways and Kafka gateways scale based on load; the controller scales based on concurrency requirements
- **Crash recovery is automatic** — if the controller restarts mid-execution, it reconciles in-progress FlowRuns from CRD state

```
  Webhook Request ──► webhook-gateway ──► creates FlowRun ──► controller picks up
  Kafka Message   ──► kafka-gateway   ──► creates FlowRun ──► controller picks up
  Cron Schedule   ──► controller      ──► creates FlowRun ──► controller picks up
  K8s Event       ──► controller      ──► creates FlowRun ──► controller picks up
```

---

## Lifecycle

```
  ┌─────────┐
  │ Pending │  FlowRun created; controller has not yet started execution
  └────┬────┘
       │  controller picks up FlowRun
       ▼
  ┌─────────┐
  │ Running │  Controller is executing steps
  └────┬────┘
       │
       ├──► all steps Succeeded ──────────────────────────────► ┌───────────┐
       │                                                         │ Succeeded │
       │                                                         └───────────┘
       ├──► any step Failed (no onFailure handler, or ──────────► ┌────────┐
       │    handler also failed)                                   │ Failed │
       │                                                           └────────┘
       └──► cancelled via annotation ──────────────────────────► ┌───────────┐
                                                                  │ Cancelled │
                                                                  └───────────┘
```

### Step Lifecycle

Each step within a FlowRun follows its own phase:

```
Pending ──► Running ──► Succeeded
                   ──► Failed ──► (retry) ──► Running
                                         ──► Failed (max attempts reached)
                   ──► Skipped   (when condition was false)
                   ──► Waiting   (wait step — paused until resumeAfter time)
```

A `Waiting` step has persisted a `resumeAfter` timestamp to `FlowRun.status`. The controller requeues the FlowRun at that time and resumes execution. This state survives controller restarts — the `resumeAfter` field in status is the authoritative source of truth for when to re-check.

A FlowRun is `Succeeded` only when all non-skipped steps reach `Succeeded`. A single step that exhausts its retries and remains `Failed` causes the entire FlowRun to be `Failed`, unless an `onFailure` handler succeeds.

---

## Who Creates FlowRuns

FlowRuns are always created by KubeZap components — never directly by users (though you can create them manually for testing).

| Creator                   | Trigger Type                           | FlowRun Naming Pattern                          |
| ------------------------- | -------------------------------------- | ----------------------------------------------- |
| `kubezap-webhook-gateway` | `spec.type: webhook`                   | `<trigger-name>-<timestamp>-<random>`           |
| `kubezap-kafka-gateway`   | `spec.type: kafka`                     | `<trigger-name>-p<partition>-offset-<offset>`   |
| `kubezap-amqp-gateway`    | `spec.type: amqp`                      | `<trigger-name>-p<partition>-offset-<offset>`   |
| `kubezap-nats-gateway`    | `spec.type: nats`                      | `<trigger-name>-<subject>-<sequence>`           |
| `kubezap-controller`      | `spec.type: cron`                      | `<trigger-name>-<scheduled-time>`               |
| `kubezap-controller`      | Kubernetes resource events _(alpha)_   | `<trigger-name>-<resource-name>-<event-type>-<timestamp>-<random>` |

The Kafka naming convention (`-p0-offset-12345`) is the deduplication key — see [Deduplication](#deduplication).

---

## Spec Reference

### FlowRunSpec

| Field              | Type                 | Required | Description                                                                                                                                                      |
| ------------------ | -------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `flowRef`          | FlowReference        | **Yes**  | Reference to the `Flow` to execute. The `Flow` must be in the same namespace as the FlowRun. See [Cross-namespace flows](#cross-namespace-flows).                |
| `params`           | []ParamValue         | No       | Explicit values for the `Flow`'s declared parameters; each entry's `value` is resolved through `$(...)` interpolation. Wins over auto-derivation from `triggerData` and over any declared `default`. See [ParamDeclaration](flow.md#paramdeclaration) for the full resolution order. |
| `triggerRef`       | TriggerReference     | No       | Reference to the Trigger that created this FlowRun.                                                                                                              |
| `triggerData`      | TriggerData          | No       | Snapshot of the triggering event (payload, metadata).                                                                                                            |
| `ttlAfterFinished` | duration             | No       | How long to retain the FlowRun after it reaches a terminal phase. Overrides the operator-level default. Examples: `24h`, `7d`. Set to `0` to delete immediately. |

### TriggerReference

| Field  | Type   | Description                                                          |
| ------ | ------ | -------------------------------------------------------------------- |
| `name` | string | Name of the Trigger that created this FlowRun                        |
| `type` | string | Trigger type: `webhook`, `cron`, `kafka`, `amqp`, `nats`, `resource` |

### TriggerData

Snapshot of the event that caused this FlowRun. The full set of fields depends on the trigger type; unpopulated fields are omitted.

| Field           | Type              | Description                                                |
| --------------- | ----------------- | ---------------------------------------------------------- |
| `source`        | string            | `webhook`, `cron`, `kafka`, `kubernetes-event`             |
| `method`        | string            | HTTP method (webhook only)                                 |
| `path`          | string            | URL path (webhook only)                                    |
| `headers`       | map[string]string | Request headers (webhook only; sensitive headers redacted) |
| `topic`         | string            | Kafka topic (kafka only)                                   |
| `partition`     | integer           | Kafka partition (kafka only)                               |
| `offset`        | integer           | Kafka message offset (kafka only)                          |
| `kafkaHeaders`  | map[string]string | Kafka message headers (kafka only)                         |
| `scheduledTime`        | timestamp         | Scheduled fire time (cron only)                                    |
| `body`                 | string            | Request or message body (truncated at 64KB by default for webhook triggers — configurable via the webhook gateway's `--max-stored-body-bytes` flag; see [Webhook Security](../guides/webhook-security.md#body-size-limits)) |
| `bodyTruncated`        | boolean           | `true` if the body exceeded the limit and was truncated            |
| `contentType`          | string            | Content-Type of the body                                           |
| `eventType`            | string            | Kubernetes watch event type: `ADDED`, `MODIFIED`, `DELETED` (resource triggers only) |
| `resourceName`         | string            | Name of the watched resource (resource triggers only)              |
| `resourceNamespace`    | string            | Namespace of the watched resource (resource triggers only)         |
| `resourceAPIVersion`   | string            | API version of the watched resource (resource triggers only)       |
| `resourceKind`         | string            | Kind of the watched resource (resource triggers only)              |

### FlowReference

| Field  | Type   | Required | Description                                                                        |
| ------ | ------ | -------- | ---------------------------------------------------------------------------------- |
| `name` | string | **Yes**  | Name of the `Flow` CR to execute. Must be in the same namespace as the `FlowRun`. |

Cross-namespace references (`flowRef.namespace`) are **not supported** in `v1alpha1`. Attempting to set this field is rejected by the validating admission webhook. See [Cross-namespace flows](#cross-namespace-flows) below.

### ParamValue

| Field   | Type   | Description                                                  |
| ------- | ------ | ------------------------------------------------------------ |
| `name`  | string | Parameter name (must match a `Flow.spec.params` declaration) |
| `value` | string | Parameter value                                              |

---

## Cross-namespace flows

Cross-namespace FlowRefs — where a `FlowRun` in namespace A triggers a `Flow` in namespace B — are **not supported in v1alpha1**.

This restriction exists for security reasons: allowing arbitrary cross-namespace access would require the controller to hold cluster-wide read access to `Flow` resources, which violates the least-privilege principle and conflicts with OpenShift restricted SCC requirements.

A validating admission webhook enforces this at the API layer: any `FlowRun` whose `spec.flowRef` carries a `namespace` field (for example, created against an older schema via raw YAML) is rejected with:

```
cross-namespace FlowRef is not supported; FlowRef.Namespace must be empty
(cross-namespace flows deferred to v1beta1 with FlowGrant CRD)
```

**Planned v1beta1 support:** Cross-namespace flows will be re-introduced in `v1beta1` via a `FlowGrant` CRD. A `FlowGrant` in the target namespace explicitly grants one or more source namespaces permission to reference a named `Flow`. This preserves namespace isolation while enabling controlled cross-namespace reuse.

---

## Status Reference

### FlowRunStatus

| Field            | Type            | Description                                                                                  |
| ---------------- | --------------- | -------------------------------------------------------------------------------------------- |
| `observedGeneration` | integer     | Most recent generation observed by the controller                                            |
| `phase`          | string          | Overall execution phase: `Pending`, `Running`, `Succeeded`, `Failed`, `Cancelled`            |
| `conditions`     | []Condition     | Standard conditions (see below)                                                              |
| `startTime`      | timestamp       | When the controller began executing the FlowRun                                              |
| `completionTime` | timestamp       | When the FlowRun reached a terminal phase                                                    |
| `steps`          | []StepRunStatus | Per-step execution status (see below)                                                        |
| `message`        | string          | Human-readable summary, especially on failure                                                |

### Conditions

| Type        | Status  | Meaning                          |
| ----------- | ------- | -------------------------------- |
| `Succeeded` | `True`  | All steps completed successfully |
| `Succeeded` | `False` | One or more steps failed         |
| `Running`   | `True`  | Execution is in progress         |

### StepRunStatus

| Field            | Type          | Description                                                                                                                                |
| ---------------- | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `name`           | string        | Step name (matches `Flow.spec.steps[].name`)                                                                                               |
| `phase`          | string        | `Pending`, `Running`, `Succeeded`, `Failed`, `Skipped`, `Waiting`                                                                          |
| `startTime`      | timestamp     | When this step began executing                                                                                                             |
| `completionTime` | timestamp     | When this step reached a terminal phase                                                                                                    |
| `attempts`       | integer       | Number of execution attempts (1 on first try; incremented on retry)                                                                        |
| `message`        | string        | Error message or skip reason                                                                                                               |
| `results`        | []ResultValue | Output values produced by this step                                                                                                        |
| `resumeAfter`    | timestamp     | Set by wait steps: the time after which the controller will re-evaluate this step. Persisted in status so it survives controller restarts. |

### ResultValue

| Field   | Type   | Description                                              |
| ------- | ------ | -------------------------------------------------------- |
| `name`  | string | Result name (matches `Flow.spec.steps[].results[].name`) |
| `value` | string | Result value captured from the step output               |

### Printer Columns

```bash
kubectl get flowruns -n automation
```

```
NAME                           FLOW           PHASE       AGE    DURATION
order-received-1710412335-x8k  handle-order   Succeeded   5m     3.2s
order-received-1710412280-j2q  handle-order   Failed      12m    1.8s
nightly-report-2026031402      gen-report     Running     30s    —
```

---

## Garbage Collection

FlowRuns accumulate over time. KubeZap garbage collects completed FlowRuns through two independent mechanisms that both run on each reconcile of a terminal FlowRun:

### TTL-Based GC (time-to-live)

Priority order (highest wins):

1. **`spec.ttlAfterFinished`** on the FlowRun — overrides everything; set per-FlowRun by the gateway at creation time
2. **`spec.flowRunGC.ttlAfterSucceeded` / `ttlAfterFailed`** on the `Trigger` — per-trigger override
3. **Operator-level defaults** — `--flowrun-ttl-succeeded` (default: `24h`) and `--flowrun-ttl-failed` (default: `72h`)

Failed FlowRuns default to a longer retention window than succeeded because they are more likely to be needed for debugging.

### Count-Based GC (history limit)

Mirrors the Kubernetes Job history limits pattern. Configured on the `Trigger` via `spec.flowRunGC`:

```yaml
spec:
  flowRunGC:
    maxSucceeded: 10      # keep last 10 succeeded FlowRuns
    maxFailed: 25         # keep last 25 failed FlowRuns (separate cap)
    ttlAfterSucceeded: 2h # per-trigger TTL override for succeeded
    ttlAfterFailed: 48h   # per-trigger TTL override for failed
```

### FlowRunGCPolicy

| Field               | Type     | Required | Default | Description                                                  |
| ------------------- | -------- | -------- | ------- | -------------------------------------------------------------- |
| `maxSucceeded`       | integer  | No       | `0` (no limit) | Maximum number of `Succeeded` FlowRuns to retain per Trigger. Oldest excess FlowRuns are deleted first. |
| `maxFailed`          | integer  | No       | `0` (no limit) | Maximum number of `Failed` FlowRuns to retain per Trigger, tracked separately from `maxSucceeded`. |
| `ttlAfterSucceeded`  | duration | No       | —       | Per-trigger override of the operator-level `--flowrun-ttl-succeeded` default for this Trigger's `Succeeded` FlowRuns. |
| `ttlAfterFailed`     | duration | No       | —       | Per-trigger override of the operator-level `--flowrun-ttl-failed` default for this Trigger's `Failed` FlowRuns. |

Count-based and TTL-based GC are **independent** — a FlowRun is eligible for deletion when either condition is met first.

Set `maxSucceeded: 0` or `maxFailed: 0` to disable count-based GC for that phase (TTL still applies).

### Active FlowRuns are exempt

FlowRuns in `Pending` or `Running` phase are never garbage collected automatically. (`Waiting` is a *step*-level phase, not a FlowRun-level one — a FlowRun with a step currently `Waiting` on a timer is itself still in `Running` phase. See [StepRunStatus](#steprunstatus) vs. the FlowRun-level phase enum above.)

### Retain annotation

To exempt a specific FlowRun from all GC (TTL and count-based):

```bash
kubectl annotate flowrun order-received-1710412335-x8k \
  -n automation \
  kubezap.io/retain=true
```

The garbage collector skips FlowRuns with this annotation.

---

## Deduplication

### Kafka (at-least-once delivery)

Kafka guarantees at-least-once delivery. A gateway crash after consuming a message but before committing the offset can cause the same message to be delivered again. KubeZap handles this by encoding the Kafka partition and offset in the FlowRun name:

```
<trigger-name>-p<partition>-offset-<offset>
```

For example: `order-events-p0-offset-12345`

Because Kubernetes object names are unique within a namespace, a second attempt to create a FlowRun with the same name will fail with a `409 Conflict` — the controller ignores this error and moves on. The original FlowRun (already created and potentially already executing) is unaffected.

### Webhook

Webhook FlowRuns include a random suffix (`<trigger-name>-<timestamp>-<random>`) and are not deduplicated. If idempotency matters for your use case, include a deduplication key in the request payload and use a `when` condition in the Flow to check for it.

---

## Examples

### Example 1: Webhook-triggered FlowRun

This is what the `kubezap-webhook-gateway` creates when it receives a POST to `/hooks/orders`:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: order-received-1710412335-x8k
  namespace: automation
  labels:
    kubezap.io/trigger: order-received
    kubezap.io/trigger-type: webhook
    kubezap.io/flow: handle-order
spec:
  flowRef:
    name: handle-order
  params:
    - name: orderId
      value: "ORD-9921"
  triggerRef:
    name: order-received
    type: webhook
  triggerData:
    source: webhook
    method: POST
    path: /hooks/orders
    body: '{"orderId": "ORD-9921", "customer": "Acme Corp"}'
    contentType: application/json
    headers:
      X-Request-Id: "abc123"
  ttlAfterFinished: 48h
```

After execution:

```yaml
status:
  phase: Succeeded
  startTime: "2026-03-14T10:32:15Z"
  completionTime: "2026-03-14T10:32:18Z"
  steps:
    - name: enrich-order
      phase: Succeeded
      startTime: "2026-03-14T10:32:15Z"
      completionTime: "2026-03-14T10:32:17Z"
      attempts: 1
      results:
        - name: customerName
          value: "Acme Corp"
        - name: status
          value: "confirmed"
    - name: notify-slack
      phase: Succeeded
      startTime: "2026-03-14T10:32:17Z"
      completionTime: "2026-03-14T10:32:18Z"
      attempts: 1
  conditions:
    - type: Succeeded
      status: "True"
      lastTransitionTime: "2026-03-14T10:32:18Z"
```

---

### Example 2: Kafka-triggered FlowRun

Created by `kubezap-kafka-gateway` for message at partition 0, offset 12345:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: order-events-p0-offset-12345
  namespace: automation
  labels:
    kubezap.io/trigger: order-events
    kubezap.io/trigger-type: kafka
    kubezap.io/flow: process-order
spec:
  flowRef:
    name: process-order
  params:
    - name: orderId
      value: "ORD-9922"
  triggerRef:
    name: order-events
    type: kafka
  triggerData:
    source: kafka
    topic: orders.created
    partition: 0
    offset: 12345
    body: '{"orderId": "ORD-9922"}'
    contentType: application/json
    kafkaHeaders:
      X-Correlation-Id: "corr-789"
```

---

### Example 3: Cron-triggered FlowRun

Created by the controller at the scheduled fire time:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: nightly-report-2026031402
  namespace: automation
  labels:
    kubezap.io/trigger: nightly-report
    kubezap.io/trigger-type: cron
    kubezap.io/flow: generate-report
spec:
  flowRef:
    name: generate-report
  triggerRef:
    name: nightly-report
    type: cron
  triggerData:
    source: cron
    scheduledTime: "2026-03-14T02:00:00Z"
  ttlAfterFinished: 168h  # 7 days
```

---

## kubectl Reference

| Command                                                             | Description                          |
| ------------------------------------------------------------------- | ------------------------------------ |
| `kubectl get flowruns -n <ns>`                                      | List all FlowRuns with phase and age |
| `kubectl get flowrun <name> -n <ns> -o yaml`                        | Full spec and status                 |
| `kubectl get flowrun <name> -n <ns> -o jsonpath='{.status.steps}'`  | Step statuses                        |
| `kubectl get flowruns -n <ns> -l kubezap.io/trigger=<trigger-name>` | All FlowRuns for a trigger           |
| `kubectl get flowruns -n <ns> --field-selector=status.phase=Failed` | All failed FlowRuns                  |
| `kubectl annotate flowrun <name> -n <ns> kubezap.io/cancel=true`    | Cancel a running FlowRun             |
| `kubectl annotate flowrun <name> -n <ns> kubezap.io/retain=true`    | Exempt from garbage collection       |
| `kubectl delete flowrun <name> -n <ns>`                             | Delete a FlowRun manually            |

### Watch a FlowRun execute in real time

```bash
kubectl get flowrun order-received-1710412335-x8k -n automation -w
```

### Get all step results

```bash
kubectl get flowrun order-received-1710412335-x8k -n automation \
  -o jsonpath='{range .status.steps[*]}{.name}{": "}{.phase}{"\n"}{end}'
```

### Find the most recent FlowRun for a trigger

```bash
kubectl get flowruns -n automation \
  -l kubezap.io/trigger=order-received \
  --sort-by=.metadata.creationTimestamp \
  -o name | tail -1
```
