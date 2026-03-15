# Incident Response Escalation

This guide walks you through a complete working example that demonstrates KubeZap's
incident response automation: a Kubernetes-native on-call escalation policy defined
entirely as code.

Instead of a runbook stored in a wiki (and ignored at 3 AM), the escalation logic lives
in a `Flow` CRD checked into source control, versioned, and executed reliably by the
operator every time an alert fires.

By the end you will have:

- A webhook endpoint that accepts alert payloads from any alerting system
- A flow that pages on-call and triggers auto-remediation in parallel
- A 2-minute wait step that pauses the flow to allow remediation to take effect
- A health check that determines whether escalation to the next tier is needed
- An escalate step that is conditionally skipped when the system recovers

---

## What You'll Build

```
Alert webhook  POST /hooks/alert
  │
  ├── page-oncall (HTTP → Slack/PagerDuty)         ─┐
  └── trigger-mitigation (HTTP → auto-remediation)  ─┘ [parallel]
                    ↓ (both complete)
        wait-for-resolution (2 min)
                    ↓
        check-health (GET health endpoint)
                    ↓
        escalate (HTTP → next tier)  [skipped if healthy]
```

`page-oncall` and `trigger-mitigation` have no `runAfter` dependency on each other,
so the operator executes them concurrently. Both are fast fire-and-forget HTTP calls
that complete before `wait-for-resolution` begins. This is the recommended pattern
for parallel side effects ahead of a wait step.

---

## Prerequisites

- A running Kubernetes cluster (k3s, kind, or any cluster with CRD support)
- `kubectl` configured to reach the cluster
- KubeZap operator deployed (see [Installation](../overview.md#installation))
- The webhook gateway accessible — either via `kubectl port-forward` or a Service of type LoadBalancer

---

## Step 1: Apply the CRs

```bash
kubectl apply -k config/samples/demo/incident-escalation/
```

This creates two resources in the `default` namespace:

- `Flow/incident-escalation` — the 5-step escalation workflow
- `Trigger/incident-escalation` — the webhook trigger on `/hooks/alert`

---

## Step 2: Verify the Trigger is Accepted

The operator reconciles the Trigger and registers the route with the webhook gateway:

```bash
kubectl get trigger incident-escalation \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

If the status is not `True` after a few seconds, check the controller logs:

```bash
kubectl logs -l control-plane=controller-manager -n kubezap-system
```

---

## Step 3: Send a Test Alert

Forward the webhook gateway port if not already externally accessible:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Fire a test alert payload:

```bash
curl -X POST http://localhost:8080/hooks/alert \
  -H "Content-Type: application/json" \
  -d '{"alertname":"HighErrorRate","service":"api-server"}'
```

The webhook gateway creates a `FlowRun` and returns immediately. The operator picks
up the FlowRun and begins executing the flow.

---

## Step 4: Watch the FlowRun

```bash
kubectl get flowruns -w
```

You will see the FlowRun transition through phases. Within the first few seconds,
`page-oncall` and `trigger-mitigation` both complete (they call httpbin, which
responds immediately). The FlowRun then enters `Waiting` phase as
`wait-for-resolution` pauses execution for 2 minutes.

---

## Step 5: Observe the Waiting Phase

While the flow is paused, inspect the wait step status:

```bash
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[?(@.name=="wait-for-resolution")].phase}'
# Expected: Waiting
```

The resume timestamp is persisted in the FlowRun status so the wait survives
controller restarts:

```bash
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[?(@.name=="wait-for-resolution")].resumeAfter}'
# Expected: an RFC3339 timestamp 2 minutes after the FlowRun started
```

---

## Step 6: Observe Completion and the Skipped Escalation

After 2 minutes, the operator resumes the flow. `check-health` runs (a GET to
httpbin), and then `escalate` is evaluated. Because the demo uses
`"true" == "false"` as the `when` condition, `escalate` is always `Skipped`.

Inspect all step phases once the FlowRun completes:

```bash
# Step names
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[*].name}' | tr ' ' '\n'

