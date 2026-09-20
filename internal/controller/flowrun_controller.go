/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"github.com/google/cel-go/cel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
	"github.com/kubezap/kubezap-operator/internal/metrics"
)

const retainAnnotation = "kubezap.io/retain"
const cancelAnnotation = "kubezap.io/cancel"
const executingFinalizer = "kubezap.io/executing"

// annotationValueTrue is the string value Kubernetes annotations are compared
// against for the boolean-flag annotations above (annotations are always strings).
const annotationValueTrue = "true"

// resultKeyStatus is the map key used for the HTTP status code / step phase
// value in step result and expression-evaluation maps.
const resultKeyStatus = "status"

// kafkaProducerIdleTTL is the maximum idle time before a cached Kafka producer
// is closed and recreated on next use.
const kafkaProducerIdleTTL = 10 * time.Minute

// executorTransportBackoff is the requeue delay used when the http-executor pod
// is unreachable (connection refused, DNS failure, TLS handshake error, context
// deadline caused by pod unavailability). A short fixed backoff is used rather
// than full exponential jitter because the executor pod is either up or not —
// there is no per-request variance to smooth out.
const executorTransportBackoff = 5 * time.Second

// executorTransportError wraps a transport-layer error returned by callExecutor.
// Transport errors indicate that the http-executor pod itself was unreachable
// (connection refused, DNS failure, TLS error, context deadline from pod
// unavailability). They are distinct from application-level errors (4xx/5xx
// responses from the executor process) and trigger a RequeueAfter rather than
// an immediate step failure.
type executorTransportError struct {
	cause error
}

func (e *executorTransportError) Error() string { return e.cause.Error() }
func (e *executorTransportError) Unwrap() error { return e.cause }

// integrationCacheKeyType is a private key type for storing the per-reconcile
// Integration object cache in a context value.
type integrationCacheKeyType struct{}

var integrationCacheKey = integrationCacheKeyType{}

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows,verbs=get;list;watch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers,verbs=get;list;watch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// FlowRunReconciler reconciles a FlowRun object.
type FlowRunReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	HTTPClient   *http.Client
	TTLSucceeded time.Duration
	TTLFailed    time.Duration

	// MaxConcurrentReconciles controls how many FlowRun reconciliations may run
	// in parallel. Defaults to 10 when unset or <= 0.
	MaxConcurrentReconciles int

	// ExecutionTimeout is the maximum time a FlowRun may remain in Running phase
	// before it is failed as orphaned. Set to 0 to disable.
	ExecutionTimeout time.Duration

	// kafkaProducers caches sarama.SyncProducer instances keyed by bootstrap-server
	// address string. Producers are created lazily and reused across publish steps to
	// avoid the per-call TCP handshake + metadata fetch overhead. Access is
	// synchronized via kafkaProducersMu.
	kafkaProducersMu      sync.Mutex
	kafkaProducers        map[string]sarama.SyncProducer
	kafkaProducerLastUsed map[string]time.Time

	// celEnv is the shared CEL environment, initialized eagerly in SetupWithManager.
	celEnv *cel.Env

	// SSRFBlockedCIDRs is the list of IP ranges that HTTP step outbound requests
	// must not connect to. Defaults to defaultSSRFBlockedCIDRs (RFC1918, loopback,
	// link-local, cloud metadata) when nil. Populated from --http-step-blocked-cidrs.
	// Used as a defence-in-depth pre-flight check before forwarding to the executor.
	SSRFBlockedCIDRs []*net.IPNet

	// SSRFAllowClusterInternal disables the controller-side SSRF pre-check for
	// .svc.cluster.local endpoints only (including the RFC1918 ClusterIP a Service
	// name resolves to) — it does not weaken the CIDR blocklist for any other
	// target. For dev/test environments only. Controlled by --ssrf-allow-in-cluster
	// on the controller binary. See the checkSSRF doc comment in ssrf.go.
	SSRFAllowClusterInternal bool

	// ExecutorBaseURL is the base URL format string for the http-executor Service,
	// with a single %s placeholder for the target namespace.
	// Default: "http://kubezap-http-executor.%s.svc.cluster.local:8091"
	// When mTLS is enabled the scheme is automatically overridden to https by executorScheme().
	ExecutorBaseURL string

	// ExecutorTLSConfig is the TLS config to use for executor RPC calls.
	// Nil when mTLS is disabled (plain HTTP). When non-nil, callExecutor uses
	// executorMTLSClient (initialized once on first use) instead of HTTPClient.
	// Set by cmd/main.go when --executor-mtls=true using MTLSBundle.ClientTLSConfig().
	ExecutorTLSConfig *tls.Config

	// executorMTLSClient is the reusable *http.Client for mTLS executor calls.
	// Initialized lazily on the first call when ExecutorTLSConfig is non-nil.
	// Using sync.Once ensures thread-safe single initialization.
	executorMTLSClient     *http.Client
	executorMTLSClientOnce sync.Once

	// DisableCELCache bypasses the compiled-program cache so every eval recompiles.
	// The cache is unbounded: it grows to hold one entry per distinct `when` expression
	// across all deployed Flows and converges once those Flows stabilise. For typical
	// deployments (< ~10 000 distinct expressions) the memory footprint is negligible
	// and the cache is recommended. Enable this flag only when:
	//   - you are continuously deploying Flows with unique, throwaway expressions and
	//     the cache is observed to grow without bound, OR
	//   - you need fully deterministic per-reconcile behaviour for debugging.
	// CEL compilation is fast (~microseconds), so disabling the cache has no measurable
	// throughput impact under normal load.
	DisableCELCache bool

	// CELCostLimit is the maximum CEL evaluation cost budget per expression.
	// 0 means no limit. Set via --cel-cost-limit flag in cmd/main.go.
	// Prevents DoS via combinatorially-expensive expressions (e.g. nested comprehensions).
	CELCostLimit int

	// celCache maps CEL expression string → compiled cel.Program for reuse across reconciles.
	// sync.Map is used because the reconciler can run in multiple goroutines concurrently.
	// Bypassed when DisableCELCache is true.
	celCache sync.Map
}

