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

## 16. Cron cooldown enforcement

**Status:** `[x]`

**Why:** `CooldownPolicy` is defined in the Trigger spec but never checked. Without enforcement,
a misconfigured cron expression or rapid re-reconciliation can flood the cluster with FlowRuns.

**Files to change:** `internal/controller/cron_scheduler.go`

```
Add cooldown enforcement to the cron scheduler's job function.

In CronScheduler.Register, the job function that creates a FlowRun currently runs
unconditionally. Add the following before FlowRun creation:

1. Fetch the Trigger from the API server (use the stored client):
   trigger := &automationv1alpha1.Trigger{}
   err := s.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, trigger)
   If not found or error: log and skip FlowRun creation.

2. Check cooldown. If trigger.Spec.Cooldown is non-nil and MaxInvocations > 0:
   a. Determine the window duration: use trigger.Spec.Cooldown.Window (default 60s if nil).
   b. Determine the window start: trigger.Status.LastTriggeredTime. If nil, window has not
      started — allow the firing.
   c. If now is within [LastTriggeredTime, LastTriggeredTime + window):
      - If CurrentInvocationCount >= MaxInvocations:
        Update trigger.Status.LastResult = "RateLimited" (status patch), log, and return
        without creating a FlowRun.
      - Otherwise: increment CurrentInvocationCount.
   d. If now is at or after LastTriggeredTime + window:
      Reset CurrentInvocationCount to 1, set LastTriggeredTime = now.

3. After creating the FlowRun successfully:
   - Set trigger.Status.LastTriggeredTime = now
   - Set trigger.Status.LastResult = "Success"
   - Patch the trigger status using client.Status().Patch(...)

Use a status patch (not full update) to avoid conflicts:
   base := trigger.DeepCopy()
   trigger.Status = updatedStatus
   s.client.Status().Patch(ctx, trigger, client.MergeFrom(base))

Add an RBAC marker to CronScheduler's comment block (or note in the controller):
   // +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/status,verbs=get;update;patch

Run make manifests. Commit:
"Cron scheduler: cooldown enforcement with invocation count and status update"
```

---

## 17. Flow timeout enforcement + publish step implementation

**Status:** `[x]`

**Why:** HTTP steps in long-running flows can block forever if the remote is slow. Flow-level
timeout is in the spec but not enforced. The `type: publish` step is currently a no-op stub.

**Files to change:** `internal/controller/flowrun_controller.go`

```
Two changes to the FlowRun reconciler — implement in the same commit.

--- Part A: Flow-level timeout ---

In the Reconcile method, after fetching the Flow and before the step execution loop, derive
a deadline context from flow.Spec.Timeout:

  execCtx := ctx
  if flow.Spec.Timeout != nil && flow.Spec.Timeout.Duration > 0 {
      var cancel context.CancelFunc
      execCtx, cancel = context.WithTimeout(ctx, flow.Spec.Timeout.Duration)
      defer cancel()
  }

Pass execCtx (instead of ctx) to executeStep calls. If the context deadline is exceeded,
executeStep's HTTP call will return a context.DeadlineExceeded error. Treat this as a
step failure with message "flow timeout exceeded".

Also enforce per-step timeout from step.Timeout (already in FlowStep spec):
  In executeHTTPStep, wrap the request context with step.Timeout if set:
  if step.Timeout != nil && step.Timeout.Duration > 0 {
      reqCtx, cancel = context.WithTimeout(execCtx, step.Timeout.Duration)
      defer cancel()
  }

--- Part B: publish step implementation ---

Replace the publish step placeholder in executeStep with real logic:

  case "publish":
    result, err := r.executePublishStep(ctx, flowRun, step, triggerData, stepResults)
    if err != nil {
        status.Phase = "Failed"
        status.Message = err.Error()
    } else {
        status.Phase = "Succeeded"
        status.Results = mapsToResults(result)
    }
    completionTime := metav1.Now()
    status.CompletionTime = &completionTime

Add executePublishStep method:

  func (r *FlowRunReconciler) executePublishStep(ctx context.Context,
      flowRun *automationv1alpha1.FlowRun,
      step automationv1alpha1.FlowStep,
      triggerData *automationv1alpha1.TriggerData,
      stepResults map[string]map[string]string) (map[string]string, error)

Requirements:
1. action.Publish must be non-nil. integrationRef.name must be non-empty.
2. Fetch the Integration: look up integrationRef.name in the same namespace as the FlowRun.
3. The plugin service URL is:
     http://kubezap-plugin-<integration-name>.<namespace>.svc.cluster.local:<publisherPort>/publish
   Use integration.Spec.Plugin.PublisherPort (default 8090).
4. Apply substituteVars to action.Publish.Body and each header value in action.Publish.Headers.
5. Make a POST request to the URL with the substituted body.
   Set Content-Type: application/json if not overridden by action.Publish.Headers.
   Set each header from action.Publish.Headers.
6. If the response status >= 400, return an error with the status code.
7. On success, return an empty results map.
8. Use r.HTTPClient for the request (same as executeHTTPStep). Apply a 30s timeout if no
   per-step timeout is set.

Add import "fmt" if not present. Run go build ./.... Commit:
"FlowRun: flow/step timeout enforcement and publish step implementation"
```

