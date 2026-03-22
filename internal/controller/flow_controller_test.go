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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("Flow Controller", func() {
	const namespace = "default"

	ctx := context.Background()

	newFlow := func(name string, spec automationv1alpha1.FlowSpec) *automationv1alpha1.Flow {
		return &automationv1alpha1.Flow{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: spec,
		}
	}

	reconcileFlow := func(name string) (reconcile.Result, error) {
		r := &FlowReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		return r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
	}

	fetchFlow := func(name string) *automationv1alpha1.Flow {
		flow := &automationv1alpha1.Flow{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, flow)).To(Succeed())
		return flow
	}

	deleteFlow := func(name string) {
		flow := &automationv1alpha1.Flow{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, flow)
		if err == nil {
			Expect(k8sClient.Delete(ctx, flow)).To(Succeed())
		}
	}

	Describe("Reconciling a Flow resource", func() {
		Context("when spec.steps is empty", func() {
			// The Flow CRD schema enforces +kubebuilder:validation:MinItems=1 on spec.steps,
			// so the API server rejects a Flow with no steps at admission time (HTTP 422).
			// The reconciler's own empty-steps guard is defence-in-depth and is not reachable
			// via the normal API path.
			It("is rejected by the API server with a validation error", func() {
				flow := newFlow("flow-empty-steps", automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{},
				})
				err := k8sClient.Create(ctx, flow)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("spec.steps"))
			})
		})

		Context("when two steps have the same name", func() {
			const flowName = "flow-duplicate-step-names"

			BeforeEach(func() {
				flow := newFlow(flowName, automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{
						{
							Name: "step-a",
							Action: automationv1alpha1.StepAction{
								Type: "http",
								HTTP: &automationv1alpha1.HTTPAction{URL: "https://example.com"},
							},
						},
						{
							Name: "step-a",
							Action: automationv1alpha1.StepAction{
								Type: "transform",
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			})

			AfterEach(func() {
				deleteFlow(flowName)
			})

			It("sets Ready=False with reason=InvalidSpec", func() {
				_, err := reconcileFlow(flowName)
				Expect(err).NotTo(HaveOccurred())

				flow := fetchFlow(flowName)
				cond := apimeta.FindStatusCondition(flow.Status.Conditions, "Ready")
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal("InvalidSpec"))
			})
		})

		Context("when a step runAfter references a nonexistent step", func() {
			const flowName = "flow-bad-runafter"

			BeforeEach(func() {
				flow := newFlow(flowName, automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{
						{
							Name:     "step-b",
							RunAfter: []string{"nonexistent-step"},
							Action: automationv1alpha1.StepAction{
								Type: "transform",
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			})

			AfterEach(func() {
				deleteFlow(flowName)
			})

			It("sets Ready=False with reason=InvalidSpec", func() {
				_, err := reconcileFlow(flowName)
				Expect(err).NotTo(HaveOccurred())

				flow := fetchFlow(flowName)
				cond := apimeta.FindStatusCondition(flow.Status.Conditions, "Ready")
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal("InvalidSpec"))
			})
		})

		Context("when an http step has no URL", func() {
			const flowName = "flow-http-no-url"

			BeforeEach(func() {
				flow := newFlow(flowName, automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{
						{
							Name: "step-http",
							Action: automationv1alpha1.StepAction{
								Type: "http",
								HTTP: &automationv1alpha1.HTTPAction{URL: ""},
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			})

			AfterEach(func() {
				deleteFlow(flowName)
			})

			It("sets Ready=False with reason=InvalidSpec", func() {
				_, err := reconcileFlow(flowName)
				Expect(err).NotTo(HaveOccurred())

				flow := fetchFlow(flowName)
				cond := apimeta.FindStatusCondition(flow.Status.Conditions, "Ready")
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal("InvalidSpec"))
			})
		})

		Context("when a publish step has no integrationRef", func() {
			const flowName = "flow-publish-no-ref"

			BeforeEach(func() {
				flow := newFlow(flowName, automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{
						{
							Name: "step-publish",
							Action: automationv1alpha1.StepAction{
								Type: "publish",
								Publish: &automationv1alpha1.PublishAction{
									Topic: "my-topic",
									// IntegrationRef.Name intentionally left empty
								},
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			})

			AfterEach(func() {
				deleteFlow(flowName)
			})

			It("sets Ready=False with reason=InvalidSpec", func() {
				_, err := reconcileFlow(flowName)
				Expect(err).NotTo(HaveOccurred())

				flow := fetchFlow(flowName)
				cond := apimeta.FindStatusCondition(flow.Status.Conditions, "Ready")
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal("InvalidSpec"))
			})
		})

		Context("when the spec is fully valid (one http step with URL)", func() {
			const flowName = "flow-valid"

			BeforeEach(func() {
				flow := newFlow(flowName, automationv1alpha1.FlowSpec{
					Steps: []automationv1alpha1.FlowStep{
						{
							Name: "call-api",
							Action: automationv1alpha1.StepAction{
								Type: "http",
								HTTP: &automationv1alpha1.HTTPAction{
									URL:    "https://api.example.com/notify",
									Method: "POST",
								},
							},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			})

			AfterEach(func() {
				deleteFlow(flowName)
			})

			It("sets Ready=True with reason=FlowReady", func() {
				_, err := reconcileFlow(flowName)
				Expect(err).NotTo(HaveOccurred())

				flow := fetchFlow(flowName)
				cond := apimeta.FindStatusCondition(flow.Status.Conditions, "Ready")
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("FlowReady"))
			})
		})
	})
})