// nolint:gocyclo // single dispatch-heavy reconcile loop; splitting it apart without a
// specific extraction plan trades one auditable function for several that only make
// sense read together.
func (r *FlowRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var flowRun automationv1alpha1.FlowRun
	if err := r.Get(ctx, req.NamespacedName, &flowRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Tracing: extract W3C traceparent from FlowRun annotation if present,
	// then start a root span for this reconciliation loop.
	tracer := otel.Tracer("kubezap.io/flowrun")
	ctx = extractTraceContext(ctx, flowRun.Annotations)
	ctx, span := tracer.Start(ctx, "flowrun.reconcile",
		trace.WithAttributes(
			attribute.String("flowrun.name", flowRun.Name),
			attribute.String("flowrun.namespace", flowRun.Namespace),
		))
	defer span.End()

	// FlowRun was deleted while running — cancel it and remove executing finalizer.
	if !flowRun.DeletionTimestamp.IsZero() && flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseRunning {
		if err := r.cancelFlowRun(ctx, &flowRun, "FlowRun deleted while running"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// User requested cancellation via `kubectl annotate flowrun ... kubezap.io/cancel=true`
	// (docs/api/flowrun.md kubectl cheat sheet). Only meaningful while Running — a FlowRun
	// that hasn't started yet or has already reached a terminal phase is handled by the
	// existing paths above/below.
	if flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseRunning && flowRun.Annotations[cancelAnnotation] == annotationValueTrue {
		if err := r.cancelFlowRun(ctx, &flowRun, "FlowRun cancelled via kubezap.io/cancel annotation"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// GC: handle terminal FlowRuns (TTL expiry + maxFlowRuns cap).
	if flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseSucceeded || flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseFailed || flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseCancelled {
		if requeue, err := r.reconcileGC(ctx, &flowRun); err != nil {
			return ctrl.Result{}, err
		} else if requeue > 0 {
			return ctrl.Result{RequeueAfter: requeue}, nil
		}
		return ctrl.Result{}, nil
	}

	// Skip already-terminal FlowRuns.
	switch flowRun.Status.Phase {
	case automationv1alpha1.FlowRunPhaseSucceeded, automationv1alpha1.FlowRunPhaseFailed, automationv1alpha1.FlowRunPhaseCancelled:
		return ctrl.Result{}, nil
	}

	// Fetch referenced Flow from the FlowRun's own namespace.
	// Cross-namespace FlowRefs are not supported in v1alpha1.
	var flow automationv1alpha1.Flow
	if err := r.Get(ctx, types.NamespacedName{
		Name:      flowRun.Spec.FlowRef.Name,
		Namespace: flowRun.Namespace,
	}, &flow); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, "Flow not found: "+flowRun.Spec.FlowRef.Name)
		}
		return ctrl.Result{}, err
	}

	// Inject a per-reconcile Integration cache so that multiple steps referencing
	// the same Integration do not each issue a separate API server call.
	integCache := make(map[string]*automationv1alpha1.Integration)
	ctx = context.WithValue(ctx, integrationCacheKey, integCache)

	// Transition Pending → Running.
	if flowRun.Status.Phase == "" || flowRun.Status.Phase == automationv1alpha1.FlowRunPhasePending {
		if !containsString(flowRun.Finalizers, executingFinalizer) {
			flowRun.Finalizers = append(flowRun.Finalizers, executingFinalizer)
			if err := r.Update(ctx, &flowRun); err != nil {
				return ctrl.Result{}, err
			}
			// r.Update returns the server-side object in-place (including the new
			// resourceVersion), so no re-fetch is needed here.  A cache-based Get
			// can return a stale pre-Update version and cause a conflict on the
			// following Status().Update.
		}
		now := metav1.Now()
		flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
		flowRun.Status.StartTime = &now
		setFlowRunCondition(&flowRun, metav1.Condition{
			Type:               "Running",
			Status:             metav1.ConditionTrue,
			Reason:             "FlowRunRunning",
			Message:            "FlowRun is running",
			LastTransitionTime: now,
		})
		// Observe scheduling latency: time from FlowRun creation to first Running transition.
		queueSecs := now.Time.Sub(flowRun.CreationTimestamp.Time).Seconds()
		metrics.FlowRunQueueDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name,
		).Observe(queueSecs)
		if err := r.Status().Update(ctx, &flowRun); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Derive execution context with flow-level timeout.
	execCtx := ctx
	if flow.Spec.Timeout != nil && flow.Spec.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, flow.Spec.Timeout.Duration)
		defer cancel()
	}

	// Orphan recovery: fail Running FlowRuns that have exceeded the operator-level
	// execution timeout. This recovers FlowRuns abandoned mid-execution after a
	// controller restart.
	if r.ExecutionTimeout > 0 && flowRun.Status.StartTime != nil {
		if time.Since(flowRun.Status.StartTime.Time) > r.ExecutionTimeout {
			return ctrl.Result{}, r.failFlowRun(ctx, &flowRun,
				fmt.Sprintf("execution timeout exceeded (running for %s, limit %s)",
					time.Since(flowRun.Status.StartTime.Time).Truncate(time.Second),
					r.ExecutionTimeout))
		}
	}

	// One-step-per-reconcile execution model.
	//
	// Design: Each Reconcile call processes exactly one "wave" — the set of
	// steps whose runAfter dependencies are all satisfied AND that have not yet
	// started. Steps within the same wave (same dependency set) are executed in
	// parallel goroutines within this call; their results are batched into a
	// single status update. After the wave completes, we return Requeue: true so
	// the next reconcile picks up the following wave. This keeps each goroutine
	// short-lived and prevents reconciler goroutine starvation under load.
	//
	// Wait steps are handled specially: if the wait has not elapsed, we return
	// RequeueAfter and do NOT execute other steps in that reconcile call — the
	// wait step acts as a barrier until it completes.

	// Rebuild stepResults from current step statuses so that downstream steps
	// can reference outputs of already-completed steps on re-entry.
	stepResults := make(map[string]map[string]string) // stepName → resultName → value
	for _, ss := range flowRun.Status.Steps {
		if (ss.Phase == automationv1alpha1.StepPhaseSucceeded || ss.Phase == automationv1alpha1.StepPhaseSkipped) && ss.Results != nil {
			stepResults[ss.Name] = resultsToMap(ss.Results)
		}
	}

	// Resolve Flow parameters before dispatching any step — see
	// docs/design/flow-parameters.md. A required parameter that cannot be
	// resolved fails the FlowRun immediately, before any step executes. Resolution is
	// deterministic given FlowRunSpec (immutable after creation) and Flow.spec.params, so
	// recomputing it on every reconcile (rather than caching it in status) is safe and
	// matches how stepResults is already rebuilt fresh above.
	flowParams, paramErr := resolveFlowParams(flow.Spec.Params, flowRun.Spec.Params, stepResults, flowRun.Spec.TriggerData)
	if paramErr != nil {
		return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, paramErr.Error())
	}

	// Check whether all steps have reached a terminal state. If yes, we fall
	// through to the "All steps done — succeed" block below.
	allTerminal := true
	anyRunning := false
	for _, step := range flow.Spec.Steps {
		existing := findStepStatus(flowRun.Status.Steps, step.Name)
		if existing == nil {
			allTerminal = false
		} else {
			switch existing.Phase {
			case automationv1alpha1.StepPhaseSucceeded, automationv1alpha1.StepPhaseSkipped, automationv1alpha1.StepPhaseFailed:
				// terminal — ok
			case automationv1alpha1.StepPhaseRunning, automationv1alpha1.StepPhaseWaiting:
				anyRunning = true
				allTerminal = false
			default:
				allTerminal = false
			}
		}
	}

	if !allTerminal {
		// Scan all steps and collect the ones that are ready to execute this wave.
		// A step is "ready" when:
		//   1. Its runAfter deps are all in Succeeded/Skipped state, AND
		//   2. It has not yet started (no status entry, or status is empty/Pending).
		//
		// Cascade-skip and when-condition skips are resolved inline here because
		// they do not require IO and complete immediately.

		// First, handle any immediate (non-IO) transitions: cascade-skip and
		// when=false skips. These may unblock subsequent waves so we process
		// them inline before deciding whether to requeue.
		failurePolicyContinue := flow.Spec.FailurePolicy == automationv1alpha1.FailurePolicyContinue
		skippedAny := false
		for _, step := range flow.Spec.Steps {
			if !r.dependenciesMet(step, flowRun.Status.Steps, flow.Spec.Steps, failurePolicyContinue) {
				continue
			}
			existing := findStepStatus(flowRun.Status.Steps, step.Name)
			if existing != nil && existing.Phase != "" && existing.Phase != automationv1alpha1.StepPhasePending {
				// Already processed.
				continue
			}

			// Cascade-skip: all runAfter deps were Skipped.
			if allDepsSkipped(step, &flowRun) {
				now := metav1.Now()
				flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
					Name:           step.Name,
					Phase:          automationv1alpha1.StepPhaseSkipped,
					Message:        "all runAfter dependencies were skipped",
					CompletionTime: &now,
				})
				skippedAny = true
				continue
			}

			// Evaluate when conditions (pure CEL — no IO).
			if len(step.When) > 0 {
				run, err := r.evaluateWhen(step.When, stepResults, flowRun.Status.Steps, flowRun.Spec.TriggerData, flowParams)
				if err != nil {
					whenErrReason := "eval_error"
					if strings.Contains(err.Error(), "compile error") {
						whenErrReason = "compile_error"
					} else if strings.Contains(err.Error(), "program error") {
						whenErrReason = "program_error"
					}
					metrics.WhenExpressionErrors.WithLabelValues(flowRun.Spec.FlowRef.Name, whenErrReason).Inc()
					now := metav1.Now()
					failMsg := fmt.Sprintf("when expression error: %v", err)
					flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
						Name:           step.Name,
						Phase:          automationv1alpha1.StepPhaseFailed,
						Message:        failMsg,
						CompletionTime: &now,
					})
					if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
						return ctrl.Result{}, err2
					}
					if step.OnFailure == automationv1alpha1.OnFailureActionContinue || flow.Spec.FailurePolicy == automationv1alpha1.FailurePolicyContinue {
						skippedAny = true
						continue
					}
					return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fmt.Sprintf("step %q when expression error: %v", step.Name, err))
				}
				if !run {
					now := metav1.Now()
					flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
						Name:           step.Name,
						Phase:          automationv1alpha1.StepPhaseSkipped,
						Message:        "when condition evaluated to false",
						CompletionTime: &now,
					})
					skippedAny = true
					continue
				}
			}
		}

		// Persist any inline skip/fail transitions before checking for IO steps.
		if skippedAny {
			if err := r.Status().Update(ctx, &flowRun); err != nil {
				return ctrl.Result{}, err
			}
			// Re-fetch to get a fresh resourceVersion and up-to-date step statuses.
			if err := r.Get(ctx, req.NamespacedName, &flowRun); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			// Requeue immediately: the skips may have made new steps ready.
			//nolint:staticcheck // Requeue:true (no RequeueAfter) deliberately defers to the
			// controller's own rate limiter -- not equivalent to any RequeueAfter value.
			return ctrl.Result{Requeue: true}, nil
		}

		// Collect all steps ready for IO execution this wave.
		// A step is eligible if:
		//   - Its runAfter deps are satisfied, AND
		//   - It has not yet started (no status entry, or status is Pending), OR
		//   - It is a wait step in "Waiting" phase that may have elapsed.
		type readyStep struct {
			step    automationv1alpha1.FlowStep
			stepIdx int
		}
		var waveSteps []readyStep
		for i, step := range flow.Spec.Steps {
			if !r.dependenciesMet(step, flowRun.Status.Steps, flow.Spec.Steps, failurePolicyContinue) {
				continue
			}
			existing := findStepStatus(flowRun.Status.Steps, step.Name)
			if existing != nil && existing.Phase != "" && existing.Phase != automationv1alpha1.StepPhasePending {
				// Re-admit wait steps that are in Waiting phase — they need to be
				// rechecked to see if the wait duration has elapsed.
				if existing.Phase == automationv1alpha1.StepPhaseWaiting && step.Action.Type == stepActionWait {
					waveSteps = append(waveSteps, readyStep{step: step, stepIdx: i})
				}
				continue
			}
			// Skip steps already handled inline above (cascade-skip, when=false).
			// Those are already in terminal state from the loop above.
			waveSteps = append(waveSteps, readyStep{step: step, stepIdx: i})
		}

		if len(waveSteps) == 0 {
			if anyRunning {
				// Something is still Running/Waiting (e.g. a wait step that
				// set Waiting and returned; we are waiting for it to elapse).
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			// All reachable steps are terminal — fall through to succeed below.
		} else {
			// Check for a wait step in the wave. A wait step acts as a serial
			// barrier: if one is present and has not elapsed, we return RequeueAfter
			// without executing any other steps in the wave.
			for _, rs := range waveSteps {
				step := rs.step
				if step.Action.Type == stepActionWait {
					now := metav1.Now()
					startTime := &now
					if existing := findStepStatus(flowRun.Status.Steps, step.Name); existing != nil && existing.StartTime != nil {
						startTime = existing.StartTime
					}
					ss := automationv1alpha1.StepRunStatus{
						Name:      step.Name,
						StartTime: startTime,
						Attempts:  1,
					}
					requeueAfter, err := r.executeWaitStep(ctx, log, &flowRun, step, &ss)
					if err != nil {
						completionTime := metav1.Now()
						ss.Phase = automationv1alpha1.StepPhaseFailed
						ss.Message = err.Error()
						ss.CompletionTime = &completionTime
						flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, ss)
						if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
							return ctrl.Result{}, err2
						}
						if step.OnFailure == automationv1alpha1.OnFailureActionContinue || flow.Spec.FailurePolicy == automationv1alpha1.FailurePolicyContinue {
							//nolint:staticcheck // Requeue:true (no RequeueAfter) deliberately defers to the
							// controller's own rate limiter -- not equivalent to any RequeueAfter value.
							return ctrl.Result{Requeue: true}, nil
						}
						return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fmt.Sprintf("step %q failed: %s", step.Name, ss.Message))
					}
					if requeueAfter > 0 {
						// Wait has not elapsed — persist Waiting status and requeue.
						flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, ss)
						if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
							return ctrl.Result{}, err2
						}
						return ctrl.Result{RequeueAfter: requeueAfter}, nil
					}
					// Wait elapsed — mark Succeeded and requeue to process next wave.
					completionTime := metav1.Now()
					ss.Phase = automationv1alpha1.StepPhaseSucceeded
					ss.CompletionTime = &completionTime
					flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, ss)
					if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
						return ctrl.Result{}, err2
					}
					//nolint:staticcheck // Requeue:true (no RequeueAfter) deliberately defers to the
					// controller's own rate limiter -- not equivalent to any RequeueAfter value.
					return ctrl.Result{Requeue: true}, nil
				}
			}

			// Execute all IO wave steps in parallel goroutines.
			type stepResult struct {
				name         string
				stepType     string
				status       automationv1alpha1.StepRunStatus
				failFatal    bool // true = non-Continue failure; stop FlowRun
				failMsg      string
				duration     time.Duration
				requeueAfter time.Duration // >0 when executor pod was unreachable (transport error)
			}
			results := make([]stepResult, len(waveSteps))
			var wg sync.WaitGroup
			for i, rs := range waveSteps {
				wg.Add(1)
				go func(i int, step automationv1alpha1.FlowStep) {
					defer wg.Done()
					stepStart := time.Now()
					ss, err := r.executeStep(execCtx, log, &step, &flowRun, stepResults, flowRun.Spec.TriggerData, flowParams)
					dur := time.Since(stepStart)
					if err != nil {
						// executeStep only returns a non-nil error for executor
						// transport failures. Signal the reconciler to requeue rather
						// than marking the step as Failed.
						var te *executorTransportError
						if errors.As(err, &te) {
							results[i] = stepResult{
								name:         step.Name,
								stepType:     step.Action.Type,
								status:       automationv1alpha1.StepRunStatus{Name: step.Name, Phase: automationv1alpha1.StepPhaseRunning},
								duration:     dur,
								requeueAfter: executorTransportBackoff,
							}
							return
						}
						// Unexpected non-transport error from executeStep (should not
						// occur under current implementation, but handled defensively).
						results[i] = stepResult{
							name: step.Name, stepType: step.Action.Type,
							status:   automationv1alpha1.StepRunStatus{Name: step.Name, Phase: automationv1alpha1.StepPhaseFailed, Message: err.Error()},
							duration: dur,
						}
						return
					}
					fr := stepResult{
						name: step.Name, stepType: step.Action.Type,
						status:   *ss,
						duration: dur,
					}
					if ss.Phase == automationv1alpha1.StepPhaseFailed {
						if step.OnFailure != automationv1alpha1.OnFailureActionContinue && flow.Spec.FailurePolicy != automationv1alpha1.FailurePolicyContinue {
							fr.failFatal = true
							fr.failMsg = fmt.Sprintf("step %q failed: %s", step.Name, ss.Message)
						}
					}
					results[i] = fr
				}(i, rs.step)
			}
			wg.Wait()

			// Batch all result statuses into the FlowRun status in one update.
			// Check for transport errors first: if the executor pod was unreachable
			// for any step, requeue the entire FlowRun wave without marking any
			// steps as Failed (they stay in Running phase for the retry).
			var transportRequeue time.Duration
			for _, res := range results {
				if res.requeueAfter > transportRequeue {
					transportRequeue = res.requeueAfter
				}
			}
			if transportRequeue > 0 {
				// Do not persist "Running" status noise for steps that haven't
				// changed phase — skip the status update and requeue cleanly.
				return ctrl.Result{RequeueAfter: transportRequeue}, nil
			}

			var fatalMsg string
			for _, res := range results {
				metrics.StepDuration.WithLabelValues(
					flowRun.Namespace, flowRun.Spec.FlowRef.Name,
					res.stepType, string(res.status.Phase),
				).Observe(res.duration.Seconds())
				switch res.status.Phase {
				case automationv1alpha1.StepPhaseSucceeded, automationv1alpha1.StepPhaseFailed, automationv1alpha1.StepPhaseSkipped:
					res.status.DurationMillis = ptr.To(res.duration.Milliseconds())
				}
				flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, res.status)
				if res.failFatal && fatalMsg == "" {
					fatalMsg = res.failMsg
				}
			}
			if err := r.Status().Update(ctx, &flowRun); err != nil {
				return ctrl.Result{}, err
			}

			if fatalMsg != "" {
				return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fatalMsg)
			}

			// Wave complete — requeue immediately to process the next wave.
			//nolint:staticcheck // Requeue:true (no RequeueAfter) deliberately defers to the
			// controller's own rate limiter -- not equivalent to any RequeueAfter value.
			return ctrl.Result{Requeue: true}, nil
		} // end else (waveSteps non-empty)
	}

	// All steps done. With failurePolicy:Continue the flow runs to completion even
	// after step failures, but the FlowRun is Failed if any step without
	// onFailure:Continue ended in Failed state.
	for _, ss := range flowRun.Status.Steps {
		if ss.Phase != automationv1alpha1.StepPhaseFailed {
			continue
		}
		for _, step := range flow.Spec.Steps {
			if step.Name == ss.Name && step.OnFailure != automationv1alpha1.OnFailureActionContinue && flow.Spec.FailurePolicy != automationv1alpha1.FailurePolicyContinue {
				msg := fmt.Sprintf("step %q failed: %s", ss.Name, ss.Message)
				return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, msg)
			}
		}
	}

	now := metav1.Now()
	flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseSucceeded
	flowRun.Status.CompletionTime = &now
	setFlowRunCondition(&flowRun, metav1.Condition{
		Type:               "Succeeded",
		Status:             metav1.ConditionTrue,
		Reason:             "FlowRunSucceeded",
		Message:            "FlowRun completed successfully",
		LastTransitionTime: now,
	})
	if flowRun.Status.StartTime != nil {
		duration := time.Since(flowRun.Status.StartTime.Time)
		metrics.FlowRunDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name, "Succeeded",
		).Observe(duration.Seconds())
		flowRun.Status.DurationMillis = ptr.To(duration.Milliseconds())
	}
	span.SetStatus(otelcodes.Ok, "")
	// Status update must happen BEFORE the metadata Update (finalizer removal).
	// r.Update() overwrites the local object with the server response, which still
	// has the old phase ("Running") until Status().Update is called.
	if err := r.Status().Update(ctx, &flowRun); err != nil {
		return ctrl.Result{}, err
	}
	// Always update metadata to persist the kubezap.io/phase label (and remove
	// the executing finalizer when present).
	if flowRun.Labels == nil {
		flowRun.Labels = make(map[string]string)
	}
	flowRun.Labels["kubezap.io/phase"] = "Succeeded"
	if containsString(flowRun.Finalizers, executingFinalizer) {
		flowRun.Finalizers = removeString(flowRun.Finalizers, executingFinalizer)
	}
	return ctrl.Result{}, r.Update(ctx, &flowRun)
}

