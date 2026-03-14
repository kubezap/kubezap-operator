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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	webhookGatewayDeploymentName = "kubezap-webhook-gateway"
	webhookGatewayImage          = "kubezap/webhook-gateway"
	webhookGatewayPort           = int32(8080)
)

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
						RunAsNonRoot: ptr.To(true),
					},
					Containers: []corev1.Container{
						{
							Name:  "webhook-gateway",
							Image: webhookGatewayImage,
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
