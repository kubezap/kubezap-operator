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
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=mockendpoints,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=mockendpoints/status,verbs=get;update;patch

// MockEndpointReconciler reconciles a MockEndpoint object.
type MockEndpointReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *MockEndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var me automationv1alpha1.MockEndpoint
	if err := r.Get(ctx, req.NamespacedName, &me); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Ensure the webhook gateway is running — it serves /mock/* paths.
	if err := ensureWebhookGateway(ctx, r.Client, me.Namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring webhook gateway: %w", err)
	}

	// Validate spec.path.
	if strings.TrimSpace(me.Spec.Path) == "" {
		log.Info("MockEndpoint has empty path, marking InvalidSpec", "name", me.Name)
		setMockCondition(&me, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            "spec.path must not be empty",
			ObservedGeneration: me.Generation,
		})
		if err := r.Status().Update(ctx, &me); err != nil && !apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Build status URL.
	base := strings.TrimRight(os.Getenv("KUBEZAP_GATEWAY_BASE_URL"), "/")
	suffix := "/mock/" + strings.TrimPrefix(strings.TrimSpace(me.Spec.Path), "/")
	url := base + suffix

	me.Status.URL = url
	setMockCondition(&me, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Registered",
		Message:            "Mock endpoint registered",
		ObservedGeneration: me.Generation,
	})

	if err := r.Status().Update(ctx, &me); err != nil && !apierrors.IsConflict(err) {
		return ctrl.Result{}, err
	}

	log.Info("MockEndpoint reconciled", "name", me.Name, "url", url)
	return ctrl.Result{}, nil
}

// setMockCondition upserts a condition into the MockEndpoint status.
func setMockCondition(me *automationv1alpha1.MockEndpoint, cond metav1.Condition) {
	now := metav1.Now()
	for i, existing := range me.Status.Conditions {
		if existing.Type == cond.Type {
			if existing.Status != cond.Status {
				cond.LastTransitionTime = now
			} else {
				cond.LastTransitionTime = existing.LastTransitionTime
			}
			me.Status.Conditions[i] = cond
			return
		}
	}
	cond.LastTransitionTime = now
	me.Status.Conditions = append(me.Status.Conditions, cond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MockEndpointReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.MockEndpoint{}).
		Named("mockendpoint").
		Complete(r)
}