func (r *FlowRunReconciler) executeStep(
	ctx context.Context,
	log logr.Logger,
	step *automationv1alpha1.FlowStep,
	flowRun *automationv1alpha1.FlowRun,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
) (*automationv1alpha1.StepRunStatus, error) {
	// Start a child span for this step execution.
	ctx, stepSpan := otel.Tracer("kubezap.io/flowrun").Start(ctx, "flowrun.step",
		trace.WithAttributes(
			attribute.String("step.name", step.Name),
			attribute.String("step.type", step.Action.Type),
		))
	defer stepSpan.End()

	now := metav1.Now()
	status := &automationv1alpha1.StepRunStatus{
		Name:      step.Name,
		Phase:     automationv1alpha1.StepPhaseRunning,
		StartTime: &now,
		Attempts:  1,
	}

	switch step.Action.Type {
	case stepActionHTTP:
		results, msg, attempts, err := r.executeHTTPStep(ctx, log, step, stepResults, triggerData, params, flowRun.Namespace)
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if attempts > 0 {
			status.Attempts = int32(attempts)
		}
		if err != nil {
			var te *executorTransportError
			if errors.As(err, &te) {
				// Transport error — executor pod unreachable. Do not mark the step
				// as Failed; return the error so the reconciler can requeue instead.
				return status, err
			}
			status.Phase = automationv1alpha1.StepPhaseFailed
			status.Message = err.Error()
		} else {
			status.Phase = automationv1alpha1.StepPhaseSucceeded
			status.Message = msg
			status.Results = mapsToResults(results)
		}
	case stepActionTransform:
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if step.Action.Transform == nil {
			status.Phase = automationv1alpha1.StepPhaseFailed
			status.Message = fmt.Sprintf("step %q has type=transform but no transform spec", step.Name)
		} else {
			substituted := make(map[string]string, len(step.Action.Transform.Mappings))
			for k, v := range step.Action.Transform.Mappings {
				substituted[k] = substituteVars(v, stepResults, triggerData, params)
			}
			status.Phase = automationv1alpha1.StepPhaseSucceeded
			status.Results = mapsToResults(substituted)
		}
	case stepActionPublish:
		result, attempts, err := r.executePublishStep(ctx, flowRun, step, triggerData, stepResults, params)
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if attempts > 0 {
			status.Attempts = int32(attempts)
		}
		if err != nil {
			status.Phase = automationv1alpha1.StepPhaseFailed
			status.Message = err.Error()
		} else {
			status.Phase = automationv1alpha1.StepPhaseSucceeded
			status.Results = mapsToResults(result)
		}
	default:
		completionTime := metav1.Now()
		status.Phase = automationv1alpha1.StepPhaseFailed
		status.CompletionTime = &completionTime
		status.Message = fmt.Sprintf("unknown step action type: %q", step.Action.Type)
	}

	return status, nil
}

// nolint:gocyclo // single-pass HTTP step request/response builder with a dispatch
// case per config option, kept as one function.
func (r *FlowRunReconciler) executeHTTPStep(
	ctx context.Context,
	log logr.Logger,
	step *automationv1alpha1.FlowStep,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
	namespace string,
) (map[string]string, string, int, error) {
	if step.Action.HTTP == nil {
		return nil, "", 0, fmt.Errorf("step %q has type=http but no http spec", step.Name)
	}

	// Apply per-step timeout from FlowStep.Timeout if set; otherwise fall back to HTTPAction.TimeoutSeconds.
	if step.Timeout != nil && step.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Timeout.Duration)
		defer cancel()
	}

	h := step.Action.HTTP
	// resolvedURL is the fully-resolved URL (with secret values substituted).
	// displayURL has secret placeholders intact for safe inclusion in log/error messages.
	resolvedURL, displayURL, err := r.substituteVarsWithSecrets(ctx, namespace, h.URL, stepResults, triggerData, params)
	if err != nil {
		return nil, "", 0, fmt.Errorf("resolving secrets in URL for step %q: %w", step.Name, err)
	}
	body, _, err := r.substituteVarsWithSecrets(ctx, namespace, h.Body, stepResults, triggerData, params)
	if err != nil {
		return nil, "", 0, fmt.Errorf("resolving secrets in body for step %q: %w", step.Name, err)
	}
	headers := make(map[string]string, len(h.Headers))
	for k, v := range h.Headers {
		actual, _, herr := r.substituteVarsWithSecrets(ctx, namespace, v, stepResults, triggerData, params)
		if herr != nil {
			return nil, "", 0, fmt.Errorf("resolving secrets in header %q for step %q: %w", k, step.Name, herr)
		}
		headers[k] = actual
	}

	// If an HTTP Integration is referenced, merge its base URL, auth headers, default
	// headers, and any configured TLS trust material (CA bundle / client cert).
	var tlsMaterial httpTLSMaterial
	if h.IntegrationRef != nil && h.IntegrationRef.Name != "" {
		var err error
		resolvedURL, tlsMaterial, err = r.applyHTTPIntegration(ctx, h.IntegrationRef.Name, namespace, resolvedURL, headers, stepResults, triggerData, params)
		if err != nil {
			return nil, "", 0, err
		}
	}

	// Defence-in-depth SSRF pre-check: reject blocked targets before forwarding
	// to the executor. The executor re-validates independently to guard against
	// DNS rebinding between this check and the outbound connection.
	if err := checkSSRF(ctx, resolvedURL, r.SSRFBlockedCIDRs, r.SSRFAllowClusterInternal); err != nil {
		return nil, "", 0, fmt.Errorf("step %q blocked by SSRF protection: %w", step.Name, err)
	}

	method := h.Method
	if method == "" {
		method = "POST"
	}

	timeoutSec := h.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	// Apply per-action timeout (only when FlowStep.Timeout is not already applied).
	stepCtx := ctx
	if step.Timeout == nil || step.Timeout.Duration == 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	// Retry logic.
	maxAttempts := 1
	var retryPolicy *automationv1alpha1.RetryPolicy
	if step.RetryPolicy != nil {
		retryPolicy = step.RetryPolicy
		maxAttempts = int(retryPolicy.MaxRetries) + 1
	}

	// Build the executor request once — it is the same for every retry attempt.
	execReq := executorhttp.ExecuteRequest{
		Method:         method,
		URL:            resolvedURL,
		Headers:        headers,
		Body:           body,
		TimeoutSeconds: int(timeoutSec),
		TLSCABundle:    tlsMaterial.CABundle,
		TLSClientCert:  tlsMaterial.ClientCert,
		TLSClientKey:   tlsMaterial.ClientKey,
	}

	var lastErr error
	attemptsMade := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptsMade = attempt + 1
		if attempt > 0 {
			delay := r.retryDelay(retryPolicy, attempt)
			select {
			case <-stepCtx.Done():
				return nil, "", attemptsMade, stepCtx.Err()
			case <-time.After(delay):
			}
		}

		execResp, err := r.callExecutor(stepCtx, namespace, execReq)
		if err != nil {
			var te *executorTransportError
			if errors.As(err, &te) {
				// The executor pod itself was unreachable — retrying immediately
				// within this reconcile is pointless. Return the transport error
				// directly so the caller can requeue with backoff instead of
				// marking the step as Failed.
				log.Info("executor pod unreachable, requeueing FlowRun with backoff",
					"step", step.Name, "attempt", attempt+1, "error", err.Error())
				return nil, "", attemptsMade, err
			}
			// Non-transport error (e.g. request build failure) — record and retry.
			lastErr = fmt.Errorf("executor RPC for step %q (target: %s): %w", step.Name, displayURL, err)
			log.Error(err, "executor RPC failed", "step", step.Name, "attempt", attempt+1)
			continue
		}

		if execResp.Error != "" {
			// The executor reached the target but the call failed (SSRF block, DNS error,
			// upstream connection failure, etc.). Prefixed error strings (ssrf_blocked:,
			// dns_error:, etc.) allow callers to distinguish permanent failures from transient.
			//
			// Redact the resolved URL from the error string: Go's net/http embeds the full
			// URL (including any secret values substituted into it) in transport error messages.
			// Replace it with displayURL so secret values are not persisted to etcd via
			// StepRunStatus.Message.
			errMsg := execResp.Error
			if resolvedURL != displayURL {
				errMsg = strings.ReplaceAll(errMsg, resolvedURL, displayURL)
			}
			lastErr = fmt.Errorf("%s", errMsg)
			log.Info("HTTP step executor error", "step", step.Name, "error", errMsg, "attempt", attempt+1)
			continue
		}

		if execResp.StatusCode >= 200 && execResp.StatusCode < 300 {
			results := map[string]string{
				"body":          execResp.Body,
				resultKeyStatus: fmt.Sprintf("%d", execResp.StatusCode),
			}
			if len(h.ResultMappings) > 0 {
				var jsonBody map[string]interface{}
				if jsonErr := json.Unmarshal([]byte(execResp.Body), &jsonBody); jsonErr == nil {
					for resultKey, jsonPath := range h.ResultMappings {
						if val := extractSimpleJSONPath(jsonPath, jsonBody); val != "" {
							results[resultKey] = val
						}
					}
				}
			}
			return results, fmt.Sprintf("HTTP %d", execResp.StatusCode), attemptsMade, nil
		}

		lastErr = fmt.Errorf("HTTP %d: %s", execResp.StatusCode, truncate(execResp.Body, 256))
		log.Info("HTTP step non-2xx response", "step", step.Name, "status", execResp.StatusCode, "attempt", attempt+1)
	}

	return nil, "", attemptsMade, lastErr
}

// executorScheme returns "https" when mTLS is enabled and "http" otherwise.
func (r *FlowRunReconciler) executorScheme() string {
	if r.ExecutorTLSConfig != nil {
		return portNameHTTPS
	}
	return portNameHTTP
}

