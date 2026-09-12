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

	Source        string            `json:"source,omitempty"`
	Method        string            `json:"method,omitempty"`
	Path          string            `json:"path,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Topic         string            `json:"topic,omitempty"`
	Partition     int32             `json:"partition,omitempty"`
	Offset        int64             `json:"offset,omitempty"`
	KafkaHeaders  map[string]string `json:"kafkaHeaders,omitempty"`
	ScheduledTime *metav1.Time      `json:"scheduledTime,omitempty"`
	Body          string            `json:"body,omitempty"`
	BodyTruncated bool              `json:"bodyTruncated,omitempty"`
	ContentType   string            `json:"contentType,omitempty"`
}

// FlowRunSpec defines the desired state of FlowRun.
type FlowRunSpec struct {
	// Reference to the Flow to execute. The Flow must be in the same namespace as the FlowRun.
	// Cross-namespace FlowRefs are not supported in v1alpha1 and are deferred to v1beta1 with a FlowGrant CRD.
	FlowRef FlowReference `json:"flowRef"`

	// Optional runtime parameters.
	Params []ParamValue `json:"params,omitempty"`

	// Originating trigger reference.
	TriggerRef *TriggerReference `json:"triggerRef,omitempty"`

	// Originating trigger data.
	TriggerData *TriggerData `json:"triggerData,omitempty"`

	// TTL for cleanup after completion.
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
// documented as a valid FlowRunPhase too. See docs/design/2026-09-11-typed-phase-enums.md.
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
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="FLOW",type=string,JSONPath=`.spec.flowRef.name`,description="Flow name"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=`.status.phase`,description="FlowRun phase"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="DURATION",type=string,JSONPath=`.status.completionTime`,description="Completion timestamp"

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
