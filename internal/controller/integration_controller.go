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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;patch;delete

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
		if err := r.reconcilePluginRBAC(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin RBAC: %w", err)
		}
		if err := r.reconcilePluginDeployment(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling plugin deployment: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
	case "kafka":
		deploymentName, err := r.reconcileKafkaGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling kafka gateway: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName

		if err := r.reconcileKafkaScaledObject(ctx, &integration); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling kafka scaledobject: %w", err)
		}

		// Set GatewayAvailable condition based on Deployment available replicas.
		existingDep := &appsv1.Deployment{}
		depKey := client.ObjectKey{Name: deploymentName, Namespace: integration.Namespace}
		var gatewayAvailCond metav1.Condition
		if err := r.Get(ctx, depKey, existingDep); err == nil && existingDep.Status.AvailableReplicas > 0 {
			gatewayAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionTrue,
				Reason:             "DeploymentAvailable",
				Message:            "Kafka gateway Deployment has available replicas",
				ObservedGeneration: integration.Generation,
			}
		} else {
			gatewayAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionFalse,
				Reason:             "DeploymentUnavailable",
				Message:            "Kafka gateway Deployment has no available replicas yet",
				ObservedGeneration: integration.Generation,
			}
		}
		apimeta.SetStatusCondition(&integration.Status.Conditions, gatewayAvailCond)
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

// reconcilePluginRBAC ensures a ServiceAccount, Role, and RoleBinding exist for a plugin Integration.
// All three resources are owner-referenced to the Integration so they are garbage-collected when
// the Integration is deleted.
func (r *IntegrationReconciler) reconcilePluginRBAC(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	resourceName := "kubezap-plugin-" + integration.Name

	// --- ServiceAccount ---
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	if err := ctrl.SetControllerReference(integration, sa, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ServiceAccount: %w", err)
	}
	existingSA := &corev1.ServiceAccount{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(sa), existingSA); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created plugin ServiceAccount", "name", resourceName, "namespace", integration.Namespace)
	}

	// --- Role ---
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"automation.kubezap.io"},
				Resources: []string{"triggers"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"automation.kubezap.io"},
				Resources: []string{"flowruns"},
				Verbs:     []string{"get", "list", "create", "update", "patch"},
			},
		},
	}
	if err := ctrl.SetControllerReference(integration, role, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Role: %w", err)
	}
	existingRole := &rbacv1.Role{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(role), existingRole); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, role); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created plugin Role", "name", resourceName, "namespace", integration.Namespace)
	}

	// --- RoleBinding ---
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     resourceName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      resourceName,
				Namespace: integration.Namespace,
			},
		},
	}
	if err := ctrl.SetControllerReference(integration, rb, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on RoleBinding: %w", err)
	}
	existingRB := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rb), existingRB); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info("created plugin RoleBinding", "name", resourceName, "namespace", integration.Namespace)
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

	// NOTE: The operator does not auto-grant the plugin Deployment permission to read these
	// Secrets. The cluster administrator must create a Role + RoleBinding granting
	// the plugin ServiceAccount access to the referenced Secrets.

	// Inject secret-derived env vars from spec.plugin.secretRefs
	for _, secretRef := range plugin.SecretRefs {
		for secretKey, envVarName := range secretRef.EnvVarMappings {
			envVars = append(envVars, corev1.EnvVar{
				Name: envVarName,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretRef.SecretName},
						Key:                  secretKey,
					},
				},
			})
		}
	}

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
					ServiceAccountName: deploymentName,
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

// reconcileKafkaGateway ensures the Kafka gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileKafkaGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	if err := r.Get(ctx, client.ObjectKeyFromObject(sa), sa); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("creating kafka gateway ServiceAccount: %w", err)
		}
		log.Info("created kafka gateway ServiceAccount", "namespace", ns)
	} else if err != nil {
		return "", err
	}

	// Ensure Role.
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"triggers"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"flowruns"}, Verbs: []string{"create"}},
		},
	}
	existingRole := &rbacv1.Role{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(role), existingRole); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, role); err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("creating kafka gateway Role: %w", err)
		}
	} else if err != nil {
		return "", err
	} else {
		existingRole.Rules = role.Rules
		if err := r.Update(ctx, existingRole); err != nil {
			return "", fmt.Errorf("updating kafka gateway Role: %w", err)
		}
	}

	// Ensure RoleBinding.
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "kubezap-gateway"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "kubezap-gateway", Namespace: ns}},
	}
	existingRB := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(rb), existingRB); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("creating kafka gateway RoleBinding: %w", err)
		}
	} else if err != nil {
		return "", err
	}

	desired := desiredKafkaGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on kafka gateway Deployment: %w", err)
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return "", err
		}
		if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return "", err
		}
		log.Info("created kafka gateway deployment", "deployment", desired.Name, "namespace", integration.Namespace)
		return desired.Name, nil
	}

	// Update image if it has drifted from the desired value.
	desiredImage := desired.Spec.Template.Spec.Containers[0].Image
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		c := &existing.Spec.Template.Spec.Containers[0]
		if c.Image != desiredImage {
			c.Image = desiredImage
			if err := r.Update(ctx, existing); err != nil {
				return "", err
			}
			log.Info("updated kafka gateway deployment image", "deployment", desired.Name, "namespace", integration.Namespace)
		}
	}
	return desired.Name, nil
}

