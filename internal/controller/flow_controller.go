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
	"context"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flows/status,verbs=get;update;patch

// FlowReconciler reconciles a Flow object.
type FlowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *FlowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var flow automationv1alpha1.Flow
	if err := r.Get(ctx, req.NamespacedName, &flow); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate the spec.
	if err := validateFlowSpec(flow.Spec); err != nil {
		log.Info("Flow spec validation failed", "flow", req.NamespacedName, "error", err)
		cond := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            err.Error(),
			ObservedGeneration: flow.Generation,
		}
		apimeta.SetStatusCondition(&flow.Status.Conditions, cond)
		if err := r.Status().Update(ctx, &flow); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Validation passed — set Ready=True.
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "FlowReady",
		Message:            "Flow is valid and ready",
		ObservedGeneration: flow.Generation,
	}
	apimeta.SetStatusCondition(&flow.Status.Conditions, cond)
	if err := r.Status().Update(ctx, &flow); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// validateFlowSpec validates the Flow spec and returns the first error found.
func validateFlowSpec(spec automationv1alpha1.FlowSpec) error {
	if len(spec.Steps) == 0 {
		return fmt.Errorf("spec.steps must contain at least one step")
	}

	// Build a set of step names for uniqueness and runAfter validation.
	stepNames := make(map[string]struct{}, len(spec.Steps))
	for _, step := range spec.Steps {
		if _, exists := stepNames[step.Name]; exists {
			return fmt.Errorf("duplicate step name %q: all step names must be unique", step.Name)
		}
		stepNames[step.Name] = struct{}{}
	}

	for _, step := range spec.Steps {
		// Validate runAfter references.
		for _, dep := range step.RunAfter {
			if _, exists := stepNames[dep]; !exists {
				return fmt.Errorf("step %q has runAfter reference to unknown step %q", step.Name, dep)
			}
		}

		// Validate action type and required fields.
		switch step.Action.Type {
		case "http":
			if step.Action.HTTP == nil {
				return fmt.Errorf("step %q has type=http but action.http is not set", step.Name)
			}
			if step.Action.HTTP.URL == "" {
				return fmt.Errorf("step %q has type=http but action.http.url is empty", step.Name)
			}
		case "transform":
			// No additional required fields.
		case "publish":
			if step.Action.Publish == nil {
				return fmt.Errorf("step %q has type=publish but action.publish is not set", step.Name)
			}
			if step.Action.Publish.IntegrationRef.Name == "" {
				return fmt.Errorf("step %q has type=publish but action.publish.integrationRef.name is empty", step.Name)
			}
			if step.Action.Publish.Topic == "" {
				return fmt.Errorf("step %q has type=publish but action.publish.topic is empty", step.Name)
			}
		case "wait":
			if step.Action.Wait == nil {
				return fmt.Errorf("step %q has type=wait but action.wait is not set", step.Name)
			}
			if step.Action.Wait.Duration == "" {
				return fmt.Errorf("step %q has type=wait but action.wait.duration is empty", step.Name)
			}
			if _, err := time.ParseDuration(step.Action.Wait.Duration); err != nil {
				return fmt.Errorf("step %q has type=wait but action.wait.duration %q is not a valid Go duration: %w", step.Name, step.Action.Wait.Duration, err)
			}
		default:
			return fmt.Errorf("step %q has unknown action type %q: must be one of http, transform, publish, wait", step.Name, step.Action.Type)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FlowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Flow{}).
		Named("flow").
		Complete(r)
}
