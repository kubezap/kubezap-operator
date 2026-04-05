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
	"net"
	"net/http"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
	"github.com/kubezap/kubezap-operator/internal/metrics"
)

const retainAnnotation = "kubezap.io/retain"
const executingFinalizer = "kubezap.io/executing"

// kafkaProducerIdleTTL is the maximum idle time before a cached Kafka producer
// is closed and recreated on next use.
const kafkaProducerIdleTTL = 10 * time.Minute

// executorTransportBackoff is the requeue delay used when the http-executor pod
// is unreachable (connection refused, DNS failure, TLS handshake error, context
// deadline caused by pod unavailability). A short fixed backoff is used rather
// than full exponential jitter because the executor pod is either up or not —
// there is no per-request variance to smooth out.
const executorTransportBackoff = 5 * time.Second

// executorTransportBackoffCap is the maximum requeue delay for consecutive
// transport failures within a single FlowRun execution. Not currently enforced
// here (each reconcile uses a fresh backoff), but documented for future use.
const executorTransportBackoffCap = 2 * time.Minute

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
	// .svc.cluster.local endpoints and RFC1918 CIDRs. For dev/test environments
	// only. Controlled by --ssrf-allow-in-cluster on the controller binary.
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

	// FlowRun was deleted while running — fail it and remove executing finalizer.
	if !flowRun.DeletionTimestamp.IsZero() && flowRun.Status.Phase == "Running" {
		if err := r.failFlowRun(ctx, &flowRun, "FlowRun deleted while running"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// GC: handle terminal FlowRuns (TTL expiry + maxFlowRuns cap).
	if flowRun.Status.Phase == "Succeeded" || flowRun.Status.Phase == "Failed" || flowRun.Status.Phase == "Cancelled" {
		if requeue, err := r.reconcileGC(ctx, &flowRun); err != nil {
			return ctrl.Result{}, err
		} else if requeue > 0 {
			return ctrl.Result{RequeueAfter: requeue}, nil
		}
		return ctrl.Result{}, nil
	}

	// Skip already-terminal FlowRuns.
	switch flowRun.Status.Phase {
	case "Succeeded", "Failed", "Cancelled":
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
	if flowRun.Status.Phase == "" || flowRun.Status.Phase == "Pending" {
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
		flowRun.Status.Phase = "Running"
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

	// §12b: One-step-per-reconcile execution model.
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
		if (ss.Phase == "Succeeded" || ss.Phase == "Skipped") && ss.Results != nil {
			stepResults[ss.Name] = resultsToMap(ss.Results)
		}
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
			case "Succeeded", "Skipped", "Failed":
				// terminal — ok
			case "Running", "Waiting":
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
		failurePolicyContinue := flow.Spec.FailurePolicy == "Continue"
		skippedAny := false
		for _, step := range flow.Spec.Steps {
			if !r.dependenciesMet(step, flowRun.Status.Steps, failurePolicyContinue) {
				continue
			}
			existing := findStepStatus(flowRun.Status.Steps, step.Name)
			if existing != nil && existing.Phase != "" && existing.Phase != "Pending" {
				// Already processed.
				continue
			}

			// Cascade-skip: all runAfter deps were Skipped.
			if allDepsSkipped(step, &flowRun) {
				now := metav1.Now()
				flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
					Name:           step.Name,
					Phase:          "Skipped",
					Message:        "all runAfter dependencies were skipped",
					CompletionTime: &now,
				})
				skippedAny = true
				continue
			}

			// Evaluate when conditions (pure CEL — no IO).
			if len(step.When) > 0 {
				run, err := r.evaluateWhen(step.When, stepResults, flowRun.Status.Steps, flowRun.Spec.TriggerData)
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
						Phase:          "Failed",
						Message:        failMsg,
						CompletionTime: &now,
					})
					if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
						return ctrl.Result{}, err2
					}
					if step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue" {
						skippedAny = true
						continue
					}
					return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fmt.Sprintf("step %q when expression error: %v", step.Name, err))
				}
				if !run {
					now := metav1.Now()
					flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
						Name:           step.Name,
						Phase:          "Skipped",
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
			if !r.dependenciesMet(step, flowRun.Status.Steps, failurePolicyContinue) {
				continue
			}
			existing := findStepStatus(flowRun.Status.Steps, step.Name)
			if existing != nil && existing.Phase != "" && existing.Phase != "Pending" {
				// Re-admit wait steps that are in Waiting phase — they need to be
				// rechecked to see if the wait duration has elapsed.
				if existing.Phase == "Waiting" && step.Action.Type == "wait" {
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
				if step.Action.Type == "wait" {
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
						ss.Phase = "Failed"
						ss.Message = err.Error()
						ss.CompletionTime = &completionTime
						flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, ss)
						if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
							return ctrl.Result{}, err2
						}
						if step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue" {
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
					ss.Phase = "Succeeded"
					ss.CompletionTime = &completionTime
					flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, ss)
					if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
						return ctrl.Result{}, err2
					}
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
					ss, err := r.executeStep(execCtx, log, &step, &flow, &flowRun, stepResults, flowRun.Spec.TriggerData)
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
								status:       automationv1alpha1.StepRunStatus{Name: step.Name, Phase: "Running"},
								duration:     dur,
								requeueAfter: executorTransportBackoff,
							}
							return
						}
						// Unexpected non-transport error from executeStep (should not
						// occur under current implementation, but handled defensively).
						results[i] = stepResult{
							name: step.Name, stepType: step.Action.Type,
							status:   automationv1alpha1.StepRunStatus{Name: step.Name, Phase: "Failed", Message: err.Error()},
							duration: dur,
						}
						return
					}
					fr := stepResult{
						name: step.Name, stepType: step.Action.Type,
						status:   *ss,
						duration: dur,
					}
					if ss.Phase == "Failed" {
						if step.OnFailure != "Continue" && flow.Spec.FailurePolicy != "Continue" {
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
					res.stepType, res.status.Phase,
				).Observe(res.duration.Seconds())
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
			return ctrl.Result{Requeue: true}, nil
		} // end else (waveSteps non-empty)
	}

	// All steps done. With failurePolicy:Continue the flow runs to completion even
	// after step failures, but the FlowRun is Failed if any step without
	// onFailure:Continue ended in Failed state.
	for _, ss := range flowRun.Status.Steps {
		if ss.Phase != "Failed" {
			continue
		}
		for _, step := range flow.Spec.Steps {
			if step.Name == ss.Name && step.OnFailure != "Continue" && flow.Spec.FailurePolicy != "Continue" {
				msg := fmt.Sprintf("step %q failed: %s", ss.Name, ss.Message)
				return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, msg)
			}
		}
	}

	now := metav1.Now()
	flowRun.Status.Phase = "Succeeded"
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
	flow *automationv1alpha1.Flow,
	flowRun *automationv1alpha1.FlowRun,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
) (*automationv1alpha1.StepRunStatus, error) {
	_ = flow // reserved for future param resolution

	// Start a child span for this step execution.
	ctx, stepSpan := otel.Tracer("kubezap.io/flowrun").Start(ctx, "flowrun.step",
		trace.WithAttributes(
			attribute.String("step.name", step.Name),
			attribute.String("step.type", string(step.Action.Type)),
		))
	defer stepSpan.End()

	now := metav1.Now()
	status := &automationv1alpha1.StepRunStatus{
		Name:      step.Name,
		Phase:     "Running",
		StartTime: &now,
		Attempts:  1,
	}

	switch step.Action.Type {
	case "http":
		results, msg, attempts, err := r.executeHTTPStep(ctx, log, step, stepResults, triggerData, flowRun.Namespace)
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
			status.Phase = "Failed"
			status.Message = err.Error()
		} else {
			status.Phase = "Succeeded"
			status.Message = msg
			status.Results = mapsToResults(results)
		}
	case "transform":
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if step.Action.Transform == nil {
			status.Phase = "Failed"
			status.Message = fmt.Sprintf("step %q has type=transform but no transform spec", step.Name)
		} else {
			substituted := make(map[string]string, len(step.Action.Transform.Mappings))
			for k, v := range step.Action.Transform.Mappings {
				substituted[k] = substituteVars(v, stepResults, triggerData)
			}
			status.Phase = "Succeeded"
			status.Results = mapsToResults(substituted)
		}
	case "publish":
		result, attempts, err := r.executePublishStep(ctx, flowRun, step, triggerData, stepResults)
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if attempts > 0 {
			status.Attempts = int32(attempts)
		}
		if err != nil {
			status.Phase = "Failed"
			status.Message = err.Error()
		} else {
			status.Phase = "Succeeded"
			status.Results = mapsToResults(result)
		}
	default:
		completionTime := metav1.Now()
		status.Phase = "Failed"
		status.CompletionTime = &completionTime
		status.Message = fmt.Sprintf("unknown step action type: %q", step.Action.Type)
	}

	return status, nil
}

