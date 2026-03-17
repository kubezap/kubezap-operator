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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
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
		It("sets Ready=False, reason=InvalidSpec", func() {
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
})
