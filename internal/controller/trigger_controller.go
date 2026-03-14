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

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// TriggerReconciler reconciles a Trigger object
type TriggerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Trigger object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *TriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	var trg automationv1alpha1.Trigger
	if err := r.Get(ctx, req.NamespacedName, &trg); err != nil {
		// NotFound or other errors are handled by controller-runtime
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if trg.Spec.Type == "webhook" && trg.Spec.Enabled {
		if err := r.reconcileWebhookGatewayDeployment(ctx, trg.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling webhook gateway deployment: %w", err)
		}
	}

	// Simple prototype behavior: when the Trigger is enabled, record a lastTriggeredTime
	// and set a LastResult of "Accepted". This provides a visible status update
	// for testing the reconciliation flow. Production logic will create Run CRs.
	if trg.Spec.Enabled {
		now := metav1.Now()
		if trg.Status.LastTriggeredTime == nil || trg.Status.LastTriggeredTime.Time.IsZero() {
			trg.Status.LastTriggeredTime = &now
			trg.Status.LastResult = "Accepted"
			if err := r.Status().Update(ctx, &trg); err != nil {
				return ctrl.Result{}, fmt.Errorf("updating Trigger status: %w", err)
			}
		}
	}

	return ctrl.Result{}, nil
}

// reconcileWebhookGatewayDeployment ensures the webhook gateway Deployment exists in the given
// namespace and that its image is up to date. The Deployment is shared across all webhook
// Triggers in the namespace — only one instance is ever created.
func (r *TriggerReconciler) reconcileWebhookGatewayDeployment(ctx context.Context, namespace string) error {
	log := logf.FromContext(ctx)
	desired := desiredWebhookGatewayDeployment(namespace)

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created webhook gateway deployment", "namespace", namespace)
		return nil
	}

	// Update image if it has drifted from the desired value.
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		c := &existing.Spec.Template.Spec.Containers[0]
		if c.Image != webhookGatewayImage {
			c.Image = webhookGatewayImage
			if err := r.Update(ctx, existing); err != nil {
				return err
			}
			log.Info("updated webhook gateway deployment", "namespace", namespace)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Trigger{}).
		Named("trigger").
		Complete(r)
}
