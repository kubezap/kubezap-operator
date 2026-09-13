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

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=webhookgatewayconfigs,verbs=get;list;watch

const (
	webhookGatewayDeploymentName = "kubezap-webhook-gateway"
	webhookGatewayPort           = int32(8080)
)

// componentWebhookGateway is the "webhook-gateway" value used for both the
// labelComponent label and the gateway container/ServiceAccount name suffix.
const componentWebhookGateway = "webhook-gateway"

// labelNamespace is the kubezap.io/namespace label key recording which
// namespace a webhook gateway resource belongs to.
const labelNamespace = "kubezap.io/namespace"

// portNameHTTP/portNameHTTPS name the single container/service port exposed by
// the webhook gateway and http-executor Deployments/Services, and the URL
// scheme returned by executorScheme — all mean "plain HTTP" vs "TLS-terminated".
const (
	portNameHTTP  = "http"
	portNameHTTPS = "https"
)

// WebhookGatewayTLSConfig carries TLS/mTLS configuration for the webhook gateway Deployment.
// An empty struct means plain HTTP with no TLS termination.
type WebhookGatewayTLSConfig struct {
	// TLSSecretName is the name of the Secret in the gateway namespace containing
	// tls.crt and tls.key (cert-manager compatible). Empty means plain HTTP.
	TLSSecretName string

	// MTLSCASecretName is the name of the Secret containing ca.crt used to verify
	// client certificates. Only effective when TLSSecretName is also set.
	MTLSCASecretName string
}

func webhookGatewayImage() string {
	if img := os.Getenv("WEBHOOK_GATEWAY_IMAGE"); img != "" {
		return img
	}
	return "ghcr.io/kubezap/webhook-gateway:latest"
}

// desiredWebhookGatewayServiceAccount returns the desired ServiceAccount for the webhook gateway.
func desiredWebhookGatewayServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels:    map[string]string{labelApp: webhookGatewayDeploymentName},
		},
	}
}

// desiredWebhookGatewayRole returns the desired Role for the webhook gateway.
func desiredWebhookGatewayRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{apiGroupAutomation},
				Resources: []string{resourceTriggers},
				Verbs:     []string{verbGet, verbList, verbWatch},
			},
			{
				APIGroups: []string{apiGroupAutomation},
				Resources: []string{resourceFlowRuns},
				Verbs:     []string{verbCreate},
			},
			{
				// Required to resolve Trigger webhook auth secrets (HMAC, bearer,
				// basic, apiKey, header-equals) at route-registration time, and to
				// watch them so a rotated secret's new value is picked up without
				// waiting for the referencing Trigger to be reconciled again.
				APIGroups: []string{""},
				Resources: []string{resourceSecrets},
				Verbs:     []string{verbGet, verbList, verbWatch},
			},
		},
	}
}

// desiredWebhookGatewayRoleBinding returns the desired RoleBinding for the webhook gateway.
func desiredWebhookGatewayRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: apiGroupRBAC,
			Kind:     kindRole,
			Name:     webhookGatewayDeploymentName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      kindServiceAccount,
				Name:      webhookGatewayDeploymentName,
				Namespace: namespace,
			},
		},
	}
}

// desiredWebhookGatewayHPA returns the desired HorizontalPodAutoscaler for the webhook
// gateway Deployment in the given namespace. It targets CPU utilization at 70% with a
// min of 1 and max of 10 replicas.
//
// This is a thin wrapper around desiredWebhookGatewayHPAFromConfig with a nil config,
// preserved so existing callers (and their compiled behavior) are unaffected by the
// addition of WebhookGatewayConfig support.
func desiredWebhookGatewayHPA(namespace string) *autoscalingv2.HorizontalPodAutoscaler {
	return desiredWebhookGatewayHPAFromConfig(namespace, nil)
}

// desiredWebhookGatewayHPAFromConfig returns the desired HorizontalPodAutoscaler for the
// webhook gateway Deployment in the given namespace, reading MinReplicas, MaxReplicas, and
// TargetCPUUtilization from cfg.Spec.HPA when cfg is non-nil.
//
// A nil cfg, a cfg with a nil Spec.HPA, or a Spec.HPA with individual nil fields all fall
// back — per field — to today's exact hardcoded defaults (min=1, max=10, target-CPU=70%),
// so a namespace with no WebhookGatewayConfig (or one that leaves HPA fields unset)
// reconciles byte-identically to before WebhookGatewayConfig existed.
func desiredWebhookGatewayHPAFromConfig(namespace string, cfg *automationv1alpha1.WebhookGatewayConfig) *autoscalingv2.HorizontalPodAutoscaler {
	cpuUtilization := int32(70)
	minReplicas := int32(1)
	maxReplicas := int32(10)

	if cfg != nil && cfg.Spec.HPA != nil {
		hpa := cfg.Spec.HPA
		if hpa.MinReplicas != nil {
			minReplicas = *hpa.MinReplicas
		}
		if hpa.MaxReplicas != nil {
			maxReplicas = *hpa.MaxReplicas
		}
		if hpa.TargetCPUUtilization != nil {
			cpuUtilization = *hpa.TargetCPUUtilization
		}
	}

	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				labelComponent: componentWebhookGateway,
				labelNamespace: namespace,
			},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       webhookGatewayDeploymentName,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &cpuUtilization,
						},
					},
				},
			},
		},
	}
}

