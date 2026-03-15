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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/cel-go/cel"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
	"github.com/yourname/kubezap/internal/metrics"
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

	// Derive execution context with flow-level timeout.
	execCtx := ctx
	if flow.Spec.Timeout != nil && flow.Spec.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, flow.Spec.Timeout.Duration)
		defer cancel()
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

		// Cascade-skip: if all runAfter deps were skipped, skip this step too.
		if allDepsSkipped(step, &flowRun) {
			now := metav1.Now()
			flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
				Name:           step.Name,
				Phase:          "Skipped",
				Message:        "all runAfter dependencies were skipped",
				CompletionTime: &now,
			})
			if err := r.Status().Update(ctx, &flowRun); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}

		// Evaluate when conditions.
		if len(step.When) > 0 {
			run, err := evaluateWhen(step.When, stepResults, flowRun.Status.Steps, flowRun.Spec.TriggerData)
			if err != nil {
				now := metav1.Now()
				flowRun.Status.Steps = upsertStepStatus(flowRun.Status.Steps, automationv1alpha1.StepRunStatus{
					Name:           step.Name,
					Phase:          "Failed",
					Message:        fmt.Sprintf("when expression error: %v", err),
					CompletionTime: &now,
				})
				if err2 := r.Status().Update(ctx, &flowRun); err2 != nil {
					return ctrl.Result{}, err2
				}
				if step.OnFailure == "Continue" || flow.Spec.FailurePolicy == "Continue" {
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
				if err := r.Status().Update(ctx, &flowRun); err != nil {
					return ctrl.Result{}, err
				}
				continue
			}
		}

		// Execute the step.
		stepStart := time.Now()
		stepStatus, err := r.executeStep(execCtx, log, &step, &flow, &flowRun, stepResults, flowRun.Spec.TriggerData)
		if err != nil {
			return ctrl.Result{}, err
		}
		metrics.StepDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name,
			step.Action.Type, string(stepStatus.Phase),
		).Observe(time.Since(stepStart).Seconds())

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
	if flowRun.Status.StartTime != nil {
		duration := time.Since(flowRun.Status.StartTime.Time)
		metrics.FlowRunDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name, "Succeeded",
		).Observe(duration.Seconds())
	}
	return ctrl.Result{}, r.Status().Update(ctx, &flowRun)
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
	now := metav1.Now()
	status := &automationv1alpha1.StepRunStatus{
		Name:      step.Name,
		Phase:     "Running",
		StartTime: &now,
		Attempts:  1,
	}

	switch step.Action.Type {
	case "http":
		results, msg, err := r.executeHTTPStep(ctx, log, step, stepResults, triggerData)
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
		result, err := r.executePublishStep(ctx, flowRun, step, triggerData, stepResults)
		completionTime := metav1.Now()
		status.CompletionTime = &completionTime
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
) (map[string]string, string, error) {
	if step.Action.HTTP == nil {
		return nil, "", fmt.Errorf("step %q has type=http but no http spec", step.Name)
	}

	// Apply per-step timeout from FlowStep.Timeout if set; otherwise fall back to HTTPAction.TimeoutSeconds.
	if step.Timeout != nil && step.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Timeout.Duration)
		defer cancel()
	}

	h := step.Action.HTTP
	url := substituteVars(h.URL, stepResults, triggerData)
	body := substituteVars(h.Body, stepResults, triggerData)
	headers := make(map[string]string, len(h.Headers))
	for k, v := range h.Headers {
		headers[k] = substituteVars(v, stepResults, triggerData)
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
		if body != "" {
			bodyReader = bytes.NewBufferString(body)
		}

		req, err := http.NewRequestWithContext(stepCtx, method, url, bodyReader)
		if err != nil {
			return nil, "", fmt.Errorf("building HTTP request: %w", err)
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

func (r *FlowRunReconciler) executePublishStep(
	ctx context.Context,
	flowRun *automationv1alpha1.FlowRun,
	step *automationv1alpha1.FlowStep,
	triggerData *automationv1alpha1.TriggerData,
	stepResults map[string]map[string]string,
) (map[string]string, error) {
	if step.Action.Publish == nil || step.Action.Publish.IntegrationRef.Name == "" {
		return nil, fmt.Errorf("step %q has type=publish but no integrationRef.name", step.Name)
	}

	// Fetch the Integration.
	var integration automationv1alpha1.Integration
	if err := r.Get(ctx, types.NamespacedName{
		Name:      step.Action.Publish.IntegrationRef.Name,
		Namespace: flowRun.Namespace,
	}, &integration); err != nil {
		return nil, fmt.Errorf("fetching integration %q: %w", step.Action.Publish.IntegrationRef.Name, err)
	}

	if integration.Spec.Plugin == nil {
		return nil, fmt.Errorf("integration %q is not a plugin type", integration.Name)
	}

	port := integration.Spec.Plugin.PublisherPort
	if port == 0 {
		port = 8090
	}

	pluginURL := fmt.Sprintf("http://kubezap-plugin-%s.%s.svc.cluster.local:%d/publish",
		integration.Name, flowRun.Namespace, port)

	body := substituteVars(step.Action.Publish.Body, stepResults, triggerData)
	headers := make(map[string]string, len(step.Action.Publish.Headers))
	for k, v := range step.Action.Publish.Headers {
		headers[k] = substituteVars(v, stepResults, triggerData)
	}

	// Wrap ctx with a 30s timeout unless ctx already has a shorter deadline.
	publishCtx := ctx
	const publishTimeout = 30 * time.Second
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > publishTimeout {
		var cancel context.CancelFunc
		publishCtx, cancel = context.WithTimeout(ctx, publishTimeout)
		defer cancel()
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(publishCtx, http.MethodPost, pluginURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building publish request: %w", err)
	}

	// Set Content-Type default; allow step headers to override.
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
		return nil, fmt.Errorf("publish endpoint returned status %d", resp.StatusCode)
	}

	return map[string]string{}, nil
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
	if flowRun.Status.StartTime != nil {
		duration := time.Since(flowRun.Status.StartTime.Time)
		metrics.FlowRunDuration.WithLabelValues(
			flowRun.Namespace, flowRun.Spec.FlowRef.Name, "Failed",
		).Observe(duration.Seconds())
	}
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

// evaluateWhen evaluates all WhenExpression conditions using CEL.
// Returns true if all conditions pass (or the list is empty), false if any fail.
func evaluateWhen(
	when []automationv1alpha1.WhenExpression,
	stepResults map[string]map[string]string,
	stepStatuses []automationv1alpha1.StepRunStatus,
	triggerData *automationv1alpha1.TriggerData,
) (bool, error) {
	if len(when) == 0 {
		return true, nil
	}

	env, err := cel.NewEnv(
		cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return false, err
	}

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
		ast, iss := env.Compile(expr.Expression)
		if iss != nil && iss.Err() != nil {
			return false, fmt.Errorf("CEL compile error: %w", iss.Err())
		}
		prog, err := env.Program(ast)
		if err != nil {
			return false, fmt.Errorf("CEL program error: %w", err)
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

// substituteVars replaces template placeholders in s with values from stepResults and triggerData.
// Supported syntax:
//   - $(steps.<name>.results.<key>) — step output value
//   - $(trigger.body) — raw trigger request body
//   - $(trigger.headers.<name>) — trigger request header value (case-insensitive)
//   - $(trigger.topic), $(trigger.partition), $(trigger.offset), $(trigger.scheduledTime)
func substituteVars(s string, stepResults map[string]map[string]string, triggerData *automationv1alpha1.TriggerData) string {
	// Substitute step results: $(steps.<name>.results.<key>)
	for stepName, results := range stepResults {
		for key, value := range results {
			placeholder := fmt.Sprintf("$(steps.%s.results.%s)", stepName, key)
			s = strings.ReplaceAll(s, placeholder, value)
		}
	}

	// Substitute trigger fields.
	if triggerData != nil {
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
		// Build a lowercase key map once.
		lowerHeaders := make(map[string]string, len(triggerData.Headers))
		for k, v := range triggerData.Headers {
			lowerHeaders[strings.ToLower(k)] = v
		}
		// Scan for $(trigger.headers.*) placeholders.
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
	}

	return s
}
