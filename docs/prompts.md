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

**Status:** `[x]`

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

**Status:** `[x]`

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

**Status:** `[x]`

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

---

## 10. Lint cleanup — fix pre-existing issues

**Status:** `[ ]`

**Why:** Four pre-existing lint issues have been reported on every lint run since early in the project. Fixing them now keeps `make lint` clean so new issues are immediately visible.

**Files to change:** `internal/gateway/webhook/watcher.go`, `internal/gateway/webhook/handler.go`

```
Fix four pre-existing golangci-lint issues in the webhook gateway package.
Do not change any logic — only fix the lint violations.

File: internal/gateway/webhook/watcher.go

1. errcheck (line ~63): `triggerInformer.AddEventHandler(...)` returns
   `(cache.ResourceEventHandlerRegistration, error)`. Assign and check the error:

   registration, err := triggerInformer.AddEventHandler(...)
   if err != nil {
       return fmt.Errorf("adding trigger event handler: %w", err)
   }
   _ = registration

2. revive import-shadowing (line ~36): the parameter named `client` in
   NewTriggerWatcher shadows the imported `client` package. Rename the
   parameter to `k8sClient`:

   func NewTriggerWatcher(k8sClient client.Client, ...) (*TriggerWatcher, error) {

   Update the struct field assignment and any uses inside the function body.
   The TriggerWatcher struct field is also named `client` — rename it to
   `k8sClient` and update all usages throughout the file.

File: internal/gateway/webhook/handler.go

3. revive import-shadowing (line ~32): the parameter named `client` in
   NewWebhookHandler shadows the imported `client` package. Rename the
   parameter to `k8sClient`:

   func NewWebhookHandler(k8sClient client.Client, ...) *WebhookHandler {

   Update the struct field assignment. The WebhookHandler struct field is also
   named `client` — rename it to `k8sClient` and update all usages.

4. staticcheck SA9003 (line ~164): empty if-branch:

   if entry.FlowNamespace != "" && entry.FlowNamespace != entry.TriggerNamespace {
       // FlowRef in FlowRunSpec is LocalObjectReference and does not support namespace
       ...
   }

   Replace the empty branch with a comment on the field assignment or remove
   the if-block entirely since there is no code to run. Keep any explanatory
   comment as a regular comment above the FlowRun creation, not inside an
   empty branch.

After changes: run `make lint` to confirm all four issues are resolved.
Commit with message: "Fix pre-existing lint issues in webhook gateway package"
```

---

## 11. Flow reconciler — spec validation and Ready condition

**Status:** `[ ]`

**Why:** The `Flow` CRD has no reconciler. FlowRun execution fetches the Flow directly without knowing if it is valid. A reconciler that validates the spec and sets a `Ready` condition allows the operator to surface misconfigured Flows early and gives FlowRun reconciler a signal to check.

**Files to change/create:** `internal/controller/flow_controller.go` (new), `cmd/main.go`

```
Create a controller-runtime reconciler for the Flow CRD.

File: internal/controller/flow_controller.go

Requirements:

1. Watch Flow resources. On each reconcile:
   a. Fetch the Flow. If not found, return (deleted).
   b. Validate the spec:
      - steps must be non-empty (at least one step required)
      - all step names must be unique within the Flow
      - all runAfter references must name a step that exists in the same Flow
      - each step action type must be one of: http, transform, publish
      - for type=http: action.http must be non-nil and url must be non-empty
      - for type=publish: action.publish must be non-nil, integrationRef.name and
        topic must be non-empty
   c. If validation fails: set condition Ready=False, reason=InvalidSpec,
      message=<first validation error found>. Return without requeue.
   d. If validation passes: set condition Ready=True, reason=FlowReady,
      message="Flow is valid and ready".

2. Use metav1.SetStatusCondition to update conditions (standard pattern).

3. RBAC markers:
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows,verbs=get;list;watch;update;patch
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows/status,verbs=get;update;patch

4. Register in cmd/main.go after FlowRunReconciler.

5. Run make manifests after adding markers.

Use the TriggerReconciler and FlowRunReconciler in the same package as style reference.
The condition type string should be "Ready". Reason strings: "FlowReady", "InvalidSpec".
```

---

## 12. Step result passing — template substitution between steps

**Status:** `[ ]`

**Why:** The FlowRun reconciler tracks step results in `stepResults map[string]map[string]string` but currently never uses them. Multi-step flows are not useful without data flowing between steps.

**Files to change:** `internal/controller/flowrun_controller.go`

