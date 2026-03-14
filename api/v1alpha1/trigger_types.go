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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TriggerSpec defines the desired state of Trigger.
type TriggerSpec struct {
	// Type of trigger (webhook, cron, pubsub)
	// +kubebuilder:validation:Enum=webhook;cron;pubsub
	Type string `json:"type"`

	// Whether this trigger is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// Webhook configuration (only for type=webhook)
	Webhook *WebhookTrigger `json:"webhook,omitempty"`

	// Cron configuration (only for type=cron)
	Cron *CronTrigger `json:"cron,omitempty"`

	// PubSub configuration (only for type=pubsub)
	PubSub *PubSubTrigger `json:"pubsub,omitempty"`

	// Reference to the flow this trigger invokes. FlowRef is the primary
	// action target for the MVP. If omitted, the optional inline Action can
	// be used to perform a quick action (e.g., call an external webhook).
	FlowRef *FlowReference `json:"flowRef,omitempty"`

	// Inline action definition (optional). Starts with webhook action type.
	Action *ActionDefinition `json:"action,omitempty"`

	// Optional target resource in the cluster to watch or reference. Kept
	// optional for the MVP (external events only by default) but present
	// so future resource-based triggers can be added without schema changes.
	Target *TargetResource `json:"target,omitempty"`

	// Event or condition type for resource triggers (create/update/delete).
	// For external triggers (webhook/cron/pubsub) this is typically empty.
	// +kubebuilder:validation:Optional
	Event string `json:"event,omitempty"`

	// Cooldown/debounce policy to limit the number of firings in a window.
	Cooldown *CooldownPolicy `json:"cooldown,omitempty"`

	// MaxFlowRuns caps the number of retained FlowRuns for this Trigger.
	// When exceeded, the oldest completed FlowRuns are garbage-collected.
	// If zero or omitted, no cap is applied.
	// +kubebuilder:validation:Minimum=0
	MaxFlowRuns *int32 `json:"maxFlowRuns,omitempty"`
}

type WebhookTrigger struct {
	// HTTP path exposed by the operator
	Path string `json:"path"`

	// +kubebuilder:validation:Enum=POST;PUT
	// +kubebuilder:default=POST
	Method string `json:"method,omitempty"`

	// Auth configures authentication for this webhook endpoint.
	// If omitted, the endpoint accepts requests from any caller.
	Auth *WebhookAuth `json:"auth,omitempty"`
}

// CronTrigger configures a cron-based scheduled trigger.
type CronTrigger struct {
	// Schedule in standard cron format (e.g. "*/5 * * * *").
	// Supports the robfig/cron v3 extended syntax including @every and @daily.
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// Timezone for the schedule, e.g. "America/New_York".
	// Defaults to UTC if omitted.
	// +kubebuilder:validation:Optional
	Timezone string `json:"timezone,omitempty"`
}

type FlowReference struct {
	// Name of the Flow CR to execute
	Name string `json:"name"`

	// Namespace of the Flow CR. If omitted the Trigger's namespace is used.
	Namespace string `json:"namespace,omitempty"`
}

// ActionDefinition defines a simple inline action for quick responses.
// Start with a webhook action for MVP; later it can include Job/Kubernetes actions.
type ActionDefinition struct {
	// Type of action (webhook)
	// +kubebuilder:validation:Enum=webhook
	Type string `json:"type"`

	// Webhook action details
	Webhook *WebhookAction `json:"webhook,omitempty"`
}

type WebhookAction struct {
	// URL to call when the trigger fires
	URL string `json:"url"`

	// HTTP method to use (default POST)
	// +kubebuilder:validation:Enum=POST;PUT;PATCH;GET
	// +kubebuilder:default=POST
	Method string `json:"method,omitempty"`

	// Optional headers to include in the call
	Headers map[string]string `json:"headers,omitempty"`

	// Optional body template (raw string or templating placeholder)
	Body string `json:"body,omitempty"`
}

