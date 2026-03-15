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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

// IntegrationReconciler reconciles an Integration object.
type IntegrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *IntegrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var integration automationv1alpha1.Integration
	if err := r.Get(ctx, req.NamespacedName, &integration); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Validate spec based on type.
	if err := validateIntegrationSpec(integration.Spec); err != nil {
		log.Info("Integration spec validation failed", "integration", req.NamespacedName, "error", err)
		cond := metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidSpec",
			Message:            err.Error(),
			ObservedGeneration: integration.Generation,
		}
		apimeta.SetStatusCondition(&integration.Status.Conditions, cond)
		if err := r.Status().Update(ctx, &integration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Type-specific reconciliation.
	switch integration.Spec.Type {
	case "plugin":
		deploymentName := "kubezap-plugin-" + integration.Name
		if err := r.reconcilePluginDeployment(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin deployment: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
	case "kafka":
		// No Deployment to manage for kafka integrations.
	}

	// Set Ready=True after successful reconcile.
	now := metav1.Now()
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "IntegrationReady",
		Message:            "Integration is configured and ready",
		ObservedGeneration: integration.Generation,
	}
	apimeta.SetStatusCondition(&integration.Status.Conditions, cond)
	integration.Status.LastReconciledTime = &now

	if err := r.Status().Update(ctx, &integration); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// validateIntegrationSpec validates the Integration spec and returns the first error found.
func validateIntegrationSpec(spec automationv1alpha1.IntegrationSpec) error {
	switch spec.Type {
	case "kafka":
		if spec.Kafka == nil {
			return fmt.Errorf("spec.kafka must be set when type=kafka")
		}
		if len(spec.Kafka.BootstrapServers) == 0 {
			return fmt.Errorf("spec.kafka.bootstrapServers must be non-empty when type=kafka")
		}
	case "plugin":
		if spec.Plugin == nil {
			return fmt.Errorf("spec.plugin must be set when type=plugin")
		}
		if spec.Plugin.Image == "" {
			return fmt.Errorf("spec.plugin.image must be non-empty when type=plugin")
		}
	default:
		return fmt.Errorf("unknown integration type %q", spec.Type)
	}
	return nil
}

// reconcilePluginDeployment ensures the plugin Deployment exists and is up to date.
func (r *IntegrationReconciler) reconcilePluginDeployment(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	desired := desiredPluginDeployment(integration)

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created plugin deployment", "deployment", desired.Name, "namespace", integration.Namespace)
		return nil
	}

	// Update image if it has drifted from the desired value.
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		c := &existing.Spec.Template.Spec.Containers[0]
		if c.Image != integration.Spec.Plugin.Image {
			c.Image = integration.Spec.Plugin.Image
			if err := r.Update(ctx, existing); err != nil {
				return err
			}
			log.Info("updated plugin deployment image", "deployment", desired.Name, "namespace", integration.Namespace)
		}
	}
	return nil
}

// desiredPluginDeployment returns the desired Deployment for a plugin Integration.
func desiredPluginDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	plugin := integration.Spec.Plugin
	publisherPort := plugin.PublisherPort
	if publisherPort == 0 {
		publisherPort = 8090
	}

	deploymentName := "kubezap-plugin-" + integration.Name
	labels := map[string]string{
		"app": deploymentName,
	}
	replicas := int32(1)

	envVars := []corev1.EnvVar{
		{Name: "KUBEZAP_NAMESPACE", Value: integration.Namespace},
		{Name: "KUBEZAP_INTEGRATION_NAME", Value: integration.Name},
		{Name: "KUBEZAP_PUBLISHER_PORT", Value: fmt.Sprintf("%d", publisherPort)},
		{Name: "KUBEZAP_LOG_LEVEL", Value: "info"},
	}
	// Append any user-specified env vars.
	envVars = append(envVars, plugin.Env...)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:  "plugin",
							Image: plugin.Image,
							Ports: []corev1.ContainerPort{
								{Name: "publisher", ContainerPort: publisherPort, Protocol: corev1.ProtocolTCP},
							},
							Env: envVars,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(publisherPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Integration{}).
		Named("integration").
		Complete(r)
}
