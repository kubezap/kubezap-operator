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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("ExecutorReconciler", func() {
	const namespace = "default"
	const testImage = "ghcr.io/kubezap/http-executor:test"

	newReconciler := func() *ExecutorReconciler {
		return &ExecutorReconciler{
			Client:        k8sClient,
			Scheme:        scheme.Scheme,
			ExecutorImage: testImage,
			ExecutorPort:  defaultExecutorPort,
		}
	}

	// newFlowRun creates a minimal FlowRun in the given namespace and returns its name.
	newFlowRun := func(name string) *automationv1alpha1.FlowRun {
		fr := &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef: automationv1alpha1.FlowReference{
					Name: "some-flow",
				},
			},
		}
		Expect(k8sClient.Create(ctx, fr)).To(Succeed())
		return fr
	}

	reconcile := func(name string) {
		r := newReconciler()
		_, err := r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	Context("when a FlowRun exists in a namespace", func() {
		var flowRun *automationv1alpha1.FlowRun

		BeforeEach(func() {
			flowRun = newFlowRun("test-flowrun-executor")
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, flowRun)

				// Clean up executor resources created by reconciler.
				dep := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, dep); err == nil {
					_ = k8sClient.Delete(ctx, dep)
				}
				svc := &corev1.Service{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, svc); err == nil {
					_ = k8sClient.Delete(ctx, svc)
				}
				np := &networkingv1.NetworkPolicy{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: executorNetworkPolicyName, Namespace: namespace}, np); err == nil {
					_ = k8sClient.Delete(ctx, np)
				}
			})
		})

		It("creates the executor Deployment", func() {
			reconcile(flowRun.Name)

			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal(testImage))
		})

		It("creates the executor Service", func() {
			reconcile(flowRun.Name)

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(defaultExecutorPort))
		})

		It("creates the executor NetworkPolicy", func() {
			reconcile(flowRun.Name)

			np := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorNetworkPolicyName, Namespace: namespace}, np)).To(Succeed())
			Expect(np.Spec.Ingress).To(HaveLen(1))
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
		})

		It("is idempotent when reconciled twice", func() {
			reconcile(flowRun.Name)
			reconcile(flowRun.Name)

			// All three resources should still exist with a single instance each.
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, dep)).To(Succeed())

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, svc)).To(Succeed())

			np := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorNetworkPolicyName, Namespace: namespace}, np)).To(Succeed())
		})
	})

	Context("when the FlowRun has been deleted before reconcile runs", func() {
		It("returns without error (NotFound is a no-op)", func() {
			r := newReconciler()
			_, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
