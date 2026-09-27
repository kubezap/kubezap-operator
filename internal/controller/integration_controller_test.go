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

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("IntegrationReconciler", func() {
	const namespace = "default"

	newReconciler := func() *IntegrationReconciler {
		return &IntegrationReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
		}
	}

	reconcile := func(name string) {
		r := newReconciler()
		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	Context("when type=kafka and bootstrapServers is empty", func() {
		It("sets Ready=False, reason=InvalidSpec", func() {
			name := "kafka-no-servers"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "kafka",
					Kafka: &automationv1alpha1.KafkaIntegrationSpec{
						BootstrapServers: []string{},
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("InvalidSpec"))
		})
	})

	Context("when type=kafka and bootstrapServers is non-empty", func() {
		It("sets Ready=True, reason=IntegrationReady", func() {
			name := "kafka-valid"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "kafka",
					Kafka: &automationv1alpha1.KafkaIntegrationSpec{
						BootstrapServers: []string{"broker:9092"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("IntegrationReady"))
		})
	})

	Context("when type=plugin and image is empty", func() {
		It("sets Ready=False, reason=InvalidSpec", func() {
			name := "plugin-no-image"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "plugin",
					Plugin: &automationv1alpha1.PluginIntegrationSpec{
						Image: "",
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("InvalidSpec"))
		})
	})

	Context("when type=plugin with a valid image", func() {
		const pluginName = "my-plugin"
		const pluginImage = "example.io/my-plugin:v1.0.0"
		const deploymentName = "kubezap-plugin-" + pluginName

		var integration *automationv1alpha1.Integration

		BeforeEach(func() {
			integration = &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "plugin",
					Plugin: &automationv1alpha1.PluginIntegrationSpec{
						Image: pluginImage,
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				// Delete Deployment first (if it exists), then the Integration.
				dep := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespace}, dep); err == nil {
					_ = k8sClient.Delete(ctx, dep)
				}
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(pluginName)
		})

		It("sets Ready=True", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pluginName, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("IntegrationReady"))
		})

		It("creates a Deployment named kubezap-plugin-<name>", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespace}, dep)).To(Succeed())
		})

		It("the Deployment has the correct image and env vars injected", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: namespace}, dep)).To(Succeed())

			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := dep.Spec.Template.Spec.Containers[0]

			Expect(container.Image).To(Equal(pluginImage))

			envNames := make(map[string]string, len(container.Env))
			for _, e := range container.Env {
				envNames[e.Name] = e.Value
			}
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_NAMESPACE", namespace))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_INTEGRATION_NAME", pluginName))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_PUBLISHER_PORT", fmt.Sprintf("%d", int32(8090))))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_LOG_LEVEL", "info"))
		})
	})

	// ---- AMQP ----

	Context("when type=amqp and url is empty", func() {
		It("sets Ready=False, reason=InvalidSpec", func() {
			name := "amqp-no-url"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "amqp",
					Amqp: &automationv1alpha1.AmqpIntegrationSpec{
						URL: "",
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("InvalidSpec"))
		})
	})

	Context("when type=amqp and url is non-empty", func() {
		It("sets Ready=True, reason=IntegrationReady", func() {
			name := "amqp-valid"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "amqp",
					Amqp: &automationv1alpha1.AmqpIntegrationSpec{
						URL: "amqp://rabbitmq.default.svc.cluster.local:5672/",
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("IntegrationReady"))
		})
	})

	// ---- NATS ----

	Context("when type=nats and servers list is empty", func() {
		It("is rejected by CRD validation (MinItems=1 on spec.nats.servers)", func() {
			// The CRD enforces +kubebuilder:validation:MinItems=1 on servers,
			// so the API server rejects the Create with a 422 before the reconciler runs.
			name := "nats-no-servers"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "nats",
					Nats: &automationv1alpha1.NatsIntegrationSpec{
						Servers: []string{},
					},
				},
			}
			err := k8sClient.Create(ctx, integration)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 1 items"))
			// Object was rejected — no cleanup needed, no reconcile to trigger.
		})
	})

	Context("when type=nats and servers list is non-empty", func() {
		It("sets Ready=True, reason=IntegrationReady", func() {
			name := "nats-valid"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "nats",
					Nats: &automationv1alpha1.NatsIntegrationSpec{
						Servers: []string{"nats://nats.default.svc.cluster.local:4222"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("IntegrationReady"))
		})
	})

	Context("when type is unknown/unsupported", func() {
		// The CRD enum marker prevents creating an Integration with an unsupported type
		// via the Kubernetes API server. We validate the reconciler's own spec validation
		// function directly, which is the code path that would run if validation were
		// relaxed in a future version or if the object were patched around the webhook.
		It("sets Ready=False, reason=InvalidSpec (validated via validateIntegrationSpec)", func() {
			spec := automationv1alpha1.IntegrationSpec{
				Type: "rabbitmq",
			}
			err := validateIntegrationSpec(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown integration type"))
		})
	})

	// ---- Image digest pinning ----

	Describe("pluginImageRef helper", func() {
		Context("when ImageDigest is not set", func() {
			It("returns the bare image tag unchanged", func() {
				plugin := &automationv1alpha1.PluginIntegrationSpec{
					Image: "ghcr.io/my-org/my-plugin:v1.2.3",
				}
				Expect(pluginImageRef(plugin)).To(Equal("ghcr.io/my-org/my-plugin:v1.2.3"))
			})
		})

		Context("when ImageDigest is set", func() {
			It("returns image@sha256:<digest>", func() {
				digest := "a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4"
				plugin := &automationv1alpha1.PluginIntegrationSpec{
					Image:       "ghcr.io/my-org/my-plugin:v1.2.3",
					ImageDigest: digest,
				}
				Expect(pluginImageRef(plugin)).To(Equal("ghcr.io/my-org/my-plugin:v1.2.3@sha256:" + digest))
			})
		})
	})

	Context("when type=plugin with imageDigest set", func() {
		const digestPluginName = "my-plugin-pinned"
		const digestPluginImage = "ghcr.io/my-org/my-plugin:v1.2.3"
		const testDigest = "a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4"
		const digestDeploymentName = "kubezap-plugin-" + digestPluginName

		var digestIntegration *automationv1alpha1.Integration

		BeforeEach(func() {
			digestIntegration = &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: digestPluginName, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "plugin",
					Plugin: &automationv1alpha1.PluginIntegrationSpec{
						Image:       digestPluginImage,
						ImageDigest: testDigest,
					},
				},
			}
			Expect(k8sClient.Create(ctx, digestIntegration)).To(Succeed())
			DeferCleanup(func() {
				dep := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: digestDeploymentName, Namespace: namespace}, dep); err == nil {
					_ = k8sClient.Delete(ctx, dep)
				}
				_ = k8sClient.Delete(ctx, digestIntegration)
			})

			reconcile(digestPluginName)
		})

		It("sets the Deployment container image to image@sha256:<digest>", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: digestDeploymentName, Namespace: namespace}, dep)).To(Succeed())

			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal(digestPluginImage + "@sha256:" + testDigest))
		})

		It("sets the PluginDeployed condition message to include the digest-pinned image ref", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: digestPluginName, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "PluginDeployed")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("@sha256:" + testDigest))
		})

		It("sets Ready=True", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: digestPluginName, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "Ready")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("when type=plugin without imageDigest", func() {
		const plainPluginName = "my-plugin-plain"
		const plainPluginImage = "ghcr.io/my-org/my-plugin:v1.0.0"
		const plainDeploymentName = "kubezap-plugin-" + plainPluginName

		var plainIntegration *automationv1alpha1.Integration

		BeforeEach(func() {
			plainIntegration = &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: plainPluginName, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "plugin",
					Plugin: &automationv1alpha1.PluginIntegrationSpec{
						Image: plainPluginImage,
					},
				},
			}
			Expect(k8sClient.Create(ctx, plainIntegration)).To(Succeed())
			DeferCleanup(func() {
				dep := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: plainDeploymentName, Namespace: namespace}, dep); err == nil {
					_ = k8sClient.Delete(ctx, dep)
				}
				_ = k8sClient.Delete(ctx, plainIntegration)
			})

			reconcile(plainPluginName)
		})

		It("sets the Deployment container image to the bare image tag (no digest suffix)", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: plainDeploymentName, Namespace: namespace}, dep)).To(Succeed())

			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal(plainPluginImage))
		})

		It("sets the PluginDeployed condition message to include the bare image ref", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: plainPluginName, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "PluginDeployed")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring(plainPluginImage))
			Expect(cond.Message).NotTo(ContainSubstring("@sha256:"))
		})
	})

	Context("when type=plugin with spec.plugin.mtls.enabled=true", func() {
		const mtlsPluginName = "my-plugin-mtls"
		const mtlsPluginImage = "ghcr.io/my-org/my-plugin:v1.0.0"
		const mtlsDeploymentName = "kubezap-plugin-" + mtlsPluginName
		const mtlsSecretName = "kubezap-plugin-" + mtlsPluginName + "-mtls"

		var mtlsIntegration *automationv1alpha1.Integration

		BeforeEach(func() {
			mtlsIntegration = &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: mtlsPluginName, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "plugin",
					Plugin: &automationv1alpha1.PluginIntegrationSpec{
						Image: mtlsPluginImage,
						Mtls:  &automationv1alpha1.PluginMTLSSpec{Enabled: true},
					},
				},
			}
			Expect(k8sClient.Create(ctx, mtlsIntegration)).To(Succeed())
			DeferCleanup(func() {
				dep := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: mtlsDeploymentName, Namespace: namespace}, dep); err == nil {
					_ = k8sClient.Delete(ctx, dep)
				}
				secret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: mtlsSecretName, Namespace: namespace}, secret); err == nil {
					_ = k8sClient.Delete(ctx, secret)
				}
				_ = k8sClient.Delete(ctx, mtlsIntegration)
			})

			reconcile(mtlsPluginName)
		})

		It("creates a per-Integration mTLS Secret with tls.crt, tls.key, and ca.crt", func() {
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsSecretName, Namespace: namespace}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("tls.crt"))
			Expect(secret.Data).To(HaveKey("tls.key"))
			Expect(secret.Data).To(HaveKey("ca.crt"))
		})

		It("owner-references the mTLS Secret to the Integration for GC on delete", func() {
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsSecretName, Namespace: namespace}, secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(mtlsPluginName))
		})

		It("injects KUBEZAP_MTLS_* env vars pointing at the mounted Secret", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := dep.Spec.Template.Spec.Containers[0]

			envNames := make(map[string]string, len(container.Env))
			for _, e := range container.Env {
				envNames[e.Name] = e.Value
			}
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_MTLS_ENABLED", "true"))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_MTLS_CERT_FILE", "/etc/kubezap/mtls/tls.crt"))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_MTLS_KEY_FILE", "/etc/kubezap/mtls/tls.key"))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_MTLS_CA_FILE", "/etc/kubezap/mtls/ca.crt"))
			Expect(envNames).To(HaveKeyWithValue("KUBEZAP_MTLS_HEALTH_PORT", fmt.Sprintf("%d", defaultPluginMTLSHealthPort)))
		})

		It("mounts the mTLS Secret read-only into the plugin container", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			container := dep.Spec.Template.Spec.Containers[0]

			Expect(container.VolumeMounts).To(HaveLen(1))
			Expect(container.VolumeMounts[0].MountPath).To(Equal("/etc/kubezap/mtls"))
			Expect(container.VolumeMounts[0].ReadOnly).To(BeTrue())

			Expect(dep.Spec.Template.Spec.Volumes).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Volumes[0].Secret.SecretName).To(Equal(mtlsSecretName))
		})

		// Acceptance criterion: GET /healthz must not require a client cert
		// (kubelet's readinessProbe never presents one). The only way to
		// exempt a path from a TLS listener configured with
		// RequireAndVerifyClientCert is to serve it on a different,
		// plain-HTTP port — so the readiness probe must target that
		// separate port, not the mTLS-protected publisher port.
		It("points the readiness probe at the separate plain-HTTP mTLS health port, not the publisher port", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			container := dep.Spec.Template.Spec.Containers[0]

			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet.Path).To(Equal("/healthz"))
			Expect(container.ReadinessProbe.HTTPGet.Port.IntVal).To(Equal(defaultPluginMTLSHealthPort))
			Expect(container.ReadinessProbe.HTTPGet.Port.IntVal).NotTo(Equal(int32(8090)))
			Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
		})

		It("exposes the mTLS health port as a named ContainerPort", func() {
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			container := dep.Spec.Template.Spec.Containers[0]

			var found bool
			for _, p := range container.Ports {
				if p.Name == "mtls-health" {
					found = true
					Expect(p.ContainerPort).To(Equal(defaultPluginMTLSHealthPort))
				}
			}
			Expect(found).To(BeTrue())
		})

		It("sets a PluginMTLSReady=True condition", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsPluginName, Namespace: namespace}, fetched)).To(Succeed())

			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, "PluginMTLSReady")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("CertBundleReconciled"))
		})

		It("requeues periodically so the bundle is rechecked for rotation", func() {
			r := newReconciler()
			result, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: mtlsPluginName, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pluginMTLSRecheckInterval))
		})

		It("removes the mTLS Secret and tracked bundle when mtls.enabled is set back to false", func() {
			fetched := &automationv1alpha1.Integration{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mtlsPluginName, Namespace: namespace}, fetched)).To(Succeed())
			fetched.Spec.Plugin.Mtls.Enabled = false
			Expect(k8sClient.Update(ctx, fetched)).To(Succeed())

			r := newReconciler()
			Expect(r.pluginMTLSStore().ClientTLSConfigFor(namespace, mtlsPluginName)).To(BeNil(), "fresh reconciler has its own lazily-initialized store")
			_, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: mtlsPluginName, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: mtlsSecretName, Namespace: namespace}, secret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	// See docs/design/secret-rotation-watches.md: the kafka/amqp/nats
	// gateways previously had no secrets RBAC at all, so readSecretKey's Get
	// call must already have been failing with Forbidden in any real cluster.
	Context("broker gateway RBAC — secrets permission", func() {
		It("grants get/list/watch on secrets in the shared kubezap-gateway Role", func() {
			name := "kafka-rbac-secrets"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type:  "kafka",
					Kafka: &automationv1alpha1.KafkaIntegrationSpec{BootstrapServers: []string{"broker:9092"}},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, integration)
			})

			reconcile(name)

			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kubezap-gateway", Namespace: namespace}, role)).To(Succeed())

			var hasSecretsRule bool
			for _, rule := range role.Rules {
				if containsString(rule.APIGroups, "") && containsString(rule.Resources, "secrets") {
					hasSecretsRule = true
					Expect(rule.Verbs).To(ConsistOf("get", "list", "watch"),
						"secrets rule must grant get/list/watch — list/watch are required for the Secret informer that detects rotation")
				}
			}
			Expect(hasSecretsRule).To(BeTrue(), "kubezap-gateway Role must grant a secrets rule, or SASL/TLS secretRefs can never resolve against a live cluster")
		})
	})

	// STORY-067: docs/design/gateway-credential-failure-visibility.md. The
	// mechanism deliberately grants only events:create — never any write verb
	// on triggers/triggers-status — to each of the three broker gateway Role
	// rule sets (kafkaGatewayRules/amqpGatewayRules/natsGatewayRules), so a
	// gateway can surface a credential-resolution failure without needing
	// write access to Trigger or its status subresource.
	Context("broker gateway RBAC — events:create permission", func() {
		assertEventsCreateRule := func(role *rbacv1.Role) {
			var hasEventsRule bool
			for _, rule := range role.Rules {
				if containsString(rule.APIGroups, "") && containsString(rule.Resources, "events") {
					hasEventsRule = true
					Expect(rule.Verbs).To(ConsistOf("create"),
						"events rule must grant create only — no read/write verb beyond that is needed for this mechanism")
				}
				// Guard against the RBAC bump having accidentally widened the
				// triggers/triggers-status rules instead of adding a new events rule.
				if containsString(rule.APIGroups, apiGroupAutomation) && containsString(rule.Resources, resourceTriggers) {
					Expect(rule.Verbs).To(ConsistOf("get", "list", "watch"),
						"the triggers rule must remain read-only — this mechanism must never grant a broker gateway write access to Trigger")
				}
			}
			Expect(hasEventsRule).To(BeTrue(), "kubezap-gateway Role must grant an events:create rule")
		}

		It("grants create on events via the kafka Integration reconcile path", func() {
			name := "kafka-rbac-events"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type:  "kafka",
					Kafka: &automationv1alpha1.KafkaIntegrationSpec{BootstrapServers: []string{"broker:9092"}},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, integration) })
			reconcile(name)

			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kubezap-gateway", Namespace: namespace}, role)).To(Succeed())
			assertEventsCreateRule(role)
		})

		It("grants create on events via the amqp Integration reconcile path", func() {
			name := "amqp-rbac-events"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "amqp",
					Amqp: &automationv1alpha1.AmqpIntegrationSpec{URL: "amqp://rabbitmq.default.svc.cluster.local:5672/"},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, integration) })
			reconcile(name)

			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kubezap-gateway", Namespace: namespace}, role)).To(Succeed())
			assertEventsCreateRule(role)
		})

		It("grants create on events via the nats Integration reconcile path", func() {
			name := "nats-rbac-events"
			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: automationv1alpha1.IntegrationSpec{
					Type: "nats",
					Nats: &automationv1alpha1.NatsIntegrationSpec{Servers: []string{"nats://nats.default.svc.cluster.local:4222"}},
				},
			}
			Expect(k8sClient.Create(ctx, integration)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, integration) })
			reconcile(name)

			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "kubezap-gateway", Namespace: namespace}, role)).To(Succeed())
			assertEventsCreateRule(role)
		})
	})
})

