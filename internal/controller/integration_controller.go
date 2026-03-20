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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=integrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
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
	case "amqp":
		deploymentName, err := r.reconcileAmqpGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling amqp gateway: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
		existingDep := &appsv1.Deployment{}
		depKey := client.ObjectKey{Name: deploymentName, Namespace: integration.Namespace}
		var gatewayAvailCond metav1.Condition
		if err := r.Get(ctx, depKey, existingDep); err == nil && existingDep.Status.AvailableReplicas > 0 {
			gatewayAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionTrue,
				Reason:             "DeploymentAvailable",
				Message:            "AMQP gateway Deployment has available replicas",
				ObservedGeneration: integration.Generation,
			}
		} else {
			gatewayAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionFalse,
				Reason:             "DeploymentUnavailable",
				Message:            "AMQP gateway Deployment has no available replicas yet",
				ObservedGeneration: integration.Generation,
			}
		}
		apimeta.SetStatusCondition(&integration.Status.Conditions, gatewayAvailCond)
	case "nats":
		deploymentName, err := r.reconcileNatsGateway(ctx, &integration)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling nats gateway: %w", err)
		}
		integration.Status.GatewayDeploymentName = deploymentName
		existingDep2 := &appsv1.Deployment{}
		depKey2 := client.ObjectKey{Name: deploymentName, Namespace: integration.Namespace}
		var natsAvailCond metav1.Condition
		if err := r.Get(ctx, depKey2, existingDep2); err == nil && existingDep2.Status.AvailableReplicas > 0 {
			natsAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionTrue,
				Reason:             "DeploymentAvailable",
				Message:            "NATS gateway Deployment has available replicas",
				ObservedGeneration: integration.Generation,
			}
		} else {
			natsAvailCond = metav1.Condition{
				Type:               "GatewayAvailable",
				Status:             metav1.ConditionFalse,
				Reason:             "DeploymentUnavailable",
				Message:            "NATS gateway Deployment has no available replicas yet",
				ObservedGeneration: integration.Generation,
			}
		}
		apimeta.SetStatusCondition(&integration.Status.Conditions, natsAvailCond)
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
	case "amqp":
		if spec.Amqp == nil {
			return fmt.Errorf("spec.amqp must be set when type=amqp")
		}
		if spec.Amqp.URL == "" {
			return fmt.Errorf("spec.amqp.url must be non-empty")
		}
	case "nats":
		if spec.Nats == nil {
			return fmt.Errorf("spec.nats must be set when type=nats")
		}
		if len(spec.Nats.Servers) == 0 {
			return fmt.Errorf("spec.nats.servers must be non-empty")
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

// reconcilePluginRBAC ensures a ServiceAccount, Role, and RoleBinding exist for a plugin Integration
// and are kept up to date on every reconcile pass. All three resources are owner-referenced to the
// Integration so they are garbage-collected when the Integration is deleted.
func (r *IntegrationReconciler) reconcilePluginRBAC(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	resourceName := "kubezap-plugin-" + integration.Name

	desiredRules := []rbacv1.PolicyRule{
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
	}
	desiredRoleRef := rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     resourceName,
	}
	desiredSubjects := []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}

	// --- ServiceAccount ---
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		// ServiceAccount has no spec fields to reconcile beyond metadata/owner reference.
		return ctrl.SetControllerReference(integration, sa, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upserting plugin ServiceAccount: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin ServiceAccount", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	// --- Role ---
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err = controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		// Always overwrite Rules to pick up any permission changes.
		role.Rules = desiredRules
		return ctrl.SetControllerReference(integration, role, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("upserting plugin Role: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin Role", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	// --- RoleBinding ---
	// RoleRef is immutable after creation. If it has changed, delete and recreate.
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: integration.Namespace,
		},
	}
	result, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		// If the RoleBinding already exists with a different RoleRef we must delete and recreate
		// because RoleRef is immutable. Signal this by returning a typed sentinel.
		if rb.ResourceVersion != "" && rb.RoleRef != desiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = desiredRoleRef
		rb.Subjects = desiredSubjects
		return ctrl.SetControllerReference(integration, rb, r.Scheme)
	})
	if err != nil {
		if err == errRoleRefChanged {
			// Delete the old RoleBinding and create a fresh one with the correct RoleRef.
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("deleting stale plugin RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: integration.Namespace,
				},
				RoleRef:  desiredRoleRef,
				Subjects: desiredSubjects,
			}
			if err := ctrl.SetControllerReference(integration, rb, r.Scheme); err != nil {
				return fmt.Errorf("setting owner reference on recreated RoleBinding: %w", err)
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("recreating plugin RoleBinding: %w", err)
			}
			log.Info("recreated plugin RoleBinding (RoleRef changed)", "name", resourceName, "namespace", integration.Namespace)
		} else {
			return fmt.Errorf("upserting plugin RoleBinding: %w", err)
		}
	} else if result != controllerutil.OperationResultNone {
		log.Info("reconciled plugin RoleBinding", "name", resourceName, "namespace", integration.Namespace, "result", result)
	}

	return nil
}

