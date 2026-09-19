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

// TriggerReference points back to the originating Trigger.
type TriggerReference struct {
	// Name of the trigger.
	Name string `json:"name"`

	// Type of the trigger.
	// +kubebuilder:validation:Enum=webhook;cron;kafka;amqp;nats;resource
	Type string `json:"type"`
}

// TriggerData captures event metadata that led to a FlowRun.
type TriggerData struct {
	// EventType is the Kubernetes watch event type for resource triggers: ADDED, MODIFIED, DELETED.
	EventType string `json:"eventType,omitempty"`
	// ResourceName is the name of the resource that fired the trigger.
	ResourceName string `json:"resourceName,omitempty"`
	// ResourceNamespace is the namespace of the resource that fired the trigger.
	ResourceNamespace string `json:"resourceNamespace,omitempty"`
	// ResourceAPIVersion is the API version of the watched resource.
	ResourceAPIVersion string `json:"resourceAPIVersion,omitempty"`
	// ResourceKind is the kind of the watched resource.
	ResourceKind string `json:"resourceKind,omitempty"`

	Source    string            `json:"source,omitempty"`
	Method    string            `json:"method,omitempty"`
	Path      string            `json:"path,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Topic     string            `json:"topic,omitempty"`
	Partition int32             `json:"partition,omitempty"`
	Offset    int64             `json:"offset,omitempty"`
	// Key is the Kafka record key (kafka triggers only), captured verbatim when it
	// decodes as valid UTF-8, or base64-encoded otherwise. Unset (zero value) when
	// the record key is nil. See KeyEncoding to determine which form this is in.
	Key string `json:"key,omitempty"`
	// KeyEncoding indicates how Key is encoded: "utf8" or "base64". Unset when Key
	// is unset (nil record key).
	KeyEncoding   string       `json:"keyEncoding,omitempty"`
	ScheduledTime *metav1.Time `json:"scheduledTime,omitempty"`
	Body          string       `json:"body,omitempty"`
	BodyTruncated bool         `json:"bodyTruncated,omitempty"`
	// BodyEncoding indicates how Body is encoded: "utf8" or "base64". Body is
	// captured verbatim as UTF-8 when the (possibly truncated, per
	// BodyTruncated) payload decodes as valid UTF-8, or base64-encoded
	// otherwise — mirrors the Key/KeyEncoding convention above.
	BodyEncoding string `json:"bodyEncoding,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
}

// FlowRunSpec defines the desired state of FlowRun.
type FlowRunSpec struct {
	// Reference to the Flow to execute. The Flow must be in the same namespace as the FlowRun.
	// Cross-namespace FlowRefs are not supported in v1alpha1 and are deferred to v1beta1 with a FlowGrant CRD.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Flow Reference"
	FlowRef FlowReference `json:"flowRef"`

	// Optional runtime parameters.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Parameters"
	Params []ParamValue `json:"params,omitempty"`

	// Originating trigger reference.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Trigger Reference"
	TriggerRef *TriggerReference `json:"triggerRef,omitempty"`

	// Originating trigger data.
	TriggerData *TriggerData `json:"triggerData,omitempty"`

	// TTL for cleanup after completion.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TTL After Finished"
	TTLAfterFinished *metav1.Duration `json:"ttlAfterFinished,omitempty"`
}

// ResultValue is a named result emitted by a step.
type ResultValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// StepPhase is the execution phase of an individual Flow step. Distinct from
// FlowRunPhase (rather than sharing one "Phase" type) so the compiler catches a
// value valid for one but not the other being used in place of the wrong one —
// e.g. Waiting is a valid StepPhase but was, before this type existed, mistakenly
// documented as a valid FlowRunPhase too. See docs/design/typed-phase-enums.md.
type StepPhase string

const (
	StepPhasePending   StepPhase = "Pending"
	StepPhaseRunning   StepPhase = "Running"
	StepPhaseSucceeded StepPhase = "Succeeded"
	StepPhaseFailed    StepPhase = "Failed"
	StepPhaseSkipped   StepPhase = "Skipped"
	StepPhaseWaiting   StepPhase = "Waiting"
)

// StepRunStatus tracks execution state for an individual Flow step.
type StepRunStatus struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Skipped;Waiting
	Phase StepPhase `json:"phase,omitempty"`

	StartTime      *metav1.Time  `json:"startTime,omitempty"`
	CompletionTime *metav1.Time  `json:"completionTime,omitempty"`
	Attempts       int32         `json:"attempts,omitempty"`
	Message        string        `json:"message,omitempty"`
	Results        []ResultValue `json:"results,omitempty"`

	// DurationMillis is the step's execution time in milliseconds, set once the
	// step reaches a terminal phase (Succeeded, Failed, or Skipped) — the same
	// full-precision time.Duration observed into the kubezap_step_duration_seconds
	// histogram, in case a caller needs it and can't fall back to subtracting the
	// second-precision StartTime/CompletionTime. Unset while the step is Pending,
	// Running, or Waiting.
	// +optional
	DurationMillis *int64 `json:"durationMillis,omitempty"`

	// ResumeAfter is set by the wait step executor and records when the step should resume.
	// The controller requeues the FlowRun until this time has elapsed.
	// +optional
	ResumeAfter *metav1.Time `json:"resumeAfter,omitempty"`
}

// FlowRunPhase is the overall execution phase of a FlowRun. Distinct from
// StepPhase — see its doc comment for why.
type FlowRunPhase string

const (
	FlowRunPhasePending   FlowRunPhase = "Pending"
	FlowRunPhaseRunning   FlowRunPhase = "Running"
	FlowRunPhaseSucceeded FlowRunPhase = "Succeeded"
	FlowRunPhaseFailed    FlowRunPhase = "Failed"
	FlowRunPhaseCancelled FlowRunPhase = "Cancelled"
)

// FlowRunStatus defines the observed state of FlowRun.
type FlowRunStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Cancelled
	Phase FlowRunPhase `json:"phase,omitempty"`

	Conditions     []metav1.Condition `json:"conditions,omitempty"`
	StartTime      *metav1.Time       `json:"startTime,omitempty"`
	CompletionTime *metav1.Time       `json:"completionTime,omitempty"`
	Steps          []StepRunStatus    `json:"steps,omitempty"`
	Message        string             `json:"message,omitempty"`

	// DurationMillis is the FlowRun's total execution time in milliseconds, set
	// once the FlowRun reaches a terminal phase (Succeeded, Failed, or
	// Cancelled) — the same full-precision time.Duration observed into the
	// kubezap_flowrun_duration_seconds histogram. Unset while the FlowRun is
	// Pending or Running.
	// +optional
	DurationMillis *int64 `json:"durationMillis,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="FLOW",type=string,JSONPath=`.spec.flowRef.name`,description="Flow name"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=`.status.phase`,description="FlowRun phase"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="DURATION",type=string,JSONPath=`.status.completionTime`,description="Completion timestamp"

// FlowRun is an execution instance of a Flow, created by a gateway when a trigger fires.
// +operator-sdk:csv:customresourcedefinitions:resources={{FlowRun,v1alpha1,""}}
type FlowRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FlowRunSpec   `json:"spec,omitempty"`
	Status FlowRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type FlowRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FlowRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FlowRun{}, &FlowRunList{})
}
