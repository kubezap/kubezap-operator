# KubeZap — Copilot / Claude / Codex Prompts

Working prompt list for the next implementation steps. Feed these directly to Copilot (via
chat or inline comment), Claude, or Codex. Each section has a status checkbox — mark `[x]`
when the task is done and verified.

Work top-to-bottom. Each prompt assumes the previous ones are complete.

---

## Status Key

- `[ ]` Pending
- `[x]` Done

---

## 1. Add `CronTrigger` and `PubSubTrigger` sub-specs to Trigger CRD

**Status:** `[x]`

**Why:** The `Trigger` CRD currently only has a `Webhook` sub-spec. Without `Cron` and
`PubSub` sub-specs the cron scheduler and Kafka gateway have no configuration to read from.
This unblocks both workstreams.

**Files to change:** `api/v1alpha1/trigger_types.go`

**After this task:** Run `make generate && make manifests` and commit the generated files.

```
In api/v1alpha1/trigger_types.go, add the following to TriggerSpec and create the
supporting types. Follow the same style as the existing WebhookTrigger type.

1. Add field to TriggerSpec:
   Cron   *CronTrigger   `json:"cron,omitempty"`
   PubSub *PubSubTrigger `json:"pubsub,omitempty"`

2. Add CronTrigger struct:
   type CronTrigger struct {
       // Standard five-field cron expression (UTC).
       // Examples: "0 * * * *" (hourly), "*/15 * * * *" (every 15 min)
       // +kubebuilder:validation:Pattern=`^(\*|[0-9,-/]+)\s+(\*|[0-9,-/]+)\s+(\*|[0-9,-/]+)\s+(\*|[0-9,-/]+)\s+(\*|[0-9,-/]+)$`
       Schedule string `json:"schedule"`
   }

3. Add PubSubTrigger struct:
   type PubSubTrigger struct {
       // Message broker type.
       // +kubebuilder:validation:Enum=kafka
       Type string `json:"type"`

       // Reference to an Integration CR with broker connection details.
       IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`

       // Topic to consume from.
       Topic string `json:"topic"`

       // Kafka consumer group ID. Defaults to "kubezap-<trigger-name>" at runtime.
       ConsumerGroup string `json:"consumerGroup,omitempty"`
   }

4. Add import for corev1 "k8s.io/api/core/v1" if not already present.

5. Update the TriggerSpec.Type enum marker to keep webhook;cron;pubsub (already correct).

Do not change anything else in the file.
```

---

## 2. Add `WebhookAuth` stub to `WebhookTrigger`

**Status:** `[x]`

**Why:** The API docs (`docs/api/trigger.md`) specify `spec.webhook.auth` for HMAC, bearer
token, OIDC, Basic, mTLS, API key, and IP allowlist. The field needs to exist in the schema
now so CRDs can be updated once and auth implementations can fill in the logic without another
schema break.

**Files to change:** `api/v1alpha1/trigger_types.go`

**After this task:** Run `make generate && make manifests` and commit the generated files.

```
In api/v1alpha1/trigger_types.go, add an Auth field to WebhookTrigger and create the
WebhookAuth type. Follow the same naming and style as the existing types.

1. Add to WebhookTrigger:
   // Auth configures authentication for this webhook endpoint.
   // If omitted, the endpoint accepts requests from any caller.
   Auth *WebhookAuth `json:"auth,omitempty"`