// callExecutor sends a fully-resolved ExecuteRequest to the http-executor Service
// running in the given namespace and returns the response. The caller holds all
// resolved secret values; none are written to etcd or logged here.
//
// When ExecutorTLSConfig is non-nil (mTLS enabled) a dedicated *http.Client with
// the appropriate TLS transport is used for this call; r.HTTPClient is unchanged.
func (r *FlowRunReconciler) callExecutor(ctx context.Context, namespace string, req executorhttp.ExecuteRequest) (*executorhttp.ExecuteResponse, error) {
	baseURL := r.ExecutorBaseURL
	if baseURL == "" {
		baseURL = r.executorScheme() + "://kubezap-http-executor.%s.svc.cluster.local:8091"
	}
	// Use strings.ReplaceAll so that a plain URL (no %s placeholder) also works,
	// e.g. when ExecutorBaseURL is set to a test server address in unit tests.
	executorURL := strings.ReplaceAll(baseURL, "%s", namespace) + "/execute"

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling executor request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, executorURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("building executor HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Propagate W3C trace context across the internal RPC boundary to the
	// http-executor process, so its http_call span nests correctly under this
	// step's flowrun.step span rather than starting a disconnected trace.
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	// When mTLS is enabled, use a reusable client with the TLS transport.
	// The client is initialized exactly once via sync.Once to avoid allocating
	// a new http.Client/http.Transport (and incurring a TLS handshake) per call.
	var httpClient *http.Client
	if r.ExecutorTLSConfig != nil {
		r.executorMTLSClientOnce.Do(func() {
			r.executorMTLSClient = &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: r.ExecutorTLSConfig,
				},
			}
		})
		httpClient = r.executorMTLSClient
	} else {
		httpClient = r.HTTPClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		// Transport-layer failure: the executor pod was unreachable (connection
		// refused, DNS failure, TLS error, context deadline from pod being down).
		// Wrap in executorTransportError so the caller can distinguish this from
		// an application-level error and requeue instead of failing the step.
		return nil, &executorTransportError{cause: fmt.Errorf("sending request to executor: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Application-level error: the executor process responded but indicated
		// failure (e.g. bad request, internal error in the executor itself).
		// This is NOT a transport error — fail immediately, no requeue.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("executor returned HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	var execResp executorhttp.ExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&execResp); err != nil {
		return nil, fmt.Errorf("decoding executor response: %w", err)
	}

	return &execResp, nil
}

func (r *FlowRunReconciler) executePublishStep(
	ctx context.Context,
	flowRun *automationv1alpha1.FlowRun,
	step *automationv1alpha1.FlowStep,
	triggerData *automationv1alpha1.TriggerData,
	stepResults map[string]map[string]string,
	params map[string]string,
) (map[string]string, int, error) {
	ctx, pubSpan := otel.Tracer("kubezap.io/flowrun").Start(ctx, "publish_call",
		trace.WithAttributes(attribute.String("step.name", step.Name)))
	defer pubSpan.End()

	if step.Action.Publish == nil || step.Action.Publish.IntegrationRef.Name == "" {
		return nil, 0, fmt.Errorf("step %q has type=publish but no integrationRef.name", step.Name)
	}

	// Fetch the Integration — use the per-reconcile cache when available.
	integCacheKey := flowRun.Namespace + "/" + step.Action.Publish.IntegrationRef.Name
	var integration *automationv1alpha1.Integration
	if cache, _ := ctx.Value(integrationCacheKey).(map[string]*automationv1alpha1.Integration); cache != nil {
		integration = cache[integCacheKey]
	}
	if integration == nil {
		var fetched automationv1alpha1.Integration
		if err := r.Get(ctx, types.NamespacedName{
			Name:      step.Action.Publish.IntegrationRef.Name,
			Namespace: flowRun.Namespace,
		}, &fetched); err != nil {
			return nil, 0, fmt.Errorf("fetching integration %q: %w", step.Action.Publish.IntegrationRef.Name, err)
		}
		integration = &fetched
		if cache, _ := ctx.Value(integrationCacheKey).(map[string]*automationv1alpha1.Integration); cache != nil {
			cache[integCacheKey] = integration
		}
	}

	// Retry policy — same pattern as executeHTTPStep.
	maxAttempts := 1
	var retryPolicy *automationv1alpha1.RetryPolicy
	if step.RetryPolicy != nil {
		retryPolicy = step.RetryPolicy
		maxAttempts = int(retryPolicy.MaxRetries) + 1
	}

	// Route to appropriate publish backend based on integration type.
	if integration.Spec.Kafka != nil {
		topic := substituteVars(step.Action.Publish.Topic, stepResults, triggerData, params)
		body, _, berr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, step.Action.Publish.Body, stepResults, triggerData, params)
		if berr != nil {
			return nil, 0, fmt.Errorf("resolving secrets in publish body for step %q: %w", step.Name, berr)
		}
		headers := make(map[string]string, len(step.Action.Publish.Headers))
		for k, v := range step.Action.Publish.Headers {
			actual, _, herr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, v, stepResults, triggerData, params)
			if herr != nil {
				return nil, 0, fmt.Errorf("resolving secrets in publish header %q for step %q: %w", k, step.Name, herr)
			}
			headers[k] = actual
		}
		var result map[string]string
		var lastErr error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, attempt, ctx.Err()
				case <-time.After(r.retryDelay(retryPolicy, attempt)):
				}
			}
			result, lastErr = r.publishToKafka(ctx, integration, topic, body, headers)
			if lastErr == nil {
				return result, attempt + 1, nil
			}
		}
		return nil, maxAttempts, lastErr
	}

	if integration.Spec.Plugin == nil {
		return nil, 0, fmt.Errorf("integration %q is not a plugin type", integration.Name)
	}

	port := integration.Spec.Plugin.PublisherPort
	if port == 0 {
		port = 8090
	}

	pluginURL := fmt.Sprintf("http://kubezap-plugin-%s.%s.svc.cluster.local:%d/publish",
		integration.Name, flowRun.Namespace, port)

	destination := substituteVars(step.Action.Publish.Topic, stepResults, triggerData, params)

	body, _, berr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, step.Action.Publish.Body, stepResults, triggerData, params)
	if berr != nil {
		return nil, 0, fmt.Errorf("resolving secrets in publish body for step %q: %w", step.Name, berr)
	}
	headers := make(map[string]string, len(step.Action.Publish.Headers))
	for k, v := range step.Action.Publish.Headers {
		actual, _, herr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, v, stepResults, triggerData, params)
		if herr != nil {
			return nil, 0, fmt.Errorf("resolving secrets in publish header %q for step %q: %w", k, step.Name, herr)
		}
		headers[k] = actual
	}

	// Wrap ctx with a 30s timeout unless ctx already has a shorter deadline.
	const publishTimeout = 30 * time.Second

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, attempt, ctx.Err()
			case <-time.After(r.retryDelay(retryPolicy, attempt)):
			}
		}

		result, attemptErr := r.doPluginPublish(ctx, pluginURL, integration.Name, flowRun.Namespace, destination, body, headers, publishTimeout)
		if attemptErr == nil {
			return result, attempt + 1, nil
		}
		lastErr = attemptErr
	}
	return nil, maxAttempts, lastErr
}

// pluginPublishEnvelope is the JSON envelope POSTed to a plugin's /publish
// endpoint, per the Publisher contract in docs/api/integration.md.
type pluginPublishEnvelope struct {
	Integration string            `json:"integration"`
	Namespace   string            `json:"namespace"`
	Destination string            `json:"destination"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body"`
}

// pluginPublishSuccess is the documented success response body. messageId is
// optional and advisory only — its absence is not an error.
type pluginPublishSuccess struct {
	MessageID string `json:"messageId"`
}

// pluginPublishFailure is the documented failure response body.
type pluginPublishFailure struct {
	Error string `json:"error"`
}

// doPluginPublish performs a single HTTP POST to a plugin publisher endpoint,
// sending the JSON envelope documented in docs/api/integration.md's Publisher
// contract. It creates a per-call context with the given timeout so
// cancellation is scoped to this attempt rather than leaking across retry
// iterations.
func (r *FlowRunReconciler) doPluginPublish(
	ctx context.Context,
	resolvedURL, integrationName, namespace, destination, body string,
	headers map[string]string,
	timeout time.Duration,
) (map[string]string, error) {
	publishCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		var cancel context.CancelFunc
		publishCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	envelope := pluginPublishEnvelope{
		Integration: integrationName,
		Namespace:   namespace,
		Destination: destination,
		Headers:     headers,
		Body:        body,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshalling publish envelope: %w", err)
	}

	req, err := http.NewRequestWithContext(publishCtx, http.MethodPost, resolvedURL, bytes.NewReader(envelopeBytes))
	if err != nil {
		return nil, fmt.Errorf("building publish request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode >= 400 {
		var failure pluginPublishFailure
		if jsonErr := json.Unmarshal(respBytes, &failure); jsonErr == nil && failure.Error != "" {
			return nil, fmt.Errorf("publish endpoint returned status %d: %s", resp.StatusCode, failure.Error)
		}
		return nil, fmt.Errorf("publish endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var success pluginPublishSuccess
	if jsonErr := json.Unmarshal(respBytes, &success); jsonErr == nil && success.MessageID != "" {
		return map[string]string{"messageId": success.MessageID}, nil
	}

	return map[string]string{}, nil
}

func (r *FlowRunReconciler) fetchSecretValue(ctx context.Context, namespace string, ref corev1.SecretKeySelector) (string, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &secret); err != nil {
		return "", fmt.Errorf("secret %q not found: %w", ref.Name, err)
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", ref.Key, ref.Name)
	}
	// Audit: emit structured log + Prometheus counter for every secret read.
	// Secret values are never included — only the name and key are logged.
	ctrl.LoggerFrom(ctx).V(1).Info("secret accessed",
		"namespace", namespace,
		"secretName", ref.Name,
		"secretKey", ref.Key,
	)
	metrics.SecretAccesses.WithLabelValues(namespace, ref.Name).Inc()
	return string(val), nil
}

// httpTLSMaterial holds resolved, inline TLS trust material for an HTTP
// step's outbound call: a PEM CA bundle and/or a PEM client certificate/key
// pair. The zero value means no TLS customization applies — the executor
// falls back to the system root pool with no client certificate, exactly as
// it does for an Integration with no TLS configured. This material is
// forwarded to the executor via ExecuteRequest only; it is never written to
// FlowRun.Status and never logged.
type httpTLSMaterial struct {
	CABundle   string
	ClientCert string
	ClientKey  string
}

// applyHTTPIntegration fetches the named HTTP Integration and merges its base URL,
// default headers, and auth into the provided url and headers. Step-level headers
// take precedence over integration defaults. It also resolves any configured TLS
// trust material (CA bundle / client certificate), returned separately since it
// does not belong in the URL or headers.
func (r *FlowRunReconciler) applyHTTPIntegration(
	ctx context.Context,
	integrationName string,
	namespace string,
	resolvedURL string,
	headers map[string]string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
) (string, httpTLSMaterial, error) {
	// Use the per-reconcile Integration cache when available to avoid repeated
	// API server calls when multiple HTTP steps reference the same Integration.
	cacheKey := namespace + "/" + integrationName
	var integration *automationv1alpha1.Integration
	if cache, _ := ctx.Value(integrationCacheKey).(map[string]*automationv1alpha1.Integration); cache != nil {
		integration = cache[cacheKey]
	}
	if integration == nil {
		var fetched automationv1alpha1.Integration
		if err := r.Get(ctx, types.NamespacedName{
			Name:      integrationName,
			Namespace: namespace,
		}, &fetched); err != nil {
			return "", httpTLSMaterial{}, fmt.Errorf("fetching http integration %q: %w", integrationName, err)
		}
		integration = &fetched
		if cache, _ := ctx.Value(integrationCacheKey).(map[string]*automationv1alpha1.Integration); cache != nil {
			cache[cacheKey] = integration
		}
	}
	if integration.Spec.HTTP == nil {
		return resolvedURL, httpTLSMaterial{}, nil
	}
	httpInteg := integration.Spec.HTTP

	// Apply defaultHeaders first (step headers override).
	for k, v := range httpInteg.DefaultHeaders {
		if _, exists := headers[k]; !exists {
			headers[k] = substituteVars(v, stepResults, triggerData, params)
		}
	}

	// Apply baseUrl: prepend if step URL is a path (not already absolute).
	if httpInteg.BaseURL != "" && !strings.HasPrefix(resolvedURL, "http://") && !strings.HasPrefix(resolvedURL, "https://") {
		resolvedURL = strings.TrimRight(httpInteg.BaseURL, "/") + "/" + strings.TrimLeft(resolvedURL, "/")
	}

	// Apply auth.
	if httpInteg.Auth != nil {
		var err error
		resolvedURL, err = r.applyHTTPAuth(ctx, httpInteg.Auth, integrationName, namespace, resolvedURL, headers)
		if err != nil {
			return "", httpTLSMaterial{}, err
		}
	}

	// Resolve TLS trust material (CA bundle / client cert), if configured.
	var tlsMaterial httpTLSMaterial
	if httpInteg.TLS != nil {
		var err error
		tlsMaterial, err = r.resolveHTTPTLS(ctx, httpInteg.TLS, integrationName, namespace)
		if err != nil {
			return "", httpTLSMaterial{}, err
		}
	}

	return resolvedURL, tlsMaterial, nil
}

// resolveHTTPTLS resolves an HTTP Integration's TLS configuration into inline
// PEM content for forwarding to the executor via ExecuteRequest. The CA bundle
// is read from a ConfigMap key (public trust material, not a secret); the
// client certificate is read from a Secret's standard "tls.crt"/"tls.key" keys
// (matching the kubernetes.io/tls Secret shape, and the existing
// Kafka/AMQP/NATS ClientCertSecretRef convention). The executor never resolves
// these references itself — only the resolved PEM content ever leaves this
// controller, over the internal executor RPC, never persisted to FlowRun.Status
// or logged.
func (r *FlowRunReconciler) resolveHTTPTLS(
	ctx context.Context,
	tlsSpec *automationv1alpha1.HttpTLSSpec,
	integrationName string,
	namespace string,
) (httpTLSMaterial, error) {
	var material httpTLSMaterial

	if tlsSpec.CABundleConfigMapRef != nil {
		bundle, err := r.fetchConfigMapValue(ctx, namespace, *tlsSpec.CABundleConfigMapRef)
		if err != nil {
			return httpTLSMaterial{}, fmt.Errorf("fetching CA bundle for integration %q: %w", integrationName, err)
		}
		material.CABundle = bundle
	}

	if tlsSpec.ClientCertSecretRef != nil {
		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{
			Name:      tlsSpec.ClientCertSecretRef.Name,
			Namespace: namespace,
		}, &secret); err != nil {
			return httpTLSMaterial{}, fmt.Errorf("fetching client cert secret %q for integration %q: %w",
				tlsSpec.ClientCertSecretRef.Name, integrationName, err)
		}
		cert, ok := secret.Data[corev1.TLSCertKey]
		if !ok {
			return httpTLSMaterial{}, fmt.Errorf("key %q not found in client cert secret %q for integration %q",
				corev1.TLSCertKey, tlsSpec.ClientCertSecretRef.Name, integrationName)
		}
		key, ok := secret.Data[corev1.TLSPrivateKeyKey]
		if !ok {
			return httpTLSMaterial{}, fmt.Errorf("key %q not found in client cert secret %q for integration %q",
				corev1.TLSPrivateKeyKey, tlsSpec.ClientCertSecretRef.Name, integrationName)
		}
		material.ClientCert = string(cert)
		material.ClientKey = string(key)

		// Audit: emit structured log + Prometheus counter for every secret read,
		// same as fetchSecretValue — but never the private key value itself.
		ctrl.LoggerFrom(ctx).V(1).Info("secret accessed",
			"namespace", namespace,
			"secretName", tlsSpec.ClientCertSecretRef.Name,
			"secretKey", corev1.TLSCertKey+","+corev1.TLSPrivateKeyKey,
		)
		metrics.SecretAccesses.WithLabelValues(namespace, tlsSpec.ClientCertSecretRef.Name).Inc()
	}

	return material, nil
}

// fetchConfigMapValue reads a single key's value from a ConfigMap. Mirrors
// fetchSecretValue's audit-logging pattern (name + key only, never the value).
// ConfigMap content is not treated as a secret (see HttpTLSSpec's CA bundle
// field), so no SecretAccesses metric is emitted here.
func (r *FlowRunReconciler) fetchConfigMapValue(ctx context.Context, namespace string, ref corev1.ConfigMapKeySelector) (string, error) {
	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &cm); err != nil {
		return "", fmt.Errorf("configmap %q not found: %w", ref.Name, err)
	}
	val, ok := cm.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in configmap %q", ref.Key, ref.Name)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("configmap accessed",
		"namespace", namespace,
		"configMapName", ref.Name,
		"configMapKey", ref.Key,
	)
	return val, nil
}

