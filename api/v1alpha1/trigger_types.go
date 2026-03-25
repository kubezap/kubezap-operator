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

// NOTE: json tags are required. Any new fields you add must have json tags for the fields to be serialized.

// TriggerSpec defines the desired state of Trigger.
// +kubebuilder:validation:XValidation:rule="has(self.flowRef) != has(self.action)",message="exactly one of flowRef or action must be set"
type TriggerSpec struct {
	// Type of trigger (webhook, cron, kafka, amqp, nats, resource)
	// +kubebuilder:validation:Enum=webhook;cron;kafka;amqp;nats;resource
	Type string `json:"type"`

	// Whether this trigger is active
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// Webhook configuration (only for type=webhook)
	Webhook *WebhookTrigger `json:"webhook,omitempty"`

	// Cron configuration (only for type=cron)
	Cron *CronTrigger `json:"cron,omitempty"`

	// Kafka configuration (only for type=kafka)
	Kafka *KafkaTrigger `json:"kafka,omitempty"`

	// Amqp configuration (only for type=amqp)
	Amqp *AmqpTrigger `json:"amqp,omitempty"`

	// Nats configuration (only for type=nats)
	Nats *NatsTrigger `json:"nats,omitempty"`

	// Resource configuration (only for type=resource).
	// Watches a Kubernetes resource type for create/update/delete events
	// and fires the trigger when a matching event occurs.
	Resource *ResourceTrigger `json:"resource,omitempty"`

	// Reference to the flow this trigger invokes. FlowRef is the primary
	// action target for the MVP. If omitted, the optional inline Action can
	// be used to perform a quick action (e.g., call an external webhook).
	FlowRef *FlowReference `json:"flowRef,omitempty"`

	// Inline action definition (optional). Starts with webhook action type.
	Action *ActionDefinition `json:"action,omitempty"`

	// Event or condition type for resource triggers (create/update/delete).
	// For external triggers (webhook/cron/kafka/amqp/nats) this is typically empty.
	// +kubebuilder:validation:Optional
	Event string `json:"event,omitempty"`

	// Cooldown/debounce policy to limit the number of firings in a window.
	Cooldown *CooldownPolicy `json:"cooldown,omitempty"`

	// FlowRunGC configures garbage collection of completed FlowRuns for this Trigger.
	// Operator-level defaults apply when omitted; set individual fields to override per-trigger.
	FlowRunGC *FlowRunGCPolicy `json:"flowRunGC,omitempty"`
}