# Step phases
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[*].phase}' | tr ' ' '\n'
```

Expected output:

```
page-oncall          → Succeeded
trigger-mitigation   → Succeeded
wait-for-resolution  → Succeeded
check-health         → Succeeded
escalate             → Skipped    # when condition was false
```

---

## What This Demonstrates

| Feature | How it shows up |
|---|---|
| Webhook trigger | `curl` to `/hooks/alert` creates a FlowRun |
| Parallel steps | `page-oncall` and `trigger-mitigation` run concurrently |
| Wait step | `wait-for-resolution` pauses the flow for 2 minutes |
| Step result passing | `resultMappings` captures the health check response |
| Conditional escalation | `when` expression determines whether `escalate` runs |
| Skipped steps | `escalate` is `Skipped` when the condition is false |

---

## Adapting for Production

**Replace httpbin URLs with real endpoints**

- `page-oncall`: replace with your Slack incoming webhook URL or PagerDuty Events API endpoint
- `trigger-mitigation`: replace with your auto-remediation service endpoint (restart API, Ansible AWX job trigger, etc.)
- `escalate`: replace with your next-tier paging endpoint

**Set a realistic wait duration**

Change `duration: "2m"` on `wait-for-resolution` to match your SLA. Common values:

```yaml
wait:
  duration: "10m"   # fast SLA (P1 incidents)
  # duration: "30m"  # medium SLA (P2 incidents)
  # duration: "1h"   # slow SLA (P3/change windows)
```

**Update the health check**

Replace the httpbin GET with your real health endpoint and adjust the JSONPath
to extract a meaningful health signal:

```yaml
url: "https://healthcheck.internal/api/services/$(trigger.payload.service)"
method: GET
resultMappings:
  status: "$.status"   # expects {"status": "healthy"} or {"status": "degraded"}
```

**Update the escalation condition**

Change the `when` expression on `escalate` from the demo stub to a real check
against the health step result:

```yaml
when:
  - expression: 'steps.check_health.results.status != "healthy"'
```

The step name uses underscores in CEL expressions (`check_health` not `check-health`).

**Parallel-then-wait pattern**

The parallel steps (`page-oncall`, `trigger-mitigation`) must complete before the
`wait-for-resolution` step begins — this is enforced by listing both in its
`runAfter`. For this to work smoothly, your real-world equivalents should be fast
HTTP calls that return `202 Accepted` immediately (fire-and-forget). Long-running
synchronous calls will delay the start of the wait period and throw off your SLA
timing. If a step needs to poll for completion, model it as a separate step after
the wait.

---

## Cleanup

```bash
kubectl delete -k config/samples/demo/incident-escalation/
```

---

## Troubleshooting

**FlowRun stuck in `Running` before the wait step**

The parallel steps may be failing. Check which step is stuck:

```bash
kubectl get flowrun <name> -o jsonpath='{.status.steps[*].phase}' | tr ' ' '\n'
```

Then check the controller logs for error details:

```bash
kubectl logs -l control-plane=controller-manager -n kubezap-system
```

**FlowRun stuck in `Waiting` longer than expected**

The wait is working as designed. Verify the resume timestamp:

```bash
kubectl get flowrun <name> \
  -o jsonpath='{.status.steps[?(@.name=="wait-for-resolution")].resumeAfter}'
```

If the timestamp has passed and the flow is still Waiting, the controller may have
lost its requeue. Restart the controller pod to trigger a re-sync:

```bash
kubectl rollout restart deployment/kubezap-controller-manager -n kubezap-system
```

**Gateway not registering the route**

Confirm the Trigger has `status.conditions[Accepted]=True` and check the gateway logs:

```bash
kubectl logs -l app=kubezap-webhook-gateway -n default | grep incident-escalation
```

---

## Next Steps

- Add [HMAC authentication](webhook-security.md) to restrict who can fire the alert webhook
- Add a `retryPolicy` to `page-oncall` and `trigger-mitigation` so transient network errors don't silently drop the page
- Replace the httpbin mock with real Slack and PagerDuty endpoints
- Set up [Prometheus metrics](observability.md) to track FlowRun durations and escalation rates
