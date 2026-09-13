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

// KafkaTLSConfig defines TLS options for Kafka connections.
type KafkaTLSConfig struct {
	// Enable TLS for Kafka communication.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// Skip server certificate verification.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CA certificate reference.
	CASecretRef *corev1.SecretKeySelector `json:"caSecretRef,omitempty"`

	// Client certificate secret reference.
	ClientCertSecretRef *corev1.LocalObjectReference `json:"clientCertSecretRef,omitempty"`
}

// KafkaSASLConfig defines SASL options for Kafka connections.
type KafkaSASLConfig struct {
	// SASL mechanism.
	// +kubebuilder:validation:Enum=PLAIN;SCRAM-SHA-256;SCRAM-SHA-512
	Mechanism string `json:"mechanism"`

	// Username secret reference.
	UsernameSecretRef corev1.SecretKeySelector `json:"usernameSecretRef"`

	// Password secret reference.
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`
}

// KafkaIntegrationSpec contains Kafka-specific integration configuration.
type KafkaIntegrationSpec struct {
	// List of Kafka bootstrap servers.
	BootstrapServers []string `json:"bootstrapServers"`

	// TLS configuration.
	TLS *KafkaTLSConfig `json:"tls,omitempty"`

	// SASL configuration.
	SASL *KafkaSASLConfig `json:"sasl,omitempty"`

	// Prefix for consumer groups.
	ConsumerGroupPrefix string `json:"consumerGroupPrefix,omitempty"`
}

// AmqpTLSConfig defines TLS options for AMQP connections.
type AmqpTLSConfig struct {
	// Enable TLS. Use amqps:// URL to enable automatically.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Skip server certificate verification. Development only.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CA certificate secret reference.
	CASecretRef *corev1.SecretKeySelector `json:"caSecretRef,omitempty"`

	// Client certificate secret for mTLS.
	ClientCertSecretRef *corev1.LocalObjectReference `json:"clientCertSecretRef,omitempty"`
}

// AmqpIntegrationSpec contains AMQP-specific integration configuration.
// Covers AMQP 0-9-1 (RabbitMQ, ActiveMQ Classic) and AMQP 1.0
// (ActiveMQ Artemis, Azure Service Bus, Solace, IBM MQ).
type AmqpIntegrationSpec struct {
	// AMQP broker URL. Examples:
	//   amqp://rabbitmq.infra:5672/production
	//   amqps://artemis.infra:5671
	URL string `json:"url"`

	// Wire-protocol version.
	// +kubebuilder:validation:Enum="0-9-1";"1.0"
	// +kubebuilder:default="0-9-1"
	Version string `json:"version,omitempty"`

	// TLS configuration. Inferred from amqps:// URL if not set explicitly.
	TLS *AmqpTLSConfig `json:"tls,omitempty"`

	// Username secret reference.
	UsernameSecretRef *corev1.SecretKeySelector `json:"usernameSecretRef,omitempty"`

	// Password secret reference.
	PasswordSecretRef *corev1.SecretKeySelector `json:"passwordSecretRef,omitempty"`
}

// NatsTLSConfig defines TLS options for NATS connections.
type NatsTLSConfig struct {
	// Skip server certificate verification. Development only.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// CA certificate secret reference.
	CASecretRef *corev1.SecretKeySelector `json:"caSecretRef,omitempty"`

	// Client certificate secret for mTLS.
	ClientCertSecretRef *corev1.LocalObjectReference `json:"clientCertSecretRef,omitempty"`
}

// NatsIntegrationSpec contains NATS-specific integration configuration.
// Supports NATS Core and NATS JetStream.
type NatsIntegrationSpec struct {
	// NATS server URLs. Multiple URLs are used for cluster failover.
	// Example: ["nats://nats-0.nats.infra:4222", "nats://nats-1.nats.infra:4222"]
	// +kubebuilder:validation:MinItems=1
	Servers []string `json:"servers"`

	// TLS configuration.
	TLS *NatsTLSConfig `json:"tls,omitempty"`

	// NATS credentials file secret reference (NKey or User JWT credentials).
	// The secret key must be "nats.creds".
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`

	// Username secret reference (basic auth, not recommended for production).
	UsernameSecretRef *corev1.SecretKeySelector `json:"usernameSecretRef,omitempty"`

	// Password secret reference.
	PasswordSecretRef *corev1.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// Enable JetStream for durable, persistent message delivery.
	// +kubebuilder:default=false
	JetStream bool `json:"jetStream,omitempty"`
}