```
Add template variable substitution to the FlowRun reconciler so step results
and trigger payload fields can be referenced in downstream step inputs.

Substitution syntax: $(steps.<stepName>.results.<resultKey>)
Trigger data syntax: $(trigger.body), $(trigger.headers.<name>), $(trigger.topic),
                     $(trigger.partition), $(trigger.offset), $(trigger.scheduledTime)

Requirements:

1. Add a helper function:

   func substituteVars(s string, stepResults map[string]map[string]string,
       triggerData *automationv1alpha1.TriggerData) string

   - Replace all occurrences of $(steps.<name>.results.<key>) with the corresponding
     value from stepResults[name][key]. If the key does not exist, leave the
     placeholder unchanged.
   - Replace $(trigger.body) with triggerData.Body (if triggerData != nil).
   - Replace $(trigger.headers.<name>) with the header value (case-insensitive lookup).
   - Replace $(trigger.topic), $(trigger.partition), $(trigger.offset),
     $(trigger.scheduledTime) with the corresponding TriggerData fields.
   - Use strings.ReplaceAll for each substitution. No regex needed.

2. In executeHTTPStep, apply substituteVars to:
   - h.URL
   - h.Body
   - each header value in h.Headers

   Pass flowRun.Spec.TriggerData down from Reconcile through executeStep to
   executeHTTPStep (add a *TriggerData parameter where needed).

3. In executeStep (or the transform stub), for type=transform:
   Apply substituteVars to each value in action.Transform.Mappings.
   Store the substituted mappings as the step's results (one ResultValue per
   mapping key). Set phase=Succeeded.
   This makes transform steps actually useful for data reshaping between steps.

4. The stepResults map is already populated after each successful step —
   no change needed there.

Do not add CEL evaluation yet — plain string substitution only.
Run make build to verify. Commit with:
"FlowRun: step result and trigger data substitution in HTTP and transform steps"
```

---

## 13. Webhook HMAC authentication

**Status:** `[ ]`

**Why:** Without auth, any caller that can reach the webhook endpoint can fire triggers. HMAC is the most widely used webhook auth method (GitHub, Stripe, Slack all use it) and is the highest-priority auth mode.

**Files to change:** `internal/gateway/webhook/handler.go`, `internal/gateway/webhook/registry.go`, `internal/gateway/webhook/watcher.go`

**Reference:** `api/v1alpha1/trigger_types.go` — `WebhookAuth` struct with `HMACSecretRef`.

```
Implement HMAC-SHA256 signature verification for webhook triggers.

The webhook gateway does not have direct Kubernetes Secret access today.
Add it as follows:

1. In RouteEntry (registry.go), add:
   AuthType      string // "hmac", "bearer", "apiKey", "ipAllowlist", "" (none)
   HMACSecret    string // pre-loaded secret value (loaded at route registration time)
   BearerToken   string // pre-loaded token value
   APIKey        string // pre-loaded key value
   APIKeyHeader  string // header name for API key (default "X-Api-Key")
   IPAllowlist   []string // CIDR blocks

2. In watcher.go, when building a RouteEntry from a Trigger, read the auth
   config from trigger.Spec.Webhook.Auth. If auth.Type == "hmac", fetch
   the secret value from the Kubernetes Secret referenced by auth.HMACSecretRef
   using the k8sClient. Store the value in RouteEntry.HMACSecret.
   Similarly load BearerToken (from auth.BearerTokenSecretRef) and APIKey
   (from auth.APIKeySecretRef). For ipAllowlist, copy auth.IPAllowlist directly.
   If fetching the secret fails, log the error and do NOT register the route
   (return without registering so the endpoint is not exposed unauthenticated).

3. In handler.go, add an authenticateRequest function:

   func authenticateRequest(r *http.Request, body []byte, entry RouteEntry) (int, string)
   // returns (http.StatusOK, "") on success, or (statusCode, errorMessage) on failure

   Implement for each auth type:
   - "hmac": compute HMAC-SHA256 of body using entry.HMACSecret as key.
     Accept the signature from the X-Hub-Signature-256 header in the format
     "sha256=<hex>". Use hmac.Equal for constant-time comparison. Return 401
     if header is missing or signature does not match.
   - "bearer": check Authorization header equals "Bearer <token>". Return 401
     if missing or mismatched.
   - "apiKey": check the header named entry.APIKeyHeader equals entry.APIKey.
     Return 401 if missing or mismatched.
   - "ipAllowlist": parse r.RemoteAddr, check if the IP is contained in any
     CIDR in entry.IPAllowlist. Return 403 if not in the list.
   - "" (none): return 200 OK immediately.

4. In ServeHTTP, after looking up the entry and before reading the body,
   call authenticateRequest. For HMAC, you need the body first — read the body
   before calling auth, then pass it to authenticateRequest and reuse the bytes
   for the FlowRun TriggerData.Body.

5. Add imports: "crypto/hmac", "crypto/sha256", "encoding/hex", "net".

Run make build. Commit:
"Webhook gateway: HMAC, bearer token, API key, and IP allowlist authentication"
```

---

## 14. MockEndpoint reconciler and webhook gateway mock route support

**Status:** `[ ]`

**Why:** MockEndpoints are how developers test flows without real external services. The webhook gateway already serves on `/hooks/*` — mock routes live on `/mock/*` on the same server.

**Files to change/create:**
- `internal/controller/mockendpoint_controller.go` (new)
- `internal/gateway/webhook/mock_handler.go` (new)
- `internal/gateway/webhook/mock_registry.go` (new)
- `internal/gateway/webhook/watcher.go` (add MockEndpoint watch)
- `cmd/main.go` (register reconciler)
- `cmd/webhook-gateway/main.go` (mount mock handler)