func (r *FlowRunReconciler) executeHTTPStep(
	ctx context.Context,
	log logr.Logger,
	step *automationv1alpha1.FlowStep,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
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
	// url is the fully-resolved URL (with secret values substituted).
	// displayURL has secret placeholders intact for safe inclusion in log/error messages.
	url, displayURL, err := r.substituteVarsWithSecrets(ctx, namespace, h.URL, stepResults, triggerData)
	if err != nil {
		return nil, "", 0, fmt.Errorf("resolving secrets in URL for step %q: %w", step.Name, err)
	}
	body, _, err := r.substituteVarsWithSecrets(ctx, namespace, h.Body, stepResults, triggerData)
	if err != nil {
		return nil, "", 0, fmt.Errorf("resolving secrets in body for step %q: %w", step.Name, err)
	}
	headers := make(map[string]string, len(h.Headers))
	for k, v := range h.Headers {
		actual, _, herr := r.substituteVarsWithSecrets(ctx, namespace, v, stepResults, triggerData)
		if herr != nil {
			return nil, "", 0, fmt.Errorf("resolving secrets in header %q for step %q: %w", k, step.Name, herr)
		}
		headers[k] = actual
	}

	// If an HTTP Integration is referenced, merge its base URL, auth headers, and default headers.
	if h.IntegrationRef != nil && h.IntegrationRef.Name != "" {
		var err error
		url, err = r.applyHTTPIntegration(ctx, h.IntegrationRef.Name, namespace, url, headers, stepResults, triggerData)
		if err != nil {
			return nil, "", 0, err
		}
	}

	// Defence-in-depth SSRF pre-check: reject blocked targets before forwarding
	// to the executor. The executor re-validates independently to guard against
	// DNS rebinding between this check and the outbound connection.
	if err := checkSSRF(ctx, url, r.SSRFBlockedCIDRs, r.SSRFAllowClusterInternal); err != nil {
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
		URL:            url,
		Headers:        headers,
		Body:           body,
		TimeoutSeconds: int(timeoutSec),
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
			if url != displayURL {
				errMsg = strings.ReplaceAll(errMsg, url, displayURL)
			}
			lastErr = fmt.Errorf("%s", errMsg)
			log.Info("HTTP step executor error", "step", step.Name, "error", errMsg, "attempt", attempt+1)
			continue
		}

		if execResp.StatusCode >= 200 && execResp.StatusCode < 300 {
			results := map[string]string{
				"body":   execResp.Body,
				"status": fmt.Sprintf("%d", execResp.StatusCode),
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
		return "https"
	}
	return "http"
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
) (map[string]string, int, error) {
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
		body, _, berr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, step.Action.Publish.Body, stepResults, triggerData)
		if berr != nil {
			return nil, 0, fmt.Errorf("resolving secrets in publish body for step %q: %w", step.Name, berr)
		}
		headers := make(map[string]string, len(step.Action.Publish.Headers))
		for k, v := range step.Action.Publish.Headers {
			actual, _, herr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, v, stepResults, triggerData)
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
			result, lastErr = r.publishToKafka(ctx, integration, step.Action.Publish.Topic, body, headers)
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

	body, _, berr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, step.Action.Publish.Body, stepResults, triggerData)
	if berr != nil {
		return nil, 0, fmt.Errorf("resolving secrets in publish body for step %q: %w", step.Name, berr)
	}
	headers := make(map[string]string, len(step.Action.Publish.Headers))
	for k, v := range step.Action.Publish.Headers {
		actual, _, herr := r.substituteVarsWithSecrets(ctx, flowRun.Namespace, v, stepResults, triggerData)
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

		result, attemptErr := r.doPluginPublish(ctx, pluginURL, body, headers, publishTimeout)
		if attemptErr == nil {
			return result, attempt + 1, nil
		}
		lastErr = attemptErr
	}
	return nil, maxAttempts, lastErr
}

