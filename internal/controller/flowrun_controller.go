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
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

const retainAnnotation = "kubezap.io/retain"

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows,verbs=get;list;watch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers,verbs=get;list;watch

// FlowRunReconciler reconciles a FlowRun object.
type FlowRunReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	HTTPClient   *http.Client
	TTLSucceeded time.Duration
	TTLFailed    time.Duration
}

func (r *FlowRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var flowRun automationv1alpha1.FlowRun
	if err := r.Get(ctx, req.NamespacedName, &flowRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// GC: handle terminal FlowRuns (TTL expiry + maxFlowRuns cap).
	if flowRun.Status.Phase == "Succeeded" || flowRun.Status.Phase == "Failed" {
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

	// Fetch referenced Flow.
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

	// Transition Pending → Running.
	if flowRun.Status.Phase == "" || flowRun.Status.Phase == "Pending" {
		now := metav1.Now()
		flowRun.Status.Phase = "Running"
		flowRun.Status.StartTime = &now
		if err := r.Status().Update(ctx, &flowRun); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Execute steps in order.
	stepResults := make(map[string]map[string]string) // stepName → resultName → value

	for _, step := range flow.Spec.Steps {
		// Check runAfter dependencies.
		if !r.dependenciesMet(step, flowRun.Status.Steps) {
			continue
		}

		// Check if already completed.
		existing := findStepStatus(flowRun.Status.Steps, step.Name)
		if existing != nil && (existing.Phase == "Succeeded" || existing.Phase == "Skipped") {
			if existing.Results != nil {
				stepResults[step.Name] = resultsToMap(existing.Results)
			}
			continue
		}
		if existing != nil && existing.Phase == "Failed" {
			if step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue" {
				continue
			}
			return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fmt.Sprintf("step %q failed", step.Name))
		}

		// Execute the step.
		stepStatus, err := r.executeStep(ctx, log, &step, &flow, stepResults)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Merge step status into FlowRun.
		flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, *stepStatus)
		if err := r.Status().Update(ctx, &flowRun); err != nil {
			return ctrl.Result{}, err
		}

		if stepStatus.Phase == "Failed" {
			if step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue" {
				continue
			}
			return ctrl.Result{}, r.failFlowRun(ctx, &flowRun, fmt.Sprintf("step %q failed: %s", step.Name, stepStatus.Message))
		}

		if stepStatus.Results != nil {
			stepResults[step.Name] = resultsToMap(stepStatus.Results)
		}
	}

	// All steps done — succeed.
	now := metav1.Now()
	flowRun.Status.Phase = "Succeeded"
	flowRun.Status.CompletionTime = &now
	return ctrl.Result{}, r.Status().Update(ctx, &flowRun)
}

func (r *FlowRunReconciler) executeStep(
	ctx context.Context,
	log logr.Logger,
	step *automationv1alpha1.FlowStep,
	flow *automationv1alpha1.Flow,
	_ map[string]map[string]string,
) (*automationv1alpha1.StepRunStatus, error) {
	_ = flow // reserved for future param resolution
	now := metav1.Now()
	status := &automationv1alpha1.StepRunStatus{
		Name:      step.Name,
		Phase:     "Running",
		StartTime: &now,
		Attempts:  1,
	}

	switch step.Action.Type {
	case "http":
		results, msg, err := r.executeHTTPStep(ctx, log, step)
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
		if err != nil {
			status.Phase = "Failed"
			status.Message = err.Error()
		} else {
			status.Phase = "Succeeded"
			status.Message = msg
			status.Results = mapsToResults(results)
		}
	case "transform", "publish":
		// Placeholder — mark succeeded immediately.
		completionTime := metav1.Now()
		status.Phase = "Succeeded"
		status.CompletionTime = &completionTime
		log.Info("step type not yet implemented, marking succeeded", "type", step.Action.Type, "step", step.Name)
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
) (map[string]string, string, error) {
	if step.Action.HTTP == nil {
		return nil, "", fmt.Errorf("step %q has type=http but no http spec", step.Name)
	}

	h := step.Action.HTTP
	method := h.Method
	if method == "" {
		method = "POST"
	}

	timeoutSec := h.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	// Apply per-step timeout.
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Retry logic.
	maxAttempts := 1
	var retryPolicy *automationv1alpha1.RetryPolicy
	if step.RetryPolicy != nil {
		retryPolicy = step.RetryPolicy
		maxAttempts = int(retryPolicy.MaxRetries) + 1
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := r.retryDelay(retryPolicy, attempt)
			select {
			case <-stepCtx.Done():
				return nil, "", stepCtx.Err()
			case <-time.After(delay):
			}
		}

		var bodyReader io.Reader
		if h.Body != "" {
			bodyReader = bytes.NewBufferString(h.Body)
		}

		req, err := http.NewRequestWithContext(stepCtx, method, h.URL, bodyReader)
		if err != nil {
			return nil, "", fmt.Errorf("building HTTP request: %w", err)
		}
		for k, v := range h.Headers {
			req.Header.Set(k, v)
		}

		httpClient := r.HTTPClient
		if httpClient == nil {
			httpClient = http.DefaultClient
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Error(err, "HTTP step request failed", "step", step.Name, "attempt", attempt+1)
			continue
		}
		defer resp.Body.Close() //nolint:gocritic

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			results := make(map[string]string)
			if len(h.ResultMappings) > 0 {
				results["body"] = string(respBody)
				results["status"] = fmt.Sprintf("%d", resp.StatusCode)
			}
			return results, fmt.Sprintf("HTTP %d", resp.StatusCode), nil
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 256))
		log.Info("HTTP step non-2xx response", "step", step.Name, "status", resp.StatusCode, "attempt", attempt+1)
	}

	return nil, "", lastErr
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
	flowRun.Status.Phase = "Failed"
	flowRun.Status.CompletionTime = &now
	flowRun.Status.Message = msg
	return r.Status().Update(ctx, flowRun)
}