// errRoleRefChanged is a sentinel error returned from a CreateOrUpdate mutate function when the
// existing RoleBinding's RoleRef does not match the desired value. Because RoleRef is immutable in
// Kubernetes, the RoleBinding must be deleted and recreated rather than updated.
var errRoleRefChanged = fmt.Errorf("rolebinding RoleRef has changed and must be recreated")

// reconcilePluginDeployment ensures the plugin Deployment exists and is up to date.
func (r *IntegrationReconciler) reconcilePluginDeployment(ctx context.Context, integration *automationv1alpha1.Integration) error {
	log := logf.FromContext(ctx)
	desired := desiredPluginDeployment(integration)

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec from the helper so that env vars,
		// security contexts, resource limits, probes, args, and service account
		// all stay in sync and do not drift silently.
		desired.Spec = desiredPluginDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update plugin deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled plugin deployment", "deployment", desired.Name, "namespace", integration.Namespace, "result", op)
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

	// Ensure ServiceAccount (shared kubezap-gateway SA with amqp/nats gateways).
	// No owner reference: shared across all broker-type integrations in the namespace.
	// Setting an owner ref to this Integration would GC the SA when this Integration
	// is deleted, even if amqp or nats integrations still exist and need the SA.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting kafka gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	kafkaGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"triggers"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"integrations"}, Verbs: []string{"get"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"flowruns"}, Verbs: []string{"create"}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = kafkaGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting kafka gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	// RoleRef is immutable — if it has changed the binding must be deleted and recreated.
	kafkaDesiredRoleRef := rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "kubezap-gateway"}
	kafkaDesiredSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: "kubezap-gateway", Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != kafkaDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = kafkaDesiredRoleRef
		rb.Subjects = kafkaDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale kafka gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns},
				RoleRef:    kafkaDesiredRoleRef,
				Subjects:   kafkaDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating kafka gateway RoleBinding: %w", err)
			}
			log.Info("recreated kafka gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting kafka gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredKafkaGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on kafka gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredKafkaGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update kafka gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled kafka gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}
	return deploymentName, nil
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
							Args:  []string{"--namespace=" + integration.Namespace},
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

// kafkaTopicCGPair holds a single (topic, consumerGroup) pair for KEDA ScaledObject trigger construction.
type kafkaTopicCGPair struct {
	topic string
	cg    string
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

	// Collect unique (topic, consumerGroup) pairs — one per Trigger, not a Cartesian product.
	pairSet := make(map[kafkaTopicCGPair]struct{})

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
		cg := ps.ConsumerGroup
		if cg == "" {
			cg = "kubezap-" + trigger.Name
		}
		pairSet[kafkaTopicCGPair{topic: ps.Topic, cg: cg}] = struct{}{}
	}

	pairs := make([]kafkaTopicCGPair, 0, len(pairSet))
	for p := range pairSet {
		pairs = append(pairs, p)
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
				"triggers":        buildKafkaTriggers(integration, pairs),
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
// Each pair is one (topic, consumerGroup) from a distinct Trigger — no Cartesian product.
func buildKafkaTriggers(integration *automationv1alpha1.Integration, pairs []kafkaTopicCGPair) []interface{} {
	brokers := strings.Join(integration.Spec.Kafka.BootstrapServers, ",")
	var triggers []interface{}
	for _, p := range pairs {
		triggers = append(triggers, map[string]interface{}{
			"type": "kafka",
			"metadata": map[string]interface{}{
				"brokerList":    brokers,
				"consumerGroup": p.cg,
				"topic":         p.topic,
				"lagThreshold":  "10",
			},
		})
	}
	return triggers
}

// reconcileAmqpGateway ensures the AMQP gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileAmqpGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount (shared kubezap-gateway SA with kafka/nats gateways).
	// No owner reference: this SA is shared across all broker-type integrations in the
	// namespace. Setting an owner ref to a single Integration would GC the SA when that
	// Integration is deleted, even if other broker integrations still exist.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting amqp gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	amqpGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"triggers"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"integrations"}, Verbs: []string{"get"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"flowruns"}, Verbs: []string{"create"}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = amqpGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting amqp gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	amqpDesiredRoleRef := rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "kubezap-gateway"}
	amqpDesiredSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: "kubezap-gateway", Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != amqpDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = amqpDesiredRoleRef
		rb.Subjects = amqpDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale amqp gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns},
				RoleRef:    amqpDesiredRoleRef,
				Subjects:   amqpDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating amqp gateway RoleBinding: %w", err)
			}
			log.Info("recreated amqp gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting amqp gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredAmqpGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on amqp gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredAmqpGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update amqp gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled amqp gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}
	return deploymentName, nil
}