// desiredKafkaGatewayDeployment returns the desired Deployment for a kafka Integration.
func desiredKafkaGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("KAFKA_GATEWAY_IMAGE")
	if image == "" {
		image = "kubezap/kafka-gateway:latest"
	}

	deploymentName := "kubezap-kafka-gateway-" + integration.Name
	labels := map[string]string{
		"app":                  deploymentName,
		"kubezap.io/component": "kafka-gateway",
	}

	envVars := []corev1.EnvVar{
		{Name: "WATCH_NAMESPACES", Value: os.Getenv("WATCH_NAMESPACES")},
		{Name: "KUBEZAP_NAMESPACE", Value: integration.Namespace},
		{Name: "KUBEZAP_INTEGRATION_NAME", Value: integration.Name},
		{Name: "LOG_LEVEL", Value: "info"},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: integration.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: "kubezap-gateway",
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:  "kafka-gateway",
							Image: image,
							Env:   envVars,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("200m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			},
		},
	}
}

// reconcileKafkaScaledObject ensures a KEDA ScaledObject exists for the Kafka gateway Deployment.
// If KEDA is not installed, the function logs a warning and returns nil (graceful degradation).
func (r *IntegrationReconciler) reconcileKafkaScaledObject(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)

	// List all Triggers in this namespace and filter for kafka pubsub ones that reference this Integration.
	triggerList := &automationv1alpha1.TriggerList{}
	if err := r.List(ctx, triggerList, client.InNamespace(integration.Namespace)); err != nil {
		return fmt.Errorf("listing triggers: %w", err)
	}

	topicSet := make(map[string]struct{})
	cgSet := make(map[string]struct{})

	for _, trigger := range triggerList.Items {
		if trigger.Spec.Type != "pubsub" {
			continue
		}
		ps := trigger.Spec.PubSub
		if ps == nil {
			continue
		}
		if ps.Type != "kafka" {
			continue
		}
		if ps.IntegrationRef.Name != integration.Name {
			continue
		}
		topicSet[ps.Topic] = struct{}{}
		cg := ps.ConsumerGroup
		if cg == "" {
			cg = "kubezap-" + trigger.Name
		}
		cgSet[cg] = struct{}{}
	}

	topics := make([]string, 0, len(topicSet))
	for t := range topicSet {
		topics = append(topics, t)
	}
	consumerGroups := make([]string, 0, len(cgSet))
	for cg := range cgSet {
		consumerGroups = append(consumerGroups, cg)
	}

	scaledObjName := "kubezap-kafka-gateway-" + integration.Name

	scaledObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "keda.sh/v1alpha1",
			"kind":       "ScaledObject",
			"metadata": map[string]interface{}{
				"name":      scaledObjName,
				"namespace": integration.Namespace,
			},
			"spec": map[string]interface{}{
				"scaleTargetRef": map[string]interface{}{
					"name": scaledObjName,
				},
				"minReplicaCount": int64(0),
				"maxReplicaCount": int64(10),
				"triggers":        buildKafkaTriggers(integration, topics, consumerGroups),
			},
		},
	}
	scaledObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObject",
	})

	if err := ctrl.SetControllerReference(integration, scaledObj, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ScaledObject: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObject",
	})

	err := r.Get(ctx, client.ObjectKey{Name: scaledObjName, Namespace: integration.Namespace}, existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			// Check if KEDA CRD is missing (not installed).
			if apimeta.IsNoMatchError(err) || strings.Contains(err.Error(), "no kind is registered") {
				log.Info("KEDA not installed, skipping ScaledObject", "integration", integration.Name)
				return nil
			}
			return fmt.Errorf("getting ScaledObject: %w", err)
		}
		// NotFound — create it.
		if createErr := r.Create(ctx, scaledObj); createErr != nil {
			if apimeta.IsNoMatchError(createErr) || strings.Contains(createErr.Error(), "no kind is registered") {
				log.Info("KEDA not installed, skipping ScaledObject", "integration", integration.Name)
				return nil
			}
			if !apierrors.IsAlreadyExists(createErr) {
				return fmt.Errorf("creating ScaledObject: %w", createErr)
			}
		}
		log.Info("created KEDA ScaledObject", "name", scaledObjName, "namespace", integration.Namespace)
		return nil
	}

	// Already exists — update the spec.
	scaledObj.SetResourceVersion(existing.GetResourceVersion())
	if updateErr := r.Update(ctx, scaledObj); updateErr != nil {
		return fmt.Errorf("updating ScaledObject: %w", updateErr)
	}
	log.Info("updated KEDA ScaledObject", "name", scaledObjName, "namespace", integration.Namespace)
	return nil
}

// buildKafkaTriggers constructs the KEDA trigger entries for a ScaledObject.
func buildKafkaTriggers(integration *automationv1alpha1.Integration, topics []string, consumerGroups []string) []interface{} {
	brokers := strings.Join(integration.Spec.Kafka.BootstrapServers, ",")
	var triggers []interface{}
	for _, topic := range topics {
		for _, cg := range consumerGroups {
			triggers = append(triggers, map[string]interface{}{
				"type": "kafka",
				"metadata": map[string]interface{}{
					"brokerList":    brokers,
					"consumerGroup": cg,
					"topic":         topic,
					"lagThreshold":  "10",
				},
			})
		}
	}
	return triggers
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Integration{}).
		Named("integration").
		Complete(r)
}
