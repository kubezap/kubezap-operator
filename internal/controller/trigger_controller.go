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

const cronTriggerFinalizer = "cron.kubezap.io/scheduler-cleanup"

// TriggerReconciler reconciles a Trigger object
type TriggerReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	CronScheduler *CronScheduler
}

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *TriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var trg automationv1alpha1.Trigger
	if err := r.Get(ctx, req.NamespacedName, &trg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	key := req.Namespace + "/" + req.Name

	// Handle deletion
	if !trg.DeletionTimestamp.IsZero() {
		if r.CronScheduler != nil {
			r.CronScheduler.Deregister(key)
		}
		if containsString(trg.Finalizers, cronTriggerFinalizer) {
			trg.Finalizers = removeString(trg.Finalizers, cronTriggerFinalizer)
			if err := r.Update(ctx, &trg); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Handle cron triggers
	if trg.Spec.Type == "cron" && r.CronScheduler != nil {
		if trg.Spec.Enabled && trg.Spec.Cron != nil {
			// Ensure finalizer is present
			if !containsString(trg.Finalizers, cronTriggerFinalizer) {
				trg.Finalizers = append(trg.Finalizers, cronTriggerFinalizer)
				if err := r.Update(ctx, &trg); err != nil {
					return ctrl.Result{}, err
				}
			}
			if err := r.CronScheduler.Register(&trg); err != nil {
				log.Error(err, "failed to register cron trigger")
				return ctrl.Result{}, err
			}
		} else {
			r.CronScheduler.Deregister(key)
		}
	}

	// Handle webhook triggers — ensure gateway Deployment exists
	if trg.Spec.Type == "webhook" && trg.Spec.Enabled {
		if err := r.reconcileWebhookGatewayDeployment(ctx, trg.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling webhook gateway deployment: %w", err)
		}
	}

	// Set initial status for enabled triggers
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

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