2. Add WebhookAuth struct:
   type WebhookAuth struct {
       // +kubebuilder:validation:Enum=hmac;bearer;oidc;basic;mtls;apiKey;ipAllowlist
       Type string `json:"type"`

       // HMAC secret reference. Used when type is "hmac".
       HMACSecretRef *corev1.SecretKeySelector `json:"hmacSecretRef,omitempty"`

       // Bearer token secret reference. Used when type is "bearer".
       BearerTokenSecretRef *corev1.SecretKeySelector `json:"bearerTokenSecretRef,omitempty"`

       // OIDC/JWT issuer URL. Used when type is "oidc".
       OIDCIssuer string `json:"oidcIssuer,omitempty"`

       // OIDC audience. Used when type is "oidc".
       OIDCAudience string `json:"oidcAudience,omitempty"`

       // Basic auth credentials secret reference. Used when type is "basic".
       // Secret must have keys "username" and "password".
       BasicAuthSecretRef *corev1.LocalObjectReference `json:"basicAuthSecretRef,omitempty"`

       // API key secret reference. Used when type is "apiKey".
       APIKeySecretRef *corev1.SecretKeySelector `json:"apiKeySecretRef,omitempty"`

       // Header name for API key. Defaults to "X-Api-Key". Used when type is "apiKey".
       // +kubebuilder:default="X-Api-Key"
       APIKeyHeader string `json:"apiKeyHeader,omitempty"`

       // CIDR blocks allowed to call this endpoint. Used when type is "ipAllowlist".
       // Example: ["10.0.0.0/8", "192.168.1.0/24"]
       IPAllowlist []string `json:"ipAllowlist,omitempty"`
   }

Do not change anything else in the file.
```

---

## 3. Add `spec.maxFlowRuns` to `TriggerSpec`

**Status:** `[x]`

**Why:** The architecture specifies a `spec.maxFlowRuns` field on Trigger as a GC cap —
when the number of FlowRuns for this Trigger exceeds this value, the oldest finished ones are
deleted. Establishing the field now avoids a future schema break.

**Files to change:** `api/v1alpha1/trigger_types.go`

**After this task:** Run `make generate && make manifests` and commit the generated files.

```
In api/v1alpha1/trigger_types.go, add MaxFlowRuns to TriggerSpec.

Add after the Cooldown field:

   // MaxFlowRuns caps the number of retained FlowRuns for this Trigger.
   // When exceeded, the oldest completed FlowRuns are garbage-collected.
   // Takes precedence over operator-level --flowrun-ttl-* flags.
   // If zero or omitted, no cap is applied.
   // +kubebuilder:validation:Minimum=0
   MaxFlowRuns *int32 `json:"maxFlowRuns,omitempty"`

Do not change anything else in the file.
```

---

## 4. Fix: add source IP to webhook gateway access logs

**Status:** `[x]`

**Why:** The architecture explicitly requires source IPs in structured access logs (not as
Prometheus label values). The current handler logs method, path, status, etc. but omits the
caller's IP.

**Files to change:** `internal/gateway/webhook/handler.go`

```
In internal/gateway/webhook/handler.go, in the ServeHTTP method, add source_ip to the
structured access log emitted in the deferred function.

The remote address is available as r.RemoteAddr. Because r.RemoteAddr may include a port
(e.g. "10.0.0.1:54321"), extract just the host part using net.SplitHostPort — fall back to
the raw value if parsing fails.

Add the extraction near the top of ServeHTTP (before the defer):
   sourceIP, _, err := net.SplitHostPort(r.RemoteAddr)
   if err != nil {
       sourceIP = r.RemoteAddr
   }

Add "source_ip", sourceIP to the h.log.Info("webhook access", ...) call in the defer.

Add "net" to the import block.

Do not change any other behavior.
```

---

## 5. Fix: remove dead `RouteRegistry.ServeHTTP` method

**Status:** `[x]`

**Why:** `RouteRegistry` has a `ServeHTTP` method that is never called. Actual HTTP routing
goes through `WebhookHandler.ServeHTTP`, which calls `registry.Lookup()` directly. The dead
method is misleading and responds with a plain 202 without creating FlowRuns.

**Files to change:** `internal/gateway/webhook/registry.go`

```
In internal/gateway/webhook/registry.go, delete the ServeHTTP method (lines implementing
func (r *RouteRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request)).

Also remove the "net/http" import if it is no longer used after the deletion.

Do not change anything else in the file.
```

---

## 6. Controller: manage webhook gateway Deployment lifecycle

**Status:** `[x]`

**Why:** The architecture requires the operator to own one webhook gateway Deployment per
namespace. Currently the controller only sets trigger status — it does not create or manage
the gateway Deployment.

**Files to change:** `internal/controller/trigger_controller.go`, possibly a new
`internal/controller/gateway_deployment.go` for the Deployment builder.

**Reference:** `cmd/webhook-gateway/main.go` is the binary that will run in the Deployment.
The image name is `kubezap/webhook-gateway`. The Deployment should be named
`kubezap-webhook-gateway` and labeled with `kubezap.io/component: webhook-gateway`.

```
In the Trigger reconciler (internal/controller/trigger_controller.go), add logic to ensure
a webhook gateway Deployment exists in each namespace that has at least one enabled webhook
Trigger. Follow idempotent controller-runtime patterns (create-or-update using
controllerutil.CreateOrUpdate).