---

## 18. MockEndpoint: persist captured requests to CRD status

**Status:** `[x]`

**Why:** The mock handler captures request events but they never reach the CRD status. Developers
using MockEndpoints have no way to inspect what was received.

**Files to change:** `internal/gateway/webhook/mock_handler.go`, `internal/gateway/webhook/watcher.go`,
`cmd/webhook-gateway/main.go`

```
Move request capture out of the channel and into direct CRD status updates from the gateway.

The webhook gateway already has a k8sClient (passed to NewTriggerWatcher). Use it in the
MockHandler to write captured requests directly to MockEndpoint status.

--- Part A: MockHandler gets a k8sClient ---

Update MockHandler struct and constructor:
  type MockHandler struct {
      registry  *MockRegistry
      k8sClient client.Client
      log       logr.Logger
  }
  func NewMockHandler(registry *MockRegistry, k8sClient client.Client, log logr.Logger) *MockHandler

Remove the events channel (CapturedRequestEvent, Events() method) — it is no longer needed.

--- Part B: Status update in ServeHTTP ---

After serving the response (writing headers/body), update MockEndpoint status:

  func (h *MockHandler) updateCapturedRequest(ctx context.Context, namespace, name string,
      captured automationv1alpha1.CapturedRequest, maxHistory int32) {

  1. Fetch the MockEndpoint by namespace/name.
  2. Append the CapturedRequest to status.recentRequests.
  3. Trim recentRequests to the last maxHistory entries (oldest first).
  4. Increment status.requestCount.
  5. Patch the status using client.Status().Patch with MergeFrom.
  }

Call updateCapturedRequest in a goroutine (non-blocking) so it does not slow down the HTTP
response.

The MockEntry (from MockRegistry) should store Namespace and Name so the handler can look up
the correct MockEndpoint. These are already in MockEntry from the previous implementation.

CapturedRequest (from api/v1alpha1/mockendpoint_types.go) fields to populate:
  Timestamp, Method, Path, Headers (all request headers), Body (truncated at 4KB),
  BodyTruncated (true if body > 4KB), ResponseStatusCode (the status we returned).

--- Part C: Update callers ---

In cmd/webhook-gateway/main.go: pass k8sClient to NewMockHandler.
In watcher.go: remove any reference to the events channel if it was wired there.

Run go build ./.... Commit:
"MockEndpoint: persist captured requests to CRD status via gateway k8sClient"
```

---

## 19. Plugin secret injection via spec.plugin.secretRefs

**Status:** `[x]`

**Why:** Plugin Deployments need secrets (API keys, passwords) injected as env vars. The
`secretRefs` field is in the spec but ignored by the reconciler.

**Files to change:** `internal/controller/integration_controller.go`