// FlowRunGCPolicy defines retention limits for completed FlowRuns produced by a Trigger.
// Mirrors the Kubernetes Job history limits pattern (successfulJobsHistoryLimit /
// failedJobsHistoryLimit) but extended with TTL overrides for fine-grained control.
//
// All fields are optional. When a field is omitted the operator-level default applies
// (flags: --flowrun-ttl-succeeded, --flowrun-ttl-failed, --flowrun-gc-max-succeeded,
// --flowrun-gc-max-failed).
//
// Both count-based and TTL-based GC run independently: a FlowRun is eligible for
// deletion when EITHER its TTL has elapsed OR the count limit is exceeded.
type FlowRunGCPolicy struct {
	// MaxSucceeded is the maximum number of succeeded FlowRuns to retain for this Trigger.
	// When exceeded, the oldest succeeded FlowRuns are deleted. Set to 0 to disable
	// count-based GC for succeeded runs (TTL still applies).
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxSucceeded *int32 `json:"maxSucceeded,omitempty"`

	// MaxFailed is the maximum number of failed FlowRuns to retain for this Trigger.
	// When exceeded, the oldest failed FlowRuns are deleted. Set to 0 to disable
	// count-based GC for failed runs (TTL still applies).
	// Defaults to a higher value than MaxSucceeded to retain failure history for debugging.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxFailed *int32 `json:"maxFailed,omitempty"`

	// TTLAfterSucceeded is the duration to retain a succeeded FlowRun after completion.
	// Overrides the operator's --flowrun-ttl-succeeded flag for this Trigger.
	// Set to "0s" to delete succeeded FlowRuns immediately.
	// +optional
	TTLAfterSucceeded *metav1.Duration `json:"ttlAfterSucceeded,omitempty"`

	// TTLAfterFailed is the duration to retain a failed FlowRun after completion.
	// Overrides the operator's --flowrun-ttl-failed flag for this Trigger.
	// Set to "0s" to delete failed FlowRuns immediately (not recommended — loses debug history).
	// +optional
	TTLAfterFailed *metav1.Duration `json:"ttlAfterFailed,omitempty"`
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
	// Name of the Flow CR to execute. The Flow must be in the same namespace as the referencing resource.
	// Cross-namespace FlowRefs are not supported in v1alpha1 and are deferred to v1beta1 with a FlowGrant CRD.
	Name string `json:"name"`
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

// CooldownPolicy prevents trigger storms by limiting invocations in a time window.
type CooldownPolicy struct {
	// MaxInvocations allowed within Window. If zero, no limit is applied.
	MaxInvocations int32 `json:"maxInvocations,omitempty"`

	// Window for counting invocations, e.g. "60s". If omitted, defaults to 60s.
	// +kubebuilder:default="60s"
	Window *metav1.Duration `json:"window,omitempty"`
}

// KafkaTrigger configures a Kafka-based trigger (type=kafka).
type KafkaTrigger struct {
	// Reference to an Integration CR with Kafka connection details.
	IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`

	// Topic to consume from.
	Topic string `json:"topic"`

	// ConsumerGroup ID. Defaults to "kubezap-<trigger-name>" at runtime.
	ConsumerGroup string `json:"consumerGroup,omitempty"`
}

// AmqpTrigger configures an AMQP-based trigger (type=amqp).
type AmqpTrigger struct {
	// Reference to an Integration CR with AMQP connection details.
	IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`

	// Topic (queue name) to consume from.
	Topic string `json:"topic"`

	// RoutingKey is the AMQP routing key or binding pattern.
	RoutingKey string `json:"routingKey,omitempty"`
}

// NatsTrigger configures a NATS-based trigger (type=nats).
type NatsTrigger struct {
	// Reference to an Integration CR with NATS connection details.
	IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`

	// Subject is the NATS subject to subscribe to.
	// Supports NATS wildcards (e.g. "orders.*", "events.>").
	Subject string `json:"subject"`
}

// ResourceTrigger watches a Kubernetes resource type for events.
type ResourceTrigger struct {
	// APIVersion of the resource to watch (e.g. "v1", "apps/v1", "automation.kubezap.io/v1alpha1").
	// +kubebuilder:validation:MinLength=1
	APIVersion string `json:"apiVersion"`

	// Kind of the resource to watch (e.g. "Pod", "ConfigMap", "Deployment").
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Namespace to watch. If empty, watches the Trigger's own namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// LabelSelector limits events to resources matching these labels.
	// If omitted, all resources of the specified kind are watched.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// Events specifies which event types fire the trigger.
	// Valid values: create, update, delete. Defaults to [create] if omitted.
	// +kubebuilder:validation:MinItems=1
	// +optional
	Events []string `json:"events,omitempty"`

	// WatchFields limits update events to fire only when one of these
	// JSON path expressions changes. Only effective for update events.
	// Uses dot-notation paths, e.g. ".status.phase", ".spec.replicas".
	// If omitted, all updates fire the trigger.
	// +optional
	WatchFields []string `json:"watchFields,omitempty"`

	// Cooldown is a minimum duration that must elapse between FlowRun creations
	// for the same resource and event type. Events arriving within this window
	// after the last FlowRun creation are suppressed. Useful for resources that
	// churn rapidly (e.g. Pod status updates). If omitted, no per-resource
	// cooldown is applied.
	// +optional
	Cooldown *metav1.Duration `json:"cooldown,omitempty"`
}

// WebhookAuth configures authentication for a webhook trigger endpoint.
// mTLS is not supported at the handler layer — use TLS termination at the gateway
// server level (--tls-cert-file / --tls-key-file flags) for transport-layer mutual
// auth with broker gateways. Client-cert auth for HTTP webhooks is out of scope.
type WebhookAuth struct {
	// Authentication method.
	// +kubebuilder:validation:Enum=hmac;bearer;oidc;basic;apiKey;ipAllowlist;header-equals
	Type string `json:"type"`

	// HMAC signature verification. Used when type is "hmac".
	// +optional
	HMAC *HMACConfig `json:"hmac,omitempty"`

	// Bearer token verification. Used when type is "bearer".
	// +optional
	Bearer *BearerConfig `json:"bearer,omitempty"`

	// OIDC/JWT verification. Used when type is "oidc".
	// +optional
	OIDC *OIDCConfig `json:"oidc,omitempty"`

	// HTTP Basic authentication. Used when type is "basic".
	// +optional
	Basic *WebhookBasicAuth `json:"basic,omitempty"`

	// API key header verification. Used when type is "apiKey".
	// +optional
	APIKey *APIKeyConfig `json:"apiKey,omitempty"`

	// IP address allowlist. Used when type is "ipAllowlist".
	// +optional
	IPAllowlist *IPAllowlistConfig `json:"ipAllowlist,omitempty"`

	// Exact header value match. Used when type is "header-equals".
	// +optional
	HeaderEquals *HeaderEqualsConfig `json:"headerEquals,omitempty"`
}

// HMACConfig configures HMAC-SHA256 signature verification.
type HMACConfig struct {
	// Secret reference — key contains the shared HMAC secret.
	// +kubebuilder:validation:Required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// BearerConfig configures bearer token verification.
type BearerConfig struct {
	// Secret reference — key contains the expected bearer token value.
	// +kubebuilder:validation:Required
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`
}

// OIDCConfig configures OIDC/JWT verification.
type OIDCConfig struct {
	// OIDC issuer URL (e.g. https://accounts.google.com).
	// +kubebuilder:validation:Required
	Issuer string `json:"issuer"`

	// Expected audience claim value.
	// +optional
	Audience string `json:"audience,omitempty"`
}

// APIKeyConfig configures API key header verification.
type APIKeyConfig struct {
	// Secret reference — key contains the expected API key value.
	// +kubebuilder:validation:Required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`

	// Header name to check for the API key.
	// +kubebuilder:default="X-Api-Key"
	Header string `json:"header,omitempty"`
}

// IPAllowlistConfig configures IP address allowlisting.
type IPAllowlistConfig struct {
	// CIDR blocks allowed to call this endpoint.
	// Example: ["10.0.0.0/8", "192.168.1.0/24"]
	// +kubebuilder:validation:Required
	CIDRs []string `json:"cidrs"`
}

// HeaderEqualsConfig configures exact header value matching.
type HeaderEqualsConfig struct {
	// Header name to check (e.g. "X-Gitlab-Token").
	// +kubebuilder:validation:Required
	Header string `json:"header"`

	// Secret reference — key contains the expected header value.
	// +kubebuilder:validation:Required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// WebhookBasicAuth configures HTTP Basic authentication for a webhook endpoint.
type WebhookBasicAuth struct {
	// Reference to the Secret containing the username and password.
	// +kubebuilder:validation:Required
	SecretRef corev1.LocalObjectReference `json:"secretRef"`

	// Key in the Secret that holds the username.
	// +kubebuilder:default="username"
	UsernameKey string `json:"usernameKey,omitempty"`

	// Key in the Secret that holds the password.
	// +kubebuilder:default="password"
	PasswordKey string `json:"passwordKey,omitempty"`
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
