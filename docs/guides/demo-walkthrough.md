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
# All four mock endpoints should appear
kubectl get mockendpoints -n $NS

# The webhook gateway must be running — it serves /mock/* for the flow steps.
# The MockEndpoint controller ensures the gateway is created automatically.
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
# Exec into the Strimzi broker pod — no extra image needed
kubectl exec -n kafka my-cluster-dual-role-0 -- \
  bash -c 'echo "{\"customerId\":\"cust-001\",\"eventType\":\"purchase\"}" | \
  /opt/kafka/bin/kafka-console-producer.sh \
    --bootstrap-server my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092 \
    --topic customer-events'
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
  -o jsonpath='{.status.recentRequests[-1:]}'
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

## Demo 2 — Incident Response Escalation

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

---

## Demo 3 — GitHub Webhook → Auto-Label PR

**What it shows:** A GitHub pull_request webhook event (HMAC-signed) triggers a flow that extracts
the PR number and action, then conditionally applies a label via the GitHub API. Opening a PR adds
"needs-review"; closing it adds "merged". Demonstrates `$(trigger.headers.*)` access, CEL
conditional branching, and GitHub API integration with a bearer token from a Secret.

**Requires:** A GitHub repository with admin access, a Personal Access Token with `issues: write`
scope, and the KubeZap webhook gateway exposed externally (ngrok or LoadBalancer). See
`docs/guides/github-autolabel.md` for the full setup.

### Step 1 — Create secrets

```bash
kubectl create secret generic github-webhook-secret \
  --from-literal=secret='<your-github-webhook-secret>' -n $NS

kubectl create secret generic github-api-token \
  --from-literal=token='ghp_<your-github-personal-access-token>' -n $NS
```

### Step 2 — Patch the Flow with your repo name

```bash
# Edit flow.yaml and replace YOUR_ORG/YOUR_REPO before applying
# Or patch after applying:
kubectl patch flow github-autolabel -n $NS --type=json \
  -p '[{"op":"replace","path":"/spec/steps/1/action/http/url","value":"https://api.github.com/repos/ACTUAL_ORG/ACTUAL_REPO/issues/$(steps.extract_pr.results.prNumber)/labels"},{"op":"replace","path":"/spec/steps/2/action/http/url","value":"https://api.github.com/repos/ACTUAL_ORG/ACTUAL_REPO/issues/$(steps.extract_pr.results.prNumber)/labels"}]'
```

### Step 3 — Apply CRs

```bash
kubectl apply -k config/samples/demo/github-autolabel/
```

### Step 4 — Verify Trigger is Accepted and gateway is running

```bash
kubectl get trigger github-pr-label -n $NS \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True

kubectl get deployment kubezap-webhook-gateway -n $NS
# Expected: 1/1 READY
```

### Step 5 — Expose the gateway and configure the GitHub webhook

```bash
# Port-forward for local testing (requires ngrok or similar for GitHub to reach you)
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n $NS &

# With ngrok:
# ngrok http 8080
# Note the public URL (e.g. https://abc123.ngrok.io)
```

In GitHub repo → Settings → Webhooks → Add webhook:
- Payload URL: `https://<your-ngrok-url>/hooks/github-pr`
- Content type: `application/json`
- Secret: your webhook secret
- Events: **Pull requests** only

### Step 6 — Open a test PR and watch the FlowRun appear

```bash
# Open a PR using gh CLI
gh pr create --title "Test KubeZap auto-label" --body "Testing" --base main

# Watch FlowRuns
kubectl get flowruns -n $NS -l kubezap.io/trigger=github-pr-label -w
# A FlowRun should appear within seconds
```

### Step 7 — Inspect step phases

```bash
FR=$(kubectl get flowruns -n $NS -l kubezap.io/trigger=github-pr-label \
  -o jsonpath='{.items[0].metadata.name}')

kubectl get flowrun $FR -n $NS \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Expected for PR opened:
#   extract-pr          Succeeded
#   label-needs-review  Succeeded
#   label-closed        Skipped
```

### Step 8 — Verify the label on GitHub

```bash
PR_NUM=$(kubectl get flowrun $FR -n $NS \
  -o jsonpath='{.status.steps[?(@.name=="extract-pr")].results[?(@.name=="prNumber")].value}')

curl -s -H "Authorization: Bearer $(kubectl get secret github-api-token -n $NS \
  -o jsonpath='{.data.token}' | base64 -d)" \
  https://api.github.com/repos/YOUR_ORG/YOUR_REPO/issues/${PR_NUM}/labels | jq '.[].name'
# Expected: "needs-review"
```

### Step 9 — Test the close path

```bash
# Close the PR (without merging)
gh pr close <pr-number>

# A new FlowRun should appear; label-closed runs, label-needs-review is Skipped
kubectl get flowruns -n $NS -l kubezap.io/trigger=github-pr-label -w
```

### Cleanup

```bash
kubectl delete -k config/samples/demo/github-autolabel/
kubectl delete secret github-webhook-secret github-api-token -n $NS
# Also delete the webhook in GitHub Settings → Webhooks
```
