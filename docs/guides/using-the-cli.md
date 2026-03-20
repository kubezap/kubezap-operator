# Using the kubezap CLI

The `kubezap` CLI provides richer views of KubeZap resources than `kubectl get` offers. Its primary value is for FlowRun history — filtering by trigger, phase, and time window, and inspecting per-step execution timelines.

All commands are **read-only**. No resources are created, modified, or deleted.

---

## Installation

See [Overview → Installation](../overview.md#installation) for download links and binary placement.

After installing, verify connectivity:

```bash
kubezap version
# KubeZap CLI v0.0.1
# Operator: v0.0.1 (in namespace kubezap-system)
```

`kubezap` respects the same kubeconfig as `kubectl`. Use `--context` and `--namespace` / `-n` flags the same way:

```bash
kubezap history -n my-namespace --context prod-cluster
```

---

## FlowRun History

### List recent FlowRuns

```bash
kubezap history
```

```
NAME                              TRIGGER            FLOW              PHASE      DURATION   AGE
order-20260318-abc4f              order-webhook      order-router      Succeeded  0.45s      2h
order-20260318-dd12c              order-webhook      order-router      Failed     1.23s      2h
nightly-20260318-020000           nightly-report     generate-report   Succeeded  12.3s      6h
customer-events-p0-offset-18842   customer-events    process-order     Succeeded  0.89s      3h
```

### Filter by trigger

```bash
kubezap history --trigger order-webhook
```

Useful when a namespace has many triggers and you want to focus on one workflow.

### Filter by phase

```bash
# See only failures
kubezap history --phase Failed

# See currently running flows
kubezap history --phase Running
```

### Filter by time window

```bash
# Failures in the last hour
kubezap history --phase Failed --since 1h

# Everything from the last 24 hours, all namespaces
kubezap history -A --since 24h
```

### Combine filters

```bash
kubezap history --trigger order-webhook --phase Failed --since 6h
```

### Output as JSON or YAML

```bash
kubezap history --flow order-router -o json | jq '.[] | {name, phase, duration}'
```

### Live-tail FlowRun completions

```bash
kubezap history --watch
```

Prints a new row each time a FlowRun finishes. Useful during development to watch a trigger fire in real time without running `kubectl get flowruns -w`.

---

## Inspect a Single FlowRun

```bash
kubezap history order-20260318-abc4f
```

Shows a per-step execution timeline:

```
FlowRun: order-20260318-abc4f
Flow:    order-router
Trigger: order-webhook
Phase:   Succeeded
Started: 2026-03-18T02:00:00Z (2h ago)
Duration: 0.45s

STEP                PHASE      STARTED   DURATION   ATTEMPTS
extract-type        Succeeded  +0.00s    0.01s      1
enrich-order        Succeeded  +0.01s    0.38s      1
notify-express      Succeeded  +0.39s    0.06s      1
notify-standard     Skipped    +0.39s    —          —

Results from enrich-order:
  customerId = cust-001
  tier       = express
```

Use this whenever a FlowRun finishes unexpectedly or takes longer than expected.

---

## Trigger Status

```bash
kubezap triggers
```

```
NAME              TYPE      STATUS    LAST FIRED          ACTIVE FLOWRUNS   GC POLICY
order-webhook     webhook   Enabled   2026-03-18T04:12Z   0                 max 100 succeeded
nightly-report    cron      Enabled   2026-03-18T02:00Z   0                 ttl 24h
customer-events   pubsub    Enabled   2026-03-18T04:09Z   1                 max 50 succeeded
incident-alert    webhook   Disabled  —                   0                 —
```

**STATUS** reflects the `Accepted` condition: `Enabled` means the trigger is registered and active; `Disabled` means `spec.enabled: false`.

**ACTIVE FLOWRUNS** is the count of FlowRuns in `Running` or `Waiting` phase for that trigger. A persistently non-zero count indicates a backlog or stuck flow.

---

## Flow Status

```bash
kubezap flows
```

```
NAME               STEPS   READY   LAST USED               DESCRIPTION
order-router       4       True    2026-03-18T04:12Z       Routes orders by tier
generate-report    6       True    2026-03-18T02:00Z       —
process-order      5       True    2026-03-18T04:09Z       —
```

**LAST USED** is inferred from the most recent FlowRun that referenced this Flow. `—` means no FlowRun exists yet.

**READY** reflects the `Ready` condition set by the Flow reconciler. A `False` value means the Flow spec has a validation error — check the Flow status for details:

```bash
kubectl get flow order-router -o jsonpath='{.status.conditions}'
```

---

## Integration Status

```bash
kubezap integrations
```

```
NAME              TYPE    PLUGIN HEALTH   GATEWAY DEPLOYMENT
kafka-cluster     kafka   —               kubezap-kafka-gateway (1/1 Ready)
customer-svc      plugin  Healthy         customer-svc-plugin (1/1 Ready)
broken-plugin     plugin  Unhealthy       broken-plugin-plugin (0/1 Ready)
```

**PLUGIN HEALTH** shows the readiness probe status for `type: plugin` Integrations. `Healthy` means `GET /healthz → 200`.

**GATEWAY DEPLOYMENT** shows the Deployment created by the controller for broker-type Integrations and plugins.

---

## Tips for Debugging

### Find what triggered a stuck FlowRun

```bash
# Find the oldest Running FlowRun
kubezap history --phase Running --since 24h

# Inspect it
kubezap history <flowrun-name>
```

The step timeline will show which step is stuck and how many retry attempts have been made.

### Correlate a FlowRun with logs

Every FlowRun has a label `kubezap.io/trigger` you can use to find associated controller log lines:

```bash
FR=order-20260318-abc4f
kubectl logs -l control-plane=controller-manager -n kubezap-system | grep $FR
```

If OTel tracing is enabled, the FlowRun name appears as the `kubezap.flowrun.name` span attribute. Search for it in Jaeger, Tempo, or your trace backend.

### Check auth failures on a webhook trigger

```bash
kubezap triggers
# Note the trigger name

# Then inspect metrics (requires port-forward to gateway)
kubectl port-forward svc/kubezap-webhook-gateway -n <ns> 8080:8080
curl -s http://localhost:8080/metrics | grep kubezap_webhook_auth_failures
```

See the [Observability guide](observability.md) for PromQL queries.

---

## Next Steps

- [Getting Started](getting-started.md) — run an end-to-end webhook workflow
- [Observability](observability.md) — Prometheus metrics and OTel traces
- [Troubleshooting](troubleshooting.md) — common issues and fixes