```
In desiredPluginDeployment, add secret-derived env vars from spec.plugin.secretRefs.

Each PluginSecretRef has:
  SecretName    string            // Kubernetes Secret name
  EnvVarMappings map[string]string // map from secret key → env var name

For each PluginSecretRef, for each entry in EnvVarMappings, append to the container's
Env slice:
  corev1.EnvVar{
      Name: envVarName,
      ValueFrom: &corev1.EnvVarSource{
          SecretKeyRef: &corev1.SecretKeySelector{
              LocalObjectReference: corev1.LocalObjectReference{Name: secretRef.SecretName},
              Key: secretKey,
          },
      },
  }

These must be appended AFTER the standard injected env vars (KUBEZAP_NAMESPACE etc.) so
that operators can override injected vars via secretRefs if they choose.

No RBAC change needed — the reconciler already has apps/deployments create/update/patch.
However the plugin Deployment will need to be able to read its own secrets. Document this
as a NOTE comment in the code: the operator does not auto-grant secret RBAC to plugin
Deployments — the user must grant it via a Role/RoleBinding.

Run go build ./.... Commit:
"Integration: inject plugin secrets as env vars from spec.plugin.secretRefs"
```

---

## 20. Prometheus metrics

**Status:** `[x]`

**Why:** Observability from day one is a design goal. Without metrics, there is no way to
alert on trigger storms, slow flows, or high step failure rates.

**Files to create/change:**
- `internal/metrics/metrics.go` (new)
- `internal/controller/cron_scheduler.go`
- `internal/controller/flowrun_controller.go`

```
Add Prometheus metrics for trigger firings, FlowRun completion, and step outcomes.

--- Part A: Define metrics (internal/metrics/metrics.go) ---

Use controller-runtime's metrics registry (sigs.k8s.io/controller-runtime/pkg/metrics):

  import ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

  var (
      TriggerFirings = prometheus.NewCounterVec(prometheus.CounterOpts{
          Name: "kubezap_trigger_firings_total",
          Help: "Total number of trigger firings by trigger type.",
      }, []string{"namespace", "trigger", "type", "result"})
      // result: "success" | "rate_limited" | "error"

      FlowRunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
          Name:    "kubezap_flowrun_duration_seconds",
          Help:    "Duration of FlowRun execution from start to terminal phase.",
          Buckets: prometheus.DefBuckets,
      }, []string{"namespace", "flow", "phase"})
      // phase: "Succeeded" | "Failed"

      StepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
          Name:    "kubezap_step_duration_seconds",
          Help:    "Duration of individual step execution.",
          Buckets: prometheus.DefBuckets,
      }, []string{"namespace", "flow", "step_type", "outcome"})
      // outcome: "Succeeded" | "Failed"
  )

  func init() {
      ctrlmetrics.Registry.MustRegister(TriggerFirings, FlowRunDuration, StepDuration)
  }

--- Part B: Instrument cron_scheduler.go ---

In the cron job function, after the cooldown check:
- On successful FlowRun creation:
    metrics.TriggerFirings.WithLabelValues(ns, name, "cron", "success").Inc()
- On rate-limited:
    metrics.TriggerFirings.WithLabelValues(ns, name, "cron", "rate_limited").Inc()

--- Part C: Instrument flowrun_controller.go ---

1. Record FlowRun duration when it transitions to a terminal phase (Succeeded or Failed).
   In the code path that sets phase=Succeeded or phase=Failed for the overall FlowRun,
   compute duration = now - flowRun.Status.StartTime (if StartTime is set) and record:
     metrics.FlowRunDuration.WithLabelValues(flowRun.Namespace, flowRun.Spec.FlowRef.Name,
         phase).Observe(duration.Seconds())

2. Record step duration after each step execution.
   Before executeStep, note start := time.Now().
   After executeStep returns, record:
     metrics.StepDuration.WithLabelValues(flowRun.Namespace, flowRun.Spec.FlowRef.Name,
         step.Action.Type, stepStatus.Phase).Observe(time.Since(start).Seconds())

Import "github.com/yourname/kubezap/internal/metrics" in both files.

Run go build ./.... Commit:
"Observability: Prometheus metrics for trigger firings, FlowRun duration, step outcomes"
```

