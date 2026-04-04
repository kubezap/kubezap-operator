# Manual E2E Validation Context — 2026-04-04

This document provides context for agents running the manual end-to-end validation
pass (§16 VALIDATION / §19 example tasks) on the local k3s cluster.

---

## Cluster Environment

- **k3s** running locally on `caleb-xps15`, kubectl context name: **`default`**
- **KubeZap controller** deployed in `kubezap-system`, running and healthy as of 2026-04-04
- Always use `kubectl --context default` to target k3s (not the Kind cluster used by e2e tests)
- The Kind cluster (`kind-kubezap-test-e2e`) is used only for automated `make test-e2e` runs

### Switch context to k3s

```bash
kubectl config use-context default
```

### Verify controller is running

```bash
kubectl get pods -n kubezap-system
# Expected: kubezap-controller-manager-* Running
```

---

## Image Refresh Before Running Examples

If the controller image on k3s is stale, rebuild and redeploy:

```bash
# Build and load into k3s (k3s uses containerd; import directly)
make docker-build IMG=ghcr.io/kubezap/controller:latest
docker save ghcr.io/kubezap/controller:latest | sudo k3s ctr images import -
kubectl rollout restart deployment/kubezap-controller-manager -n kubezap-system
```

For the webhook gateway (needed by webhook-trigger examples):

```bash
docker build -t ghcr.io/kubezap/webhook-gateway:latest -f cmd/webhook-gateway/Dockerfile .
docker save ghcr.io/kubezap/webhook-gateway:latest | sudo k3s ctr images import -
```

---

## Webhook Gateway Access

The webhook gateway is deployed per-namespace as a Deployment when a Trigger with
`type: webhook` is applied. After applying an example:

```bash
# Port-forward to access the webhook gateway (replace NAMESPACE as needed)
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

---

## Examples Overview and Requirements

| Example | Trigger Type | Automation Level | External Requirements |
|---------|-------------|------------------|-----------------------|
| order-router | Webhook | **Fully automatable** | None (Mockoon in-cluster) |
| incident-escalation | Webhook | **Fully automatable** | httpbin.org (internet) |
| github-autolabel | Webhook (HMAC) | **User must run** | GitHub repo + PAT |
| slack-router | Webhook (HMAC) | **Automatable with curl** | Slack app (or simulate HMAC locally) |
| nightly-export | Cron | **Fully automatable** | MinIO (in-cluster), Slack optional |
| kafka-enrichment | Kafka | **Fully automatable** | Strimzi on k3s (kafka ns) |
| dlq-handler | Kafka | **Fully automatable** | Strimzi on k3s (kafka ns) |
| k8s-pod-failure-ticket | Resource (alpha) | **Fully automatable** | None (Mockoon in-cluster) |
| multi-tenant-fanout | Webhook | **Fully automatable** | None (Mockoon in-cluster) |
| oidc-webhook | Webhook (OIDC) | **Fully automatable** | Dex (in-cluster) |

---

## Example-Specific Notes

### order-router
- Self-contained: Mockoon provides `/enrich-order`, `/notify-express`, `/notify-standard`
- Uses SEQUENTIAL Mockoon mode — first request → express path, second → standard path
- Apply: `kubectl apply -k examples/order-router/` (namespace: default)
- Verify: Trigger Accepted, Mockoon running, port-forward to 8080, curl, check FlowRun

### incident-escalation
- Uses httpbin.org for HTTP calls (needs internet access from within k3s)
- Has a 2-minute wait step — FlowRun will be in `Waiting` phase during that time
- Expected total duration: ~2.5 minutes
- Apply: `kubectl apply -k examples/incident-escalation/`

### github-autolabel
- Requires: GitHub repo with admin access, Personal Access Token, ngrok or LoadBalancer
- User must create Secrets manually before applying manifests (see example README)
- User must configure GitHub webhook in repo settings
- Cannot be automated without GitHub credentials
- **Pending input**: See `docs/tech-debt/pending-input-required.md`

### slack-router
- Requires: Slack app with slash command, or simulate with curl + local HMAC signing
- The HMAC signing can be simulated locally — no real Slack app required for basic validation
- IP allowlist in trigger.yaml may need to be removed for local testing
- See example README for curl-based HMAC simulation steps

### nightly-export
- Deploy MinIO via: `kubectl apply -f examples/nightly-export/minio.yaml`
- Create `exports` bucket via mc CLI after port-forwarding MinIO
- Slack notifications are optional — the flow uses `onFailure: Continue` for notify-slack
- Trigger a test run manually with a FlowRun (cron fires at 02:00 ET — don't wait)

### kafka-enrichment and dlq-handler
- **Kafka is available on k3s**: Strimzi cluster `my-cluster` in the `kafka` namespace
- Bootstrap server (in-cluster): `my-cluster-kafka-bootstrap.kafka.svc.cluster.local:9092`
- External NodePort (from laptop): `<k3s-node-ip>:32750`
- Before applying, edit each example's `integration.yaml` broker to use the Strimzi address
- For dlq-handler: create topics `orders` and `orders.dlq` via KafkaTopic CRs first

### k8s-pod-failure-ticket
- Alpha resource trigger — see known limitations in README
- Fully self-contained with Mockoon
- Cause a pod failure with: `kubectl run fail-test --image=busybox --restart=Never -- /bin/false`
- Note: requires RBAC for pod watching in the target namespace (see README)

### multi-tenant-fanout
- Fully self-contained: three Mockoon stubs, three Integration resources
- Tests per-tenant credential injection and `failurePolicy: Continue`
- Apply: `kubectl apply -k examples/multi-tenant-fanout/`

### oidc-webhook
- Fully self-contained: Dex as in-cluster OIDC provider
- Requires port-forwarding Dex (5556) and gateway (8080)
- Token obtained via: `curl -X POST http://localhost:5556/dex/token ...`

---

## CLI Verification Checklist

After running examples, verify the kubezap CLI:

```bash
# Build the CLI if not already done
go build -o bin/kubezap ./cmd/kubezap/

# Watch FlowRuns live
./bin/kubezap watch -n default

# History for a specific FlowRun
./bin/kubezap history <flowrun-name> -n default

# List triggers
./bin/kubezap triggers -n default

# List flows
./bin/kubezap flows -n default
```

---

## Web Dashboard Verification

```bash
# Port-forward the UI service
kubectl port-forward svc/kubezap-ui 8082:8082 -n kubezap-system

# Open in browser
# http://localhost:8082
```

Key checks:
- Dashboard loads and shows namespace selector
- FlowRuns list updates live
- Trigger and Flow listings work
- Activity feed is populated

---

## Output Format for Validation Results

Each example task should produce a brief validation note in `docs/tech-debt/` named
`manual-e2e-results-YYYY-MM-DD.md` with:
- Example name
- Pass / Fail / Skip (with reason)
- Any surprising behavior
- Follow-up schedule items if issues found

---

## Known Issues at Time of Validation (2026-04-04)

- `StepRunStatus.Attempts` now correctly threaded (§18 P1 fix applied 2026-03-27)
- `type: resource` trigger is alpha-quality — k8s-pod-failure-ticket may exhibit known limitations
- Executor RPC transport failures still fail immediately (§18 P2 — not yet fixed)
