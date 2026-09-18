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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// NOTE: json tags are required. Any new fields you add must have json tags for the fields to be serialized.

// WebhookGatewayConfigSpec defines per-namespace configuration for the webhook
// gateway Deployment. At most one WebhookGatewayConfig may exist per namespace
// — enforced by requiring the object be named "default" (see the
// WebhookGatewayConfig type's XValidation rule), so Kubernetes' own
// per-(namespace, name) uniqueness guarantees the singleton with no admission
// webhook involved. See docs/design/webhookgatewayconfig-singleton-name.md.
//
// A namespace with no WebhookGatewayConfig object — and any field left unset on
// one that does exist — behaves exactly as it did before this CRD existed:
// HPA min=1/max=10/target-CPU=70%, no PodDisruptionBudget, no gateway TLS.
type WebhookGatewayConfigSpec struct {
	// TLS configures inbound TLS/mTLS termination for the webhook gateway.
	// When omitted, the gateway serves plain HTTP.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TLS"
	TLS *WebhookGatewayTLSSpec `json:"tls,omitempty"`

	// HPA overrides the webhook gateway's HorizontalPodAutoscaler behavior.
	// Any field left unset keeps today's operator default for that field
	// (minReplicas=1, maxReplicas=10, targetCPUUtilization=70).
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="HPA"
	HPA *WebhookGatewayHPASpec `json:"hpa,omitempty"`

	// PodDisruptionBudget configures a PodDisruptionBudget for the webhook
	// gateway Deployment. When omitted (or MinAvailable is unset), no
	// PodDisruptionBudget is created — matching today's behavior (none exists).
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Pod Disruption Budget"
	PodDisruptionBudget *WebhookGatewayPDBSpec `json:"podDisruptionBudget,omitempty"`
}

// WebhookGatewayTLSSpec configures TLS/mTLS termination for the webhook
// gateway Deployment. Secret references follow the same same-namespace
// corev1.LocalObjectReference convention already used by
// Integration.spec.kafka.tls.clientCertSecretRef — no cross-namespace Secret
// references.
type WebhookGatewayTLSSpec struct {
	// ServerSecretRef references a Secret in this namespace containing tls.crt
	// and tls.key (cert-manager compatible) used to terminate TLS on the
	// webhook gateway's listener. When omitted, the gateway serves plain HTTP.
	// +optional
	ServerSecretRef *corev1.LocalObjectReference `json:"serverSecretRef,omitempty"`

	// ClientCASecretRef references a Secret in this namespace containing
	// ca.crt, used to verify client certificates for mutual TLS. Only
	// effective when ServerSecretRef is also set.
	// +optional
	ClientCASecretRef *corev1.LocalObjectReference `json:"clientCASecretRef,omitempty"`
}

// WebhookGatewayHPASpec overrides the webhook gateway's
// HorizontalPodAutoscaler behavior. All fields are optional; when a field is
// omitted the operator's hardcoded default for that field applies (the same
// default that has always applied: minReplicas=1, maxReplicas=10,
// targetCPUUtilization=70).
type WebhookGatewayHPASpec struct {
	// MinReplicas is the floor of the HPA's replica range. Only validated to
	// be a positive integer — no minimum-for-HA is enforced, since 1 replica
	// remains a legitimate, currently-default, non-HA choice for a
	// low-traffic namespace. Defaults to 1 when unset.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the ceiling of the HPA's replica range. Defaults to 10
	// when unset.
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// TargetCPUUtilization is the target average CPU utilization percentage
	// the HPA scales toward. Defaults to 70 when unset.
	// +optional
	TargetCPUUtilization *int32 `json:"targetCPUUtilization,omitempty"`
}

// WebhookGatewayPDBSpec configures a PodDisruptionBudget for the webhook
// gateway Deployment. When MinAvailable is unset, no PodDisruptionBudget is
// created (today's behavior); setting it is the only way to opt in.
type WebhookGatewayPDBSpec struct {
	// MinAvailable is the minimum number (or percentage) of webhook gateway
	// Pods that must remain available during a voluntary disruption. Unset
	// means no PodDisruptionBudget is created for this namespace's gateway.
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`
}

// WebhookGatewayConfigStatus defines the observed state of WebhookGatewayConfig.
type WebhookGatewayConfigStatus struct {
	// Standard Kubernetes conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default'",message="the only valid name for a WebhookGatewayConfig is 'default'"

// WebhookGatewayConfig is the Schema for the webhookgatewayconfigs API.
//
// At most one WebhookGatewayConfig may exist per namespace. This is enforced
// by requiring the object be named "default" (see the XValidation rule
// above) rather than by an admission webhook — Kubernetes' own
// per-(namespace, name) uniqueness in etcd then makes "at most one" hold
// unconditionally, with no dependency on webhook/cert infrastructure being
// deployed or healthy. See docs/design/webhookgatewayconfig-singleton-name.md.
// +operator-sdk:csv:customresourcedefinitions:resources={{HorizontalPodAutoscaler,v2,""},{PodDisruptionBudget,v1,""}}
type WebhookGatewayConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookGatewayConfigSpec   `json:"spec,omitempty"`
	Status WebhookGatewayConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookGatewayConfigList contains a list of WebhookGatewayConfig.
type WebhookGatewayConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookGatewayConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookGatewayConfig{}, &WebhookGatewayConfigList{})
}
