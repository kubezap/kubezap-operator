# GitHub Webhook → Auto-Label PR

This example walks through the **GitHub PR auto-label** pattern — GitHub sends a
`pull_request` event to KubeZap's webhook gateway, the flow extracts the relevant
fields, and a label is automatically applied to the PR via the GitHub API based on
the PR's current state.

This example showcases three things that are hard to do without an operator: HMAC
signature verification at the gateway layer, header extraction in a transform
step, and parallel conditional branches that each make an authenticated API call —
all declared as Kubernetes resources with no custom code to run or operate.

---

## What you'll build

```
GitHub repository
        │
        │  POST /hooks/github-pr
        │  X-Hub-Signature-256: sha256=<hmac>
        │  X-GitHub-Event: pull_request
        ▼
  ┌──────────────────────┐
  │ webhook gateway      │  verifies HMAC, creates FlowRun
  └──────────┬───────────┘
             │
             ▼
  ┌──────────────────────┐
  │ extract-pr           │  transform: pull prNumber, action from body;
  │                      │  eventType from X-GitHub-Event header
  └──────┬───────────────┘
         │
    ┌────┴──────────────┐
    ▼                   ▼
label-needs-review   label-closed
  (when: action        (when: action
   == "opened")         == "closed")
    │                   │
    └────────┬──────────┘
             │  (parallel; each skipped if its condition is false)
             ▼
        FlowRun phase: Succeeded
```

Key properties:

- **HMAC authentication**: the gateway rejects any request whose
  `X-Hub-Signature-256` does not match the shared secret — no unsigned events
  can ever trigger a FlowRun.
- **Header extraction**: the `X-GitHub-Event` header is lifted into step output
  so downstream steps can act on the event type without additional API calls.
- **Parallel conditional steps**: both label steps share `runAfter: [extract-pr]`
  and execute concurrently; the one whose `when` condition is false is marked
  `Skipped` rather than failing.
- **Idempotent labeling**: GitHub's label API is idempotent — calling it twice
  with the same label name is safe.

---

## Prerequisites

- A running Kubernetes cluster with KubeZap installed
- A GitHub repository with admin access — you need permission to configure
  webhooks and create a Personal Access Token
- The KubeZap webhook gateway exposed to the internet (ngrok, a LoadBalancer
  Service, or an Ingress) — GitHub's webhook delivery cannot reach an
  in-cluster-only ClusterIP endpoint
- `kubectl` configured for the target namespace

This example uses the namespace `default`. Change the `namespace:` field in the
manifests if you prefer a dedicated namespace.

---

## Step 1 — Create the required Secrets

The example needs two Secrets: one holding the HMAC secret you will register in
GitHub's webhook settings, and one holding a Personal Access Token (PAT) that
the label steps use to call the GitHub API.

```bash
kubectl create secret generic github-webhook-secret \
  --from-literal=secret='<your-github-webhook-secret>'

kubectl create secret generic github-api-token \
  --from-literal=token='ghp_<your-github-personal-access-token>'
```

**Token permissions**: the PAT requires the `repo` scope for private
repositories, or `public_repo` for public ones. The specific permission used
by the label API call is `issues: write` — GitHub's label endpoints sit under
the Issues API even when applied to pull requests.

> **Choosing a webhook secret**: generate a strong random string, for example
> `openssl rand -hex 32`. You will paste this same value into GitHub's webhook
> configuration in Step 6.

---

## Step 2 — Customize the Flow

Before applying the manifests, open `examples/github-autolabel/flow.yaml` and
replace the placeholder repository with your actual repository path:

```yaml
# flow.yaml — find and replace both occurrences
url: https://api.github.com/repos/YOUR_ORG/YOUR_REPO/issues/$(steps.extract-pr.prNumber)/labels
```

Replace `YOUR_ORG/YOUR_REPO` with your repository, for example `acme/platform`.
The placeholder appears twice — once in each of the `label-needs-review` and
`label-closed` step definitions.

---

## Step 3 — Expose the webhook gateway

GitHub must be able to deliver HTTP POST requests to the webhook gateway.
Choose the option that fits your environment:

```bash
# Option A: ngrok (recommended for local testing)
ngrok http <cluster-node-ip>:8080
# Note the public HTTPS URL, e.g. https://abc123.ngrok-free.app
```

```bash
# Option B: port-forward (requires your local machine to have a public IP,
#            or you are testing with a self-hosted GitHub runner)
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080
```

For production, use a LoadBalancer Service or Ingress. The webhook gateway
Service is `kubezap-webhook-gateway` in the operator namespace. The gateway
listens on port 8080 by default.

The path registered by the Trigger (see Step 4) will be `/hooks/github-pr`.
Your full webhook URL will be:

```
https://<your-gateway-url>/hooks/github-pr
```

---