---

## 21. Ginkgo unit tests — Flow reconciler

**Status:** `[x]`

**Why:** The Flow reconciler has no tests. CI will not catch regressions in spec validation logic.

**Files to create:** `internal/controller/flow_controller_test.go`

```
Write Ginkgo v2 + Gomega unit tests for the Flow reconciler using envtest.

File: internal/controller/flow_controller_test.go

Use the existing suite setup in internal/controller/suite_test.go (or create it if it
does not exist — check first). The suite sets up an envtest environment and a Manager.

Test cases (use Describe/Context/It structure):

Describe("FlowReconciler"):

  Context("when a Flow has no steps"):
    It("should set Ready=False with reason InvalidSpec")

  Context("when a Flow has duplicate step names"):
    It("should set Ready=False with reason InvalidSpec")

  Context("when a Flow step has an invalid runAfter reference"):
    It("should set Ready=False with reason InvalidSpec")

  Context("when a Flow has an http step with no URL"):
    It("should set Ready=False with reason InvalidSpec")

  Context("when a Flow has a publish step with no integrationRef"):
    It("should set Ready=False with reason InvalidSpec")

  Context("when a Flow spec is valid"):
    It("should set Ready=True with reason FlowReady")

  Context("when the Flow is deleted"):
    It("should reconcile without error")

For each test, create a Flow CR using the k8s client, trigger reconciliation by calling
r.Reconcile(ctx, ctrl.Request{NamespacedName: ...}), then fetch the Flow and assert
the condition using gomega.

Read internal/controller/flow_controller.go and any existing test files for patterns.
Check if there is already a suite_test.go — if not, create one based on kubebuilder
scaffolding conventions.

Run make test to verify. Commit:
"Tests: Ginkgo unit tests for Flow reconciler"
```

---

## 22. Ginkgo unit tests — FlowRun reconciler

**Status:** `[x]`

**Why:** The FlowRun reconciler is the core of the system and has no tests.

**Files to create:** `internal/controller/flowrun_controller_test.go`

```
Write Ginkgo v2 + Gomega unit tests for the FlowRun reconciler.

File: internal/controller/flowrun_controller_test.go

Use the same envtest suite as the Flow tests. Mock the HTTP client using
httptest.NewServer to avoid real network calls.

Test cases:

Describe("FlowRunReconciler"):

  Context("when the referenced Flow does not exist"):
    It("should set FlowRun phase=Failed")

  Context("when the Flow has a single http step"):
    Setup: create a Flow with one http step pointing to an httptest.Server that
           returns 200 with body '{"id":"42"}'.
    It("should execute the step and set FlowRun phase=Succeeded")
    It("should store the step result in status.stepStatuses")

  Context("when the http step returns a 5xx response"):
    It("should retry and eventually set phase=Failed")
    (Use a retry policy with maxRetries=1 to keep the test fast)

  Context("when a step has an unsatisfied runAfter dependency"):
    It("should not execute the step until the dependency succeeds")

  Context("when FlowRun is already in phase=Succeeded"):
    It("should not re-execute steps")

  Context("TTL GC"):
    It("should delete a succeeded FlowRun after TTLSucceeded")

Read internal/controller/flowrun_controller.go for the reconciler structure.

Run make test to verify. Commit:
"Tests: Ginkgo unit tests for FlowRun reconciler"
```

---

---

## 23. Gateway ServiceAccount, Role, and RoleBinding

**Status:** `[ ]`

**Why:** The webhook gateway Deployment already sets `serviceAccountName: kubezap-webhook-gateway`
but the controller never creates that ServiceAccount, Role, or RoleBinding. Nothing in the
gateway can work without this — it is the top deploy blocker.
See `docs/architecture.md#gateway-serviceaccount-and-rbac` for the full design.