// desiredAmqpGatewayDeployment returns the desired Deployment for an amqp Integration.
func desiredAmqpGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("AMQP_GATEWAY_IMAGE")
	if image == "" {
		image = "kubezap/amqp-gateway:latest"
	}

	deploymentName := "kubezap-amqp-gateway-" + integration.Name
	labels := map[string]string{
		"app":                  deploymentName,
		"kubezap.io/component": "amqp-gateway",
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
							Name:  "amqp-gateway",
							Image: image,
							Args:  []string{"--namespace=" + integration.Namespace},
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

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.Integration{}).
		Named("integration").
		Complete(r)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reconcileNatsGateway ensures the NATS gateway ServiceAccount, Role, RoleBinding, and
// Deployment exist and are up to date. It returns the Deployment name.
func (r *IntegrationReconciler) reconcileNatsGateway(ctx context.Context, integration *automationv1alpha1.Integration) (string, error) {
	log := logf.FromContext(ctx)

	ns := integration.Namespace

	// Ensure ServiceAccount (shared kubezap-gateway SA with kafka/amqp gateways).
	// No owner reference: shared across all broker-type integrations in the namespace.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	saResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting nats gateway ServiceAccount: %w", err)
	}
	if saResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway ServiceAccount", "namespace", ns, "result", saResult)
	}

	// Ensure Role (shared kubezap-gateway Role).
	natsGatewayRules := []rbacv1.PolicyRule{
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"triggers"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"integrations"}, Verbs: []string{"get"}},
		{APIGroups: []string{"automation.kubezap.io"}, Resources: []string{"flowruns"}, Verbs: []string{"create"}},
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	roleResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = natsGatewayRules
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("upserting nats gateway Role: %w", err)
	}
	if roleResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway Role", "namespace", ns, "result", roleResult)
	}

	// Ensure RoleBinding (shared kubezap-gateway RoleBinding).
	natsDesiredRoleRef := rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "kubezap-gateway"}
	natsDesiredSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: "kubezap-gateway", Namespace: ns}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns}}
	rbResult, rbErr := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.ResourceVersion != "" && rb.RoleRef != natsDesiredRoleRef {
			return errRoleRefChanged
		}
		rb.RoleRef = natsDesiredRoleRef
		rb.Subjects = natsDesiredSubjects
		return nil
	})
	if rbErr != nil {
		if rbErr == errRoleRefChanged {
			if delErr := r.Delete(ctx, rb); delErr != nil && !apierrors.IsNotFound(delErr) {
				return "", fmt.Errorf("deleting stale nats gateway RoleBinding: %w", delErr)
			}
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "kubezap-gateway", Namespace: ns},
				RoleRef:    natsDesiredRoleRef,
				Subjects:   natsDesiredSubjects,
			}
			if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
				return "", fmt.Errorf("recreating nats gateway RoleBinding: %w", err)
			}
			log.Info("recreated nats gateway RoleBinding (RoleRef changed)", "namespace", ns)
		} else {
			return "", fmt.Errorf("upserting nats gateway RoleBinding: %w", rbErr)
		}
	} else if rbResult != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway RoleBinding", "namespace", ns, "result", rbResult)
	}

	desired := desiredNatsGatewayDeployment(integration)

	if err := ctrl.SetControllerReference(integration, desired, r.Scheme); err != nil {
		return "", fmt.Errorf("setting owner reference on nats gateway Deployment: %w", err)
	}

	deploymentName := desired.Name
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		// desired is populated with the live object by CreateOrUpdate before this func
		// is called. Overwrite the full spec so that env vars, security contexts,
		// resource limits, probes, args, and service account do not drift silently.
		desired.Spec = desiredNatsGatewayDeployment(integration).Spec
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to create/update nats gateway deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled nats gateway deployment", "deployment", deploymentName, "namespace", integration.Namespace, "result", op)
	}
	return deploymentName, nil
}

// desiredNatsGatewayDeployment returns the desired Deployment for a nats Integration.
func desiredNatsGatewayDeployment(integration *automationv1alpha1.Integration) *appsv1.Deployment {
	image := os.Getenv("NATS_GATEWAY_IMAGE")
	if image == "" {
		image = "kubezap/nats-gateway:latest"
	}

	deploymentName := "kubezap-nats-gateway-" + integration.Name
	labels := map[string]string{
		"app":                  deploymentName,
		"kubezap.io/component": "nats-gateway",
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
							Name:  "nats-gateway",
							Image: image,
							Args:  []string{"--namespace=" + integration.Namespace},
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
