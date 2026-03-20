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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
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

	Context("execution timeout recovery", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-timeout-%d", seed)
			flowRunName := fmt.Sprintf("fr-timeout-%d", seed)

			// CRD requires MinItems=1; timeout fires before any step is executed.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{Name: "placeholder", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"key": "val"}},
				}},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			// Simulate a Running FlowRun that started 2 hours ago.
			startedAt := metav1.NewTime(time.Now().Add(-2 * time.Hour))
			flowRun.Status.Phase = "Running"
			flowRun.Status.StartTime = &startedAt
			Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("fails a Running FlowRun that exceeded ExecutionTimeout", func() {
			r := newReconciler()
			r.ExecutionTimeout = time.Hour // 1h timeout; FlowRun has been running 2h

			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Failed"))
			Expect(updated.Status.Message).To(ContainSubstring("execution timeout exceeded"))
		})
	})

	Context("executing finalizer lifecycle", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			// A wait step with a 1h duration causes the reconciler to requeue after
			// setting Running, leaving the finalizer visible for the "adds finalizer" test.
			// The "removes finalizer" It re-creates with a fast transform step instead.
			flow = makeFlow(fmt.Sprintf("flow-finalizer-%d", seed), []automationv1alpha1.FlowStep{
				{Name: "wait-step", Action: automationv1alpha1.StepAction{
					Type: "wait",
					Wait: &automationv1alpha1.WaitAction{Duration: "1h"},
				}},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(fmt.Sprintf("fr-finalizer-%d", seed), flow.Name)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				// Clear any finalizer the reconciler may have added so Delete is not blocked.
				var fr automationv1alpha1.FlowRun
				if err := k8sClient.Get(context.Background(),
					types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}, &fr); err == nil {
					fr.Finalizers = nil
					_ = k8sClient.Update(context.Background(), &fr)
				}
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("adds the kubezap.io/executing finalizer when transitioning Pending→Running", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			// First reconcile: transitions Pending → Running (adds finalizer).
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement("kubezap.io/executing"))
		})

		It("removes the finalizer when the FlowRun succeeds", func() {
			// Use a transform step (completes immediately) so the FlowRun reaches
			// Succeeded in a single reconcile pass.
			seed := GinkgoRandomSeed()
			fastFlow := makeFlow(fmt.Sprintf("flow-fast-%d", seed), []automationv1alpha1.FlowStep{
				{Name: "t", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"k": "v"}},
				}},
			})
			Expect(k8sClient.Create(ctx, fastFlow)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), fastFlow) })

			fastRun := makeFlowRun(fmt.Sprintf("fr-fast-%d", seed), fastFlow.Name)
			Expect(k8sClient.Create(ctx, fastRun)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), fastRun) })

			r := newReconciler()
			nn := types.NamespacedName{Name: fastRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Succeeded"))
			Expect(updated.Finalizers).NotTo(ContainElement("kubezap.io/executing"))
		})
	})

	Context("DeletionTimestamp while running", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-del-running-%d", seed)
			flowRunName := fmt.Sprintf("fr-del-running-%d", seed)

			// CRD requires MinItems=1; reconciler takes the DeletionTimestamp path
			// before executing any steps.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{Name: "placeholder", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"key": "val"}},
				}},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			// Pre-add the finalizer so deletion is blocked until we remove it.
			flowRun.Finalizers = []string{"kubezap.io/executing"}
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			// Set phase=Running via status subresource.
			now := metav1.Now()
			flowRun.Status.Phase = "Running"
			flowRun.Status.StartTime = &now
			Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())

			// Issue delete to set DeletionTimestamp (finalizer blocks actual removal).
			Expect(k8sClient.Delete(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				// Ensure the finalizer is gone so the object can be cleaned up.
				var fr automationv1alpha1.FlowRun
				if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: flowRunName, Namespace: testNamespace}, &fr); err == nil {
					fr.Finalizers = nil
					_ = k8sClient.Update(context.Background(), &fr)
					_ = k8sClient.Delete(context.Background(), &fr)
				}
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("fails the FlowRun and removes the executing finalizer", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			getErr := k8sClient.Get(ctx, nn, &updated)
			if apierrors.IsNotFound(getErr) {
				// Object was fully GC'd — finalizer removal triggered immediate deletion.
				// Status().Update (Phase=Failed) succeeded before the object was purged.
				return
			}
			Expect(getErr).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal("Failed"))
			Expect(updated.Finalizers).NotTo(ContainElement("kubezap.io/executing"))
		})
	})
})
