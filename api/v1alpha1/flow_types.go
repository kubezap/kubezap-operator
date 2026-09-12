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

// FailurePolicy controls Flow-level behavior when a step fails.
type FailurePolicy string

const (
	FailurePolicyFail     FailurePolicy = "Fail"
	FailurePolicyContinue FailurePolicy = "Continue"
)

// FlowSpec defines the desired state of Flow.
type FlowSpec struct {
	// Optional human-readable description for this Flow.
	Description string `json:"description,omitempty"`

	// Timeout for the entire Flow execution.
	// +kubebuilder:default="10m"
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Behavior when a step fails.
	// +kubebuilder:validation:Enum=Fail;Continue
	// +kubebuilder:default=Fail
	FailurePolicy FailurePolicy `json:"failurePolicy,omitempty"`

	// Parameters that can be passed into the Flow.
	Params []ParamDeclaration `json:"params,omitempty"`

	// Ordered list of steps in the Flow.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Steps []FlowStep `json:"steps"`
}

// ParamDeclaration defines an input parameter for a Flow.
type ParamDeclaration struct {
	// Name of the parameter.
	Name string `json:"name"`

	// Optional description for the parameter.
	Description string `json:"description,omitempty"`

	// Whether this parameter is required.
	// +kubebuilder:default=false
	Required bool `json:"required,omitempty"`

	// Default value used when parameter is omitted.
	Default string `json:"default,omitempty"`
}

// OnFailureAction controls step-level behavior when this specific step fails.
// Distinct from FailurePolicy (rather than sharing one type) because it has a
// third valid value, Skip, that FailurePolicy does not.
type OnFailureAction string

const (
	OnFailureActionFail     OnFailureAction = "Fail"
	OnFailureActionContinue OnFailureAction = "Continue"
	OnFailureActionSkip     OnFailureAction = "Skip"
)

// FlowStep represents a single step in a Flow.
type FlowStep struct {
	// Name of the step.
	Name string `json:"name"`

	// Optional description for the step.
	Description string `json:"description,omitempty"`

	// Step names to run before this step.
	RunAfter []string `json:"runAfter,omitempty"`

	// Conditions to evaluate before running this step.
	When []WhenExpression `json:"when,omitempty"`

	// Action to execute.
	Action StepAction `json:"action"`

	// Results produced by the step.
	Results []ResultDeclaration `json:"results,omitempty"`

	// Retry policy for this step.
	RetryPolicy *RetryPolicy `json:"retryPolicy,omitempty"`

	// Timeout for this step.
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Policy for step failure handling.
	// +kubebuilder:validation:Enum=Fail;Continue;Skip
	OnFailure OnFailureAction `json:"onFailure,omitempty"`
}

// WhenExpression specifies a condition to evaluate prior to step execution.
type WhenExpression struct {
	// Expression that yields true/false.
	Expression string `json:"expression"`
}

// StepAction describes one of the supported step action types.
type StepAction struct {
	// Type of step action.
	// +kubebuilder:validation:Enum=http;transform;publish;wait
	Type string `json:"type"`

	// HTTP action details.
	HTTP *HTTPAction `json:"http,omitempty"`

	// Transform action details.
	Transform *TransformAction `json:"transform,omitempty"`

	// Publish action details.
	Publish *PublishAction `json:"publish,omitempty"`

	// Wait action details.
	Wait *WaitAction `json:"wait,omitempty"`
}

// HTTPAction represents an HTTP call to be made as a step.
type HTTPAction struct {
	// URL to call.
	URL string `json:"url"`

	// HTTP method for the call.
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE
	// +kubebuilder:default=POST
	Method string `json:"method,omitempty"`

	// Request headers.
	Headers map[string]string `json:"headers,omitempty"`

	// Inline body template.
	Body string `json:"body,omitempty"`

	// Request timeout in seconds.
	// +kubebuilder:default=30
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// Map names for result extraction. Keys are result names; values are JSONPath
	// expressions of the form "$.field". Only single-level paths (e.g., "$.tier")
	// are supported — multi-level paths (e.g., "$.order.id") silently return empty string.
	ResultMappings map[string]string `json:"resultMappings,omitempty"`

	// Reference to an http-type Integration providing base URL, auth, and default headers.
	// When set, the Integration's auth and defaultHeaders are merged into the request.
	// For type=secretUrl Integrations, the step URL is replaced by the Integration's URL secret.
	// +optional
	IntegrationRef *corev1.LocalObjectReference `json:"integrationRef,omitempty"`
}

// TransformAction represents a data transformation step.
type TransformAction struct {
	// Mappings used to generate transformed output.
	// +kubebuilder:validation:MinProperties=1
	Mappings map[string]string `json:"mappings"`
}

// PublishAction represents a message publish step.
type PublishAction struct {
	// Reference to an Integration resource.
	IntegrationRef corev1.LocalObjectReference `json:"integrationRef"`
	Topic          string                      `json:"topic"`

	// Optional body to publish.
	Body string `json:"body,omitempty"`

	// Optional publish headers.
	Headers map[string]string `json:"headers,omitempty"`
}

// WaitAction pauses the FlowRun for a fixed duration before continuing.
type WaitAction struct {
	// Duration is the amount of time to wait, as a Go duration string (e.g. "10m", "30s", "1h", "1h30m").
	// Compound durations such as "1h30m" are accepted, matching what time.ParseDuration accepts.
	// +kubebuilder:validation:Pattern=`^([0-9]+(ns|us|µs|ms|s|m|h))+$`
	Duration string `json:"duration"`
}

// ResultDeclaration declares what a step result will expose.
type ResultDeclaration struct {
	// Name of the result field.
	Name string `json:"name"`

	// Optional description of the result.
	Description string `json:"description,omitempty"`
}

// ParamValue assigns an explicit value to a Flow parameter declared via
// FlowSpec.Params (see ParamDeclaration). Used by FlowRunSpec.Params.
type ParamValue struct {
	// Name of the parameter.
	Name string `json:"name"`

	// Value to set.
	Value string `json:"value"`
}

// RetryPolicy configures retries for a step.
type RetryPolicy struct {
	// Maximum retries allowed.
	// +kubebuilder:validation:Minimum=0
	MaxRetries int32 `json:"maxRetries"`

	// Backoff strategy.
	// +kubebuilder:validation:Enum=Fixed;Linear;Exponential
	// +kubebuilder:default=Exponential
	BackoffType string `json:"backoffType,omitempty"`

	// Initial retry delay.
	// +kubebuilder:default="1s"
	InitialDelay *metav1.Duration `json:"initialDelay,omitempty"`

	// Maximum delay for retries.
	// +kubebuilder:default="60s"
	MaxDelay *metav1.Duration `json:"maxDelay,omitempty"`

	// Backoff multiplier.
	// +kubebuilder:default="2.0"
	Multiplier string `json:"multiplier,omitempty"`
}

// FlowStatus defines the observed state of Flow.
type FlowStatus struct {
	// Standard Kubernetes conditions.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Number of times this Flow has executed.
	ExecutionCount int64 `json:"executionCount,omitempty"`

	// Timestamp of last execution.
	LastExecutionTime *metav1.Time `json:"lastExecutionTime,omitempty"`

	// Last result status string (e.g., Success/Failed).
	LastResult string `json:"lastResult,omitempty"`

	// Last error message if any.
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.conditions[?(@.type==\"Ready\")].status`,description="Current phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type Flow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FlowSpec   `json:"spec,omitempty"`
	Status FlowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type FlowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Flow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Flow{}, &FlowList{})
}
