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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// webhookGatewayConfigName is the only name a WebhookGatewayConfig object may
// have — enforced by that type's XValidation rule, not by an admission
// webhook. See docs/design/webhookgatewayconfig-singleton-name.md.
const webhookGatewayConfigName = "default"

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=webhookgatewayconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=webhookgatewayconfigs/status,verbs=get;update

const (
	webhookGatewayDeploymentName = "kubezap-webhook-gateway"
	webhookGatewayPort           = int32(8080)
	// webhookGatewayMetricsPort must match cmd/webhook-gateway/main.go's
	// --metrics-port default (:9090), which is what the binary actually
	// listens on for /metrics — see docs/guides/observability.md.
	webhookGatewayMetricsPort = int32(9090)
)

// portNameMetrics names the metrics container/Service port exposed by the
// webhook gateway and kafka gateway Deployments/Services, matched by the
// ServiceMonitor examples in docs/guides/observability.md.
const portNameMetrics = "metrics"

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
// existing at all. Only a non-nil MinAvailable opts a namespace in.
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
// none exists. The singleton-per-namespace invariant is enforced by that type's
// XValidation rule requiring the name "default" — Kubernetes' own per-(namespace, name)
// uniqueness then guarantees there is at most one, so a Get is sufficient here.
//
// Called once per reconcile from ensureWebhookGateway (internal/controller/trigger_controller.go),
// which feeds the result into desiredWebhookGatewayHPAFromConfig, desiredWebhookGatewayPDB,
// and the gateway's TLS configuration (spec.tls) — see that function's doc comment.
func getWebhookGatewayConfig(ctx context.Context, c client.Client, namespace string) (*automationv1alpha1.WebhookGatewayConfig, error) {
	cfg := &automationv1alpha1.WebhookGatewayConfig{}
	key := client.ObjectKey{Namespace: namespace, Name: webhookGatewayConfigName}
	if err := c.Get(ctx, key, cfg); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting WebhookGatewayConfig %s/%s: %w", namespace, webhookGatewayConfigName, err)
	}
	return cfg, nil
}

// reconcileWebhookGatewayConfigStatus validates cfg.Spec.TLS's Secret references and
// writes a Ready condition onto cfg.Status.Conditions reflecting the result. This is the
// only place WebhookGatewayConfig's status is ever written — there is no dedicated
// WebhookGatewayConfig controller (see docs/api/webhookgatewayconfig.md's Limitations
// section); it is called from ensureWebhookGateway, which already reads cfg once per
// Trigger reconcile, rather than standing up a second controller for this alone.
//
// A status-update failure is logged, not returned — this is best-effort observability,
// not load-bearing for the gateway Deployment/Service/HPA reconciliation ensureWebhookGateway
// actually depends on, and must not fail a Trigger's reconcile.
func reconcileWebhookGatewayConfigStatus(
	ctx context.Context, c client.Client, cfg *automationv1alpha1.WebhookGatewayConfig,
) {
	log := logf.FromContext(ctx)

	cond := validateWebhookGatewayConfigTLS(ctx, c, cfg)
	cond.ObservedGeneration = cfg.Generation
	apimeta.SetStatusCondition(&cfg.Status.Conditions, cond)

	if err := c.Status().Update(ctx, cfg); err != nil {
		log.Error(err, "failed to update WebhookGatewayConfig status",
			"namespace", cfg.Namespace, "name", cfg.Name)
	}
}