// TargetResource describes an optional cluster resource target for resource-based triggers.
type TargetResource struct {
	// Kind of resource (Pod, ConfigMap, CustomKind)
	Kind string `json:"kind,omitempty"`

	// Namespace of the resource. If empty and Name is set, it defaults to the Trigger's namespace.
	Namespace string `json:"namespace,omitempty"`

	// Name of a specific resource to target.
	Name string `json:"name,omitempty"`

	// LabelSelector allows selecting resources by labels instead of exact name.
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// CooldownPolicy prevents trigger storms by limiting invocations in a time window.
type CooldownPolicy struct {
	// MaxInvocations allowed within Window. If zero, no limit is applied.
	MaxInvocations int32 `json:"maxInvocations,omitempty"`

	// Window for counting invocations, e.g. "60s". If omitted, defaults to 60s.
	// +kubebuilder:default="60s"
	Window *metav1.Duration `json:"window,omitempty"`
}

// PubSubTrigger configures a message-broker-based trigger.
type PubSubTrigger struct {
	// Message broker type.
	// +kubebuilder:validation:Enum=kafka
	Type string `json:"type"`

	// Reference to an Integration CR with broker connection details.
	IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`

	// Topic to consume from.
	Topic string `json:"topic"`

	// Kafka consumer group ID. Defaults to "kubezap-<trigger-name>" at runtime.
	ConsumerGroup string `json:"consumerGroup,omitempty"`
}

// WebhookAuth configures authentication for a webhook trigger endpoint.
type WebhookAuth struct {
	// Authentication method.
	// +kubebuilder:validation:Enum=hmac;bearer;oidc;basic;mtls;apiKey;ipAllowlist
	Type string `json:"type"`

	// HMAC secret reference (key contains the shared secret). Used when type is "hmac".
	HMACSecretRef *corev1.SecretKeySelector `json:"hmacSecretRef,omitempty"`

	// Bearer token secret reference. Used when type is "bearer".
	BearerTokenSecretRef *corev1.SecretKeySelector `json:"bearerTokenSecretRef,omitempty"`

	// OIDC/JWT issuer URL. Used when type is "oidc".
	OIDCIssuer string `json:"oidcIssuer,omitempty"`

	// OIDC audience. Used when type is "oidc".
	OIDCAudience string `json:"oidcAudience,omitempty"`

	// Basic auth credentials secret (must have keys "username" and "password"). Used when type is "basic".
	BasicAuthSecretRef *corev1.LocalObjectReference `json:"basicAuthSecretRef,omitempty"`

	// API key value secret reference. Used when type is "apiKey".
	APIKeySecretRef *corev1.SecretKeySelector `json:"apiKeySecretRef,omitempty"`

	// Header name to check for the API key. Used when type is "apiKey".
	// +kubebuilder:default="X-Api-Key"
	APIKeyHeader string `json:"apiKeyHeader,omitempty"`

	// CIDR blocks allowed to call this endpoint. Used when type is "ipAllowlist".
	// Example: ["10.0.0.0/8", "192.168.1.0/24"]
	IPAllowlist []string `json:"ipAllowlist,omitempty"`
}

// TriggerStatus defines the observed state of Trigger.
type TriggerStatus struct {
	// Standard Kubernetes conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastTriggeredTime is the timestamp of the last successful trigger firing
	LastTriggeredTime *metav1.Time `json:"lastTriggeredTime,omitempty"`

	// LastResult indicates the outcome of the last trigger attempt (e.g., Success, Failed, Skipped, RateLimited)
	LastResult string `json:"lastResult,omitempty"`

	// LastError contains a short error message from the last failed attempt
	LastError string `json:"lastError,omitempty"`

	// CurrentInvocationCount is the number of invocations in the current cooldown window
	CurrentInvocationCount int32 `json:"currentInvocationCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Trigger is the Schema for the triggers API.
type Trigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TriggerSpec   `json:"spec,omitempty"`
	Status TriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TriggerList contains a list of Trigger.
type TriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Trigger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Trigger{}, &TriggerList{})
}