func (r *FlowRunReconciler) dependenciesMet(step automationv1alpha1.FlowStep, statuses []automationv1alpha1.StepRunStatus) bool {
	for _, dep := range step.RunAfter {
		s := findStepStatus(statuses, dep)
		if s == nil || (s.Phase != "Succeeded" && s.Phase != "Skipped") {
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

	// Enforce maxFlowRuns cap if trigger label is present.
	if triggerName, ok := flowRun.Labels["kubezap.io/trigger"]; ok && triggerName != "" {
		var trigger automationv1alpha1.Trigger
		if err := r.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: flowRun.Namespace}, &trigger); err == nil {
			if trigger.Spec.MaxFlowRuns != nil && *trigger.Spec.MaxFlowRuns > 0 {
				if err := r.enforceMaxFlowRuns(ctx, triggerName, flowRun.Namespace, *trigger.Spec.MaxFlowRuns); err != nil {
					return 0, err
				}
			}
		}
		// Ignore NotFound — trigger may have been deleted.
	}

	// Determine TTL: per-FlowRun spec overrides operator flag.
	ttl := r.TTLSucceeded
	if flowRun.Status.Phase == "Failed" {
		ttl = r.TTLFailed
	}
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

// enforceMaxFlowRuns deletes the oldest completed FlowRuns for a trigger
// until the count is within the maxFlowRuns cap.
func (r *FlowRunReconciler) enforceMaxFlowRuns(ctx context.Context, triggerName, namespace string, maxFlowRuns int32) error {
	log := logf.FromContext(ctx)

	var list automationv1alpha1.FlowRunList
	if err := r.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{"kubezap.io/trigger": triggerName},
	); err != nil {
		return err
	}

	var completed []automationv1alpha1.FlowRun
	for _, fr := range list.Items {
		if fr.Status.Phase != "Succeeded" && fr.Status.Phase != "Failed" {
			continue
		}
		if fr.Annotations[retainAnnotation] == "true" {
			continue
		}
		completed = append(completed, fr)
	}

	if int32(len(completed)) <= maxFlowRuns {
		return nil
	}

	sort.Slice(completed, func(i, j int) bool {
		ti := completed[i].Status.CompletionTime
		tj := completed[j].Status.CompletionTime
		if ti == nil {
			return true
		}
		if tj == nil {
			return false
		}
		return ti.Before(tj)
	})

	toDelete := int(int32(len(completed)) - maxFlowRuns)
	for i := 0; i < toDelete; i++ {
		fr := completed[i]
		log.Info("enforcing maxFlowRuns, deleting oldest", "flowRun", fr.Name, "trigger", triggerName)
		if err := r.Delete(ctx, &fr); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FlowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.FlowRun{}).
		Named("flowrun").
		Complete(r)
}

// --- helpers ---

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