// applyHTTPAuth resolves the auth configuration from an HTTP Integration and
// sets the appropriate headers or replaces the URL.
func (r *FlowRunReconciler) applyHTTPAuth(
	ctx context.Context,
	auth *automationv1alpha1.HttpAuthSpec,
	integrationName string,
	namespace string,
	resolvedURL string,
	headers map[string]string,
) (string, error) {
	switch auth.Type {
	case automationv1alpha1.HttpAuthBearer:
		if auth.Bearer != nil {
			token, err := r.fetchSecretValue(ctx, namespace, auth.Bearer.TokenSecretRef)
			if err != nil {
				return "", fmt.Errorf("fetching bearer token for integration %q: %w", integrationName, err)
			}
			headers["Authorization"] = "Bearer " + token
		}
	case automationv1alpha1.HttpAuthBasic:
		if auth.Basic != nil {
			username, err := r.fetchSecretValue(ctx, namespace, auth.Basic.UsernameSecretRef)
			if err != nil {
				return "", fmt.Errorf("fetching basic auth username for integration %q: %w", integrationName, err)
			}
			password, err := r.fetchSecretValue(ctx, namespace, auth.Basic.PasswordSecretRef)
			if err != nil {
				return "", fmt.Errorf("fetching basic auth password for integration %q: %w", integrationName, err)
			}
			headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		}
	case automationv1alpha1.HttpAuthAPIKey:
		if auth.APIKey != nil {
			apiKey, err := r.fetchSecretValue(ctx, namespace, auth.APIKey.ValueSecretRef)
			if err != nil {
				return "", fmt.Errorf("fetching api key for integration %q: %w", integrationName, err)
			}
			headers[auth.APIKey.HeaderName] = apiKey
		}
	case automationv1alpha1.HttpAuthSecretURL:
		if auth.SecretURL != nil {
			secretURL, err := r.fetchSecretValue(ctx, namespace, auth.SecretURL.URLSecretRef)
			if err != nil {
				return "", fmt.Errorf("fetching secret URL for integration %q: %w", integrationName, err)
			}
			resolvedURL = secretURL
		}
	}
	return resolvedURL, nil
}

func (r *FlowRunReconciler) executeWaitStep(
	ctx context.Context,
	log logr.Logger,
	flowRun *automationv1alpha1.FlowRun,
	step automationv1alpha1.FlowStep,
	stepStatus *automationv1alpha1.StepRunStatus,
) (time.Duration, error) {
	_ = ctx // reserved for future use

	if step.Action.Wait == nil {
		return 0, fmt.Errorf("wait step %q has no wait config", step.Name)
	}
	duration, err := time.ParseDuration(step.Action.Wait.Duration)
	if err != nil {
		return 0, fmt.Errorf("wait step %q: invalid duration %q: %w", step.Name, step.Action.Wait.Duration, err)
	}

	// Check if ResumeAfter is already set (controller restart or requeue).
	existing := findStepStatus(flowRun.Status.Steps, step.Name)
	if existing != nil && existing.ResumeAfter != nil {
		if time.Now().Before(existing.ResumeAfter.Time) {
			stepStatus.ResumeAfter = existing.ResumeAfter
			stepStatus.Phase = automationv1alpha1.StepPhaseWaiting
			remaining := time.Until(existing.ResumeAfter.Time)
			log.Info("wait step still sleeping", "step", step.Name, "remaining", remaining)
			return remaining, nil
		}
		// Time has elapsed — fall through, caller marks Succeeded.
		return 0, nil
	}

	// First time reaching this step — set ResumeAfter and return duration.
	resumeAt := metav1.NewTime(time.Now().Add(duration))
	stepStatus.ResumeAfter = &resumeAt
	stepStatus.Phase = automationv1alpha1.StepPhaseWaiting
	log.Info("wait step sleeping", "step", step.Name, "duration", duration, "resumeAfter", resumeAt)
	return duration, nil
}

func (r *FlowRunReconciler) retryDelay(policy *automationv1alpha1.RetryPolicy, attempt int) time.Duration {
	if policy == nil {
		return time.Second
	}
	initial := time.Second
	if policy.InitialDelay != nil {
		initial = policy.InitialDelay.Duration
	}
	switch policy.BackoffType {
	case "Exponential":
		d := initial
		for i := 1; i < attempt; i++ {
			d *= 2
		}
		if policy.MaxDelay != nil && d > policy.MaxDelay.Duration {
			d = policy.MaxDelay.Duration
		}
		return d
	case "Linear":
		d := initial * time.Duration(attempt)
		if policy.MaxDelay != nil && d > policy.MaxDelay.Duration {
			d = policy.MaxDelay.Duration
		}
		return d
	default: // Fixed
		return initial
	}
}

// finishFlowRun transitions a FlowRun to a terminal phase (Failed or Cancelled per the
// state model in docs/architecture/flowrun-state-model.md), setting the corresponding
// condition, persisting status, removing the executing finalizer, and recording
// FlowRunDuration under the correct phase label.
func (r *FlowRunReconciler) finishFlowRun(ctx context.Context, flowRun *automationv1alpha1.FlowRun, phase automationv1alpha1.FlowRunPhase, reason, msg string) error {
	now := metav1.Now()
	if flowRun.Labels == nil {
		flowRun.Labels = make(map[string]string)
	}
	flowRun.Labels["kubezap.io/phase"] = string(phase)
	flowRun.Status.Phase = phase
	flowRun.Status.CompletionTime = &now
	flowRun.Status.Message = msg
	setFlowRunCondition(flowRun, metav1.Condition{
		Type:               string(phase),
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
	})
	if flowRun.Status.StartTime != nil {
		duration := time.Since(flowRun.Status.StartTime.Time)
		metrics.FlowRunDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name, string(phase),
		).Observe(duration.Seconds())
		flowRun.Status.DurationMillis = ptr.To(duration.Milliseconds())
	}
	if phase == automationv1alpha1.FlowRunPhaseFailed {
		trace.SpanFromContext(ctx).SetStatus(otelcodes.Error, "FlowRun failed")
	}
	// Status update first — r.Update() would overwrite the local object with the
	// server's still-Running status before Status().Update gets to persist the phase.
	if err := r.Status().Update(ctx, flowRun); err != nil {
		return err
	}
	// Always update metadata to persist the kubezap.io/phase label (and remove
	// the executing finalizer when present).
	if containsString(flowRun.Finalizers, executingFinalizer) {
		flowRun.Finalizers = removeString(flowRun.Finalizers, executingFinalizer)
	}
	return r.Update(ctx, flowRun)
}

func (r *FlowRunReconciler) failFlowRun(ctx context.Context, flowRun *automationv1alpha1.FlowRun, msg string) error {
	return r.finishFlowRun(ctx, flowRun, automationv1alpha1.FlowRunPhaseFailed, "FlowRunFailed", msg)
}

// cancelFlowRun transitions a FlowRun to the Cancelled phase — used when the object is
// deleted while Running (see docs/architecture/flowrun-state-model.md). Cancellation is
// not a failure: it must not be reported as "Failed" in status, conditions, or metrics.
func (r *FlowRunReconciler) cancelFlowRun(ctx context.Context, flowRun *automationv1alpha1.FlowRun, msg string) error {
	return r.finishFlowRun(ctx, flowRun, automationv1alpha1.FlowRunPhaseCancelled, "FlowRunCancelled", msg)
}

func setFlowRunCondition(flowRun *automationv1alpha1.FlowRun, condition metav1.Condition) {
	if condition.LastTransitionTime.IsZero() {
		condition.LastTransitionTime = metav1.Now()
	}
	apimeta.SetStatusCondition(&flowRun.Status.Conditions, condition)
	flowRun.Status.ObservedGeneration = flowRun.Generation
}

// dependenciesMet returns true when all runAfter deps for step have reached a
// terminal state that allows the step to proceed.
//
// A Failed dep is treated as satisfied when either the dependency step's own
// onFailure is "Continue" (a per-step override — see OnFailureAction's doc comment
// on why it's a distinct type from FailurePolicy) or the flow-level failurePolicy is
// "Continue". Checking only the flow-wide failurePolicy here (and ignoring the failed
// dependency's own onFailure) would make onFailure:Continue on an individual step
// unable to unblock that step's own downstream dependents — leaving them stuck
// pending indefinitely even though the FlowRun overall may still reach Succeeded via
// a separate completion check that doesn't require every step to have run. This
// mirrors the equivalent `step.OnFailure == Continue || flow.Spec.FailurePolicy ==
// Continue` check used elsewhere in this file, applied to the dependency instead of
// the current step.
// Without either flag set, a Failed dep blocks the step permanently (the step will be
// cascade-skipped or left pending until the flow terminates).
func (r *FlowRunReconciler) dependenciesMet(step automationv1alpha1.FlowStep, statuses []automationv1alpha1.StepRunStatus, allSteps []automationv1alpha1.FlowStep, failurePolicyContinue bool) bool {
	for _, dep := range step.RunAfter {
		s := findStepStatus(statuses, dep)
		if s == nil {
			return false
		}
		switch s.Phase {
		case automationv1alpha1.StepPhaseSucceeded, automationv1alpha1.StepPhaseSkipped:
			// always satisfied
		case automationv1alpha1.StepPhaseFailed:
			// satisfied when the flow is configured to continue past failures, or the
			// failed dependency step itself opted into onFailure: Continue.
			depContinues := failurePolicyContinue
			if !depContinues {
				if depStep := findFlowStep(allSteps, dep); depStep != nil {
					depContinues = depStep.OnFailure == automationv1alpha1.OnFailureActionContinue
				}
			}
			if !depContinues {
				return false
			}
		default:
			// Pending, Running, Waiting — not yet terminal
			return false
		}
	}
	return true
}

// findFlowStep returns the FlowStep spec with the given name, or nil if absent.
func findFlowStep(steps []automationv1alpha1.FlowStep, name string) *automationv1alpha1.FlowStep {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

// reconcileGC handles TTL-based deletion and maxFlowRuns enforcement for terminal FlowRuns.
func (r *FlowRunReconciler) reconcileGC(ctx context.Context, flowRun *automationv1alpha1.FlowRun) (time.Duration, error) {
	log := logf.FromContext(ctx)

	// Exempt from GC if retain annotation is set.
	if flowRun.Annotations[retainAnnotation] == annotationValueTrue {
		return 0, nil
	}

	// Enforce per-trigger GC policy if trigger label is present.
	if triggerName, ok := flowRun.Labels[labelTrigger]; ok && triggerName != "" {
		var trigger automationv1alpha1.Trigger
		if err := r.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: flowRun.Namespace}, &trigger); err == nil {
			if trigger.Spec.FlowRunGC != nil {
				if err := r.enforceFlowRunGCPolicy(ctx, triggerName, flowRun.Namespace, *trigger.Spec.FlowRunGC); err != nil {
					return 0, err
				}
			}
		}
		// Ignore NotFound — trigger may have been deleted.
	}

	// Determine TTL. Priority: per-FlowRun spec > per-trigger GC policy > operator flag.
	ttl := r.TTLSucceeded
	if flowRun.Status.Phase == automationv1alpha1.FlowRunPhaseFailed {
		ttl = r.TTLFailed
	}
	// Apply per-trigger TTL override from FlowRunGC policy.
	if triggerName, ok := flowRun.Labels[labelTrigger]; ok && triggerName != "" {
		var trigger automationv1alpha1.Trigger
		if err := r.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: flowRun.Namespace}, &trigger); err == nil {
			if gc := trigger.Spec.FlowRunGC; gc != nil {
				switch flowRun.Status.Phase {
				case automationv1alpha1.FlowRunPhaseSucceeded:
					if gc.TTLAfterSucceeded != nil {
						ttl = gc.TTLAfterSucceeded.Duration
					}
				case automationv1alpha1.FlowRunPhaseFailed:
					if gc.TTLAfterFailed != nil {
						ttl = gc.TTLAfterFailed.Duration
					}
				}
			}
		}
	}
	// Per-FlowRun spec takes highest priority.
	if flowRun.Spec.TTLAfterFinished != nil {
		ttl = flowRun.Spec.TTLAfterFinished.Duration
	}
	if ttl == 0 {
		return 0, nil
	}

	completionTime := flowRun.Status.CompletionTime
	if completionTime == nil {
		return 0, nil
	}

	expiry := completionTime.Add(ttl)
	now := time.Now()
	if now.Before(expiry) {
		return expiry.Sub(now), nil
	}

	log.Info("garbage collecting expired FlowRun",
		"flowRun", flowRun.Name,
		"phase", flowRun.Status.Phase,
		"ttl", ttl)
	if err := r.Delete(ctx, flowRun); err != nil && !apierrors.IsNotFound(err) {
		return 0, err
	}
	return 0, nil
}

