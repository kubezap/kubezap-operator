# Nightly Database Export + S3 Upload

This example walks through a **scheduled export pipeline** — a cron trigger fires
at 02:00 every night, calls an export API to snapshot the database, uploads an
export manifest to MinIO (an in-cluster S3-compatible store), and posts a summary
to Slack. The flow continues even if the upload fails so the Slack notification
always delivers.

Features demonstrated by this example:

- **Timezone-aware cron trigger** — `schedule: "0 2 * * *"` with `timezone:
  "America/New_York"` using `$(trigger.scheduledTime)` to stamp the export.
- **Three-step result chain** — the export ID and row count flow from step 1
  through to the Slack message in step 3 via `$(steps.<name>.results.<key>)`.
- **Retry with exponential backoff** — the upload step retries up to 3 times with
  doubling delays so transient MinIO hiccups do not fail the run.
- **`failurePolicy: Continue`** — the flow continues executing even after a step
  failure, guaranteeing the Slack notification runs regardless of upload outcome.
- **`onFailure: Continue`** — a Slack delivery failure does not mark the FlowRun
  as Failed, keeping the FlowRun history clean.

---

## What you'll build

```
robfig/cron scheduler
        │  fires at 02:00 America/New_York every night
        │  sets trigger.scheduledTime = "2026-03-20T07:00:00Z"
        ▼
  ┌──────────────────────────────┐
  │ export-data                  │  POST /mock/export-api
  │                              │  body: { exportDate, tables }
  │                              │  results: exportId, rowCount, status
  └──────────────┬───────────────┘
                 │  (failurePolicy: Continue — next step runs even on failure)
                 ▼
  ┌──────────────────────────────┐
  │ upload-to-minio              │  PUT http://minio:9000/exports/nightly-<ts>.json
  │                              │  retryPolicy: Exponential, maxRetries=3
  │                              │  body: { exportDate, exportId, rowCount, status }
  └──────────────┬───────────────┘
                 │  (failurePolicy: Continue — notify always runs)
                 ▼
  ┌──────────────────────────────┐
  │ notify-slack                 │  POST Slack incoming webhook
  │ onFailure: Continue          │  text: summary with exportId + rowCount
  └──────────────────────────────┘
                 │
                 ▼
          FlowRun phase: Succeeded
```

---

## Prerequisites

- KubeZap installed and the webhook gateway Deployment running in the `default`
  namespace.
