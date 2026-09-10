# Slack Slash Command Router

This example walks through the **Slack slash command router** pattern — a Slack
slash command (`/kubezap <subcommand>`) is POSTed to KubeZap's webhook gateway,
the flow extracts the command text, and the request is routed to one of three
branches: **deploy**, **status**, or a **fallback** for unrecognised commands.

This example showcases features that are hard to replicate without an operator:
`application/x-www-form-urlencoded` payload handling, Slack HMAC signature
verification + IP allowlist in a single auth block, multi-branch CEL routing, and
a fire-and-forget response pattern — all declared as Kubernetes resources.

---

## What you'll build

```
Slack workspace
        │
        │  POST /hooks/slack-slash
        │  X-Slack-Signature: v0=<hmac>
        │  Content-Type: application/x-www-form-urlencoded
        │  body: command=/kubezap&text=deploy+staging&user_name=alice&...
        ▼
  ┌──────────────────────────┐
  │ webhook gateway          │  verifies HMAC + IP allowlist, creates FlowRun
  └────────────┬─────────────┘
               │
               ▼
  ┌──────────────────────────┐
  │ parse-command            │  transform: extract commandText, userName,
  │                          │  responseUrl, channelId from body fields
  └──────┬───────────────────┘
         │
    ┌────┼────────────────────┐
    ▼    ▼                    ▼
handle-deploy  handle-status  handle-unknown
(when: text    (when: text    (when: text does not
 starts with    starts with    start with "deploy"
 "deploy")      "status")      or "status")
    │              │                │
    └──────────────┴────────────────┘
                   │  exactly one branch runs; others are Skipped
                   ▼
            FlowRun phase: Succeeded
```

Key properties:

- **HMAC + IP allowlist**: the gateway rejects unsigned requests and requests from
  IPs outside Slack's published ranges — defence-in-depth with no custom code.
- **`application/x-www-form-urlencoded` support**: Slack slash commands POST
  form-encoded bodies; the gateway decodes them to `trigger.body.*` fields.
- **Multi-branch CEL routing**: three branches share `runAfter: [parse-command]`;
  at most one executes, the others are marked `Skipped`.
- **Fire-and-forget**: Slack requires a `200 OK` within 3 seconds of the initial
  POST — the gateway returns immediately and the flow runs asynchronously.

---

## Prerequisites