**Files to change:** `internal/controller/gateway_deployment.go`, `internal/controller/trigger_controller.go`

```
Extend the webhook gateway Deployment reconciliation to also create the ServiceAccount,
Role, and RoleBinding that the gateway pod runs under.

--- Part A: gateway_deployment.go ---

Add three new helper functions alongside desiredWebhookGatewayDeployment:

1. desiredWebhookGatewayServiceAccount(namespace string) *corev1.ServiceAccount
   Name: "kubezap-webhook-gateway", Namespace: namespace
   Labels: same as Deployment labels

2. desiredWebhookGatewayRole(namespace string) *rbacv1.Role
   Name: "kubezap-webhook-gateway", Namespace: namespace
   Rules:
     - apiGroups: ["automation.kubezap.io"]
       resources: ["triggers"]
       verbs: ["get", "list", "watch"]
     - apiGroups: ["automation.kubezap.io"]
       resources: ["flowruns"]
       verbs: ["create"]
     - apiGroups: ["automation.kubezap.io"]
       resources: ["mockendpoints"]
       verbs: ["get", "list", "watch"]
     - apiGroups: ["automation.kubezap.io"]
       resources: ["mockendpoints/status"]
       verbs: ["get", "update", "patch"]

3. desiredWebhookGatewayRoleBinding(namespace string) *rbacv1.RoleBinding
   Name: "kubezap-webhook-gateway", Namespace: namespace
   RoleRef: Kind=Role, Name="kubezap-webhook-gateway", APIGroup=rbac.authorization.k8s.io
   Subjects: [{Kind: ServiceAccount, Name: "kubezap-webhook-gateway", Namespace: namespace}]

Add imports: corev1 "k8s.io/api/core/v1", rbacv1 "k8s.io/api/rbac/v1"

--- Part B: trigger_controller.go ---

In reconcileWebhookGatewayDeployment, before the Deployment create-or-update block,
add create-or-update calls for the ServiceAccount, Role, and RoleBinding using the
same sigs.k8s.io/controller-runtime/pkg/controller/controllerutil.CreateOrUpdate
pattern. Use client.MergeFrom for patching. Idempotent — safe to call on every reconcile.

Add RBAC markers to the TriggerReconciler:
  // +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
  // +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch
  // +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch

Run make manifests. Commit:
"Gateway: create ServiceAccount, Role, and RoleBinding for webhook gateway Deployment"
```

---

## 24. CEL `when` evaluation and `Skipped` step phase

**Status:** `[ ]`

**Why:** The `when` field in FlowStep is defined but never evaluated. Without it there is
no conditional branching — the core differentiating feature of the flow engine.
`github.com/google/cel-go` is already an indirect dependency; no new dep needed.
Design: `docs/api/flow.md#conditions-and-cel` and `docs/api/flow.md#skipped-steps-and-dependency-cascading`

**Files to change:** `internal/controller/flowrun_controller.go`

