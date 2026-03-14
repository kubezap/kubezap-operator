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

// MockResponse defines the HTTP response returned by the mock endpoint.
type MockResponse struct {
	// StatusCode is the HTTP status code to return.
	// +kubebuilder:default=200
	StatusCode int32 `json:"statusCode,omitempty"`

	// Optional headers to include.
	Headers map[string]string `json:"headers,omitempty"`

	// Optional body content.
	Body string `json:"body,omitempty"`

	// Artificial delay in milliseconds before returning.
	DelayMs int32 `json:"delayMs,omitempty"`
}

// CapturedRequest stores details of a received request.
type CapturedRequest struct {
	Timestamp          metav1.Time       `json:"timestamp"`
	Method             string            `json:"method"`
	Path               string            `json:"path"`
	Headers            map[string]string `json:"headers,omitempty"`
	Body               string            `json:"body,omitempty"`
	BodyTruncated      bool              `json:"bodyTruncated,omitempty"`
	ResponseStatusCode int32             `json:"responseStatusCode,omitempty"`
}

// MockEndpointSpec defines the desired behavior of a mock HTTP endpoint.
type MockEndpointSpec struct {
	// Path is the suffix path registered under /mock/
	Path string `json:"path"`

	// Response is a default response to return.
	Response *MockResponse `json:"response,omitempty"`

	// ResponseSequence can be used to cycle through responses.
	ResponseSequence []MockResponse `json:"responseSequence,omitempty"`

	// MaxRequestHistory is how many recent requests are retained.
	// +kubebuilder:default=20
	MaxRequestHistory int32 `json:"maxRequestHistory,omitempty"`
}

// MockEndpointStatus contains status information for the mock endpoint.
type MockEndpointStatus struct {
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
	URL            string             `json:"url,omitempty"`
	RequestCount   int64              `json:"requestCount,omitempty"`
	RecentRequests []CapturedRequest  `json:"recentRequests,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="PATH",type=string,JSONPath=`.spec.path`,description="Mock endpoint path"
// +kubebuilder:printcolumn:name="REQUEST COUNT",type=integer,JSONPath=`.status.requestCount`,description="Total requests"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=`.metadata.creationTimestamp`

type MockEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MockEndpointSpec   `json:"spec,omitempty"`
	Status MockEndpointStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type MockEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MockEndpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MockEndpoint{}, &MockEndpointList{})
}
