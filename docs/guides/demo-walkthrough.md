# Demo Walkthrough Checklists

Step-by-step kubectl command sequences for running each demo against a live cluster.
These assume KubeZap is already installed and the controller is running in `kubezap-system`.

---

## Prerequisites

```bash
# Confirm controller is running
kubectl get pods -n kubezap-system

# Set a working namespace (both demos use default)
export NS=default
```

---

## Demo 1 — Kafka Event Enrichment Pipeline

**What it shows:** A Kafka message triggers a flow that enriches a customer event with profile
data, routes it to a tier-specific sink via conditional branching, then publishes the enriched
event back to Kafka.

**Requires:** A Kafka broker reachable at `kafka.kafka.svc.cluster.local:9092` (e.g. Strimzi or
a dev Kafka in-cluster). Edit `config/samples/demo/kafka-enrichment/integration.yaml` to point at
your broker before applying.

### Step 1 — Apply CRs

```bash
kubectl apply -k config/samples/demo/kafka-enrichment/
```

### Step 2 — Verify MockEndpoints are registered

```bash
# All three mock endpoints should appear
kubectl get mockendpoints -n $NS

# Gateway should be running
kubectl get deployment kubezap-webhook-gateway -n $NS
```

### Step 3 — Verify Trigger is Accepted

```bash
kubectl get trigger customer-events -n $NS \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

### Step 4 — Verify Kafka gateway Deployment is running

```bash
kubectl get deployment -n $NS -l kubezap.io/component=kafka-gateway
# Should show 1/1 READY
```

### Step 5 — Produce a test message

```bash
# Exec into any pod with kafka-console-producer, or use a one-shot pod:
kubectl run kafka-producer --restart=Never --rm -it \
  --image=bitnami/kafka:latest \
  --command -- kafka-console-producer.sh \
    --bootstrap-server kafka.kafka.svc.cluster.local:9092 \
    --topic customer-events
# Then paste and Enter:
{"customerId":"cust-001","eventType":"purchase"}
# Ctrl+C to exit
```

### Step 6 — Watch a FlowRun appear

```bash
kubectl get flowruns -n $NS -l kubezap.io/trigger=customer-events -w
# A FlowRun named customer-events-p0-offset-<N> should appear within seconds
```

### Step 7 — Inspect step-by-step execution

```bash
# Get the FlowRun name
FR=$(kubectl get flowruns -n $NS -l kubezap.io/trigger=customer-events \
  -o jsonpath='{.items[0].metadata.name}')

# Overall phase
kubectl get flowrun $FR -n $NS -o jsonpath='{.status.phase}'
# Expected: Succeeded

# Per-step phases
kubectl get flowrun $FR -n $NS \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Expected:
#   extract-customer    Succeeded
#   enrich-profile      Succeeded
#   route-enterprise    Succeeded   (tier=enterprise from mock)
#   route-standard      Skipped
#   route-trial         Skipped
#   publish-enriched    Succeeded
```

### Step 8 — Inspect enrichment results

```bash
# View the enriched profile results from step 2
kubectl get flowrun $FR -n $NS \
  -o jsonpath='{.status.steps[?(@.name=="enrich-profile")].results[*]}'
# Expected: tier=enterprise, region=us-east-1, accountManager=alice@example.com
```

### Step 9 — Inspect MockEndpoint captured requests

```bash
# enterprise-sink should have captured one request
kubectl get mockendpoint enterprise-sink -n $NS \
  -o jsonpath='{.status.capturedRequests[-1:]}'
```

### Step 10 — Verify dedup (idempotency)

```bash
# Producing the same message again with a new offset creates a new FlowRun (different offset in name)
# Producing the exact same offset (replay scenario) is rejected — the FlowRun name already exists

kubectl get flowruns -n $NS -l kubezap.io/trigger=customer-events \
  -o jsonpath='{.items[*].metadata.name}'
# Each name encodes partition + offset: customer-events-p0-offset-0, p0-offset-1, etc.
```

### Cleanup

```bash
kubectl delete -k config/samples/demo/kafka-enrichment/
```

---

## Demo 3 — Incident Response Escalation

**What it shows:** An alert webhook triggers a flow that pages on-call and fires auto-mitigation
in parallel, then waits 2 minutes, checks health, and conditionally escalates.

**Requires:** Only the webhook gateway — no external broker needed.

### Step 1 — Apply CRs

```bash
kubectl apply -k config/samples/demo/incident-escalation/
```

### Step 2 — Verify Trigger is Accepted and gateway is running

```bash
kubectl get trigger incident-escalation -n $NS \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True

kubectl get deployment kubezap-webhook-gateway -n $NS
# Expected: 1/1 READY
```

### Step 3 — Send a test alert

```bash
# Get the gateway ClusterIP (or use port-forward if running externally)
GW=$(kubectl get svc kubezap-webhook-gateway -n $NS -o jsonpath='{.spec.clusterIP}')

# From inside the cluster (run a one-shot curl pod):
kubectl run alert-sender --restart=Never --rm -it \
  --image=curlimages/curl:latest \
  --command -- curl -s -o /dev/null -w '%{http_code}' \
    -X POST http://${GW}:8080/hooks/alert \
    -H 'Content-Type: application/json' \
    -d '{"alertname":"HighErrorRate","service":"api-server"}'
# Expected: 201
```

### Step 4 — Watch the FlowRun appear

```bash
kubectl get flowruns -n $NS -l kubezap.io/trigger=incident-escalation -w
# A FlowRun appears immediately; phase starts as Running
```

### Step 5 — Observe parallel immediate steps complete

```bash
FR=$(kubectl get flowruns -n $NS -l kubezap.io/trigger=incident-escalation \
  -o jsonpath='{.items[0].metadata.name}')

kubectl get flowrun $FR -n $NS \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Within ~5s:
#   page-oncall          Succeeded
#   trigger-mitigation   Succeeded
#   wait-for-resolution  Waiting      ← flow is paused here
#   check-health         Pending
#   escalate             Pending
```

### Step 6 — Observe the Waiting phase with resumeAfter

```bash
kubectl get flowrun $FR -n $NS \
  -o jsonpath='{.status.steps[?(@.name=="wait-for-resolution")].resumeAfter}'
# Prints the ISO timestamp when the wait will resume (now + 2 minutes)
```

### Step 7 — Wait ~2 minutes, then watch the flow complete

```bash
# Keep watching — after resumeAfter elapses the controller requeues and continues
kubectl get flowrun $FR -n $NS -w

# When done, check final step phases:
kubectl get flowrun $FR -n $NS \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Expected:
#   page-oncall          Succeeded
#   trigger-mitigation   Succeeded
#   wait-for-resolution  Succeeded
#   check-health         Succeeded
#   escalate             Skipped     ← "true == false" when expression
```

### Step 8 — Verify restart-safety (optional)

```bash
# Delete the controller pod while the flow is in Waiting phase
# The new pod picks up the persisted resumeAfter and requeues correctly
kubectl delete pod -n kubezap-system -l control-plane=controller-manager

# Watch the FlowRun — it should still complete on schedule after the controller restarts
kubectl get flowrun $FR -n $NS -w
```

### Step 9 — Inspect httpbin responses (stand-in for Slack/PagerDuty)

```bash
# page-oncall and trigger-mitigation results contain the httpbin response body
kubectl get flowrun $FR -n $NS \
  -o jsonpath='{.status.steps[?(@.name=="page-oncall")].results[*]}'
```

### Cleanup

```bash
kubectl delete -k config/samples/demo/incident-escalation/
```