// enforceFlowRunGCPolicy applies per-state count caps from a FlowRunGCPolicy.
func (r *FlowRunReconciler) enforceFlowRunGCPolicy(ctx context.Context, triggerName, namespace string, policy automationv1alpha1.FlowRunGCPolicy) error {
	if policy.MaxSucceeded != nil {
		if err := r.enforceMaxFlowRunsByPhase(ctx, triggerName, namespace, automationv1alpha1.FlowRunPhaseSucceeded, *policy.MaxSucceeded); err != nil {
			return err
		}
	}
	if policy.MaxFailed != nil {
		if err := r.enforceMaxFlowRunsByPhase(ctx, triggerName, namespace, automationv1alpha1.FlowRunPhaseFailed, *policy.MaxFailed); err != nil {
			return err
		}
	}
	return nil
}

// enforceMaxFlowRunsByPhase deletes the oldest FlowRuns in the given phase for a trigger
// until the count is within the max cap.
func (r *FlowRunReconciler) enforceMaxFlowRunsByPhase(ctx context.Context, triggerName, namespace string, phase automationv1alpha1.FlowRunPhase, max int32) error {
	log := logf.FromContext(ctx)

	var list automationv1alpha1.FlowRunList
	if err := r.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{
			labelTrigger:       triggerName,
			"kubezap.io/phase": string(phase),
		},
	); err != nil {
		return err
	}

	matching := make([]automationv1alpha1.FlowRun, 0, len(list.Items))
	for _, fr := range list.Items {
		// Safety fallback: filter by status phase in case older FlowRuns predate the label.
		if fr.Status.Phase != phase {
			continue
		}
		if fr.Annotations[retainAnnotation] == annotationValueTrue {
			continue
		}
		matching = append(matching, fr)
	}

	if int32(len(matching)) <= max {
		return nil
	}

	sort.Slice(matching, func(i, j int) bool {
		ti := matching[i].Status.CompletionTime
		tj := matching[j].Status.CompletionTime
		if ti == nil {
			return true
		}
		if tj == nil {
			return false
		}
		return ti.Before(tj)
	})

	toDelete := int(int32(len(matching)) - max)
	for i := 0; i < toDelete; i++ {
		fr := matching[i]
		log.Info("enforcing GC count limit, deleting oldest",
			"flowRun", fr.Name, "trigger", triggerName, "phase", phase, "limit", max)
		if err := r.Delete(ctx, &fr); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
// It also registers the reconciler as a Runnable so that cached Kafka producers
// are closed cleanly when the manager shuts down, and initializes the CEL
// environment eagerly so that any startup failure is surfaced immediately rather
// than silently degrading when condition evaluation is first attempted.
func (r *FlowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.Add(r); err != nil {
		return fmt.Errorf("registering FlowRunReconciler as runnable: %w", err)
	}

	// Register the custom FlowRunActiveCollector so kubezap_flowruns_active is scraped
	// directly from the controller-runtime cache at each Prometheus scrape, avoiding
	// stale values across controller restarts.
	metrics.RegisterFlowRunActiveCollector(mgr.GetClient())
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		// Fallback for callers that don't set MaxConcurrentReconciles explicitly
		// (cmd/main.go always passes --max-concurrent-flowruns, default 25).
		maxConcurrent = 10
	}

	// Initialize CEL environment eagerly — failure here aborts controller startup
	// rather than silently disabling 'when' expression evaluation at runtime.
	var celErr error
	r.celEnv, celErr = cel.NewEnv(
		cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
	)
	if celErr != nil {
		return fmt.Errorf("initializing CEL environment: %w", celErr)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.FlowRun{}).
		Named("flowrun").
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}

// --- helpers ---

// evaluateWhen evaluates all WhenExpression conditions using CEL.
// Returns true if all conditions pass (or the list is empty), false if any fail.
// The CEL environment is initialized eagerly in SetupWithManager and reused here;
// compiled programs are cached per expression string for efficiency across reconcile calls.
func (r *FlowRunReconciler) evaluateWhen(
	when []automationv1alpha1.WhenExpression,
	stepResults map[string]map[string]string,
	stepStatuses []automationv1alpha1.StepRunStatus,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
) (bool, error) {
	if len(when) == 0 {
		return true, nil
	}

	if r.celEnv == nil {
		return false, fmt.Errorf("CEL environment not initialized")
	}
	env := r.celEnv

	// Build trigger activation map.
	triggerMap := map[string]interface{}{
		"body":          "",
		"bodyFields":    map[string]interface{}{},
		"topic":         "",
		"partition":     "0",
		"offset":        "0",
		"key":           "",
		"keyEncoding":   "",
		"scheduledTime": "",
		"headers":       map[string]interface{}{},
	}
	if triggerData != nil {
		triggerMap["body"] = triggerData.Body
		triggerMap["topic"] = triggerData.Topic
		triggerMap["partition"] = fmt.Sprintf("%d", triggerData.Partition)
		triggerMap["offset"] = fmt.Sprintf("%d", triggerData.Offset)
		triggerMap["key"] = triggerData.Key
		triggerMap["keyEncoding"] = triggerData.KeyEncoding
		if triggerData.ScheduledTime != nil {
			triggerMap["scheduledTime"] = triggerData.ScheduledTime.UTC().Format(time.RFC3339)
		}
		// Convert headers to map[string]interface{} for CEL.
		headers := make(map[string]interface{}, len(triggerData.Headers))
		for k, v := range triggerData.Headers {
			headers[k] = v
		}
		triggerMap["headers"] = headers
		// trigger.bodyFields exposes the same parsed body $(trigger.body.<field>)
		// interpolation traverses (JSON or form-urlencoded), as a native CEL value —
		// supports full nested dot-path/index access. Stays an empty map for content
		// types parseTriggerBody doesn't parse (e.g. XML); trigger.body (raw string)
		// is unaffected either way.
		if bodyRoot := parseTriggerBody(triggerData); bodyRoot != nil {
			triggerMap["bodyFields"] = bodyRoot
		}
	}

	// Build steps activation map — hyphens to underscores in step names.
	stepsMap := map[string]interface{}{}
	for name, results := range stepResults {
		underscoreName := strings.ReplaceAll(name, "-", "_")
		resultsIface := make(map[string]interface{}, len(results))
		for k, v := range results {
			resultsIface[k] = v
		}
		stepsMap[underscoreName] = map[string]interface{}{
			"results":       resultsIface,
			resultKeyStatus: "",
		}
	}
	for _, ss := range stepStatuses {
		underscoreName := strings.ReplaceAll(ss.Name, "-", "_")
		if existing, ok := stepsMap[underscoreName]; ok {
			existingMap := existing.(map[string]interface{})
			existingMap[resultKeyStatus] = ss.Phase
		} else {
			stepsMap[underscoreName] = map[string]interface{}{
				"results":       map[string]interface{}{},
				resultKeyStatus: ss.Phase,
			}
		}
	}

	paramsMap := make(map[string]interface{}, len(params))
	for k, v := range params {
		paramsMap[k] = v
	}

	activation := map[string]interface{}{
		"trigger": triggerMap,
		"steps":   stepsMap,
		"params":  paramsMap,
	}

	for _, expr := range when {
		var prog cel.Program
		if !r.DisableCELCache {
			if cached, ok := r.celCache.Load(expr.Expression); ok {
				prog = cached.(cel.Program)
			}
		}
		if prog == nil {
			ast, iss := env.Compile(expr.Expression)
			if iss != nil && iss.Err() != nil {
				return false, fmt.Errorf("CEL compile error: %w", iss.Err())
			}
			var progOpts []cel.ProgramOption
			if r.CELCostLimit > 0 {
				progOpts = append(progOpts, cel.CostLimit(uint64(r.CELCostLimit)))
			}
			var err error
			prog, err = env.Program(ast, progOpts...)
			if err != nil {
				return false, fmt.Errorf("CEL program error: %w", err)
			}
			if !r.DisableCELCache {
				r.celCache.Store(expr.Expression, prog)
			}
		}

		out, _, err := prog.Eval(activation)
		if err != nil {
			return false, fmt.Errorf("CEL eval error: %w", err)
		}
		result, ok := out.Value().(bool)
		if !ok {
			return false, fmt.Errorf("CEL expression did not return bool: %v", out.Value())
		}
		if !result {
			return false, nil
		}
	}
	return true, nil
}

// allDepsSkipped returns true if the step has runAfter dependencies and ALL of them are in Skipped phase.
func allDepsSkipped(step automationv1alpha1.FlowStep, flowRun *automationv1alpha1.FlowRun) bool {
	if len(step.RunAfter) == 0 {
		return false
	}
	for _, dep := range step.RunAfter {
		s := findStepStatus(flowRun.Status.Steps, dep)
		if s == nil || s.Phase != automationv1alpha1.StepPhaseSkipped {
			return false
		}
	}
	return true
}

func findStepStatus(statuses []automationv1alpha1.StepRunStatus, name string) *automationv1alpha1.StepRunStatus {
	for i := range statuses {
		if statuses[i].Name == name {
			return &statuses[i]
		}
	}
	return nil
}

func upsertStepStatus(statuses []automationv1alpha1.StepRunStatus, s automationv1alpha1.StepRunStatus) []automationv1alpha1.StepRunStatus {
	for i := range statuses {
		if statuses[i].Name == s.Name {
			statuses[i] = s
			return statuses
		}
	}
	return append(statuses, s)
}

func resultsToMap(results []automationv1alpha1.ResultValue) map[string]string {
	m := make(map[string]string, len(results))
	for _, r := range results {
		m[r.Name] = r.Value
	}
	return m
}

func mapsToResults(m map[string]string) []automationv1alpha1.ResultValue {
	results := make([]automationv1alpha1.ResultValue, 0, len(m))
	for k, v := range m {
		results = append(results, automationv1alpha1.ResultValue{Name: k, Value: v})
	}
	return results
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractTraceContext reads the kubezap.io/traceparent annotation from a
// FlowRun and, if present, extracts W3C trace context into the returned
// context so that the reconciler span is a child of the originating trace.
func extractTraceContext(ctx context.Context, annotations map[string]string) context.Context {
	if annotations == nil {
		return ctx
	}
	val, ok := annotations["kubezap.io/traceparent"]
	if !ok || val == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": val})
}

// substituteVarsWithSecrets is like substituteVars but additionally resolves
// $(secrets.<name>.<key>) placeholders by fetching Kubernetes Secrets from the
// given namespace.
//
// It returns two strings:
//   - actual: the fully substituted string, including real secret values — safe
//     to use for HTTP calls and other runtime operations.
//   - display: a parallel string where every secret-origin substitution is
//     replaced with the literal text "[REDACTED]" — safe to write to status
//     fields, log messages, and error messages that persist to etcd.
//
// A secret-fetch error aborts the entire substitution — no partial result is
// ever returned. See interpolateTemplate for the shared single-pass resolver.
func (r *FlowRunReconciler) substituteVarsWithSecrets(
	ctx context.Context,
	namespace string,
	s string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
) (actual, display string, err error) {
	resolveSecret := func(name, key string) (string, error) {
		return r.fetchSecretValue(ctx, namespace, corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name},
			Key:                  key,
		})
	}
	return interpolateTemplate(s, stepResults, triggerData, params, resolveSecret)
}

// substituteVars replaces template placeholders in s with values from stepResults,
// triggerData, and params. Supported syntax:
//   - $(steps.<name>.results.<key>) — step output value; hyphens in name are normalized to underscores
//   - $(params.<name>) — a resolved Flow parameter; see docs/design/flow-parameters.md
//   - $(trigger.body) — raw trigger request body
//   - $(trigger.body.<field>) — dot-path into the trigger body (nested objects and array indices
//     supported for JSON bodies; flat top-level fields only for application/x-www-form-urlencoded
//     bodies, keyed by trigger.contentType)
//   - $(trigger.headers.<name>) — trigger request header value (case-insensitive)
//   - $(trigger.topic), $(trigger.partition), $(trigger.offset), $(trigger.scheduledTime)
//   - $(trigger.key) — Kafka record key (kafka triggers only), emitted verbatim from
//     TriggerData.Key; if TriggerData.KeyEncoding is "base64" the caller is responsible
//     for decoding it — this is not done automatically
//
// $(secrets.*) is left as literal text — only substituteVarsWithSecrets resolves it.
func substituteVars(s string, stepResults map[string]map[string]string, triggerData *automationv1alpha1.TriggerData, params map[string]string) string {
	actual, _, _ := interpolateTemplate(s, stepResults, triggerData, params, nil)
	return actual
}