// doPluginPublish performs a single HTTP POST to a plugin publisher endpoint.
// It creates a per-call context with the given timeout so cancellation is scoped
// to this attempt rather than leaking across retry iterations.
func (r *FlowRunReconciler) doPluginPublish(
	ctx context.Context,
	url, body string,
	headers map[string]string,
	timeout time.Duration,
) (map[string]string, error) {
	publishCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		var cancel context.CancelFunc
		publishCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(publishCtx, http.MethodPost, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building publish request: %w", err)
	}

	if _, ok := headers["Content-Type"]; !ok {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("publish endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
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

// applyHTTPIntegration fetches the named HTTP Integration and merges its base URL,
// default headers, and auth into the provided url and headers. Step-level headers
// take precedence over integration defaults.
func (r *FlowRunReconciler) applyHTTPIntegration(
	ctx context.Context,
	integrationName string,
	namespace string,
	url string,
	headers map[string]string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
) (string, error) {
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
			return "", fmt.Errorf("fetching http integration %q: %w", integrationName, err)
		}
		integration = &fetched
		if cache, _ := ctx.Value(integrationCacheKey).(map[string]*automationv1alpha1.Integration); cache != nil {
			cache[cacheKey] = integration
		}
	}
	if integration.Spec.HTTP == nil {
		return url, nil
	}
	httpInteg := integration.Spec.HTTP

	// Apply defaultHeaders first (step headers override).
	for k, v := range httpInteg.DefaultHeaders {
		if _, exists := headers[k]; !exists {
			headers[k] = substituteVars(v, stepResults, triggerData)
		}
	}

	// Apply baseUrl: prepend if step URL is a path (not already absolute).
	if httpInteg.BaseURL != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = strings.TrimRight(httpInteg.BaseURL, "/") + "/" + strings.TrimLeft(url, "/")
	}

	// Apply auth.
	if httpInteg.Auth != nil {
		var err error
		url, err = r.applyHTTPAuth(ctx, httpInteg.Auth, integrationName, namespace, url, headers)
		if err != nil {
			return "", err
		}
	}

	return url, nil
}

