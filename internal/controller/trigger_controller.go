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
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

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

	// Update Trigger condition and status based on enabled state.
	acceptedCondition := metav1.Condition{
		Type:    "Accepted",
		Status:  metav1.ConditionFalse,
		Reason:  "Disabled",
		Message: "Trigger is disabled",
	}
	if trg.Spec.Enabled {
		acceptedCondition.Status = metav1.ConditionTrue
		acceptedCondition.Reason = "Enabled"
		acceptedCondition.Message = "Trigger is accepted and active"

		if trg.Status.LastTriggeredTime == nil || trg.Status.LastTriggeredTime.Time.IsZero() {
			now := metav1.Now()
			trg.Status.LastTriggeredTime = &now
		}
		trg.Status.LastResult = "Accepted"
	}
	setTriggerCondition(&trg.Status, acceptedCondition)

	if err := r.Status().Update(ctx, &trg); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating Trigger status: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileWebhookGatewayDeployment is a thin wrapper so TriggerReconciler can call the
// package-level helper without threading the client through manually.
func (r *TriggerReconciler) reconcileWebhookGatewayDeployment(ctx context.Context, namespace string) error {
	return ensureWebhookGateway(ctx, r.Client, namespace)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Trigger{}).
		Named("trigger").
		Complete(r)
}

// ensureWebhookGateway ensures the webhook gateway Deployment, Service, RBAC, and HPA
// exist in the given namespace. It is called by both the Trigger and MockEndpoint reconcilers
// so that the gateway is present whenever webhook routes or mock endpoints are needed.
func ensureWebhookGateway(ctx context.Context, c client.Client, namespace string) error {
	log := logf.FromContext(ctx)

	sa := desiredWebhookGatewayServiceAccount(namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, c, sa, func() error {
		sa.Labels = desiredWebhookGatewayServiceAccount(namespace).Labels
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update gateway ServiceAccount: %w", err)
	}

	role := desiredWebhookGatewayRole(namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, c, role, func() error {
		role.Rules = desiredWebhookGatewayRole(namespace).Rules
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update gateway Role: %w", err)
	}

	rb := desiredWebhookGatewayRoleBinding(namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, c, rb, func() error {
		rb.RoleRef = desiredWebhookGatewayRoleBinding(namespace).RoleRef
		rb.Subjects = desiredWebhookGatewayRoleBinding(namespace).Subjects
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update gateway RoleBinding: %w", err)
	}

	svc := desiredWebhookGatewayService(namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, c, svc, func() error {
		svc.Labels = desiredWebhookGatewayService(namespace).Labels
		svc.Spec = desiredWebhookGatewayService(namespace).Spec
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update gateway Service: %w", err)
	}

	desired := desiredWebhookGatewayDeployment(namespace)
	existing := &appsv1.Deployment{}
	err := c.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := c.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created webhook gateway deployment", "namespace", namespace)
		return nil
	}
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		if existing.Spec.Template.Spec.Containers[0].Image != webhookGatewayImage {
			existing.Spec.Template.Spec.Containers[0].Image = webhookGatewayImage
			if err := c.Update(ctx, existing); err != nil {
				return err
			}
			log.Info("updated webhook gateway deployment", "namespace", namespace)
		}
	}

	hpaDesired := desiredWebhookGatewayHPA(namespace)
	hpaExisting := &autoscalingv2.HorizontalPodAutoscaler{}
	err = c.Get(ctx, client.ObjectKeyFromObject(hpaDesired), hpaExisting)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := c.Create(ctx, hpaDesired); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created webhook gateway HPA", "namespace", namespace)
		return nil
	}
	hpaExisting.Spec.MinReplicas = hpaDesired.Spec.MinReplicas
	hpaExisting.Spec.MaxReplicas = hpaDesired.Spec.MaxReplicas
	hpaExisting.Spec.Metrics = hpaDesired.Spec.Metrics
	if err := c.Update(ctx, hpaExisting); err != nil {
		return err
	}
	return nil
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

func setTriggerCondition(status *automationv1alpha1.TriggerStatus, condition metav1.Condition) {
	if condition.LastTransitionTime.IsZero() {
		condition.LastTransitionTime = metav1.Now()
	}
	for i, existing := range status.Conditions {
		if existing.Type == condition.Type {
			if existing.Status != condition.Status {
				condition.LastTransitionTime = metav1.Now()
			} else {
				condition.LastTransitionTime = existing.LastTransitionTime
			}
			status.Conditions[i] = condition
			return
		}
	}
	status.Conditions = append(status.Conditions, condition)
}