## Step 4 — Apply the manifests

```bash
kubectl apply -k examples/github-autolabel/
```

This creates:

- `Trigger/github-pr-label` — registers `/hooks/github-pr` on the gateway,
  configures HMAC-SHA256 verification against the `github-webhook-secret`
  Secret, and references the `github-autolabel` Flow
- `Flow/github-autolabel` — three-step workflow: `extract-pr`, `label-needs-review`,
  `label-closed`

> **No Integration needed**: this example calls the GitHub API via plain HTTP steps
> using the PAT from the Secret. An `Integration` CRD is only required when
> KubeZap manages a long-lived connection (e.g., a Kafka cluster or a plugin
> Deployment).

---

## Step 5 — Check the Trigger is accepted

```bash
kubectl get trigger github-pr-label \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True
```

The `Accepted` condition going `True` means the gateway has registered the
`/hooks/github-pr` route and is ready to receive events. Also verify the
gateway Deployment is running:

```bash
kubectl get deployment kubezap-webhook-gateway
kubectl get pods -l app.kubernetes.io/component=webhook-gateway
```

Both pods should show `Running` before you proceed. If the Deployment does not
exist yet, the controller is still provisioning it — wait a few seconds and
re-check.

---

## Step 6 — Configure the GitHub webhook

In your GitHub repository, go to **Settings → Webhooks → Add webhook** and fill
in the following fields:

| Field | Value |
|-------|-------|
| Payload URL | `https://<your-gateway-url>/hooks/github-pr` |
| Content type | `application/json` |
| Secret | the value you used in `github-webhook-secret` (Step 1) |
| Which events? | Select **"Let me select individual events"**, then check **Pull requests** only |
| Active | checked |

Click **Add webhook**. GitHub will immediately send a `ping` event to verify
reachability. The gateway will accept it (HMAC is valid) but the Flow will not
execute — `ping` events are not `pull_request` events and the Trigger's event
filter will drop them without creating a FlowRun.

> **Content type must be `application/json`**: KubeZap's gateway parses the body
> as JSON for field extraction. The `application/x-www-form-urlencoded` content
> type is not supported for webhook triggers.

---

## Step 7 — Open a test PR

Create a branch and open a pull request in the repository. The quickest way is
with the GitHub CLI:

```bash
git checkout -b test/kubezap-autolabel
git commit --allow-empty -m "Test KubeZap auto-label"
git push origin test/kubezap-autolabel

gh pr create \
  --title "Test KubeZap auto-label" \
  --body "Testing automatic labeling via KubeZap" \
  --base main
```

GitHub will POST a `pull_request` event with `action: opened` to your gateway
URL within a second or two of the PR being created.

---

## Step 8 — Watch the FlowRun

```bash
kubectl get flowruns -l kubezap.io/trigger=github-pr-label -w
```

A FlowRun named like `github-pr-label-<timestamp>-<random>` should appear within
seconds of the PR being opened. The `-w` flag streams updates so you can watch
it progress through the `Running` and `Succeeded` phases without polling.

If nothing appears after 30 seconds, check the gateway logs:

```bash
kubectl logs -l app.kubernetes.io/component=webhook-gateway --tail=50
```

Common causes: the gateway URL in GitHub's webhook settings is wrong, the HMAC
secret does not match, or the gateway pod is not reachable from GitHub (see
Step 3).

---

## Step 9 — Inspect step results

Once the FlowRun reaches `Succeeded`, inspect the step outcomes:

```bash
FR=$(kubectl get flowruns -l kubezap.io/trigger=github-pr-label \
  --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{.items[-1].metadata.name}')

kubectl get flowrun $FR \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
```

Expected output for a PR open event:

```
extract-pr          Succeeded
label-needs-review  Succeeded
label-closed        Skipped
```

`label-closed` is `Skipped` because its `when` condition
(`steps.extract-pr.action == "closed"`) evaluated to false — the action was
`"opened"`. Skipped steps are not failures; the FlowRun phase is still
`Succeeded`.

To see the extracted field values from the transform step:

```bash
kubectl get flowrun $FR \
  -o jsonpath='{.status.stepResults.extract-pr}' | jq .
```

Expected:

```json
{
  "prNumber": "42",
  "action": "opened",
  "eventType": "pull_request"
}
```

`eventType` comes from the `X-GitHub-Event` header, not the body — the transform
step lifts it into step output so downstream steps can branch on it without
additional extraction logic.

---

## Step 10 — Verify the label on GitHub

Check the PR on GitHub — the **needs-review** label should now be visible in the
Labels section of the PR sidebar.

You can also verify programmatically:

```bash
PR_NUM=<your-pr-number>
REPO=YOUR_ORG/YOUR_REPO

curl -s \
  -H "Authorization: Bearer $(kubectl get secret github-api-token \
      -o jsonpath='{.data.token}' | base64 -d)" \
  https://api.github.com/repos/$REPO/issues/$PR_NUM/labels \
  | jq '.[].name'
# Expected output:
# "needs-review"
```

If the label does not exist in the repository yet, GitHub creates it
automatically with a default colour. You can pre-create the labels with your
preferred colours in **Settings → Labels** before running the example.

---

## Testing the close path

Close the PR without merging it (via the GitHub UI or `gh pr close <number>`).
GitHub will POST a second `pull_request` event with `action: closed`.

Watch for the new FlowRun:

```bash
kubectl get flowruns -l kubezap.io/trigger=github-pr-label -w
```

A second FlowRun will appear. Inspect its step results:

```bash
FR=$(kubectl get flowruns -l kubezap.io/trigger=github-pr-label \
  --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{.items[-1].metadata.name}')

kubectl get flowrun $FR \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
```

Expected output for a PR close event:

```
extract-pr          Succeeded
label-needs-review  Skipped
label-closed        Succeeded
```

This time `label-needs-review` is `Skipped` (action was `"closed"`, not
`"opened"`) and `label-closed` runs and applies the **merged** label.

---

## Design notes

### Why `$(trigger.body.number)` works

The `number` field is a **top-level field** in GitHub's `pull_request` event
payload. KubeZap's transform step can access top-level body fields directly
using `$(trigger.body.<field>)` interpolation.

Nested fields like `pull_request.title` or `pull_request.head.ref` are not
directly accessible through this syntax. If you need them, add an HTTP step
after `extract-pr` that calls `GET /repos/{owner}/{repo}/pulls/{number}` and
uses `resultMappings` to extract the fields you need from the response body.

### "closed" covers both merged and abandoned PRs

GitHub sends `action: closed` whether the PR was merged or simply closed without
merging. The `label-closed` step therefore applies the **merged** label to both
outcomes.

To distinguish a true merge from an abandoned close, add a second HTTP step that
calls `GET /repos/{owner}/{repo}/pulls/{number}` and uses `resultMappings` to
extract `$.merged` from the response. Branch on that value with an additional
`when` condition to apply different labels for merged vs. abandoned states.

### HMAC header mapping

The Trigger's auth configuration maps exactly to GitHub's signature format:

```yaml
spec:
  webhook:
    auth:
      type: hmac
      hmac:
        algorithm: sha256
        secretRef:
          name: github-webhook-secret
          key: secret
        header: X-Hub-Signature-256
        prefix: "sha256="
```

The `header` and `prefix` values match GitHub's documented format verbatim.
If you copy this Trigger for a different service (e.g., GitLab, which uses
`X-Gitlab-Token`), update both fields accordingly.

---

## Observability

### Prometheus metrics

The `kubezap_flowrun_duration_seconds` histogram tracks end-to-end latency from
webhook receipt to FlowRun completion. Use `kubezap_step_outcome_total` to count
`Succeeded`, `Skipped`, and `Failed` outcomes per step name across all FlowRuns:

```bash
# Port-forward to the controller metrics endpoint
kubectl port-forward -n kubezap-system \
  svc/kubezap-controller-manager-metrics-service 8443:8443

curl -sk https://localhost:8443/metrics | grep kubezap_step_outcome
```

A typical output after running both the open and close paths will show two
`Succeeded` and two `Skipped` counts for the two label steps.

### OTel traces

Each FlowRun produces a root span `flowrun.execute` with child spans for each
step. The `extract-pr` span will include the extracted field values as span
attributes. If you have Jaeger or a compatible collector configured via
`OTEL_EXPORTER_OTLP_ENDPOINT`, search by the FlowRun name to see the complete
execution trace including step timings.

See the [Observability guide](../../docs/guides/observability.md) for full setup instructions.

---

## Cleanup

```bash
kubectl delete -k examples/github-autolabel/
kubectl delete secret github-webhook-secret github-api-token
```

Also delete the webhook in the GitHub repository under
**Settings → Webhooks** — GitHub will continue attempting delivery to a URL
that no longer exists and may mark the webhook as disabled after repeated
failures.

The `kubezap-webhook-gateway` Deployment remains running as long as other
Triggers in the namespace reference it. It is removed only when no Triggers
remain in the namespace.

---

## What's next

- **Webhook security** — explore all supported auth methods (bearer token,
  OIDC/JWT, mTLS, API-key header, IP allowlist) in the
  [Webhook Security guide](../../docs/guides/webhook-security.md).
- **Flow API reference** — full spec for steps, `when` conditions, `runAfter`
  dependencies, `resultMappings`, and retry policies in
  [`docs/api/flow.md`](../../docs/api/flow.md).
- Explore the [slack-router example](../slack-router/) for a more complex webhook routing pattern.