// validateWebhookGatewayConfigTLS checks that cfg.Spec.TLS's Secret references exist and
// contain the keys ensureWebhookGateway's Deployment mount expects
// (ServerSecretRef: tls.crt/tls.key; ClientCASecretRef: ca.crt), returning a Ready
// condition describing the result. It does not check whether ClientCASecretRef is set
// without ServerSecretRef — that combination is a documented no-op
// (docs/api/webhookgatewayconfig.md), not an error.
func validateWebhookGatewayConfigTLS(
	ctx context.Context, c client.Client, cfg *automationv1alpha1.WebhookGatewayConfig,
) metav1.Condition {
	if cfg.Spec.TLS == nil {
		return metav1.Condition{
			Type:    conditionTypeReady,
			Status:  metav1.ConditionTrue,
			Reason:  "NoTLSConfigured",
			Message: "spec.tls is not set; the webhook gateway serves plain HTTP.",
		}
	}

	tlsSpec := cfg.Spec.TLS
	if tlsSpec.ServerSecretRef != nil {
		secret := &corev1.Secret{}
		key := client.ObjectKey{Namespace: cfg.Namespace, Name: tlsSpec.ServerSecretRef.Name}
		if err := c.Get(ctx, key, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return metav1.Condition{
					Type:    conditionTypeReady,
					Status:  metav1.ConditionFalse,
					Reason:  "ServerSecretNotFound",
					Message: fmt.Sprintf("spec.tls.serverSecretRef %q not found in namespace %q.", tlsSpec.ServerSecretRef.Name, cfg.Namespace),
				}
			}
			return metav1.Condition{
				Type:    conditionTypeReady,
				Status:  metav1.ConditionFalse,
				Reason:  "ServerSecretGetFailed",
				Message: fmt.Sprintf("getting spec.tls.serverSecretRef %q: %s", tlsSpec.ServerSecretRef.Name, err.Error()),
			}
		}
		if len(secret.Data["tls.crt"]) == 0 || len(secret.Data["tls.key"]) == 0 {
			return metav1.Condition{
				Type:    conditionTypeReady,
				Status:  metav1.ConditionFalse,
				Reason:  "ServerSecretMissingKeys",
				Message: fmt.Sprintf("spec.tls.serverSecretRef %q must contain both tls.crt and tls.key.", tlsSpec.ServerSecretRef.Name),
			}
		}

		if tlsSpec.ClientCASecretRef != nil {
			caSecret := &corev1.Secret{}
			caKey := client.ObjectKey{Namespace: cfg.Namespace, Name: tlsSpec.ClientCASecretRef.Name}
			if err := c.Get(ctx, caKey, caSecret); err != nil {
				if apierrors.IsNotFound(err) {
					return metav1.Condition{
						Type:    conditionTypeReady,
						Status:  metav1.ConditionFalse,
						Reason:  "ClientCASecretNotFound",
						Message: fmt.Sprintf("spec.tls.clientCASecretRef %q not found in namespace %q.", tlsSpec.ClientCASecretRef.Name, cfg.Namespace),
					}
				}
				return metav1.Condition{
					Type:    conditionTypeReady,
					Status:  metav1.ConditionFalse,
					Reason:  "ClientCASecretGetFailed",
					Message: fmt.Sprintf("getting spec.tls.clientCASecretRef %q: %s", tlsSpec.ClientCASecretRef.Name, err.Error()),
				}
			}
			if len(caSecret.Data["ca.crt"]) == 0 {
				return metav1.Condition{
					Type:    conditionTypeReady,
					Status:  metav1.ConditionFalse,
					Reason:  "ClientCASecretMissingKey",
					Message: fmt.Sprintf("spec.tls.clientCASecretRef %q must contain ca.crt.", tlsSpec.ClientCASecretRef.Name),
				}
			}
		}
	}

	return metav1.Condition{
		Type:    conditionTypeReady,
		Status:  metav1.ConditionTrue,
		Reason:  "WebhookGatewayConfigReady",
		Message: "spec.tls Secret references resolved successfully.",
	}
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
				{
					Name:       portNameMetrics,
					Protocol:   corev1.ProtocolTCP,
					Port:       webhookGatewayMetricsPort,
					TargetPort: intstr.FromInt32(webhookGatewayMetricsPort),
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
					DefaultMode: ptr.To(int32(0444)),
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
						DefaultMode: ptr.To(int32(0444)),
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
							Env: append([]corev1.EnvVar{
								{Name: envVarWatchNamespaces, Value: os.Getenv(envVarWatchNamespaces)},
							}, otelPassthroughEnv()...),
							Ports: []corev1.ContainerPort{
								{Name: portName, ContainerPort: webhookGatewayPort, Protocol: corev1.ProtocolTCP},
								{Name: portNameMetrics, ContainerPort: webhookGatewayMetricsPort, Protocol: corev1.ProtocolTCP},
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