// PluginSecretRef maps secret keys to environment variable names.
type PluginSecretRef struct {
	// Secret name.
	SecretName string `json:"secretName"`

	// Mappings from secret key to env var name.
	// +kubebuilder:validation:MinProperties=1
	EnvVarMappings map[string]string `json:"envVarMappings"`
}

// PluginIntegrationSpec contains plugin deployment configuration.
type PluginIntegrationSpec struct {
	// Container image for plugin.
	Image string `json:"image"`

	// ImageDigest is an optional SHA256 digest that pins the plugin image to a
	// specific content-addressed layer. When set, the operator constructs the
	// Deployment image reference as "image@sha256:<digest>", preventing silent
	// updates when the image tag is overwritten.
	//
	// Format: the 64-character hex portion of a SHA256 digest (without the
	// "sha256:" prefix).
	//
	// Example: "abc123...64chars..."
	// +optional
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	ImageDigest string `json:"imageDigest,omitempty"`

	// Publisher service port.
	// +kubebuilder:default=8090
	PublisherPort int32 `json:"publisherPort,omitempty"`

	// Secret references for plugin env vars.
	SecretRefs []PluginSecretRef `json:"secretRefs,omitempty"`

	// Additional environment variables.
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// HttpAuthType specifies the authentication strategy for an HTTP Integration.
// +kubebuilder:validation:Enum=bearer;basic;apiKey;secretUrl
type HttpAuthType string

const (
	HttpAuthBearer    HttpAuthType = "bearer"
	HttpAuthBasic     HttpAuthType = "basic"
	HttpAuthAPIKey    HttpAuthType = "apiKey"
	HttpAuthSecretURL HttpAuthType = "secretUrl"
)

// HttpBearerAuth configures bearer token authentication.
type HttpBearerAuth struct {
	// Secret key containing the bearer token value.
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`
}

// HttpBasicAuth configures HTTP Basic authentication.
type HttpBasicAuth struct {
	// Secret key containing the username.
	UsernameSecretRef corev1.SecretKeySelector `json:"usernameSecretRef"`
	// Secret key containing the password.
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`
}

// HttpAPIKeyAuth configures API key header authentication.
type HttpAPIKeyAuth struct {
	// HTTP header name to set (e.g. "X-Api-Key").
	HeaderName string `json:"headerName"`
	// Secret key containing the API key value.
	ValueSecretRef corev1.SecretKeySelector `json:"valueSecretRef"`
}

// HttpSecretURLAuth configures URL-as-credential authentication (e.g. Slack incoming webhook URLs).
// When type=secretUrl, the controller replaces the step URL entirely with the secret value.
type HttpSecretURLAuth struct {
	// Secret key containing the full URL (including embedded credentials).
	URLSecretRef corev1.SecretKeySelector `json:"urlSecretRef"`
}

// HttpAuthSpec configures authentication for an HTTP Integration.
type HttpAuthSpec struct {
	// Authentication type.
	// +kubebuilder:validation:Enum=bearer;basic;apiKey;secretUrl
	Type HttpAuthType `json:"type"`

	// Bearer token config. Required when type=bearer.
	Bearer *HttpBearerAuth `json:"bearer,omitempty"`

	// Basic auth config. Required when type=basic.
	Basic *HttpBasicAuth `json:"basic,omitempty"`

	// API key header config. Required when type=apiKey.
	APIKey *HttpAPIKeyAuth `json:"apiKey,omitempty"`

	// Secret URL config. Required when type=secretUrl.
	SecretURL *HttpSecretURLAuth `json:"secretUrl,omitempty"`
}

// HttpTLSSpec configures outbound TLS trust and client authentication for HTTP
// steps that reference this Integration. Both fields are independently
// optional and purely additive: an Integration with no TLS field set behaves
// exactly as it does today (system root CA pool, no client certificate).
//
// The CA bundle is sourced from a ConfigMap rather than a Secret — a CA bundle
// is public trust material, not a credential — unlike the Secret-sourced
// CASecretRef convention used by KafkaTLSConfig/AmqpTLSConfig/NatsTLSConfig.
// A configured CA bundle is added to the system root pool, not a replacement
// for it, so an Integration with a private CA configured can still reach a
// public-CA-signed endpoint.
type HttpTLSSpec struct {
	// ConfigMap key containing a PEM-encoded CA bundle (one or more concatenated
	// certificates, e.g. a private root plus intermediates) to trust in addition
	// to the system root CA pool for outbound calls using this Integration.
	// +optional
	CABundleConfigMapRef *corev1.ConfigMapKeySelector `json:"caBundleConfigMapRef,omitempty"`

	// Secret containing a client certificate for mTLS. The Secret must contain
	// standard "tls.crt" and "tls.key" keys, matching the kubernetes.io/tls
	// Secret shape.
	// +optional
	ClientCertSecretRef *corev1.LocalObjectReference `json:"clientCertSecretRef,omitempty"`
}

// HttpIntegrationSpec contains configuration for HTTP-based integrations.
type HttpIntegrationSpec struct {
	// Base URL prepended to step URLs when this Integration is referenced.
	// If set, the step's url field is treated as a path (e.g. "/repos/org/repo/labels").
	// If the step url already starts with "http://" or "https://", baseUrl is ignored.
	// +optional
	BaseURL string `json:"baseUrl,omitempty"`

	// Authentication configuration.
	// +optional
	Auth *HttpAuthSpec `json:"auth,omitempty"`

	// Default headers merged into every request using this Integration.
	// Step-level headers override these defaults.
	// +optional
	DefaultHeaders map[string]string `json:"defaultHeaders,omitempty"`

	// TLS configures a private CA bundle to trust and/or a client certificate
	// to present for outbound HTTP steps that reference this Integration.
	// +optional
	TLS *HttpTLSSpec `json:"tls,omitempty"`
}

// IntegrationSpec defines desired state for Integration.
type IntegrationSpec struct {
	// Integration type.
	// kafka: first-party Kafka gateway (IBM/sarama).
	// amqp: first-party AMQP gateway; covers RabbitMQ, ActiveMQ, Solace, Azure Service Bus.
	//   Use spec.amqp.version to select 0-9-1 or 1.0 wire protocol.
	// nats: first-party NATS gateway; supports Core and JetStream.
	// plugin: community or custom image implementing the KubeZap plugin contract.
	//   See docs/api/plugin-contract.md.
	// http: lightweight HTTP endpoint with base URL, auth, and default headers.
	// +kubebuilder:validation:Enum=kafka;amqp;nats;plugin;http
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Type"
	Type string `json:"type"`

	// Kafka specific configuration. Required when type=kafka.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Kafka"
	Kafka *KafkaIntegrationSpec `json:"kafka,omitempty"`

	// AMQP specific configuration. Required when type=amqp.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="AMQP"
	Amqp *AmqpIntegrationSpec `json:"amqp,omitempty"`

	// NATS specific configuration. Required when type=nats.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="NATS"
	Nats *NatsIntegrationSpec `json:"nats,omitempty"`

	// Plugin specific configuration. Required when type=plugin.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Plugin"
	Plugin *PluginIntegrationSpec `json:"plugin,omitempty"`

	// HTTP specific configuration. Required when type=http.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="HTTP"
	HTTP *HttpIntegrationSpec `json:"http,omitempty"`
}

// IntegrationStatus defines observed state for Integration.
type IntegrationStatus struct {
	// Standard conditions.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Current phase of this integration.
	// +kubebuilder:validation:Enum=Ready;Degraded;Pending
	Phase string `json:"phase,omitempty"`

	// Managed gateway deployment name.
	GatewayDeploymentName string `json:"gatewayDeploymentName,omitempty"`

	// Last time reconciliation happened.
	LastReconciledTime *metav1.Time `json:"lastReconciledTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TYPE",type=string,JSONPath=`.spec.type`,description="Integration type"
// +kubebuilder:printcolumn:name="PHASE",type=string,JSONPath=`.status.phase`,description="Current phase"

// Integration connects KubeZap to an external system (Kafka, plugin, etc.).
// +operator-sdk:csv:customresourcedefinitions:resources={{Deployment,v1,""},{Service,v1,""},{ServiceAccount,v1,""},{Role,v1,""},{RoleBinding,v1,""}}
type Integration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IntegrationSpec   `json:"spec,omitempty"`
	Status IntegrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type IntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Integration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Integration{}, &IntegrationList{})
}