// desiredWebhookGatewayPDB returns the desired PodDisruptionBudget for the webhook gateway
// Deployment in the given namespace, or nil when no PodDisruptionBudget should exist.
//
// A nil cfg, a cfg with a nil Spec.PodDisruptionBudget, or a Spec.PodDisruptionBudget with a
// nil MinAvailable all return nil — matching today's actual behavior of no PodDisruptionBudget
// existing at all. Only a non-nil MinAvailable opts a namespace in, per
// docs/design/2026-09-12-webhookgatewayconfig-crd.md.
//
// The returned PodDisruptionBudget targets the same pod selector as the webhook gateway
// Deployment's pod template (see desiredWebhookGatewayDeployment).
func desiredWebhookGatewayPDB(namespace string, cfg *automationv1alpha1.WebhookGatewayConfig) *policyv1.PodDisruptionBudget {
	if cfg == nil || cfg.Spec.PodDisruptionBudget == nil || cfg.Spec.PodDisruptionBudget.MinAvailable == nil {
		return nil
	}

	labels := map[string]string{
		labelComponent: componentWebhookGateway,
		labelNamespace: namespace,
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: cfg.Spec.PodDisruptionBudget.MinAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}

// getWebhookGatewayConfig returns the namespace's WebhookGatewayConfig object, or nil if
// none exists. The singleton-per-namespace invariant (at most one object, any name) is
// enforced at admission time by the WebhookGatewayConfig validating webhook — this helper
// simply returns the first (and, per that invariant, only) item found.
//
// Called once per reconcile from ensureWebhookGateway (internal/controller/trigger_controller.go),
// which feeds the result into desiredWebhookGatewayHPAFromConfig, desiredWebhookGatewayPDB,
// and the gateway's TLS configuration (spec.tls) — see that function's doc comment.
func getWebhookGatewayConfig(ctx context.Context, c client.Client, namespace string) (*automationv1alpha1.WebhookGatewayConfig, error) {
	var list automationv1alpha1.WebhookGatewayConfigList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing WebhookGatewayConfig in namespace %s: %w", namespace, err)
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

// desiredWebhookGatewayService returns the desired ClusterIP service for the webhook gateway.
func desiredWebhookGatewayService(namespace string, tlsCfg WebhookGatewayTLSConfig) *corev1.Service {
	labels := map[string]string{
		labelComponent: componentWebhookGateway,
		labelNamespace: namespace,
	}

	portName := portNameHTTP
	if tlsCfg.TLSSecretName != "" {
		portName = portNameHTTPS
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       portName,
					Protocol:   corev1.ProtocolTCP,
					Port:       webhookGatewayPort,
					TargetPort: intstr.FromInt32(webhookGatewayPort),
				},
			},
		},
	}
}

// desiredWebhookGatewayDeployment returns the desired state of the webhook gateway
// Deployment for the given namespace. The caller is responsible for setting owner
// references and calling CreateOrUpdate.
func desiredWebhookGatewayDeployment(namespace string, tlsCfg WebhookGatewayTLSConfig) *appsv1.Deployment {
	labels := map[string]string{
		labelComponent: componentWebhookGateway,
		labelNamespace: namespace,
	}
	replicas := int32(1)

	// Build args, volume mounts, and volumes conditionally based on TLS config.
	args := []string{
		"--port=8080",
		"--namespace=" + namespace,
	}
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume

	portName := portNameHTTP
	probeScheme := corev1.URISchemeHTTP

	if tlsCfg.TLSSecretName != "" {
		portName = portNameHTTPS
		probeScheme = corev1.URISchemeHTTPS

		args = append(args,
			"--tls-cert-file=/etc/webhook-tls/tls.crt",
			"--tls-key-file=/etc/webhook-tls/tls.key",
		)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "webhook-tls",
			MountPath: "/etc/webhook-tls",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "webhook-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  tlsCfg.TLSSecretName,
					DefaultMode: ptr.To(int32(0400)),
				},
			},
		})

		if tlsCfg.MTLSCASecretName != "" {
			args = append(args, "--mtls-ca-file=/etc/webhook-mtls-ca/ca.crt")
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "webhook-mtls-ca",
				MountPath: "/etc/webhook-mtls-ca",
				ReadOnly:  true,
			})
			volumes = append(volumes, corev1.Volume{
				Name: "webhook-mtls-ca",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  tlsCfg.MTLSCASecretName,
						DefaultMode: ptr.To(int32(0400)),
					},
				},
			})
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: webhookGatewayDeploymentName,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Volumes: volumes,
					Containers: []corev1.Container{
						{
							Name:            componentWebhookGateway,
							Image:           webhookGatewayImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							Ports: []corev1.ContainerPort{
								{Name: portName, ContainerPort: webhookGatewayPort, Protocol: corev1.ProtocolTCP},
							},
							VolumeMounts: volumeMounts,
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   healthzPath,
										Port:   intstr.FromInt32(webhookGatewayPort),
										Scheme: probeScheme,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/readyz",
										Port:   intstr.FromInt32(webhookGatewayPort),
										Scheme: probeScheme,
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
							},
						},
					},
				},
			},
		},
	}
}