```
Implement CEL when-expression evaluation and Skipped step phase in the FlowRun reconciler.

--- Part A: CEL evaluation helper ---

Add a package-level function:

  func evaluateWhen(when []automationv1alpha1.WhenExpression,
      stepResults map[string]map[string]string,
      triggerData *automationv1alpha1.TriggerData) (bool, error)

Using github.com/google/cel-go/cel:

1. Create a CEL environment with the following variable declarations:
     cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType))
     cel.Variable("steps",   cel.MapType(cel.StringType, cel.DynType))

2. Build the activation (input map):
   - "trigger": a map containing:
       "body":          triggerData.Body (or "" if nil)
       "topic":         triggerData.Topic
       "partition":     fmt.Sprintf("%d", triggerData.Partition)
       "offset":        fmt.Sprintf("%d", triggerData.Offset)
       "scheduledTime": triggerData.ScheduledTime
       "headers":       map of header name → first value
   - "steps": a map from step name (hyphens replaced by underscores) to:
       "status":  stepStatuses[name].Phase (or "")
       "results": stepResults[name] (or empty map)

3. For each WhenExpression:
   a. Compile the expression using env.Compile(expr.Expression)
   b. Create a program with prog, err := env.Program(ast)
   c. Evaluate: out, _, err := prog.Eval(activation)
   d. Assert the result is a bool. If not bool or if err: return false, error.
   e. If the result is false: return false, nil immediately (short-circuit).

4. If all expressions are true (or when is empty): return true, nil.

Import: "github.com/google/cel-go/cel"

--- Part B: Integrate into the step execution loop ---

In the Reconcile method's step execution loop, after checking dependenciesMet and
BEFORE calling executeStep, add a when-evaluation block:

  if len(step.When) > 0 {
      run, err := evaluateWhen(step.When, stepResults, flowRun.Spec.TriggerData)
      if err != nil {
          // CEL compile/eval error → fail the step, not the whole FlowRun
          completionTime := metav1.Now()
          status := automationv1alpha1.StepStatus{
              Name: step.Name, Phase: "Failed",
              Message: fmt.Sprintf("when expression error: %v", err),
              CompletionTime: &completionTime,
          }
          upsertStepStatus(&flowRun, status)
          // continue to next step (or fail flow depending on failurePolicy)
          continue
      }
      if !run {
          completionTime := metav1.Now()
          status := automationv1alpha1.StepStatus{
              Name: step.Name, Phase: "Skipped",
              Message: "when condition evaluated to false",
              CompletionTime: &completionTime,
          }
          upsertStepStatus(&flowRun, status)
          if err := r.Status().Update(ctx, &flowRun); err != nil {
              return ctrl.Result{}, err
          }
          continue
      }
  }

--- Part C: Cascade skip in dependenciesMet ---

Update the dependenciesMet function (or the loop that calls it) to implement cascade skipping:

A step is "cascade-skipped" if ALL of its runAfter dependencies are Skipped.
In that case, treat it as Skipped (not as a blocker).

Update dependenciesMet signature or add a helper allDepsSkipped:

  func allDepsSkipped(step automationv1alpha1.FlowStep, flowRun *automationv1alpha1.FlowRun) bool {
      if len(step.RunAfter) == 0 {
          return false
      }
      for _, dep := range step.RunAfter {
          s := findStepStatus(flowRun.Status.Steps, dep)
          if s == nil || s.Phase != "Skipped" {
              return false
          }
      }
      return true
  }

In the step loop, before dependenciesMet check, check allDepsSkipped first:
  if allDepsSkipped(step, &flowRun) {
      // cascade skip — same pattern as explicit skip above
      continue
  }

Run go build ./... Commit:
"FlowRun: CEL when-expression evaluation and Skipped step phase with cascade"
```

---

## 25. Demo sample CRs

**Status:** `[ ]`

**Why:** There are no runnable sample CRs for the getting-started demo scenario.
Having them in `config/samples/demo/` lets anyone `kubectl apply` them after deploying
the operator and immediately see a working end-to-end flow.
Reference: `docs/guides/getting-started.md`

**Files to create:** `config/samples/demo/` (new directory, no conflicts with anything)