// Broker gateway images default to the ":latest" tag, which Kubernetes defaults to
// imagePullPolicy: Always unless set explicitly. Without PullIfNotPresent, a registry
// blip (or, in local dev, an image that was only ever built+imported locally, never
// pushed) makes the gateway Deployment ImagePullBackOff forever, even once a matching
// image already exists on the node.
var _ = Describe("broker gateway Deployment image pull policy", func() {
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-integration", Namespace: "default"},
	}

	It("sets ImagePullPolicy: IfNotPresent on the kafka-gateway container", func() {
		dep := desiredKafkaGatewayDeployment(integration)
		Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(dep.Spec.Template.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
	})

	It("sets ImagePullPolicy: IfNotPresent on the amqp-gateway container", func() {
		dep := desiredAmqpGatewayDeployment(integration)
		Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(dep.Spec.Template.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
	})

	It("sets ImagePullPolicy: IfNotPresent on the nats-gateway container", func() {
		dep := desiredNatsGatewayDeployment(integration)
		Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(dep.Spec.Template.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
	})
})

// buildKafkaTriggers's output metadata keys must match KEDA's actual Kafka scaler
// parameter names exactly, or the ScaledObject fails validation at the KEDA operator
// (not at kube-apiserver admission, since metadata is a freeform map[string]string) --
// live testing found this returning "brokerList" instead of the required
// "bootstrapServers", which silently broke KEDA-based Kafka gateway scaling entirely
// (the ScaledObject's Ready condition reported "missing required parameter
// bootstrapServers" and never created the underlying HPA) with no test coverage to
// catch it.
var _ = Describe("buildKafkaTriggers", func() {
	It("produces the exact metadata keys KEDA's Kafka scaler requires", func() {
		integration := &automationv1alpha1.Integration{
			Spec: automationv1alpha1.IntegrationSpec{
				Kafka: &automationv1alpha1.KafkaIntegrationSpec{
					BootstrapServers: []string{"broker-1:9092", "broker-2:9092"},
				},
			},
		}
		pairs := []kafkaTopicCGPair{{topic: "orders.created", cg: "kubezap-my-trigger"}}

		triggers := buildKafkaTriggers(integration, pairs)

		Expect(triggers).To(HaveLen(1))
		trigger, ok := triggers[0].(map[string]interface{})
		Expect(ok).To(BeTrue())
		Expect(trigger["type"]).To(Equal("kafka"))
		metadata, ok := trigger["metadata"].(map[string]interface{})
		Expect(ok).To(BeTrue())
		Expect(metadata).To(HaveKeyWithValue("bootstrapServers", "broker-1:9092,broker-2:9092"))
		Expect(metadata).To(HaveKeyWithValue("consumerGroup", "kubezap-my-trigger"))
		Expect(metadata).To(HaveKeyWithValue("topic", "orders.created"))
		Expect(metadata).NotTo(HaveKey("brokerList"))
	})
})