- `kubectl` configured to reach your cluster.
- `mc` (MinIO Client) available on your workstation — used for bucket setup.
  Install: `brew install minio/stable/mc` or download from
  [min.io/download](https://min.io/download).

---

## Step 1 — Deploy MinIO

The `minio.yaml` manifest deploys a single-replica MinIO instance in the
`default` namespace using an `emptyDir` volume (data is ephemeral — suitable for
local testing only).

```bash
kubectl apply -f examples/nightly-export/minio.yaml
kubectl rollout status deployment/minio -n default
```

Verify MinIO is reachable:

```bash
kubectl port-forward svc/minio 9000:9000 9001:9001 -n default &
curl -s http://localhost:9000/minio/health/ready
# Expected: 200 OK (empty body)
```

---

## Step 2 — Create the exports bucket

Use the MinIO Client to create the `exports` bucket and set an anonymous write
policy so the upload step in the flow can PUT objects without AWS Signature V4.

```bash
# Configure mc to point at the in-cluster MinIO (via port-forward from Step 1)
mc alias set myminio http://localhost:9000 minioadmin minioadmin

# Create the exports bucket
mc mb myminio/exports

# Allow anonymous reads and writes on the exports bucket
mc anonymous set public myminio/exports
```

> **Production note:** Anonymous write access is acceptable for a local demo.
> For production, use AWS IAM credentials, a MinIO service account, or pre-signed
> URLs. Rotate the `minioadmin` password before any production deployment.

---

## Step 3 — Configure the Slack webhook (optional)

If you want real Slack notifications, create a Slack app with an incoming webhook:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App →
   From scratch**.
2. Choose a name (e.g. `KubeZap Nightly`) and your workspace.
3. Under **Add features and functionality** → **Incoming Webhooks** → enable it.
4. Click **Add New Webhook to Workspace**, select a channel, and copy the webhook
   URL (format: `https://hooks.slack.com/services/T.../B.../...`).

Edit `examples/nightly-export/flow.yaml` and replace the placeholder:

```bash
# Replace REPLACE_WITH_SLACK_WEBHOOK_URL with your actual URL
sed -i 's|REPLACE_WITH_SLACK_WEBHOOK_URL|T.../B.../...|g' \
  examples/nightly-export/flow.yaml
```

If you skip this step, the `notify-slack` step will fail (HTTP 4xx to the
placeholder URL) but the FlowRun will still succeed because that step has
`onFailure: Continue`.

---

## Step 4 — Apply the manifests

```bash
kubectl apply -k examples/nightly-export/
```

Verify everything is accepted:

```bash
# Trigger should be Accepted
kubectl get trigger nightly-export -n default \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True

# Flow should be Ready
kubectl get flow nightly-db-export -n default \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
# Expected: True

# MockEndpoint registered
kubectl get mockendpoint export-api -n default
# Expected: export-api in the list

# MinIO running
kubectl get deployment minio -n default
# Expected: 1/1 READY
```

---

## Step 5 — Trigger a test run manually

The cron fires at 02:00 America/New_York. To test immediately, create a FlowRun
by hand that simulates a cron fire:

```bash
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: nightly-export-manual-001
  namespace: default
spec:
  flowRef:
    name: nightly-db-export
  triggerRef:
    name: nightly-export
  triggerData:
    scheduledTime: "2026-03-20T07:00:00Z"
EOF
```

Watch the FlowRun progress:

```bash
kubectl get flowrun nightly-export-manual-001 -n default -w
```

---

## Step 6 — Inspect the FlowRun

```bash
# Overall phase
kubectl get flowrun nightly-export-manual-001 -n default \
  -o jsonpath='{.status.phase}'
# Expected: Succeeded

# Per-step summary
kubectl get flowrun nightly-export-manual-001 -n default \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\t"}{.duration}{"\n"}{end}'
# Expected:
#   export-data      Succeeded   <duration>
#   upload-to-minio  Succeeded   <duration>
#   notify-slack     Succeeded   <duration>  (or Failed with onFailure: Continue)

# Step results (exportId and rowCount from step 1)
kubectl get flowrun nightly-export-manual-001 -n default \
  -o jsonpath='{range .status.steps[?(@.name=="export-data")].results[*]}{.name}{": "}{.value}{"\n"}{end}'
# Expected:
#   exportId: export-20260320-001
#   rowCount: 1250
#   status: completed
```

Use the `kubezap` CLI for a richer view:

```bash
kubezap history nightly-export-manual-001 -n default
```

---

## Step 7 — Verify the MinIO upload

Check that the manifest was written to the `exports` bucket:

```bash
mc ls myminio/exports/
# Expected: nightly-2026-03-20T07:00:00Z.json

mc cat myminio/exports/nightly-2026-03-20T07:00:00Z.json
# Expected:
# {
#   "exportDate": "2026-03-20T07:00:00Z",
#   "exportId":   "export-20260320-001",
#   "rowCount":   "1250",
#   "status":     "completed"
# }
```

---

## Step 8 — Inspect the mock export API captures

The MockEndpoint records every request the flow made to the export API:

```bash
kubectl get mockendpoint export-api -n default \
  -o jsonpath='{.status.recentRequests[-1:]}'
# Expected: JSON body with exportDate=2026-03-20T07:00:00Z and the tables list
```

---

## Step 9 — Watch nightly runs

The trigger fires automatically at 02:00 America/New_York each night. To watch
upcoming FlowRuns:

```bash
kubectl get flowruns -n default -l kubezap.io/trigger=nightly-export -w
```

Or use the CLI:

```bash
kubezap history --trigger nightly-export -n default --watch
```

Each FlowRun is named `nightly-export-<scheduled-time>` and retained for 30 days
(governed by `spec.flowRunGC.maxSucceeded: 30` and `ttlAfterSucceeded: 720h`).

---

## Simulating an upload failure

To verify that `failurePolicy: Continue` keeps the Slack notification running
even when MinIO is unavailable:

```bash
# Scale MinIO to 0 replicas to simulate an outage
kubectl scale deployment minio --replicas=0 -n default

# Create a new manual FlowRun
kubectl apply -f - <<'EOF'
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: nightly-export-manual-002
  namespace: default
spec:
  flowRef:
    name: nightly-db-export
  triggerRef:
    name: nightly-export
  triggerData:
    scheduledTime: "2026-03-20T08:00:00Z"
EOF

# Watch the steps
kubectl get flowrun nightly-export-manual-002 -n default -w

# After completion:
kubectl get flowrun nightly-export-manual-002 -n default \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Expected:
#   export-data      Succeeded
#   upload-to-minio  Failed      (MinIO unreachable after 3 retries)
#   notify-slack     Succeeded   (ran despite upload failure — failurePolicy: Continue)

# Restore MinIO
kubectl scale deployment minio --replicas=1 -n default
```

---

## Production adaptation

### AWS S3

Replace the `upload-to-minio` step URL and headers to target the AWS S3 API.
The simplest production approach is to use a sidecar or init-job that generates
an S3 pre-signed URL and stores it in a ConfigMap; the flow step then PUTs
directly to the pre-signed URL (no auth headers needed):

```yaml
- name: upload-to-minio
  action:
    type: http
    http:
      url: "$(steps.get_presigned_url.results.presignedUrl)"
      method: PUT
      headers:
        Content-Type: application/json
      body: >-
        { ... }
```

Alternatively, build a thin upload-proxy microservice (or KubeZap plugin) that
accepts the export manifest and handles S3 authentication internally.

### Real export service

Replace the MockEndpoint URL in the `export-data` step with your actual database
export service endpoint. The flow expects the response to contain at minimum:

| Field      | Type   | Description                       |
| ---------- | ------ | --------------------------------- |
| `exportId` | string | Unique identifier for this export |
| `rowCount` | number | Total rows exported               |
| `status`   | string | `"completed"` or `"partial"`      |

### Slack secrets

For better secret hygiene, store the Slack webhook URL in a Kubernetes Secret and
inject it at deploy time (for example via Helm `values.yaml`):

```bash
kubectl create secret generic slack-nightly-webhook \
  --from-literal=url='https://hooks.slack.com/services/T.../B.../...' \
  -n default
```

Then reference it in your Helm-templated flow manifest or replace via
`kustomize secretGenerator`.

---

## Cleanup

```bash
kubectl delete -k examples/nightly-export/
kubectl delete flowrun -n default -l kubezap.io/trigger=nightly-export
# Remove the mc alias
mc rm --recursive --force myminio/exports/
mc rb myminio/exports
mc alias remove myminio
```