Requirements:
- When reconciling any webhook Trigger, check if a Deployment named
  "kubezap-webhook-gateway" exists in the same namespace.
- If it does not exist, create it. If it exists but differs from the desired spec, update it.
- Desired Deployment spec:
    - Image: kubezap/webhook-gateway (use a const or configurable field)
    - Replicas: 1 (HPA will manage scaling later)
    - Container name: webhook-gateway
    - Container port: 8080
    - Args: ["--port=8080", "--namespace=<trigger-namespace>"]
    - Liveness probe: GET /healthz :8080, initialDelaySeconds=5, periodSeconds=10
    - Readiness probe: GET /readyz :8080, initialDelaySeconds=3, periodSeconds=5
    - Security context: runAsNonRoot=true, readOnlyRootFilesystem=true,
      allowPrivilegeEscalation=false
    - Labels on pod template: kubezap.io/component=webhook-gateway,
      kubezap.io/namespace=<trigger-namespace>
- Set the Deployment's owner reference to the Trigger that triggered creation using
  controllerutil.SetControllerReference — or better, use a separate controller that
  watches Triggers and manages the Deployment without tight coupling to a single Trigger.
- Add RBAC markers:
    // +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

The helper logic for building the Deployment spec can live in a new file
internal/controller/gateway_deployment.go to keep the reconciler file clean.

Do not implement HPA yet — that is a later task.
```

---

## 7. Cron trigger: scheduler implementation

**Status:** `[ ]`

**Why:** With `CronTrigger.Schedule` now in the spec (prompt 1), the controller can implement
the scheduler. Use `robfig/cron` — it is lightweight and standard for Go cron work.

**Files to change/create:** `internal/controller/cron_scheduler.go`,
`internal/controller/trigger_controller.go`, `go.mod`

**FlowRun naming for cron:** `<trigger-name>-<scheduled-time-unix>` (e.g.
`nightly-cleanup-1741824000`)

```
Implement cron trigger scheduling in the KubeZap operator.

Dependencies: add github.com/robfig/cron/v3 to go.mod.

Requirements:

1. Create internal/controller/cron_scheduler.go:
   - Maintain a map of triggerNamespacedName → cron.EntryID using robfig/cron v3.
   - Expose methods: Register(trigger), Deregister(key), Stop().
   - The cron job for each trigger creates a FlowRun when it fires:
       Name:      fmt.Sprintf("%s-%d", trigger.Name, scheduledTime.Unix())
       Namespace: trigger.Namespace
       Spec.FlowRef: trigger.Spec.FlowRef.Name
       Spec.TriggerRef: {Name: trigger.Name, Type: "cron"}
       Spec.TriggerData: {ScheduledTime: &scheduledTime}
   - Use controller-runtime client to create the FlowRun. Treat AlreadyExists as success
     (idempotent dedup by name).

2. In internal/controller/trigger_controller.go:
   - Inject the CronScheduler into TriggerReconciler.
   - In Reconcile: if trigger.Spec.Type == "cron" && trigger.Spec.Enabled &&
     trigger.Spec.Cron != nil, call scheduler.Register(trigger).
   - If the trigger is disabled or deleted (use a finalizer), call
     scheduler.Deregister(req.NamespacedName.String()).

3. In cmd/main.go, construct one CronScheduler and inject it into TriggerReconciler.

Use robfig/cron v3 with the standard 5-field parser (no seconds field).
Follow idempotent patterns: re-registering the same trigger should replace the old entry.
```

---

## 8. FlowRun reconciler: basic execution

**Status:** `[ ]`

**Why:** FlowRuns are created by the webhook gateway and (soon) the cron scheduler. Nothing
currently picks them up and executes them. This is the core of the engine.

**Files to change/create:** `internal/controller/flowrun_controller.go`

**Reference types:** `api/v1alpha1/flowrun_types.go`, `api/v1alpha1/flow_types.go`

```
Implement a controller-runtime reconciler for the FlowRun CRD.