- A Slack workspace where you can install apps (free tier is fine).
- KubeZap installed and the webhook gateway Deployment running in your cluster.
- The KubeZap webhook gateway exposed externally — Slack cannot reach an
  in-cluster-only Service. See [Gateway exposure](#gateway-exposure) below.

---

## Create the Slack app and slash command

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and click **Create New App → From scratch**.
2. Give it a name (e.g. `KubeZap`) and select your workspace.
3. In the left sidebar click **Slash Commands → Create New Command**:
   - Command: `/kubezap`
   - Request URL: `https://<your-gateway-url>/hooks/slack-slash` (fill in after
     the next section)
   - Short description: `KubeZap command router`
   - Usage hint: `deploy <env> | status <env>`
4. Click **Save**.
5. In the left sidebar click **Basic Information → App Credentials** and copy the
   **Signing Secret** — you'll need it in the next step.
6. Click **Install App → Install to Workspace** and authorise.

---

## Gateway exposure

Slack's servers must be able to reach the webhook gateway. Choose one method:

### Option A — ngrok (local development)

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default &
ngrok http 8080
# Note the HTTPS URL, e.g. https://abc123.ngrok.io
```

Use `https://abc123.ngrok.io/hooks/slack-slash` as the Slash Command Request URL.

### Option B — LoadBalancer Service (cloud cluster)

```bash
kubectl patch svc kubezap-webhook-gateway -n default \
  -p '{"spec":{"type":"LoadBalancer"}}'

kubectl get svc kubezap-webhook-gateway -n default \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
# Use http://<EXTERNAL-IP>:8080/hooks/slack-slash
```

### Option C — Ingress (production)

Create an Ingress pointing to `kubezap-webhook-gateway:8080` using your cluster's
Ingress controller. Use the Ingress hostname in the Slack Request URL.

---

## Create the signing secret

```bash
kubectl create secret generic slack-signing-secret \
  --from-literal=signingSecret='<your-slack-signing-secret>' \
  -n default
```

Replace `<your-slack-signing-secret>` with the **Signing Secret** from the Slack
app dashboard (Basic Information → App Credentials).

---

## Update Slack's IP allowlist (optional but recommended)

The Trigger's `spec.webhook.auth.ipAllowlist.cidrs` field in `trigger.yaml` is
pre-populated with Slack's current IP ranges. Slack publishes updates at
[api.slack.com/apis/ip-ranges](https://api.slack.com/apis/ip-ranges). If Slack
adds new IPs in the future, update the CIDR list and re-apply the Trigger.

If you are running locally behind ngrok (which uses its own IPs) you can remove
the `ipAllowlist` block from the trigger during development:

```bash
kubectl patch trigger slack-slash-command -n default --type=json \
  -p '[{"op":"remove","path":"/spec/webhook/auth/ipAllowlist"}]'
```

---

## Apply the manifests

```bash
kubectl apply -k examples/slack-router/
```

Verify:

```bash
# Trigger accepted
kubectl get trigger slack-slash-command -n default \
  -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}'
# Expected: True

# Gateway running
kubectl get deployment kubezap-webhook-gateway -n default
# Expected: 1/1 READY

# Mockoon mock server running
kubectl get deployment mockoon -n default
# Expected: 1/1 READY
```

---

## Send a test slash command

Slack will POST to your Request URL when a user types `/kubezap <text>`.
For local testing without Slack, simulate the request with curl:

```bash
GW_URL="http://localhost:8080"  # adjust if using LoadBalancer/Ingress

TIMESTAMP=$(date +%s)
BODY="command=%2Fkubezap&text=deploy+staging&user_name=alice&channel_id=C123ABC&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2Ffake"
SIGNING_SECRET="<your-slack-signing-secret>"

# Compute Slack-style HMAC signature
SIG_BASE="v0:${TIMESTAMP}:${BODY}"
SIGNATURE="v0=$(echo -n "$SIG_BASE" | openssl dgst -sha256 -hmac "$SIGNING_SECRET" | awk '{print $2}')"

curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${GW_URL}/hooks/slack-slash" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "X-Slack-Signature: ${SIGNATURE}" \
  -H "X-Slack-Request-Timestamp: ${TIMESTAMP}" \
  -d "${BODY}"
# Expected: 201
```

For a real Slack test, type `/kubezap deploy staging` in any channel in your
workspace. Slack will POST to the Request URL and KubeZap will create a FlowRun.

---

## Inspect the FlowRun

```bash
# Watch FlowRuns appear
kubectl get flowruns -n default -l kubezap.io/trigger=slack-slash-command -w

# Get the most recent FlowRun name
FR=$(kubectl get flowruns -n default -l kubezap.io/trigger=slack-slash-command \
  --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{.items[-1:].metadata.name}')

# Overall phase
kubectl get flowrun $FR -n default -o jsonpath='{.status.phase}'
# Expected: Succeeded

# Per-step phases (deploy command)
kubectl get flowrun $FR -n default \
  -o jsonpath='{range .status.steps[*]}{.name}{"\t"}{.phase}{"\n"}{end}'
# Expected:
#   parse-command   Succeeded
#   handle-deploy   Succeeded
#   handle-status   Skipped
#   handle-unknown  Skipped
```

---

## Inspect captured Mockoon requests

```bash
# View what the handle-deploy step posted to the deploy-sink mock
kubectl exec -n default \
  $(kubectl get pod -n default -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3000/mockoon-admin/logs | jq .
# Expected: entries showing POST /deploy-sink with action=deploy, env=deploy staging, requestedBy=alice
```

---

## Test the status branch

```bash
# Send "/kubezap status production"
BODY="command=%2Fkubezap&text=status+production&user_name=bob&channel_id=C123ABC&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2Ffake"
SIG_BASE="v0:${TIMESTAMP}:${BODY}"
SIGNATURE="v0=$(echo -n "$SIG_BASE" | openssl dgst -sha256 -hmac "$SIGNING_SECRET" | awk '{print $2}')"

curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${GW_URL}/hooks/slack-slash" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "X-Slack-Signature: ${SIGNATURE}" \
  -H "X-Slack-Request-Timestamp: ${TIMESTAMP}" \
  -d "${BODY}"

# Step phases for status command:
#   parse-command   Succeeded
#   handle-deploy   Skipped
#   handle-status   Succeeded
#   handle-unknown  Skipped
```

---

## Test the fallback branch

```bash
# Send "/kubezap help"
BODY="command=%2Fkubezap&text=help&user_name=charlie&channel_id=C123ABC&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2Ffake"
# ... (same HMAC signing as above)

# Step phases for unknown command:
#   parse-command   Succeeded
#   handle-deploy   Skipped
#   handle-status   Skipped
#   handle-unknown  Succeeded
```

---

## Fire-and-forget response pattern

Slack requires a `200 OK` response within 3 seconds of the initial POST.
KubeZap's gateway returns `201 Created` immediately upon creating the FlowRun —
the flow itself runs asynchronously in the controller. This satisfies Slack's
timeout requirement without blocking.

If you want to send a delayed response back to Slack (e.g. after the deploy
completes), add a final HTTP step that POSTs to `$(steps.parse_command.results.responseUrl)`:

```yaml
- name: notify-slack
  runAfter:
    - handle-deploy
    - handle-status
    - handle-unknown
  action:
    type: http
    http:
      url: "$(steps.parse_command.results.responseUrl)"
      method: POST
      headers:
        Content-Type: application/json
      body: '{"text":"Command received and processed."}'
      timeoutSeconds: 5
```

Note: the `response_url` is only valid for 30 minutes after the original request.

---

## Cleanup

```bash
kubectl delete -k examples/slack-router/
kubectl delete secret slack-signing-secret -n default
# Also delete the Slack app at api.slack.com/apps if no longer needed
```