// applyHTTPAuth resolves the auth configuration from an HTTP Integration and
// sets the appropriate headers or replaces the URL.
func (r *FlowRunReconciler) applyHTTPAuth(
	ctx context.Context,
	auth *automationv1alpha1.HttpAuthSpec,
	integrationName string,
	namespace string,
	url string,
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
			url = secretURL
		}
	}
	return url, nil
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
			stepStatus.Phase = "Waiting"
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
	stepStatus.Phase = "Waiting"
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

func (r *FlowRunReconciler) failFlowRun(ctx context.Context, flowRun *automationv1alpha1.FlowRun, msg string) error {
	now := metav1.Now()
	if flowRun.Labels == nil {
		flowRun.Labels = make(map[string]string)
	}
	flowRun.Labels["kubezap.io/phase"] = "Failed"
	flowRun.Status.Phase = "Failed"
	flowRun.Status.CompletionTime = &now
	flowRun.Status.Message = msg
	setFlowRunCondition(flowRun, metav1.Condition{
		Type:               "Failed",
		Status:             metav1.ConditionTrue,
		Reason:             "FlowRunFailed",
		Message:            msg,
		LastTransitionTime: now,
	})
	if flowRun.Status.StartTime != nil {
		duration := time.Since(flowRun.Status.StartTime.Time)
		metrics.FlowRunDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name, "Failed",
		).Observe(duration.Seconds())
	}
	trace.SpanFromContext(ctx).SetStatus(otelcodes.Error, "FlowRun failed")
	// Status update first — r.Update() would overwrite the local object with the
	// server's still-Running status before Status().Update gets to persist "Failed".
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
// When failurePolicy is "Continue" at the flow level, a Failed dep is treated
// as satisfied — downstream steps must still run so the flow can complete.
// Without failurePolicy:Continue, a Failed dep blocks the step permanently
// (the step will be cascade-skipped or left pending until the flow terminates).
func (r *FlowRunReconciler) dependenciesMet(step automationv1alpha1.FlowStep, statuses []automationv1alpha1.StepRunStatus, failurePolicyContinue bool) bool {
	for _, dep := range step.RunAfter {
		s := findStepStatus(statuses, dep)
		if s == nil {
			return false
		}
		switch s.Phase {
		case "Succeeded", "Skipped":
			// always satisfied
		case "Failed":
			// satisfied only when the flow is configured to continue past failures
			if !failurePolicyContinue {
				return false
			}
		default:
			// Pending, Running, Waiting — not yet terminal
			return false
		}
	}
	return true
}