File: internal/controller/flowrun_controller.go

Requirements:

1. Watch FlowRun resources. On each reconcile:
   a. Fetch the FlowRun. If not found, return (already deleted).
   b. Skip if FlowRun.Status.Phase is already Succeeded, Failed, or Cancelled.
   c. Fetch the referenced Flow (spec.flowRef.name, same namespace).
   d. If the Flow is not found, set FlowRun phase=Failed, message="Flow not found", and
      return without requeue.

2. Phase transitions:
   - If phase is empty or Pending, set phase=Running, set status.startTime=now, update status.
   - For each step in Flow.Spec.Steps (in order, respecting runAfter):
       * Check WhenExpressions — for now, treat any expression as always-true (CEL eval is a
         later task).
       * If action.type == "http", execute the HTTP call:
           - Build the request from action.http (URL, method, headers, body).
           - Apply action.http.timeoutSeconds (default 30s).
           - On success (2xx): set step phase=Succeeded, store response body in results if
             resultMappings is set.
           - On non-2xx or network error: apply retryPolicy if set; after retries exhausted,
             set step phase=Failed.
           - On step Failed: if FlowSpec.FailurePolicy==Continue or step.OnFailure==Continue,
             continue to next step; otherwise set FlowRun phase=Failed and stop.
       * If action.type == "transform", skip for now (mark step Succeeded immediately).
       * If action.type == "publish", skip for now (mark step Succeeded immediately).
   - After all steps: set FlowRun phase=Succeeded, set status.completionTime=now.

3. Update FlowRun status after each step using r.Status().Update().

4. Add RBAC markers:
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=get;list;watch;update;patch
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns/status,verbs=get;update;patch
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows,verbs=get;list;watch

5. Register the reconciler in cmd/main.go.

Keep the reconciler simple and linear for now. CEL expression evaluation, parallel step
execution, and data-passing between steps are follow-on tasks.
```

---

## 9. FlowRun GC: TTL-based cleanup

**Status:** `[ ]`

**Why:** Completed FlowRuns accumulate indefinitely without cleanup. The architecture defines
TTL-based GC via `spec.ttlAfterFinished`, operator-level flags, and a `kubezap.io/retain=true`
annotation exemption.

**Files to change/create:** `internal/controller/flowrun_controller.go` (extend),
`cmd/main.go` (flags)

```
Add FlowRun garbage collection to the FlowRun reconciler.

Requirements:

1. Add operator-level flags in cmd/main.go:
   --flowrun-ttl-succeeded   duration   default: 24h
   --flowrun-ttl-failed      duration   default: 72h

2. In the FlowRun reconciler, after a FlowRun reaches Succeeded or Failed phase:
   a. If the annotation kubezap.io/retain=true is present, skip GC entirely.
   b. Determine TTL:
      - If spec.ttlAfterFinished is set, use that value.
      - Otherwise use the operator flag (ttlSucceeded or ttlFailed based on phase).
   c. Compute expiry = status.completionTime + TTL.
   d. If now >= expiry: delete the FlowRun (r.Delete).
   e. If now < expiry: requeue after (expiry - now).

3. Also enforce spec.maxFlowRuns on the owning Trigger (if set):
   - List all FlowRuns in the namespace with label kubezap.io/trigger=<triggerName>.
   - Filter to completed (Succeeded or Failed) FlowRuns.
   - If count > maxFlowRuns, delete the oldest by completionTime until count <= maxFlowRuns.
   - Skip any with kubezap.io/retain=true annotation.

Do not delete Running or Pending FlowRuns.
```

---

## Notes

- All Go types in `api/v1alpha1/` require `make generate && make manifests` after changes.
- Module path is `github.com/yourname/kubezap` (placeholder — rename before OperatorHub submission).
- Run `make lint` before committing to catch golangci-lint issues early.
- See `docs/api/` for full CRD specs and `docs/architecture.md` for runtime design context.
