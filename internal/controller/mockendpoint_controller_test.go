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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

var _ = Describe("MockEndpointReconciler", func() {
	var (
		reconciler *MockEndpointReconciler
		namespace  string
	)

	BeforeEach(func() {
		reconciler = &MockEndpointReconciler{
			Client: k8sClient,
			Scheme: runtime.NewScheme(),
		}
		namespace = "default"
	})

	Context("when spec.path is empty", func() {
		It("sets Ready=False, reason=InvalidSpec", func() {
			me := &automationv1alpha1.MockEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-path-test",
					Namespace: namespace,
				},
				Spec: automationv1alpha1.MockEndpointSpec{
					Path: "",
				},
			}
			Expect(k8sClient.Create(ctx, me)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, me)
			})

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: me.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			fetched := &automationv1alpha1.MockEndpoint{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: me.Name, Namespace: namespace}, fetched)).To(Succeed())

			var readyCond *metav1.Condition
			for i := range fetched.Status.Conditions {
				if fetched.Status.Conditions[i].Type == "Ready" {
					readyCond = &fetched.Status.Conditions[i]
					break
				}
			}
			Expect(readyCond).NotTo(BeNil(), "expected Ready condition to be set")
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("InvalidSpec"))
		})
	})

	Context("when spec.path is non-empty", func() {
		It("sets Ready=True", func() {
			me := &automationv1alpha1.MockEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-path-test",
					Namespace: namespace,
				},
				Spec: automationv1alpha1.MockEndpointSpec{
					Path: "/my-endpoint",
				},
			}
			Expect(k8sClient.Create(ctx, me)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, me)
			})

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: me.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			fetched := &automationv1alpha1.MockEndpoint{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: me.Name, Namespace: namespace}, fetched)).To(Succeed())

			var readyCond *metav1.Condition
			for i := range fetched.Status.Conditions {
				if fetched.Status.Conditions[i].Type == "Ready" {
					readyCond = &fetched.Status.Conditions[i]
					break
				}
			}
			Expect(readyCond).NotTo(BeNil(), "expected Ready condition to be set")
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("Registered"))
		})

		It("sets status.url to contain the path", func() {
			me := &automationv1alpha1.MockEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "url-path-test",
					Namespace: namespace,
				},
				Spec: automationv1alpha1.MockEndpointSpec{
					Path: "/check-url",
				},
			}
			Expect(k8sClient.Create(ctx, me)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, me)
			})

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: me.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			fetched := &automationv1alpha1.MockEndpoint{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: me.Name, Namespace: namespace}, fetched)).To(Succeed())

			Expect(fetched.Status.URL).To(ContainSubstring("check-url"))
		})
	})

	Context("when KUBEZAP_GATEWAY_BASE_URL env var is set", func() {
		BeforeEach(func() {
			Expect(os.Setenv("KUBEZAP_GATEWAY_BASE_URL", "http://gateway.example.com")).To(Succeed())
		})

		AfterEach(func() {
			Expect(os.Unsetenv("KUBEZAP_GATEWAY_BASE_URL")).To(Succeed())
		})

		It("status.url is prefixed with the base URL", func() {
			me := &automationv1alpha1.MockEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "base-url-test",
					Namespace: namespace,
				},
				Spec: automationv1alpha1.MockEndpointSpec{
					Path: "/with-base-url",
				},
			}
			Expect(k8sClient.Create(ctx, me)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, me)
			})

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: me.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			fetched := &automationv1alpha1.MockEndpoint{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: me.Name, Namespace: namespace}, fetched)).To(Succeed())

			Expect(fetched.Status.URL).To(HavePrefix("http://gateway.example.com"))
			Expect(fetched.Status.URL).To(ContainSubstring("with-base-url"))
		})
	})

	Context("when the MockEndpoint is deleted", func() {
		It("reconciles without error (no NotFound panic)", func() {
			me := &automationv1alpha1.MockEndpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deleted-test",
					Namespace: namespace,
				},
				Spec: automationv1alpha1.MockEndpointSpec{
					Path: "/some-path",
				},
			}
			Expect(k8sClient.Create(ctx, me)).To(Succeed())
			Expect(k8sClient.Delete(ctx, me)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: me.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