// interpolateTemplate performs a single left-to-right pass over s, resolving each
// $(...) placeholder exactly once from the ORIGINAL template text. A resolved
// value is written straight to the output and the scan position advances past
// the original closing ")" — substituted content is never re-scanned.
//
// This matters for more than correctness. A scan that re-scanned already-
// substituted output would let (a) a trigger body field or header whose value
// echoes its own placeholder hang the reconciler forever (the scan keeps
// "resolving" the same text every iteration), and (b) attacker-controlled data
// (a body field, header, or upstream step/API result) landing next to one
// legitimate $(secrets.*) reference inject its own
// $(secrets.<any-name>.<any-key>) text and have it resolved — reading any
// secret in the namespace. See docs/design/single-pass-interpolation.md.
//
// resolveSecret is nil for callers that must never resolve secrets (e.g. Flow
// parameter defaults via resolveFlowParams) — $(secrets.*) is then left as
// literal text. When non-nil, it is called once per $(secrets.<name>.<key>)
// token; an error aborts the entire call with no partial result. display
// mirrors actual except every secret-resolved value becomes "[REDACTED]".
func interpolateTemplate(
	s string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
	resolveSecret func(name, key string) (string, error),
) (actual, display string, err error) {
	var lowerHeaders map[string]string
	if triggerData != nil {
		lowerHeaders = make(map[string]string, len(triggerData.Headers))
		for k, v := range triggerData.Headers {
			lowerHeaders[strings.ToLower(k)] = v
		}
	}

	// bodyRoot is parsed at most once, lazily, only if a $(trigger.body.<field>)
	// token is actually encountered.
	var bodyRoot interface{}
	bodyParsed := false

	var actualBuf, displayBuf strings.Builder
	i := 0
	for {
		start := strings.Index(s[i:], "$(")
		if start < 0 {
			actualBuf.WriteString(s[i:])
			displayBuf.WriteString(s[i:])
			break
		}
		start += i
		actualBuf.WriteString(s[i:start])
		displayBuf.WriteString(s[i:start])

		closeIdx := strings.Index(s[start:], ")")
		if closeIdx < 0 {
			// No closing paren for the rest of the string — copy verbatim and stop.
			actualBuf.WriteString(s[start:])
			displayBuf.WriteString(s[start:])
			break
		}
		closeIdx += start
		token := s[start+2 : closeIdx]
		full := s[start : closeIdx+1]

		if !bodyParsed && strings.HasPrefix(token, "trigger.body.") {
			bodyRoot = parseTriggerBody(triggerData)
			bodyParsed = true
		}

		value, matched, isSecret, rerr := resolveInterpolationToken(
			token, stepResults, triggerData, params, bodyRoot, lowerHeaders, resolveSecret)
		if rerr != nil {
			return "", "", rerr
		}
		if matched {
			actualBuf.WriteString(value)
			if isSecret {
				displayBuf.WriteString("[REDACTED]")
			} else {
				displayBuf.WriteString(value)
			}
		} else {
			actualBuf.WriteString(full)
			displayBuf.WriteString(full)
		}
		i = closeIdx + 1
	}
	return actualBuf.String(), displayBuf.String(), nil
}

// resolveInterpolationToken resolves the text found between "$(" and ")" — e.g.
// "steps.foo.results.bar" or "trigger.body.order.id". Returns matched=false to
// leave the original "$(...)" text untouched (unknown syntax, or a source that
// intentionally isn't available in this context, e.g. secrets when
// resolveSecret is nil). bodyRoot must already be parsed (or nil) by the caller
// when token starts with "trigger.body.".
func resolveInterpolationToken(
	token string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
	params map[string]string,
	bodyRoot interface{},
	lowerHeaders map[string]string,
	resolveSecret func(name, key string) (string, error),
) (value string, matched, isSecret bool, err error) {
	switch {
	case strings.HasPrefix(token, "steps."):
		// $(steps.<name>.results.<key>) — hyphens in name normalized to underscores.
		rest := strings.TrimPrefix(token, "steps.")
		const marker = ".results."
		idx := strings.Index(rest, marker)
		if idx < 0 {
			return "", false, false, nil
		}
		stepName, key := rest[:idx], rest[idx+len(marker):]
		for name, results := range stepResults {
			if strings.ReplaceAll(name, "-", "_") != stepName {
				continue
			}
			if v, ok := results[key]; ok {
				return v, true, false, nil
			}
			break
		}
		return "", false, false, nil

	case strings.HasPrefix(token, "params."):
		name := strings.TrimPrefix(token, "params.")
		if v, ok := params[name]; ok {
			return v, true, false, nil
		}
		return "", false, false, nil

	case token == "trigger.body":
		if triggerData == nil {
			return "", false, false, nil
		}
		return triggerData.Body, true, false, nil

	case strings.HasPrefix(token, "trigger.body."):
		if triggerData == nil || triggerData.Body == "" || bodyRoot == nil {
			return "", false, false, nil
		}
		fieldPath := strings.TrimPrefix(token, "trigger.body.")
		return resolveBodyPath(strings.Split(fieldPath, "."), bodyRoot), true, false, nil

	case strings.HasPrefix(token, "trigger.headers."):
		if triggerData == nil {
			return "", false, false, nil
		}
		name := strings.TrimPrefix(token, "trigger.headers.")
		return lowerHeaders[strings.ToLower(name)], true, false, nil

	case token == "trigger.topic",
		token == "trigger.partition",
		token == "trigger.offset",
		token == "trigger.key",
		token == "trigger.scheduledTime":
		// Scalar $(trigger.*) tokens that read a single TriggerData field
		// directly — split out into resolveTriggerScalarToken to keep this
		// switch's cyclomatic complexity within lint limits.
		return resolveTriggerScalarToken(token, triggerData)

	case strings.HasPrefix(token, "secrets."):
		if resolveSecret == nil {
			return "", false, false, nil
		}
		inner := strings.TrimPrefix(token, "secrets.")
		dotIdx := strings.Index(inner, ".")
		if dotIdx < 0 {
			// Malformed — no key segment. Leave verbatim.
			return "", false, false, nil
		}
		secretName, secretKey := inner[:dotIdx], inner[dotIdx+1:]
		v, ferr := resolveSecret(secretName, secretKey)
		if ferr != nil {
			return "", false, false, fmt.Errorf("resolving $(%s): %w", token, ferr)
		}
		return v, true, true, nil

	default:
		return "", false, false, nil
	}
}

// resolveTriggerScalarToken resolves the scalar $(trigger.*) tokens that each
// read a single field directly off TriggerData: topic, partition, offset,
// key, and scheduledTime. Extracted out of resolveInterpolationToken purely to
// keep that function's cyclomatic complexity within the golangci-lint gocyclo
// budget — behavior is unchanged from when these were inline cases.
func resolveTriggerScalarToken(token string, triggerData *automationv1alpha1.TriggerData) (value string, matched, isSecret bool, err error) {
	if triggerData == nil {
		return "", false, false, nil
	}
	switch token {
	case "trigger.topic":
		return triggerData.Topic, true, false, nil

	case "trigger.partition":
		return fmt.Sprintf("%d", triggerData.Partition), true, false, nil

	case "trigger.offset":
		return fmt.Sprintf("%d", triggerData.Offset), true, false, nil

	case "trigger.key":
		// Emits TriggerData.Key verbatim — no decoding based on KeyEncoding.
		// Decoding a base64-encoded key (TriggerData.KeyEncoding == "base64")
		// is the caller's responsibility, consistent with how $(trigger.body)
		// is also emitted verbatim regardless of ContentType.
		return triggerData.Key, true, false, nil

	case "trigger.scheduledTime":
		if triggerData.ScheduledTime != nil {
			return triggerData.ScheduledTime.UTC().Format(time.RFC3339), true, false, nil
		}
		return "", true, false, nil

	default:
		return "", false, false, nil
	}
}

// parseTriggerBody parses triggerData.Body into a structured value suitable for
// resolveBodyPath traversal, choosing JSON or form-urlencoded parsing based on
// ContentType. Returns nil if the body is empty or cannot be parsed as either.
// Shared by substituteVars ($(trigger.body.<field>)) and resolveFlowParams
// (auto-deriving a param from a same-named top-level body field).
func parseTriggerBody(triggerData *automationv1alpha1.TriggerData) interface{} {
	if triggerData == nil || triggerData.Body == "" {
		return nil
	}
	if isFormURLEncodedContentType(triggerData.ContentType) {
		if formBody := parseFormURLEncodedBody(triggerData.Body); formBody != nil {
			return formBody
		}
		return nil
	}
	var bodyRoot interface{}
	if err := json.Unmarshal([]byte(triggerData.Body), &bodyRoot); err != nil {
		return nil
	}
	return bodyRoot
}

// isFormURLEncodedContentType reports whether contentType identifies an
// application/x-www-form-urlencoded body, ignoring parameters such as charset.
func isFormURLEncodedContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	}
	return strings.EqualFold(mediaType, "application/x-www-form-urlencoded")
}

// parseFormURLEncodedBody parses an application/x-www-form-urlencoded body (e.g. a Slack
// slash command payload) into a flat map for $(trigger.body.<field>) resolution via
// resolveBodyPath. Repeated keys keep only the first value; form fields in supported
// webhook payloads are not repeated. Returns nil if the body cannot be parsed as a query string.
func parseFormURLEncodedBody(body string) map[string]interface{} {
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil
	}
	result := make(map[string]interface{}, len(values))
	for key, vals := range values {
		if len(vals) > 0 {
			result[key] = vals[0]
		}
	}
	return result
}

// resolveFlowParams resolves $(params.<name>) values for a FlowRun, per the resolution
// order in docs/design/flow-parameters.md, for each param declared by the
// Flow:
//  1. An explicit entry in flowRunParams with a matching name wins — its value is itself
//     resolved through substituteVars, so it may reference $(trigger.body.x)/$(steps.*)/etc.
//  2. Else, a same-named top-level field is auto-derived from the trigger body (JSON
//     objects or form-urlencoded bodies; nested dot-paths are not supported here, since
//     ParamDeclaration.Name is a flat identifier, not a path).
//  3. Else, the declared Default is used as a literal (not interpolated).
//  4. Else, if Required, returns an error naming the unresolved parameter — the caller
//     must fail the FlowRun without dispatching any step when err != nil.
//  5. Else, resolves to "" (matches existing precedent: a missing trigger.body field
//     already silently resolves to "").
func resolveFlowParams(
	paramDecls []automationv1alpha1.ParamDeclaration,
	flowRunParams []automationv1alpha1.ParamValue,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
) (map[string]string, error) {
	if len(paramDecls) == 0 {
		return nil, nil
	}

	supplied := make(map[string]string, len(flowRunParams))
	for _, pv := range flowRunParams {
		supplied[pv.Name] = pv.Value
	}

	bodyRoot := parseTriggerBody(triggerData)
	var bodyFields map[string]interface{}
	if m, ok := bodyRoot.(map[string]interface{}); ok {
		bodyFields = m
	}

	resolved := make(map[string]string, len(paramDecls))
	for _, decl := range paramDecls {
		if raw, ok := supplied[decl.Name]; ok {
			resolved[decl.Name] = substituteVars(raw, stepResults, triggerData, nil)
			continue
		}
		if bodyFields != nil {
			if v, ok := bodyFields[decl.Name]; ok {
				resolved[decl.Name] = resolveBodyPath(nil, v)
				continue
			}
		}
		if decl.Default != "" {
			resolved[decl.Name] = decl.Default
			continue
		}
		if decl.Required {
			return nil, fmt.Errorf("required parameter %q not supplied and has no default", decl.Name)
		}
		resolved[decl.Name] = ""
	}
	return resolved, nil
}

// resolveBodyPath recursively traverses v following the dot-path segments in parts.
// It supports map (object) traversal and slice (array) traversal via numeric indices.
// Returns the string representation of the value at the path, or "" if the path
// does not exist or a segment cannot be traversed.
func resolveBodyPath(parts []string, v interface{}) string {
	if len(parts) == 0 {
		return renderBodyLeaf(v)
	}
	key := parts[0]
	rest := parts[1:]
	switch node := v.(type) {
	case map[string]interface{}:
		child, ok := node[key]
		if !ok {
			return ""
		}
		return resolveBodyPath(rest, child)
	case []interface{}:
		idx, err := strconv.Atoi(key)
		if err != nil || idx < 0 || idx >= len(node) {
			return ""
		}
		return resolveBodyPath(rest, node[idx])
	default:
		return ""
	}
}

