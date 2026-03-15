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

	// Publisher service port.
	// +kubebuilder:default=8090
	PublisherPort int32 `json:"publisherPort,omitempty"`

	// Secret references for plugin env vars.
	SecretRefs []PluginSecretRef `json:"secretRefs,omitempty"`

	// Additional environment variables.
	Env []corev1.EnvVar `json:"env,omitempty"`
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
	// +kubebuilder:validation:Enum=kafka;amqp;nats;plugin
	Type string `json:"type"`

	// Kafka specific configuration. Required when type=kafka.
	Kafka *KafkaIntegrationSpec `json:"kafka,omitempty"`

	// AMQP specific configuration. Required when type=amqp.
	Amqp *AmqpIntegrationSpec `json:"amqp,omitempty"`

	// NATS specific configuration. Required when type=nats.
	Nats *NatsIntegrationSpec `json:"nats,omitempty"`

	// Plugin specific configuration. Required when type=plugin.
	Plugin *PluginIntegrationSpec `json:"plugin,omitempty"`
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
