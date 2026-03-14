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
}

type WebhookTrigger struct {
	// HTTP path exposed by the operator
	Path string `json:"path"`

	// +kubebuilder:validation:Enum=POST;PUT
	// +kubebuilder:default=POST
	Method string `json:"method,omitempty"`
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
