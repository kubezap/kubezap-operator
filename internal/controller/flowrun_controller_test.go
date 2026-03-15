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
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

var _ = Describe("FlowRunReconciler", func() {
	const testNamespace = "default"

	newReconciler := func() *FlowRunReconciler {
		return &FlowRunReconciler{
			Client:       k8sClient,
			Scheme:       k8sClient.Scheme(),
			HTTPClient:   http.DefaultClient,
			TTLSucceeded: 24 * time.Hour,
			TTLFailed:    72 * time.Hour,
		}
	}

	makeFlow := func(name string, steps []automationv1alpha1.FlowStep) *automationv1alpha1.Flow {
		return &automationv1alpha1.Flow{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: automationv1alpha1.FlowSpec{
				Steps: steps,
			},
		}
	}

	makeFlowRun := func(name, flowName string) *automationv1alpha1.FlowRun {
		return &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef: corev1.LocalObjectReference{Name: flowName},
			},
		}
	}

	reconcileAndFetch := func(flowRunName string) (*automationv1alpha1.FlowRun, error) {
		r := newReconciler()
		nn := types.NamespacedName{Name: flowRunName, Namespace: testNamespace}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
		if err != nil {
			return nil, err
		}
		var updated automationv1alpha1.FlowRun
		fetchErr := k8sClient.Get(ctx, nn, &updated)
		return &updated, fetchErr
	}

	Context("when the referenced Flow does not exist", func() {
		var flowRun *automationv1alpha1.FlowRun

		BeforeEach(func() {
			flowRun = makeFlowRun(fmt.Sprintf("fr-noflow-%d", GinkgoRandomSeed()), "nonexistent-flow")
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
			})
		})

		It("sets FlowRun phase=Failed with a descriptive message", func() {
			updated, err := reconcileAndFetch(flowRun.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal("Failed"))
			Expect(updated.Status.Message).To(ContainSubstring("nonexistent-flow"))
		})
	})

	Context("with a valid single http step that returns 200", func() {
		var (
			server  *httptest.Server
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"result":"ok"}`))
			}))

			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-200-%d", seed)
			flowRunName := fmt.Sprintf("fr-200-%d", seed)

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "call-backend",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    server.URL,
							Method: "POST",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				server.Close()
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("sets FlowRun phase=Succeeded", func() {
			updated, err := reconcileAndFetch(flowRun.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal("Succeeded"))
		})

		It("records the step result in status.stepStatuses with phase=Succeeded", func() {
			updated, err := reconcileAndFetch(flowRun.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Steps).NotTo(BeEmpty())
			Expect(updated.Status.Steps[0].Name).To(Equal("call-backend"))
			Expect(updated.Status.Steps[0].Phase).To(Equal("Succeeded"))
		})
	})

	Context("with an http step that returns 500", func() {
		var (
			server  *httptest.Server
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}))

			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-500-%d", seed)
			flowRunName := fmt.Sprintf("fr-500-%d", seed)

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "call-backend",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    server.URL,
							Method: "POST",
						},
					},
					RetryPolicy: &automationv1alpha1.RetryPolicy{
						MaxRetries: 0,
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				server.Close()
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("sets FlowRun phase=Failed", func() {
			updated, err := reconcileAndFetch(flowRun.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal("Failed"))
		})
	})

	Context("when FlowRun is already in phase Succeeded", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-already-done-%d", seed)
			flowRunName := fmt.Sprintf("fr-already-done-%d", seed)

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "noop",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    "http://127.0.0.1:0",
							Method: "POST",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			// Set status to Succeeded via the status subresource.
			now := metav1.Now()
			flowRun.Status.Phase = "Succeeded"
			flowRun.Status.CompletionTime = &now
			Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("reconciles without changing the phase or re-executing steps", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Succeeded"))
			// No step statuses should have been added by the reconciler (already terminal).
			Expect(updated.Status.Steps).To(BeEmpty())
		})
	})
})