```
Implement MockEndpoint support across the controller and webhook gateway.

--- Part A: MockRegistry (internal/gateway/webhook/mock_registry.go) ---

Similar to RouteRegistry but for mock routes. Store:
   type MockEntry struct {
       Name             string
       Namespace        string
       Response         *automationv1alpha1.MockResponse     // default response
       ResponseSequence []automationv1alpha1.MockResponse    // cycling responses
       responseIndex    int                                  // current position in sequence
       MaxHistory       int32
   }

Thread-safe map keyed by path (e.g. "/mock/my-endpoint").
Methods: Register(path, entry), Deregister(path), Lookup(path) (MockEntry, bool),
         NextResponse(path) MockResponse — advances responseIndex, wraps around.

--- Part B: MockHandler (internal/gateway/webhook/mock_handler.go) ---

http.Handler that:
1. Looks up the path in MockRegistry.
2. If not found: 404.
3. Gets the next response (single or from sequence).
4. Applies DelayMs if set (time.Sleep).
5. Writes response headers and body with the configured status code.
6. Sends a CapturedRequest notification back to the controller via a channel
   or callback so the controller can update MockEndpoint status.
   For simplicity: use a buffered channel chan CapturedRequestEvent where
   CapturedRequestEvent carries namespace/name/request. The controller drains
   this channel via a goroutine.

--- Part C: Controller (internal/controller/mockendpoint_controller.go) ---

1. Watch MockEndpoint resources.
2. On reconcile: validate spec.path is non-empty, set Ready=True condition,
   set status.URL = "http://<service>/mock/<path>" (use a configurable base
   URL from an env var KUBEZAP_GATEWAY_BASE_URL, default "").
3. RBAC markers:
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=mockendpoints,verbs=get;list;watch;update;patch
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=mockendpoints/status,verbs=get;update;patch
4. Register in cmd/main.go.

--- Part D: Webhook gateway watcher (watcher.go) ---

Add a second informer for MockEndpoint resources.
On Add/Update: if spec.path is non-empty, register the mock route.
On Delete: deregister.

--- Part E: Webhook gateway main (cmd/webhook-gateway/main.go) ---

Mount the mock handler:
   mux.Handle("/mock/", mockHandler)

Run make manifests. Commit:
"MockEndpoint: reconciler, mock route registration, and request capture"
```

---

## 15. Integration reconciler

**Status:** `[ ]`

**Why:** The `Integration` CRD has no reconciler. The controller needs to validate Integration specs and manage Deployments for plugin-type integrations.

**Files to change/create:** `internal/controller/integration_controller.go` (new), `cmd/main.go`

```
Create a controller-runtime reconciler for the Integration CRD.

File: internal/controller/integration_controller.go

Requirements:

1. Watch Integration resources.

2. On each reconcile:
   a. Fetch the Integration. If not found, return.
   b. Validate based on spec.type:
      - type=kafka: spec.kafka must be non-nil, bootstrapServers must be non-empty.
      - type=plugin: spec.plugin must be non-nil, spec.plugin.image must be non-empty.
   c. On validation failure: set condition Ready=False, reason=InvalidSpec. Return.
   d. For type=plugin: ensure a Deployment exists named
      "kubezap-plugin-<integration-name>" in the same namespace.
      Use desiredPluginDeployment (a new helper, similar to
      desiredWebhookGatewayDeployment in gateway_deployment.go) that builds:
        - Image: spec.plugin.image
        - Container name: "plugin"
        - Port: spec.plugin.publisherPort (default 8090)
        - Env vars from spec.plugin.env
        - Secret env vars from spec.plugin.secretRefs (mount each secretName's
          keys as env vars using envVarMappings)
        - Standard injected env vars:
            KUBEZAP_NAMESPACE=<namespace>
            KUBEZAP_INTEGRATION_NAME=<integration-name>
            KUBEZAP_PUBLISHER_PORT=<publisherPort>
            KUBEZAP_LOG_LEVEL=info
        - Readiness probe: GET /healthz :<publisherPort>, initialDelaySeconds=5
        - Security context: runAsNonRoot=true, readOnlyRootFilesystem=true,
          allowPrivilegeEscalation=false
      Create-or-update (idempotent). Update image if it drifts.
      Set status.gatewayDeploymentName = deployment name.
   e. For type=kafka: no Deployment to manage (handled by Kafka gateway).
      Set condition Ready=True.
   f. After successful reconcile: set condition Ready=True, update
      status.lastReconciledTime = now.

3. RBAC markers:
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get;list;watch;update;patch
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations/status,verbs=get;update;patch
   // +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

4. Register in cmd/main.go.

5. Run make manifests.

Commit:
"Integration reconciler: spec validation, plugin Deployment lifecycle"
```

---

## Notes

- All Go types in `api/v1alpha1/` require `make generate && make manifests` after changes.
- Module path is `github.com/yourname/kubezap` (placeholder — rename before OperatorHub submission).
- Run `make lint` before committing to catch golangci-lint issues early.
- See `docs/api/` for full CRD specs and `docs/architecture.md` for runtime design context.
