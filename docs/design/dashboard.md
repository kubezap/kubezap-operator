# KubeZap Web Dashboard — Design Specification

> **Status:** Approved for implementation (2026-03-21)
> **Phase:** §15 Phase 2 — schedule items reference this doc

---

## Overview

A read-only web dashboard embedded in the operator binary. Serves as a live FlowRun execution monitor for developers and a demo-quality visualization for stakeholders, accessed via `kubectl port-forward`.

**Non-goals (v1):**
- Write operations (cancel, retry, trigger) — strictly read-only
- OIDC/SSO — no auth in v1; bearer-token escape hatch for optional Ingress exposure
- Log streaming — out of scope
- Multi-cluster view — single kubeconfig context only

---

## Architecture

### Deployment

Embedded in the operator binary (`cmd/main.go`). Enabled by `--ui-port` flag (default `0` = disabled).

```
┌─────────────────────────────────┐
│  kubezap-controller-manager pod │
│                                 │
│  :9090  /metrics                │
│  :8443  /validate (webhook)     │
│  :8082  /ui/*       ◄── new     │
│         /api/v1/*   ◄── new     │
└─────────────────────────────────┘
```

Developer access pattern:
```bash
kubectl port-forward -n kubezap-system deployment/kubezap-controller-manager 8082:8082
# open http://localhost:8082/ui/
```

### Binary structure

```
cmd/main.go                    # add --ui-port and --ui-bearer-token flags
internal/ui/
  server.go                    # starts HTTP server, embeds ui/dist, registers routes
  api.go                       # /api/v1/ JSON handlers
  sse.go                       # /api/v1/events SSE endpoint
  api_test.go                  # JSON handler unit tests
  sse_test.go                  # SSE handler unit tests
ui/                            # Vue 3 + Vite project root
  src/
    views/
    components/
    router/
    composables/
  vite.config.js
  package.json
  index.html
ui/dist/                       # built output — embedded into binary via go:embed
```

### Build pipeline

```makefile
.PHONY: ui
ui:
	cd ui && npm ci && npm run build

.PHONY: build
build: ui          # ui must be built before go build embeds dist/
	go build ./...
```

`ui/dist/` and `ui/node_modules/` are in `.gitignore`. CI must run `make ui` before `make build`.

---

## Auth

### v1 — No auth (default)

`--ui-port` with no `--ui-bearer-token` = no authentication. Access requires kubectl, which enforces Kubernetes RBAC. Appropriate for port-forward access.

### Optional — Static bearer token

`--ui-bearer-token=<token>` adds a middleware that checks `Authorization: Bearer <token>` on every request. Suitable for simple Ingress exposure in a trusted network.

### Future — kube-rbac-proxy (not implemented in v1)