```
Create the demo sample CRs described in docs/guides/getting-started.md.
Read that file first for context on the full scenario.

Directory: config/samples/demo/

File 1: mockendpoints.yaml — three MockEndpoints:
  - name: enrich-order, path: enrich-order, responseSequence cycling express/standard
  - name: notify-express, path: notify-express, single response {notified:true,tier:express}
  - name: notify-standard, path: notify-standard, single response {notified:true,tier:standard}

File 2: flow.yaml — Flow named "order-router" with steps:
  - extract-type: transform step, mappings: {orderType: "$(trigger.body)"}
    (NOTE: this is a placeholder until full JSON path extraction is implemented;
    the demo can be adjusted to pass orderType directly in a header)
  - enrich-order: http POST to the enrich-order mock endpoint
      url: http://kubezap-webhook-gateway.default.svc.cluster.local:8080/mock/enrich-order
      resultMappings: {customerId: "$.customerId", tier: "$.tier"}
  - notify-express: http POST to notify-express mock, runAfter enrich-order,
      when: 'steps.enrich_order.results.tier == "express"'
      body references $(steps.enrich_order.results.customerId)
  - notify-standard: http POST to notify-standard mock, runAfter enrich-order,
      when: 'steps.enrich_order.results.tier == "standard"'
      body references $(steps.enrich_order.results.customerId)

File 3: trigger.yaml — Trigger named "order-placed":
  type: webhook, path: /hooks/order-placed, method: POST
  flowRef: order-router

File 4: kustomization.yaml — lists all three files for kubectl apply -k

Also update config/samples/demo/README.md with a one-paragraph description and
the curl commands from the getting-started guide.

Commit: "Demo: sample CRs for order-router getting-started scenario"
Push: git push -u origin claude/review-docs-schedule-LlDeD
```

---

## 26. Ginkgo unit tests — Integration reconciler

**Status:** `[ ]`

**Why:** The Integration reconciler has no tests.

**Files to create:** `internal/controller/integration_controller_test.go`

```
Write Ginkgo v2 unit tests for the IntegrationReconciler using the existing envtest suite.

Read internal/controller/integration_controller.go fully before writing tests.
Use the same package and suite setup as the existing controller tests.

Test cases:

Describe("IntegrationReconciler"):

  Context("when type=kafka and bootstrapServers is empty"):
    It("sets Ready=False, reason=InvalidSpec")

  Context("when type=kafka and bootstrapServers is non-empty"):
    It("sets Ready=True, reason=IntegrationReady")

  Context("when type=plugin and image is empty"):
    It("sets Ready=False, reason=InvalidSpec")

  Context("when type=plugin with a valid image"):
    It("sets Ready=True")
    It("creates a Deployment named kubezap-plugin-<name>")
    It("the Deployment has the correct image and env vars injected")

  Context("when type is unknown"):
    It("sets Ready=False, reason=InvalidSpec")

For plugin tests, after reconciling fetch the Deployment and assert on
deploy.Spec.Template.Spec.Containers[0].Image and the injected env var names.

Clean up all created resources in DeferCleanup.

Commit: "Tests: Ginkgo unit tests for Integration reconciler"
Push: git push -u origin claude/review-docs-schedule-LlDeD
```

---

## 27. Ginkgo unit tests — MockEndpoint reconciler

**Status:** `[ ]`

**Why:** The MockEndpoint reconciler has no tests.

**Files to create:** `internal/controller/mockendpoint_controller_test.go`

```
Write Ginkgo v2 unit tests for the MockEndpointReconciler using the existing envtest suite.

Read internal/controller/mockendpoint_controller.go fully before writing tests.

Test cases:

Describe("MockEndpointReconciler"):

  Context("when spec.path is empty"):
    It("sets Ready=False, reason=InvalidSpec")

  Context("when spec.path is non-empty"):
    It("sets Ready=True")
    It("sets status.url to contain the path")

  Context("when KUBEZAP_GATEWAY_BASE_URL is set"):
    Setup: os.Setenv("KUBEZAP_GATEWAY_BASE_URL", "http://gateway.example.com")
    It("status.url is prefixed with the base URL")
    Cleanup: os.Unsetenv

  Context("when the MockEndpoint is deleted"):
    It("reconciles without error")

Commit: "Tests: Ginkgo unit tests for MockEndpoint reconciler"
Push: git push -u origin claude/review-docs-schedule-LlDeD
```

---

## Notes

- All Go types in `api/v1alpha1/` require `make generate && make manifests` after changes.
- Module path is `github.com/yourname/kubezap` (placeholder — rename before OperatorHub submission).
- Run `make lint` before committing to catch golangci-lint issues early.
- See `docs/api/` for full CRD specs and `docs/architecture.md` for runtime design context.
- Prompts 23–27 are all safe to run in parallel (no file conflicts between them).
