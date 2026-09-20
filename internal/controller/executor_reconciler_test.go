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
			Client:            k8sClient,
			Scheme:            scheme.Scheme,
			ExecutorImage:     testImage,
			ExecutorPort:      DefaultExecutorPort,
			OperatorNamespace: "kubezap-system",
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

		// Live testing found the liveness/readiness probes pointed at the main
		// --port, which requires a valid client certificate under --mtls=true --
		// kubelet's httpGet probes can never present one, so the executor Deployment
		// never became Ready with mTLS enabled, and HTTP steps failed outright since
		// the Service only routed to the stale, non-mTLS pod. Probes must always
		// target the separate, always-plain-HTTP health port instead.
		It("points liveness and readiness probes at the separate health port, not the main port", func() {
			reconcile(flowRun.Name)

			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, dep)).To(Succeed())
			container := dep.Spec.Template.Spec.Containers[0]

			Expect(container.Ports).To(ContainElement(corev1.ContainerPort{
				Name:          portNameHealth,
				ContainerPort: executorHealthPort,
				Protocol:      corev1.ProtocolTCP,
			}))
			Expect(container.Args).To(ContainElement(fmt.Sprintf("--health-port=%d", executorHealthPort)))
			Expect(container.LivenessProbe.HTTPGet.Port.IntVal).To(Equal(executorHealthPort))
			Expect(container.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
			Expect(container.ReadinessProbe.HTTPGet.Port.IntVal).To(Equal(executorHealthPort))
			Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
		})

		It("creates the executor Service", func() {
			reconcile(flowRun.Name)

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorDeploymentName, Namespace: namespace}, svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(DefaultExecutorPort))
		})

		It("creates the executor NetworkPolicy", func() {
			reconcile(flowRun.Name)

			np := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorNetworkPolicyName, Namespace: namespace}, np)).To(Succeed())
			Expect(np.Spec.Ingress).To(HaveLen(1))
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))

			// Regression test: the ingress From peer must match the REAL
			// controller-manager pod's labels across BOTH install paths — the raw
			// kustomize manifests (config/manager/manager.yaml, app.kubernetes.io/name:
			// kubezap) and the Helm chart (charts/kubezap-operator, app.kubernetes.io/name:
			// kubezap-operator, Helm's chart-name convention) disagree on
			// app.kubernetes.io/name for the controller pod itself, so the selector
			// must key on control-plane=controller-manager ALONE (the one label both
			// paths agree on), not also require app.kubernetes.io/name. It must also
			// carry a NamespaceSelector, since the controller normally runs in a
			// different namespace than the executor it's reaching. A selector that
			// doesn't match the real pod silently breaks every HTTP step whenever
			// NetworkPolicy is actually enforced by the cluster's CNI — verified live
			// on k3s: this exact mismatch blocked 100% of HTTP steps on a Helm install.
			Expect(np.Spec.Ingress[0].From).To(HaveLen(1))
			peer := np.Spec.Ingress[0].From[0]
			Expect(peer.PodSelector).NotTo(BeNil())
			Expect(peer.PodSelector.MatchLabels).To(Equal(map[string]string{
				"control-plane": "controller-manager",
			}))
			Expect(peer.NamespaceSelector).NotTo(BeNil())
			Expect(peer.NamespaceSelector.MatchLabels).To(Equal(map[string]string{
				"kubernetes.io/metadata.name": "kubezap-system",
			}))

			// Egress: SSRF defense-in-depth (see docs/design/executor-egress-networkpolicy.md).
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
			Expect(np.Spec.Egress).To(HaveLen(2), "one rule for HTTP(S) egress minus blocked ranges, one for DNS")
			httpEgress := np.Spec.Egress[0]
			Expect(httpEgress.To).To(HaveLen(2), "one IPv4 ipBlock, one IPv6 ipBlock")
			Expect(httpEgress.To[0].IPBlock.CIDR).To(Equal("0.0.0.0/0"))
			Expect(httpEgress.To[0].IPBlock.Except).To(ContainElements(
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
			), "must exclude RFC1918 and link-local/cloud-metadata ranges")
			Expect(httpEgress.To[1].IPBlock.CIDR).To(Equal("::/0"))
			Expect(httpEgress.To[1].IPBlock.Except).To(ContainElement("fe80::/10"))
			Expect(httpEgress.Ports).To(BeEmpty(),
				"must not restrict destination ports — Flow steps and integrations legitimately target arbitrary ports, not just 80/443")
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

		It("drops the RFC1918/link-local egress exceptions when SSRFAllowClusterInternal is set", func() {
			r := newReconciler()
			r.SSRFAllowClusterInternal = true
			_, err := r.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: flowRun.Name, Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())

			np := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: executorNetworkPolicyName, Namespace: namespace}, np)).To(Succeed())
			Expect(np.Spec.Egress).To(HaveLen(2))
			httpEgress := np.Spec.Egress[0]
			Expect(httpEgress.To).To(HaveLen(2))
			Expect(httpEgress.To[0].IPBlock.CIDR).To(Equal("0.0.0.0/0"))
			Expect(httpEgress.To[0].IPBlock.Except).To(BeEmpty(),
				"in-cluster calls (e.g. to a dev Mockoon service) must not be network-blocked when the software SSRF check already allows them")
			Expect(httpEgress.To[1].IPBlock.CIDR).To(Equal("::/0"))
			Expect(httpEgress.To[1].IPBlock.Except).To(BeEmpty())
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
