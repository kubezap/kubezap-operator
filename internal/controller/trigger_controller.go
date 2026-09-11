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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

const cronTriggerFinalizer = "cron.kubezap.io/scheduler-cleanup"
const resourceTriggerFinalizer = "resource.kubezap.io/watcher-cleanup"

// TriggerReconciler reconciles a Trigger object
type TriggerReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	CronScheduler   *CronScheduler
	ResourceWatcher *ResourceWatcher
}

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

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
		if r.ResourceWatcher != nil {
			r.ResourceWatcher.Deregister(key)
		}
		needsUpdate := false
		if containsString(trg.Finalizers, cronTriggerFinalizer) {
			trg.Finalizers = removeString(trg.Finalizers, cronTriggerFinalizer)
			needsUpdate = true
		}
		if containsString(trg.Finalizers, resourceTriggerFinalizer) {
			trg.Finalizers = removeString(trg.Finalizers, resourceTriggerFinalizer)
			needsUpdate = true
		}
		if needsUpdate {
			if err := r.Update(ctx, &trg); err != nil {
				return ctrl.Result{}, err
			}
		}
		// If this was an enabled webhook Trigger, clean up gateway resources if no
		// other enabled webhook Triggers remain in the namespace. The current Trigger
		// is still present in the API server (just marked for deletion), so
		// cleanupWebhookGatewayIfUnused excludes objects with a non-zero
		// DeletionTimestamp when counting active webhook Triggers.
		if trg.Spec.Type == triggerTypeWebhook {
			if err := cleanupWebhookGatewayIfUnused(ctx, r.Client, trg.Namespace); err != nil {
				return ctrl.Result{}, fmt.Errorf("cleaning up webhook gateway: %w", err)
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

	// Handle webhook triggers — ensure gateway Deployment exists, or clean up if disabled.
	if trg.Spec.Type == triggerTypeWebhook && trg.Spec.Enabled {
		if err := r.reconcileWebhookGatewayDeployment(ctx, trg.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling webhook gateway deployment: %w", err)
		}
	} else if trg.Spec.Type == triggerTypeWebhook && !trg.Spec.Enabled {
		if err := cleanupWebhookGatewayIfUnused(ctx, r.Client, trg.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("cleaning up webhook gateway: %w", err)
		}
	}

	// Handle resource triggers — register/deregister the resource watcher.
	if trg.Spec.Type == "resource" && r.ResourceWatcher != nil {
		if trg.Spec.Enabled && trg.Spec.Resource != nil {
			// Ensure finalizer is present
			if !containsString(trg.Finalizers, resourceTriggerFinalizer) {
				trg.Finalizers = append(trg.Finalizers, resourceTriggerFinalizer)
				if err := r.Update(ctx, &trg); err != nil {
					return ctrl.Result{}, err
				}
			}
			r.ResourceWatcher.Register(&trg)
		} else {
			r.ResourceWatcher.Deregister(key)
		}
	}

	// Re-fetch to get the latest resource version before patching status.
	// The finalizer Update above (or a concurrent cron scheduler patch) may have
	// advanced the resource version since we first fetched the object.
	if err := r.Get(ctx, req.NamespacedName, &trg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Update Trigger condition and status based on enabled state.
	statusPatch := client.MergeFrom(trg.DeepCopy())
	ready := trg.Spec.Enabled
	condStatus := metav1.ConditionFalse
	condReason := "Disabled"
	condMsg := "Trigger is disabled"
	if ready {
		condStatus = metav1.ConditionTrue
		condReason = "Enabled"
		condMsg = "Trigger is accepted and active"
		trg.Status.LastResult = "Accepted"
	}
	setTriggerCondition(&trg.Status, metav1.Condition{
		Type:    "Accepted",
		Status:  condStatus,
		Reason:  condReason,
		Message: condMsg,
	})
	setTriggerCondition(&trg.Status, metav1.Condition{
		Type:    "Ready",
		Status:  condStatus,
		Reason:  condReason,
		Message: condMsg,
	})

	if err := r.Status().Patch(ctx, &trg, statusPatch); err != nil {
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
// exist in the given namespace. It is called by the Trigger reconciler
// so that the gateway is present whenever webhook routes are needed.
//
// TLS configuration is read from the Namespace annotations:
//
//	kubezap.io/webhook-tls-secret     — Secret name with tls.crt / tls.key (cert-manager compatible)
//	kubezap.io/webhook-mtls-ca-secret — Secret name with ca.crt (requires webhook-tls-secret)
func ensureWebhookGateway(ctx context.Context, c client.Client, namespace string) error {
	log := logf.FromContext(ctx)

	// Read TLS configuration from Namespace annotations.
	tlsCfg := WebhookGatewayTLSConfig{}
	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err == nil {
		tlsCfg.TLSSecretName = ns.Annotations["kubezap.io/webhook-tls-secret"]
		if tlsCfg.TLSSecretName != "" {
			tlsCfg.MTLSCASecretName = ns.Annotations["kubezap.io/webhook-mtls-ca-secret"]
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %s for TLS config: %w", namespace, err)
	}

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

	svc := desiredWebhookGatewayService(namespace, tlsCfg)
	if _, err := controllerutil.CreateOrUpdate(ctx, c, svc, func() error {
		svc.Labels = desiredWebhookGatewayService(namespace, tlsCfg).Labels
		svc.Spec = desiredWebhookGatewayService(namespace, tlsCfg).Spec
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update gateway Service: %w", err)
	}

	desired := desiredWebhookGatewayDeployment(namespace, tlsCfg)
	op, err := controllerutil.CreateOrUpdate(ctx, c, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Capture the live replicas before overwriting the spec so that an
		// HPA's replica count is not reset on every reconcile.
		liveReplicas := desired.Spec.Replicas
		desired.Spec = desiredWebhookGatewayDeployment(namespace, tlsCfg).Spec
		// Preserve HPA-managed replica count: if an HPA (already reconciled above) is
		// present, keep whatever replica count the live object had rather than
		// snapping back to the template default.
		if liveReplicas != nil && desired.Spec.Replicas == nil {
			desired.Spec.Replicas = liveReplicas
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update gateway Deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled webhook gateway deployment", "namespace", namespace, "result", op)
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

// cleanupWebhookGatewayIfUnused deletes the webhook gateway resources (Deployment,
// Service, ServiceAccount, Role, RoleBinding, HPA) from the given namespace if no
// enabled, non-terminating webhook Triggers remain. It is safe to call repeatedly —
// NotFound errors on each delete are silently ignored.
func cleanupWebhookGatewayIfUnused(ctx context.Context, c client.Client, namespace string) error {
	log := logf.FromContext(ctx)

	var triggerList automationv1alpha1.TriggerList
	if err := c.List(ctx, &triggerList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing Triggers in namespace %s: %w", namespace, err)
	}

	for i := range triggerList.Items {
		t := &triggerList.Items[i]
		if t.Spec.Type == triggerTypeWebhook && t.Spec.Enabled && t.DeletionTimestamp.IsZero() {
			// At least one active webhook Trigger remains — gateway is still needed.
			return nil
		}
	}

	log.Info("no active webhook Triggers remain; cleaning up webhook gateway resources", "namespace", namespace)

	// All six resource types share the same name: webhookGatewayDeploymentName
	// ("kubezap-webhook-gateway"). ServiceAccount uses the same name.
	key := client.ObjectKey{Name: webhookGatewayDeploymentName, Namespace: namespace}

	deploy := &appsv1.Deployment{}
	if err := c.Get(ctx, key, deploy); err == nil {
		if err := c.Delete(ctx, deploy); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway Deployment: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway Deployment: %w", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, key, svc); err == nil {
		if err := c.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway Service: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway Service: %w", err)
	}

	sa := &corev1.ServiceAccount{}
	if err := c.Get(ctx, key, sa); err == nil {
		if err := c.Delete(ctx, sa); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway ServiceAccount: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway ServiceAccount: %w", err)
	}

	role := &rbacv1.Role{}
	if err := c.Get(ctx, key, role); err == nil {
		if err := c.Delete(ctx, role); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway Role: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway Role: %w", err)
	}

	rb := &rbacv1.RoleBinding{}
	if err := c.Get(ctx, key, rb); err == nil {
		if err := c.Delete(ctx, rb); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway RoleBinding: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway RoleBinding: %w", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, key, hpa); err == nil {
		if err := c.Delete(ctx, hpa); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting webhook gateway HPA: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("getting webhook gateway HPA: %w", err)
	}

	log.Info("webhook gateway resources cleaned up", "namespace", namespace)
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
