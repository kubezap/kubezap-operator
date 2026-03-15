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
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	webhookGatewayDeploymentName = "kubezap-webhook-gateway"
	webhookGatewayImage          = "kubezap/webhook-gateway:latest"
	webhookGatewayPort           = int32(8080)
)

// desiredWebhookGatewayServiceAccount returns the desired ServiceAccount for the webhook gateway.
func desiredWebhookGatewayServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubezap-webhook-gateway",
			Namespace: namespace,
			Labels:    map[string]string{"app": "kubezap-webhook-gateway"},
		},
	}
}

// desiredWebhookGatewayRole returns the desired Role for the webhook gateway.
func desiredWebhookGatewayRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubezap-webhook-gateway",
			Namespace: namespace,
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
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"automation.kubezap.io"},
				Resources: []string{"mockendpoints"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"automation.kubezap.io"},
				Resources: []string{"mockendpoints/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
		},
	}
}

// desiredWebhookGatewayRoleBinding returns the desired RoleBinding for the webhook gateway.
func desiredWebhookGatewayRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubezap-webhook-gateway",
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "kubezap-webhook-gateway",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "kubezap-webhook-gateway",
				Namespace: namespace,
			},
		},
	}
}

// desiredWebhookGatewayHPA returns the desired HorizontalPodAutoscaler for the webhook
// gateway Deployment in the given namespace. It targets CPU utilization at 70% with a
// min of 1 and max of 10 replicas.
func desiredWebhookGatewayHPA(namespace string) *autoscalingv2.HorizontalPodAutoscaler {
	cpuUtilization := int32(70)
	minReplicas := int32(1)
	maxReplicas := int32(10)

	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookGatewayDeploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"kubezap.io/component": "webhook-gateway",
				"kubezap.io/namespace": namespace,
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

// desiredWebhookGatewayService returns the desired ClusterIP service for the webhook gateway.
func desiredWebhookGatewayService(namespace string) *corev1.Service {
	labels := map[string]string{
		"kubezap.io/component": "webhook-gateway",
		"kubezap.io/namespace": namespace,
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
					Name:       "http",
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
func desiredWebhookGatewayDeployment(namespace string) *appsv1.Deployment {
	labels := map[string]string{
		"kubezap.io/component": "webhook-gateway",
		"kubezap.io/namespace": namespace,
	}
	replicas := int32(1)

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
					ServiceAccountName: "kubezap-webhook-gateway",
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{
						{
							Name:            "webhook-gateway",
							Image:           webhookGatewayImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args: []string{
								"--port=8080",
								"--namespace=" + namespace,
							},
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: webhookGatewayPort, Protocol: corev1.ProtocolTCP},
							},
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
										Path: "/healthz",
										Port: intstr.FromInt32(webhookGatewayPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt32(webhookGatewayPort),
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
