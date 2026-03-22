# Web Dashboard

KubeZap includes a read-only web dashboard embedded in the operator binary. It provides live views of FlowRun executions, step timelines, trigger and flow inventories — accessible via `kubectl port-forward` without installing any additional tooling.

---

## Overview

The dashboard is disabled by default. Enable it by adding `--enable-ui` to your controller Deployment args:

```bash
# Minimal — starts on the default port (8082):
--enable-ui

# Override the port:
--enable-ui --ui-port=9000
```

When `--enable-ui` is set, the operator automatically creates a `kubezap-ui` Service in its own namespace exposing the configured port (detected via the `POD_NAMESPACE` environment variable injected by the Deployment). No manual Service YAML is required.

Access it via port-forward:

```bash
kubectl port-forward -n kubezap-system \
  deployment/kubezap-controller-manager 8082:8082
# Open: http://localhost:8082/ui/
```

Or via the Service once it is created:

```bash
kubectl port-forward -n kubezap-system svc/kubezap-ui 8082:8082
```

---

## Views

### FlowRun List (`/ui/runs/:namespace`)

The default landing page. Shows all FlowRuns in the selected namespace with:

- **Phase chip** — colour-coded badge: Pending (gray), Running (blue, animated), Succeeded (green), Failed (red), Cancelled (yellow)
- **Relative time** — creation timestamp in human-readable form, updating every 10 seconds
- **Duration** — formatted as `1.4s`, `2m 3s`, or `3h 12m`
- **Filter bar** — filter by phase, trigger name, flow name, or time window (30m / 1h / 6h / 24h / 7d)
- **Live updates** — an SSE connection merges in-flight FlowRun updates in real time without a full page reload

Click a row to open the FlowRun detail page.

### FlowRun Detail (`/ui/runs/:namespace/:name`)

Shows the full execution record for a single FlowRun:

- **Header** — FlowRun name, trigger → flow breadcrumb links, phase badge, elapsed duration
- **Step timeline** — vertical list of steps with `✓ ✗ ● ○ —` badges matching `kubezap watch` CLI output
- **Step rows** — per-step: name, phase, duration, and truncated message (expandable)
- **Trigger data** — inbound payload body and headers (shown when present; omitted for cron/resource runs)
- **Live updates** — SSE re-fetches the full FlowRun detail on each matching event, keeping in-progress steps live

### Trigger List (`/ui/triggers/:namespace`)

Lists all Triggers in the namespace with:

- **Type chip** — colour-coded: webhook (indigo), cron (purple), kafka (orange), amqp (yellow), nats (teal), resource (gray)
- **Ready status** — green "Ready" or red "Not Ready"
- **Schedule / endpoint** — cron schedule string or webhook path
- **Last fired** — relative time of most recent FlowRun creation
- **Active FlowRuns** — count of currently running FlowRuns (highlighted when non-zero)

### Flow List (`/ui/flows/:namespace`)

Lists all Flows in the namespace with step count, ready status, and last used time.

---

## Namespace Selection

The navbar includes a namespace selector that fetches the list of namespaces the operator is watching (respects `WATCH_NAMESPACES`). Switching namespaces re-navigates all views. The selected namespace is persisted in `localStorage`.

---

## Authentication

### No auth (default — port-forward access)

With no `--ui-bearer-token` flag, the dashboard has no authentication. Access is gated by `kubectl` permissions — if the user can `kubectl port-forward`, they can view the dashboard. This is the recommended access model for day-to-day use.

### Static bearer token (optional — Ingress exposure)

Add `--ui-bearer-token=<token>` to require an `Authorization: Bearer <token>` header on all requests. Suitable for exposing the dashboard via an Ingress in a trusted network:

```bash
--ui-bearer-token=change-me-to-something-random
```

Access via Ingress with the token in the browser URL is not practical; this option is intended for programmatic access or custom proxy setups.

### kube-rbac-proxy (recommended for production multi-team access)

For production environments with multiple teams, run [kube-rbac-proxy](https://github.com/brancz/kube-rbac-proxy) as a sidecar in front of `--ui-port`. This delegates authentication and authorization entirely to Kubernetes RBAC — no custom OIDC code in the operator.

Add the sidecar to the controller Deployment:

```yaml
containers:
  - name: kube-rbac-proxy
    image: gcr.io/kubebuilder/kube-rbac-proxy:v0.16.0
    args:
      - --secure-listen-address=0.0.0.0:8443
      - --upstream=http://127.0.0.1:8082/
      - --logtostderr=true
      - --v=0
    ports:
      - containerPort: 8443
        name: ui-https
  - name: manager
    args:
      - --ui-port=8082        # bind to loopback only, proxied by kube-rbac-proxy
```

Users access the dashboard on port `8443` with their kubeconfig credentials. The proxy enforces that they have `get` on `flowruns` (or any RBAC rule you configure).

---

## Kubernetes Service Port

To expose the UI port through a Service (required for Ingress or kube-rbac-proxy access), add a named port to the controller Service. The operator does not create this automatically — add it manually or via Helm values:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kubezap-controller-manager-service
  namespace: kubezap-system
spec:
  ports:
    - name: ui
      port: 8082
      targetPort: 8082
      protocol: TCP
    # ... existing ports (webhook, metrics) ...
```

For the Helm chart:

```bash
helm upgrade kubezap ./charts/kubezap \
  --set ui.port=8082 \
  --set ui.servicePort=8082
```

---

## API Reference

The dashboard is backed by a read-only REST API served alongside the UI. All endpoints return JSON and require no authentication in the default configuration.

| Method | Path                           | Description                                                                |
| ------ | ------------------------------ | -------------------------------------------------------------------------- |
| `GET`  | `/api/v1/namespaces`           | List namespaces the operator is watching                                   |
| `GET`  | `/api/v1/:ns/flowruns`         | List FlowRuns (supports `?phase`, `?trigger`, `?flow`, `?limit`, `?since`) |
| `GET`  | `/api/v1/:ns/flowruns/:name`   | Get a single FlowRun with full step detail                                 |
| `GET`  | `/api/v1/:ns/triggers`         | List Triggers                                                              |
| `GET`  | `/api/v1/:ns/flows`            | List Flows                                                                 |
| `GET`  | `/api/v1/events?namespace=:ns` | SSE stream of FlowRun change events                                        |

The SSE endpoint sends `event: flowrun` events with a `FlowRunSummary` JSON payload on each create/update/delete. The browser reconnects automatically on disconnect (`retry: 3000`).

---

## Flags Reference

| Flag                | Default        | Description                                                                              |
| ------------------- | -------------- | ---------------------------------------------------------------------------------------- |
| `--enable-ui`       | `false`        | Enable the web dashboard. Also creates a `kubezap-ui` Service in the operator namespace. |
| `--ui-port`         | `8082`         | Port for the UI HTTP server (only used when `--enable-ui` is set).                       |
| `--ui-bearer-token` | `""` (no auth) | Static bearer token for optional Ingress-level authentication.                           |

---

## Limitations (v1)

- **Read-only** — no cancel, retry, or trigger actions
- **No log streaming** — step messages are limited to what is stored in FlowRun status
- **Single namespace at a time** — the namespace selector switches all views; no cross-namespace aggregate view
- **No OIDC** — use kube-rbac-proxy for production auth; OIDC is deferred to a future release
- **Port-forward model** — no default Ingress/Route; expose via Service + Ingress manually if needed