For production multi-team exposure, run [kube-rbac-proxy](https://github.com/brancz/kube-rbac-proxy) as a sidecar in front of `--ui-port`. This delegates auth entirely to Kubernetes RBAC — no custom OIDC code in the operator. Document this pattern in `docs/guides/dashboard.md`.

---

## Backend API

All endpoints are read-only. The Go handlers use the controller-runtime `client.Client` already wired into the manager — no separate kubeconfig needed.

### Endpoints

```
GET  /api/v1/namespaces
     → { "namespaces": ["default", "production"] }
     // Lists namespaces the operator is watching (respects WATCH_NAMESPACES)

GET  /api/v1/:namespace/flowruns
     ?phase=Running|Succeeded|Failed|Cancelled|Pending
     ?trigger=<name>
     ?flow=<name>
     ?limit=50         (default 50, max 200)
     ?since=1h         (duration string, e.g. 30m, 2h, 24h)
     → { "items": [FlowRunSummary], "total": int }

GET  /api/v1/:namespace/flowruns/:name
     → FlowRunDetail

GET  /api/v1/:namespace/triggers
     → { "items": [TriggerSummary] }

GET  /api/v1/:namespace/flows
     → { "items": [FlowSummary] }

GET  /api/v1/events?namespace=default
     → SSE stream  (see SSE section)
```

### Response types

```typescript
// FlowRunSummary — used in list view
interface FlowRunSummary {
  name: string
  namespace: string
  triggerName: string
  flowName: string
  phase: "Pending" | "Running" | "Succeeded" | "Failed" | "Cancelled"
  creationTimestamp: string   // RFC3339
  startTime?: string          // RFC3339
  completionTime?: string     // RFC3339
  durationSeconds?: number
  stepCount: number
  stepsSucceeded: number
  stepsFailed: number
}

// FlowRunDetail — used in detail view
interface FlowRunDetail extends FlowRunSummary {
  steps: StepRunStatus[]
  triggerData?: {
    eventType: string
    body?: string
    headers?: Record<string, string>
  }
}

interface StepRunStatus {
  name: string
  phase: "Pending" | "Running" | "Succeeded" | "Failed" | "Skipped"
  attempts: number
  message?: string
  startTime?: string
  completionTime?: string
  durationSeconds?: number
  results?: Record<string, string>
}

// TriggerSummary
interface TriggerSummary {
  name: string
  namespace: string
  type: "webhook" | "cron" | "kafka" | "amqp" | "nats" | "resource"
  ready: boolean
  lastFiredTime?: string
  activeFlowRuns: number
  schedule?: string           // for type: cron
  endpoint?: string           // for type: webhook
}

// FlowSummary
interface FlowSummary {
  name: string
  namespace: string
  stepCount: number
  ready: boolean
  lastUsedTime?: string       // from most recent FlowRun
}
```

---

## SSE (Server-Sent Events)

### Endpoint

`GET /api/v1/events?namespace=<ns>` — streams FlowRun change events to the browser.

The Go handler:
1. Registers an SSE client
2. Watches FlowRuns in the specified namespace via `client.Watch`
3. On each watch event (ADD/MODIFY/DELETE), serializes the FlowRunSummary and writes it to the response with `data:` prefix and `\n\n` terminator
4. Flushes after every event via `http.Flusher`
5. Closes the stream when the client disconnects (context cancelled)

### Event format

```
event: flowrun
data: {"name":"order-abc","namespace":"default","phase":"Succeeded",...}

event: flowrun
data: {"name":"order-xyz","namespace":"default","phase":"Running",...}
```

The Vue frontend connects with the browser-native `EventSource` API and merges incoming events into the local FlowRun list reactively.

### Reconnect

`EventSource` reconnects automatically on disconnect. The server sends a `retry: 3000` directive on connect. No custom reconnect logic needed in the frontend.

---

## Vue Frontend

### Tech stack

- **Vue 3** with Composition API (`<script setup>`)
- **Vite** for dev server and production build
- **Vue Router 4** for client-side routing
- **No UI component library** — small set of hand-written components. Keeps the bundle small and avoids dependency churn.
- **Tailwind CSS** — utility-first, minimal config, no extra JS runtime

### Routing

```
/ui/                               → FlowRunList (default namespace)
/ui/runs/:namespace                → FlowRunList
/ui/runs/:namespace/:name          → FlowRunDetail
/ui/triggers/:namespace            → TriggerList
/ui/flows/:namespace               → FlowList
```

### Component tree

```
App.vue
  NavBar.vue
    NamespaceSelect.vue            # fetches /api/v1/namespaces, updates router
    NavLinks.vue                   # FlowRuns / Triggers / Flows tabs

  router-view
    FlowRunList.vue                # fetches /api/v1/:ns/flowruns + SSE connection
      FilterBar.vue                # phase, trigger, flow dropdowns + since picker
      FlowRunTable.vue
        FlowRunRow.vue
          PhaseChip.vue            # coloured badge: Pending/Running/Succeeded/Failed
          RelativeTime.vue         # "2 min ago", updates every 10s
          DurationCell.vue         # "1.4s", "2m 3s"

    FlowRunDetail.vue              # fetches /api/v1/:ns/flowruns/:name + SSE for live
      FlowRunHeader.vue            # name, trigger→flow link, phase, elapsed
      StepTimeline.vue
        StepRow.vue
          StepBadge.vue            # ✓ ✗ ● ○ - badges matching CLI watch output

    TriggerList.vue                # fetches /api/v1/:ns/triggers
      TriggerRow.vue
        TriggerTypeChip.vue        # webhook/cron/kafka/etc badge

    FlowList.vue                   # fetches /api/v1/:ns/flows
      FlowRow.vue
```

### SSE in Vue

```typescript
// composables/useFlowRunEvents.ts
export function useFlowRunEvents(namespace: Ref<string>, onEvent: (fr: FlowRunSummary) => void) {
  let es: EventSource | null = null

  watch(namespace, (ns) => {
    es?.close()
    es = new EventSource(`/api/v1/events?namespace=${ns}`)
    es.addEventListener('flowrun', (e) => {
      onEvent(JSON.parse(e.data))
    })
  }, { immediate: true })

  onUnmounted(() => es?.close())
}
```

FlowRunList merges incoming events into a reactive `Map<string, FlowRunSummary>` keyed by name, so updates to existing FlowRuns are reflected without a full list reload.

### Vite config

```js
// ui/vite.config.js
export default defineConfig({
  base: '/ui/',                // all assets under /ui/ path prefix
  build: {
    outDir: '../ui/dist',
    emptyOutDir: true,
  }
})
```

The Go server handles `/ui/*` by serving from the embedded `dist/` and falls back to `index.html` for any unmatched route (SPA fallback). The `/api/v1/*` routes are handled by Go directly — no Vite proxy needed in production.

---

## Phase 3 — Future Plans (Tier 3)

These are deferred intentionally to keep v1 scope achievable. Record here so they are not re-designed from scratch.

### Integration health page

- `GET /api/v1/:namespace/integrations` — list Integrations with gateway Deployment readiness and plugin `/healthz` status
- New view: `IntegrationList.vue`

### Activity graph

- Reuse existing `kubezap_flowrun_total` counter metric. Query via `/api/v1/metrics/flowruns?window=24h` — the Go handler pulls from the in-process Prometheus registry.
- Frontend renders a simple bar chart (use VChart or a tiny custom SVG — no Chart.js dependency).

### Search

- Add `?q=<substring>` to `/api/v1/:namespace/flowruns` — server-side filter on trigger name and FlowRun name.
- Frontend: single search input in FilterBar, debounced, re-queries the API.

### OIDC auth

- Add `--ui-oidc-issuer`, `--ui-oidc-client-id`, `--ui-oidc-client-secret` flags.
- Go middleware implements OIDC auth code flow (redirect to provider → token exchange → session cookie).
- **Prerequisite:** requires HTTPs on the UI port (add `--ui-tls-cert-file` / `--ui-tls-key-file`).
- Alternatively, use kube-rbac-proxy sidecar — avoids implementing OIDC in the operator at all.

---

## Kubernetes manifest changes

### Service port

Add a `ui` named port to the controller Service in `config/default/service.yaml` (or equivalent):

```yaml
- name: ui
  port: 8082
  targetPort: 8082
  protocol: TCP
```

Only expose when `--ui-port` is non-zero. Document in `docs/guides/dashboard.md`.

### Deployment args

`--ui-port` is opt-in (`0` by default = disabled). No manifest changes required by default; operators who want the UI add `--ui-port=8082` to the controller Deployment args.

---

## Implementation task order

Implement in this sequence to avoid rework:

1. **Go API layer** (`internal/ui/api.go`, `sse.go`) — can be written and tested without any Vue code
2. **Wire into operator** (`cmd/main.go` flags + `server.go`) — short, unblocked once API layer is done
3. **Vue scaffold** (`ui/` directory, Vite config, router, NavBar) — parallel with step 1-2
4. **FlowRunList view** — first meaningful page; validates the full stack
5. **FlowRunDetail view** — depends on FlowRunList patterns being established
6. **TriggerList + FlowList views** — straightforward after detail page
7. **SSE integration** — add after polling version works; swap fetch loop for EventSource
8. **Tests** (`api_test.go`, `sse_test.go`) — alongside each Go layer
9. **Docs** (`docs/guides/dashboard.md`) — last, once behaviour is confirmed