// reconcileGC handles TTL-based deletion and maxFlowRuns enforcement for terminal FlowRuns.
func (r *FlowRunReconciler) reconcileGC(ctx context.Context, flowRun *automationv1alpha1.FlowRun) (time.Duration, error) {
	log := logf.FromContext(ctx)

	// Exempt from GC if retain annotation is set.
	if flowRun.Annotations[retainAnnotation] == "true" {
		return 0, nil
	}

	// Enforce per-trigger GC policy if trigger label is present.
	if triggerName, ok := flowRun.Labels["kubezap.io/trigger"]; ok && triggerName != "" {
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
	if flowRun.Status.Phase == "Failed" {
		ttl = r.TTLFailed
	}
	// Apply per-trigger TTL override from FlowRunGC policy.
	if triggerName, ok := flowRun.Labels["kubezap.io/trigger"]; ok && triggerName != "" {
		var trigger automationv1alpha1.Trigger
		if err := r.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: flowRun.Namespace}, &trigger); err == nil {
			if gc := trigger.Spec.FlowRunGC; gc != nil {
				switch flowRun.Status.Phase {
				case "Succeeded":
					if gc.TTLAfterSucceeded != nil {
						ttl = gc.TTLAfterSucceeded.Duration
					}
				case "Failed":
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
		if err := r.enforceMaxFlowRunsByPhase(ctx, triggerName, namespace, "Succeeded", *policy.MaxSucceeded); err != nil {
			return err
		}
	}
	if policy.MaxFailed != nil {
		if err := r.enforceMaxFlowRunsByPhase(ctx, triggerName, namespace, "Failed", *policy.MaxFailed); err != nil {
			return err
		}
	}
	return nil
}

// enforceMaxFlowRunsByPhase deletes the oldest FlowRuns in the given phase for a trigger
// until the count is within the max cap.
func (r *FlowRunReconciler) enforceMaxFlowRunsByPhase(ctx context.Context, triggerName, namespace, phase string, max int32) error {
	log := logf.FromContext(ctx)

	var list automationv1alpha1.FlowRunList
	if err := r.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{
			"kubezap.io/trigger": triggerName,
			"kubezap.io/phase":   phase,
		},
	); err != nil {
		return err
	}

	var matching []automationv1alpha1.FlowRun
	for _, fr := range list.Items {
		// Safety fallback: filter by status phase in case older FlowRuns predate the label.
		if fr.Status.Phase != phase {
			continue
		}
		if fr.Annotations[retainAnnotation] == "true" {
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
		// WIRING NOTE: §12b — update --max-concurrent-flowruns default to 25 in cmd/main.go.
		// With one-step-per-reconcile, each goroutine is short-lived (one step, not the full
		// flow), so more concurrent reconciles are safe. The hot-file wiring pass should
		// raise the default from 10 to 25.
		maxConcurrent = 10
	}

	// Initialize CEL environment eagerly — failure here aborts controller startup
	// rather than silently disabling 'when' expression evaluation at runtime.
	var celErr error
	r.celEnv, celErr = cel.NewEnv(
		cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
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
		"topic":         "",
		"partition":     "0",
		"offset":        "0",
		"scheduledTime": "",
		"headers":       map[string]interface{}{},
	}
	if triggerData != nil {
		triggerMap["body"] = triggerData.Body
		triggerMap["topic"] = triggerData.Topic
		triggerMap["partition"] = fmt.Sprintf("%d", triggerData.Partition)
		triggerMap["offset"] = fmt.Sprintf("%d", triggerData.Offset)
		if triggerData.ScheduledTime != nil {
			triggerMap["scheduledTime"] = triggerData.ScheduledTime.UTC().Format(time.RFC3339)
		}
		// Convert headers to map[string]interface{} for CEL.
		headers := make(map[string]interface{}, len(triggerData.Headers))
		for k, v := range triggerData.Headers {
			headers[k] = v
		}
		triggerMap["headers"] = headers
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
			"results": resultsIface,
			"status":  "",
		}
	}
	for _, ss := range stepStatuses {
		underscoreName := strings.ReplaceAll(ss.Name, "-", "_")
		if existing, ok := stepsMap[underscoreName]; ok {
			existingMap := existing.(map[string]interface{})
			existingMap["status"] = ss.Phase
		} else {
			stepsMap[underscoreName] = map[string]interface{}{
				"results": map[string]interface{}{},
				"status":  ss.Phase,
			}
		}
	}

	activation := map[string]interface{}{
		"trigger": triggerMap,
		"steps":   stepsMap,
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
		if s == nil || s.Phase != "Skipped" {
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
// The function is idempotent: if the template contains no $(secrets.*) placeholders
// it returns (substituteVars(s,...), substituteVars(s,...), nil) with no API calls.
func (r *FlowRunReconciler) substituteVarsWithSecrets(
	ctx context.Context,
	namespace string,
	s string,
	stepResults map[string]map[string]string,
	triggerData *automationv1alpha1.TriggerData,
) (actual, display string, err error) {
	const secretPrefix = "$(secrets."

	// Fast path: no secret placeholders — skip API calls entirely.
	if !strings.Contains(s, secretPrefix) {
		resolved := substituteVars(s, stepResults, triggerData)
		return resolved, resolved, nil
	}

	// First, resolve non-secret placeholders in a copy of the template so that
	// subsequent secret lookups operate on the partially-substituted string.
	// We keep the secret placeholders intact at this stage.
	partial := substituteVars(s, stepResults, triggerData)

	// Now resolve all $(secrets.<name>.<key>) placeholders, building both
	// the actual string and the display (redacted) string in parallel.
	actualStr := partial
	displayStr := partial

	for {
		idx := strings.Index(actualStr, secretPrefix)
		if idx < 0 {
			break
		}
		end := strings.Index(actualStr[idx:], ")")
		if end < 0 {
			break
		}
		end += idx

		placeholder := actualStr[idx : end+1]
		// Extract "name.key" from "$(secrets.name.key)".
		inner := actualStr[idx+len(secretPrefix) : end]
		dotIdx := strings.Index(inner, ".")
		if dotIdx < 0 {
			// Malformed placeholder — leave verbatim by advancing past it.
			// Prevent infinite loop: trim the placeholder from further scanning.
			actualStr = strings.Replace(actualStr, placeholder, placeholder, 1)
			// Mark the display string the same way.
			displayStr = strings.Replace(displayStr, placeholder, placeholder, 1)
			// Remove from further scanning by replacing with a temporary sentinel
			// that does not start with secretPrefix. We use the placeholder itself
			// without the "$" prefix so it won't match again.
			break
		}
		secretName := inner[:dotIdx]
		secretKey := inner[dotIdx+1:]

		value, fetchErr := r.fetchSecretValue(ctx, namespace, corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			Key:                  secretKey,
		})
		if fetchErr != nil {
			return "", "", fmt.Errorf("resolving %s: %w", placeholder, fetchErr)
		}

		// Replace all occurrences of this placeholder in both strings.
		actualStr = strings.ReplaceAll(actualStr, placeholder, value)
		displayStr = strings.ReplaceAll(displayStr, placeholder, "[REDACTED]")
	}

	return actualStr, displayStr, nil
}

// substituteVars replaces template placeholders in s with values from stepResults and triggerData.
// Supported syntax:
//   - $(steps.<name>.results.<key>) — step output value; hyphens in name are normalized to underscores
//   - $(trigger.body) — raw trigger request body
//   - $(trigger.body.<field>) — dot-path into trigger body JSON (nested objects and array indices supported; resolved before $(trigger.body))
//   - $(trigger.headers.<name>) — trigger request header value (case-insensitive)
//   - $(trigger.topic), $(trigger.partition), $(trigger.offset), $(trigger.scheduledTime)
func substituteVars(s string, stepResults map[string]map[string]string, triggerData *automationv1alpha1.TriggerData) string {
	// Substitute step results using underscore-normalized names (hyphens → underscores).
	for stepName, results := range stepResults {
		underscoreName := strings.ReplaceAll(stepName, "-", "_")
		for key, value := range results {
			placeholder := fmt.Sprintf("$(steps.%s.results.%s)", underscoreName, key)
			s = strings.ReplaceAll(s, placeholder, value)
		}
	}

	if triggerData == nil {
		return s
	}

	// Handle $(trigger.body.<field>) BEFORE $(trigger.body) to avoid partial replacement.
	// Supports dot-path traversal into nested objects and arrays, e.g. $(trigger.body.order.id)
	// or $(trigger.body.items.0). Missing paths silently resolve to empty string.
	if triggerData.Body != "" {
		var bodyRoot interface{}
		if jsonErr := json.Unmarshal([]byte(triggerData.Body), &bodyRoot); jsonErr == nil {
			const bodyFieldPrefix = "$(trigger.body."
			for {
				idx := strings.Index(s, bodyFieldPrefix)
				if idx < 0 {
					break
				}
				end := strings.Index(s[idx:], ")")
				if end < 0 {
					break
				}
				end += idx
				placeholder := s[idx : end+1]
				fieldName := s[idx+len(bodyFieldPrefix) : end]
				parts := strings.Split(fieldName, ".")
				value := resolveBodyPath(parts, bodyRoot)
				s = strings.ReplaceAll(s, placeholder, value)
			}
		}
	}

	s = strings.ReplaceAll(s, "$(trigger.body)", triggerData.Body)
	s = strings.ReplaceAll(s, "$(trigger.topic)", triggerData.Topic)
	s = strings.ReplaceAll(s, "$(trigger.partition)", fmt.Sprintf("%d", triggerData.Partition))
	s = strings.ReplaceAll(s, "$(trigger.offset)", fmt.Sprintf("%d", triggerData.Offset))
	if triggerData.ScheduledTime != nil {
		s = strings.ReplaceAll(s, "$(trigger.scheduledTime)", triggerData.ScheduledTime.UTC().Format(time.RFC3339))
	} else {
		s = strings.ReplaceAll(s, "$(trigger.scheduledTime)", "")
	}
	// Substitute trigger headers: $(trigger.headers.<name>) — case-insensitive lookup.
	lowerHeaders := make(map[string]string, len(triggerData.Headers))
	for k, v := range triggerData.Headers {
		lowerHeaders[strings.ToLower(k)] = v
	}
	const headerPrefix = "$(trigger.headers."
	for {
		idx := strings.Index(s, headerPrefix)
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], ")")
		if end < 0 {
			break
		}
		end += idx
		placeholder := s[idx : end+1]
		headerName := s[idx+len(headerPrefix) : end]
		value := lowerHeaders[strings.ToLower(headerName)]
		s = strings.ReplaceAll(s, placeholder, value)
	}

	return s
}

// resolveBodyPath recursively traverses v following the dot-path segments in parts.
// It supports map (object) traversal and slice (array) traversal via numeric indices.
// Returns the string representation of the value at the path, or "" if the path
// does not exist or a segment cannot be traversed.
func resolveBodyPath(parts []string, v interface{}) string {
	if len(parts) == 0 {
		if v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
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
				if fr.Status.Phase != "Running" || fr.Status.StartTime == nil {
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