// renderBodyLeaf renders a value reached via $(trigger.body.<path>) as it should
// appear when embedded directly in a string template. json.Unmarshal decodes every
// JSON number as float64 and every object/array as a Go map/slice; fmt.Sprintf("%v", ...)
// on those produces scientific notation for large numbers (1234567 -> "1.234567e+06")
// and Go syntax for structures (map[id:1], [1 2]) rather than valid JSON. This renders
// numbers as plain decimal (exact for any integer within float64's ±2^53 range, which
// covers epoch-millis timestamps and ordinary record/order IDs) and structures as JSON.
func renderBodyLeaf(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case map[string]interface{}, []interface{}:
		if b, err := json.Marshal(val); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// extractSimpleJSONPath extracts a value from a JSON object using a simple "$.field" path.
// Only single-level paths are supported (e.g., "$.tier"). Multi-level paths (e.g., "$.order.id")
// silently return empty string. This limitation is documented in the FlowRun API reference.
func extractSimpleJSONPath(path string, obj map[string]interface{}) string {
	field := strings.TrimPrefix(path, "$.")
	val, ok := obj[field]
	if !ok {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", val)
}

// Start implements manager.Runnable. It performs a startup scan to fail any
// Running FlowRuns that exceeded the execution timeout (orphaned during a
// previous controller restart), then blocks until ctx is cancelled and closes
// all cached Kafka producers so that broker connections are released cleanly.
func (r *FlowRunReconciler) Start(ctx context.Context) error {
	// Startup scan: fail any Running FlowRuns that exceeded the execution timeout.
	// These were likely abandoned mid-execution during a previous controller restart.
	if r.ExecutionTimeout > 0 {
		var list automationv1alpha1.FlowRunList
		if err := r.List(ctx, &list); err != nil {
			logf.Log.Error(err, "startup scan: failed to list FlowRuns")
		} else {
			for i := range list.Items {
				fr := &list.Items[i]
				if fr.Status.Phase != automationv1alpha1.FlowRunPhaseRunning || fr.Status.StartTime == nil {
					continue
				}
				if time.Since(fr.Status.StartTime.Time) <= r.ExecutionTimeout {
					continue
				}
				if err := r.failFlowRun(ctx, fr,
					fmt.Sprintf("orphaned at startup: running for %s (limit %s)",
						time.Since(fr.Status.StartTime.Time).Truncate(time.Second),
						r.ExecutionTimeout)); err != nil {
					logf.Log.Error(err, "startup scan: failed to fail orphaned FlowRun",
						"name", fr.Name, "namespace", fr.Namespace)
				}
			}
		}
	}

	<-ctx.Done()
	r.kafkaProducersMu.Lock()
	defer r.kafkaProducersMu.Unlock()
	for addr, p := range r.kafkaProducers {
		if err := p.Close(); err != nil {
			// Log but do not fail — we are already shutting down.
			logf.Log.Error(err, "error closing cached kafka producer", "brokerAddress", addr)
		}
	}
	r.kafkaProducers = nil
	r.kafkaProducerLastUsed = nil
	return nil
}

// publishToKafka sends a message to a Kafka topic using the integration's
// bootstrap servers. Producers are cached by broker address and reused across
// calls to avoid the per-call TCP handshake and metadata fetch that would
// otherwise exhaust broker connections at any meaningful publish rate.
func (r *FlowRunReconciler) publishToKafka(
	ctx context.Context,
	integration *automationv1alpha1.Integration,
	topic string,
	body string,
	headers map[string]string,
) (map[string]string, error) {
	brokerKey := strings.Join(integration.Spec.Kafka.BootstrapServers, ",")

	producer, err := r.getOrCreateKafkaProducer(ctx, brokerKey, integration)
	if err != nil {
		return nil, err
	}

	msg := &sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.StringEncoder(body),
	}
	for k, v := range headers {
		msg.Headers = append(msg.Headers, sarama.RecordHeader{
			Key:   []byte(k),
			Value: []byte(v),
		})
	}

	partition, offset, sendErr := producer.SendMessage(msg)
	if sendErr != nil {
		// Evict the potentially broken producer so the next call creates a fresh one.
		r.kafkaProducersMu.Lock()
		if r.kafkaProducers != nil {
			delete(r.kafkaProducers, brokerKey)
		}
		if r.kafkaProducerLastUsed != nil {
			delete(r.kafkaProducerLastUsed, brokerKey)
		}
		r.kafkaProducersMu.Unlock()
		logf.Log.V(1).Info("kafka producer evicted (send error), will recreate on next use", "brokerKey", brokerKey)
		_ = producer.Close()
		return nil, fmt.Errorf("sending kafka message to topic %q: %w", topic, sendErr)
	}

	// Update last-used timestamp on success to reset the idle TTL window.
	r.kafkaProducersMu.Lock()
	if r.kafkaProducerLastUsed != nil {
		r.kafkaProducerLastUsed[brokerKey] = time.Now()
	}
	r.kafkaProducersMu.Unlock()

	return map[string]string{
		"partition": fmt.Sprintf("%d", partition),
		"offset":    fmt.Sprintf("%d", offset),
	}, nil
}

// getOrCreateKafkaProducer returns a cached producer for brokerKey or creates
// and caches a new one. integration is used only when a new producer is needed.
// Producers that have been idle for longer than kafkaProducerIdleTTL are closed
// and replaced so that stale broker connections do not survive indefinitely.
func (r *FlowRunReconciler) getOrCreateKafkaProducer(
	ctx context.Context,
	brokerKey string,
	integration *automationv1alpha1.Integration,
) (sarama.SyncProducer, error) {
	r.kafkaProducersMu.Lock()
	defer r.kafkaProducersMu.Unlock()

	if r.kafkaProducers == nil {
		r.kafkaProducers = make(map[string]sarama.SyncProducer)
	}
	if r.kafkaProducerLastUsed == nil {
		r.kafkaProducerLastUsed = make(map[string]time.Time)
	}

	if p, ok := r.kafkaProducers[brokerKey]; ok {
		// Evict and replace if the producer has been idle past the TTL.
		if time.Since(r.kafkaProducerLastUsed[brokerKey]) > kafkaProducerIdleTTL {
			logf.Log.V(1).Info("kafka producer evicted (idle TTL), recreating",
				"brokerKey", brokerKey,
				"idleDuration", time.Since(r.kafkaProducerLastUsed[brokerKey]).Truncate(time.Second),
			)
			_ = p.Close()
			delete(r.kafkaProducers, brokerKey)
			delete(r.kafkaProducerLastUsed, brokerKey)
		} else {
			logf.Log.V(1).Info("kafka producer cache hit", "brokerKey", brokerKey)
			return p, nil
		}
	}

	logf.Log.V(1).Info("kafka producer cache miss, creating new producer", "brokerKey", brokerKey)
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Version = sarama.V2_6_0_0

	kafkaSpec := integration.Spec.Kafka

	// Apply TLS if configured.
	if kafkaSpec.TLS != nil && kafkaSpec.TLS.Enabled {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: kafkaSpec.TLS.InsecureSkipVerify, //nolint:gosec // user-configured
		}
		if kafkaSpec.TLS.CASecretRef != nil {
			caPEM, err := r.fetchSecretValue(ctx, integration.Namespace, *kafkaSpec.TLS.CASecretRef)
			if err != nil {
				return nil, fmt.Errorf("reading CA cert secret for integration %q: %w", integration.Name, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(caPEM)) {
				return nil, fmt.Errorf("failed to parse CA certificate from secret %s key %s",
					kafkaSpec.TLS.CASecretRef.Name, kafkaSpec.TLS.CASecretRef.Key)
			}
			tlsCfg.RootCAs = pool
		}
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = tlsCfg
	}

	// Apply SASL if configured.
	if kafkaSpec.SASL != nil {
		saslCfg := kafkaSpec.SASL

		username, err := r.fetchSecretValue(ctx, integration.Namespace, saslCfg.UsernameSecretRef)
		if err != nil {
			return nil, fmt.Errorf("reading SASL username secret for integration %q: %w", integration.Name, err)
		}
		password, err := r.fetchSecretValue(ctx, integration.Namespace, saslCfg.PasswordSecretRef)
		if err != nil {
			return nil, fmt.Errorf("reading SASL password secret for integration %q: %w", integration.Name, err)
		}

		config.Net.SASL.Enable = true
		config.Net.SASL.User = username
		config.Net.SASL.Password = password

		switch saslCfg.Mechanism {
		case "PLAIN":
			config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "SCRAM-SHA-256":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &producerSCRAMClient{HashGeneratorFcn: sha256.New}
			}
		case "SCRAM-SHA-512":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &producerSCRAMClient{HashGeneratorFcn: sha512.New}
			}
		default:
			return nil, fmt.Errorf("unsupported SASL mechanism %q for integration %q", saslCfg.Mechanism, integration.Name)
		}
	}

	p, err := sarama.NewSyncProducer(kafkaSpec.BootstrapServers, config)
	if err != nil {
		return nil, fmt.Errorf("creating kafka producer for integration %q: %w", integration.Name, err)
	}
	r.kafkaProducers[brokerKey] = p
	// Record creation time as the initial last-used timestamp.
	r.kafkaProducerLastUsed[brokerKey] = time.Now()
	logf.Log.V(1).Info("kafka producer created and cached", "brokerKey", brokerKey)
	return p, nil
}

// producerSCRAMClient implements sarama.SCRAMClient for SCRAM-SHA-256 and
// SCRAM-SHA-512 authentication in the Kafka publish producer. It mirrors the
// xdgSCRAMClient in internal/gateway/kafka/watcher.go — both are intentionally
// kept as package-local copies so neither package imports the other.
type producerSCRAMClient struct {
	HashGeneratorFcn func() hash.Hash
	user             string
	pass             string
	clientNonce      string
	saltedPass       []byte
	authMessage      string
	step             int
}

func (x *producerSCRAMClient) Begin(userName, password, _ string) error {
	x.user = userName
	x.pass = password
	x.step = 0

	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate client nonce: %w", err)
	}
	x.clientNonce = base64.StdEncoding.EncodeToString(nonceBytes)
	return nil
}

// Step implements the RFC 5802 SCRAM exchange.
// Step 1 (challenge ""): produces client-first message.
// Step 2 (server-first): produces client-final message.
// Step 3 (server-final): verifies server signature.
func (x *producerSCRAMClient) Step(challenge string) (string, error) {
	x.step++
	switch x.step {
	case 1:
		return "n,,n=" + x.user + ",r=" + x.clientNonce, nil

	case 2:
		// Parse server-first: "r=<combinedNonce>,s=<salt-b64>,i=<iterations>"
		attrs := producerSCRAMParseAttrs(challenge)

		combinedNonce, ok := attrs["r"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'r' in server-first message")
		}
		if !strings.HasPrefix(combinedNonce, x.clientNonce) {
			return "", fmt.Errorf("SCRAM: server nonce does not begin with client nonce")
		}

		saltB64, ok := attrs["s"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 's' in server-first message")
		}
		salt, err := base64.StdEncoding.DecodeString(saltB64)
		if err != nil {
			return "", fmt.Errorf("SCRAM: decode salt: %w", err)
		}

		iterStr, ok := attrs["i"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'i' in server-first message")
		}
		iterations, err := producerSCRAMParseIter(iterStr)
		if err != nil {
			return "", fmt.Errorf("SCRAM: %w", err)
		}

		x.saltedPass = x.scramHi([]byte(x.pass), salt, iterations)

		clientKey := x.scramHMAC(x.saltedPass, []byte("Client Key"))
		storedKey := x.scramH(clientKey)

		clientFirstBare := "n=" + x.user + ",r=" + x.clientNonce
		cbind := base64.StdEncoding.EncodeToString([]byte("n,,"))
		clientFinalNP := "c=" + cbind + ",r=" + combinedNonce

		x.authMessage = clientFirstBare + "," + challenge + "," + clientFinalNP

		clientSig := x.scramHMAC(storedKey, []byte(x.authMessage))
		proof := producerSCRAMXorBytes(clientKey, clientSig)

		return clientFinalNP + ",p=" + base64.StdEncoding.EncodeToString(proof), nil

	case 3:
		// Verify server-final: "v=<server-sig-b64>"
		serverKey := x.scramHMAC(x.saltedPass, []byte("Server Key"))
		expected := x.scramHMAC(serverKey, []byte(x.authMessage))

		attrs := producerSCRAMParseAttrs(challenge)
		sigB64, ok := attrs["v"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'v' in server-final message")
		}
		received, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			return "", fmt.Errorf("SCRAM: decode server signature: %w", err)
		}
		if !bytes.Equal(expected, received) {
			return "", fmt.Errorf("SCRAM: server signature verification failed")
		}
		return "", nil

	default:
		return "", fmt.Errorf("SCRAM: unexpected step %d", x.step)
	}
}

func (x *producerSCRAMClient) Done() bool {
	return x.step >= 3
}

func (x *producerSCRAMClient) scramHi(password, salt []byte, iterations int) []byte {
	mac := hmac.New(x.HashGeneratorFcn, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	result := make([]byte, len(u))
	copy(result, u)
	for i := 2; i <= iterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}

func (x *producerSCRAMClient) scramHMAC(key, msg []byte) []byte {
	mac := hmac.New(x.HashGeneratorFcn, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

func (x *producerSCRAMClient) scramH(data []byte) []byte {
	hh := x.HashGeneratorFcn()
	hh.Write(data)
	return hh.Sum(nil)
}

func producerSCRAMParseAttrs(msg string) map[string]string {
	attrs := make(map[string]string)
	for _, part := range strings.Split(msg, ",") {
		if len(part) < 2 || part[1] != '=' {
			continue
		}
		attrs[part[:1]] = part[2:]
	}
	return attrs
}

func producerSCRAMParseIter(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid iteration count: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("iteration count must be positive, got %q", s)
	}
	return n, nil
}

func producerSCRAMXorBytes(a, b []byte) []byte {
	result := make([]byte, len(a))
	for i := range a {
		result[i] = a[i] ^ b[i]
	}
	return result
}
