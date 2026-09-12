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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/cel-go/cel"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
)

// mockSyncProducer is a minimal sarama.SyncProducer stub used by the Kafka
// producer cache unit tests. It records whether Close() was called so tests
// can assert eviction behaviour without connecting to a real broker.
type mockSyncProducer struct {
	closeCalled bool
	lastMessage *sarama.ProducerMessage
}

func (m *mockSyncProducer) SendMessage(msg *sarama.ProducerMessage) (int32, int64, error) {
	m.lastMessage = msg
	return 0, 0, nil
}
func (m *mockSyncProducer) SendMessages([]*sarama.ProducerMessage) error { return nil }
func (m *mockSyncProducer) Close() error                                 { m.closeCalled = true; return nil }
func (m *mockSyncProducer) TxnStatus() sarama.ProducerTxnStatusFlag      { return 0 }
func (m *mockSyncProducer) IsTransactional() bool                        { return false }
func (m *mockSyncProducer) BeginTxn() error                              { return nil }
func (m *mockSyncProducer) CommitTxn() error                             { return nil }
func (m *mockSyncProducer) AbortTxn() error                              { return nil }
func (m *mockSyncProducer) AddOffsetsToTxn(map[string][]*sarama.PartitionOffsetMetadata, string) error {
	return nil
}
func (m *mockSyncProducer) AddOffsetsToTxnWithGroupMetadata(
	map[string][]*sarama.PartitionOffsetMetadata, *sarama.ConsumerGroupMetadata,
) error {
	return nil
}
func (m *mockSyncProducer) AddMessageToTxn(*sarama.ConsumerMessage, string, *string) error {
	return nil
}
func (m *mockSyncProducer) AddMessageToTxnWithGroupMetadata(
	*sarama.ConsumerMessage, *sarama.ConsumerGroupMetadata, *string,
) error {
	return nil
}

var _ = Describe("FlowRunReconciler", func() {
	const testNamespace = "default"

	newReconciler := func() *FlowRunReconciler {
		env, err := cel.NewEnv(
			cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
		)
		Expect(err).NotTo(HaveOccurred(), "failed to initialize CEL env in test reconciler")

		// Start a real executor test server with SSRF disabled so tests can target
		// httptest servers bound to 127.0.0.1 without being blocked by the blocklist.
		execHandler := &executorhttp.Handler{
			BlockedCIDRs:   []*net.IPNet{}, // no SSRF blocking in tests
			BodyLimitBytes: 64 * 1024,
			HTTPClient:     http.DefaultClient,
		}
		execServer := httptest.NewServer(executorhttp.New(execHandler))
		DeferCleanup(execServer.Close)

		return &FlowRunReconciler{
			Client:           k8sClient,
			Scheme:           k8sClient.Scheme(),
			HTTPClient:       http.DefaultClient, // used for RPC to execServer
			TTLSucceeded:     24 * time.Hour,
			TTLFailed:        72 * time.Hour,
			celEnv:           env,
			SSRFBlockedCIDRs: []*net.IPNet{}, // disable SSRF pre-check in tests
			ExecutorBaseURL:  execServer.URL, // plain URL, namespace placeholder not needed
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
				FlowRef: automationv1alpha1.FlowReference{Name: flowName},
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

	// reconcileUntilTerminal drives Reconcile() in a loop until the FlowRun reaches
	// a terminal phase (Succeeded, Failed, or Cancelled) or maxIterations is exhausted.
	// This is required for multi-step flows under the §12b one-step-per-reconcile model,
	// where each call processes exactly one step wave and returns Requeue: true.
	reconcileUntilTerminal := func(r *FlowRunReconciler, flowRunName string, maxIterations int) (*automationv1alpha1.FlowRun, error) {
		nn := types.NamespacedName{Name: flowRunName, Namespace: testNamespace}
		for i := 0; i < maxIterations; i++ {
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			if err != nil {
				return nil, err
			}
			var updated automationv1alpha1.FlowRun
			if fetchErr := k8sClient.Get(ctx, nn, &updated); fetchErr != nil {
				return nil, fetchErr
			}
			switch updated.Status.Phase {
			case automationv1alpha1.FlowRunPhaseSucceeded, automationv1alpha1.FlowRunPhaseFailed, automationv1alpha1.FlowRunPhaseCancelled:
				return &updated, nil
			}
			// If no requeue is requested and phase is not terminal, stop.
			//nolint:staticcheck // Requeue (not just RequeueAfter) is a real, actively-used
			// signal in this controller's Reconcile (see flowrun_controller.go) — not vacuous.
			if !result.Requeue && result.RequeueAfter == 0 {
				return &updated, nil
			}
		}
		var final automationv1alpha1.FlowRun
		if err := k8sClient.Get(ctx, nn, &final); err != nil {
			return nil, err
		}
		return &final, nil
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
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed))
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
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))
		})

		It("records the step result in status.stepStatuses with phase=Succeeded", func() {
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Steps).NotTo(BeEmpty())
			Expect(updated.Status.Steps[0].Name).To(Equal("call-backend"))
			Expect(updated.Status.Steps[0].Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))
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
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed))
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
			flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseSucceeded
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
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))
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
			flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
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
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed))
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
			updated, err := reconcileUntilTerminal(r, fastRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			_ = nn

			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))
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
			flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
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

		It("cancels the FlowRun and removes the executing finalizer", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			getErr := k8sClient.Get(ctx, nn, &updated)
			if apierrors.IsNotFound(getErr) {
				// Object was fully GC'd — finalizer removal triggered immediate deletion.
				// Status().Update (Phase=Cancelled) succeeded before the object was purged.
				return
			}
			Expect(getErr).NotTo(HaveOccurred())
			// Deletion-while-Running is a cancellation, not a failure — see
			// docs/architecture/flowrun-state-model.md. Reporting it as "Failed" would
			// misrepresent an intentional deletion as an error in status, conditions,
			// and the kubezap_flowrun_duration_seconds metric.
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseCancelled))
			Expect(updated.Finalizers).NotTo(ContainElement("kubezap.io/executing"))

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Cancelled")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("FlowRunCancelled"))
			Expect(apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")).To(BeNil())
		})
	})

	Context("kubezap.io/cancel annotation while running", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-cancel-annotation-%d", seed)
			flowRunName := fmt.Sprintf("fr-cancel-annotation-%d", seed)

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{Name: "placeholder", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"key": "val"}},
				}},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			flowRun.Annotations = map[string]string{"kubezap.io/cancel": "true"}
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			// Set phase=Running via status subresource — the annotation only takes
			// effect while Running (docs/api/flowrun.md: "Cancel a running FlowRun").
			now := metav1.Now()
			flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
			flowRun.Status.StartTime = &now
			Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("cancels the FlowRun without touching the DeletionTimestamp path", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseCancelled))
			Expect(updated.DeletionTimestamp).To(BeNil())

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Cancelled")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("FlowRunCancelled"))
		})

		It("does not cancel a FlowRun that hasn't reached Running yet", func() {
			seed := GinkgoRandomSeed()
			pendingName := fmt.Sprintf("fr-cancel-pending-%d", seed)
			pending := makeFlowRun(pendingName, flow.Name)
			pending.Annotations = map[string]string{"kubezap.io/cancel": "true"}
			Expect(k8sClient.Create(ctx, pending)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), pending)
			})

			r := newReconciler()
			nn := types.NamespacedName{Name: pendingName, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).NotTo(Equal(automationv1alpha1.FlowRunPhaseCancelled))
		})
	})

	Context("Flow parameters", func() {
		It("fails the FlowRun before dispatching any step when a required param is missing", func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-required-param-%d", seed)
			flowRunName := fmt.Sprintf("fr-required-param-%d", seed)

			flow := makeFlow(flowName, []automationv1alpha1.FlowStep{
				{Name: "placeholder", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"key": "val"}},
				}},
			})
			flow.Spec.Params = []automationv1alpha1.ParamDeclaration{
				{Name: "orderId", Required: true},
			}
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), flow) })

			flowRun := makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), flowRun) })

			updated, err := reconcileAndFetch(flowRunName)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed))
			Expect(updated.Status.Message).To(ContainSubstring("orderId"))
			Expect(updated.Status.Steps).To(BeEmpty(), "no step should have been dispatched")
		})

		It("resolves $(params.<name>) from a same-named trigger body field into a step result", func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-param-resolution-%d", seed)
			flowRunName := fmt.Sprintf("fr-param-resolution-%d", seed)

			flow := makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "extract",
					Action: automationv1alpha1.StepAction{
						Type: "transform",
						Transform: &automationv1alpha1.TransformAction{
							Mappings: map[string]string{"resolvedOrderId": "$(params.orderId)"},
						},
					},
				},
			})
			flow.Spec.Params = []automationv1alpha1.ParamDeclaration{{Name: "orderId"}}
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), flow) })

			flowRun := makeFlowRun(flowRunName, flowName)
			flowRun.Spec.TriggerData = &automationv1alpha1.TriggerData{
				Body: `{"orderId":"ord-live-test"}`,
			}
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), flowRun) })

			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRunName, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))
			Expect(updated.Status.Steps).To(HaveLen(1))
			Expect(updated.Status.Steps[0].Results).To(ContainElement(
				automationv1alpha1.ResultValue{Name: "resolvedOrderId", Value: "ord-live-test"},
			))
		})
	})

	Context("orphan recovery for stuck Running FlowRuns", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-orphan-%d", seed)
			flowRunName := fmt.Sprintf("fr-orphan-%d", seed)

			// CRD requires MinItems=1; the step is never reached because the
			// orphan timeout fires first.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{Name: "placeholder", Action: automationv1alpha1.StepAction{
					Type:      "transform",
					Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"key": "val"}},
				}},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			// Pre-add the executing finalizer to simulate a FlowRun that was
			// mid-execution when the controller restarted.
			flowRun.Finalizers = []string{"kubezap.io/executing"}
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			// Set phase=Running with a StartTime 4 hours in the past — well
			// beyond the default 72h TTLFailed and the 1h ExecutionTimeout
			// we will configure on the reconciler.
			startedAt := metav1.NewTime(time.Now().Add(-4 * time.Hour))
			flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
			flowRun.Status.StartTime = &startedAt
			Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				// Clear any leftover finalizer so the object can be GC'd.
				var fr automationv1alpha1.FlowRun
				if err := k8sClient.Get(context.Background(),
					types.NamespacedName{Name: flowRunName, Namespace: testNamespace}, &fr); err == nil {
					fr.Finalizers = nil
					_ = k8sClient.Update(context.Background(), &fr)
				}
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("transitions a stuck Running FlowRun to Failed and removes the finalizer", func() {
			r := newReconciler()
			r.ExecutionTimeout = time.Hour // 1h timeout; FlowRun has been running 4h

			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed))
			Expect(updated.Status.Message).To(ContainSubstring("execution timeout exceeded"))
			Expect(updated.Finalizers).NotTo(ContainElement("kubezap.io/executing"))
		})
	})

	Context("CEL when=false skip cascade to dependent step", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-skip-cascade-%d", seed)
			flowRunName := fmt.Sprintf("fr-skip-cascade-%d", seed)

			// Step A: transform (no condition — runs and succeeds).
			// Step B: transform with when="false" — will be skipped.
			// Step C: transform with runAfter=["B"] — cascade-skipped because B is skipped.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "step-a",
					Action: automationv1alpha1.StepAction{
						Type:      "transform",
						Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"k": "v"}},
					},
				},
				{
					Name: "step-b",
					When: []automationv1alpha1.WhenExpression{{Expression: "false"}},
					Action: automationv1alpha1.StepAction{
						Type:      "transform",
						Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"k": "v"}},
					},
				},
				{
					Name:     "step-c",
					RunAfter: []string{"step-b"},
					Action: automationv1alpha1.StepAction{
						Type:      "transform",
						Transform: &automationv1alpha1.TransformAction{Mappings: map[string]string{"k": "v"}},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("skips B (when=false), cascade-skips C (runAfter B), and succeeds overall", func() {
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 20)
			Expect(err).NotTo(HaveOccurred())

			// Overall FlowRun should succeed — all steps reached a terminal state.
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))

			// Verify individual step statuses.
			Expect(updated.Status.Steps).To(HaveLen(3))

			stepA := findStepStatus(updated.Status.Steps, "step-a")
			Expect(stepA).NotTo(BeNil())
			Expect(stepA.Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))

			stepB := findStepStatus(updated.Status.Steps, "step-b")
			Expect(stepB).NotTo(BeNil())
			Expect(stepB.Phase).To(Equal(automationv1alpha1.StepPhaseSkipped))
			Expect(stepB.Message).To(ContainSubstring("when condition"))

			stepC := findStepStatus(updated.Status.Steps, "step-c")
			Expect(stepC).NotTo(BeNil())
			Expect(stepC.Phase).To(Equal(automationv1alpha1.StepPhaseSkipped))
			Expect(stepC.Message).To(ContainSubstring("runAfter dependencies were skipped"))
		})
	})

	Context("transform step + result chaining (T8)", func() {
		var (
			server      *httptest.Server
			flow        *automationv1alpha1.Flow
			flowRun     *automationv1alpha1.FlowRun
			capturedURL string
		)

		BeforeEach(func() {
			// httptest server captures the request URL so the test can assert that
			// variable substitution placed the correct orderId in the path.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedURL = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))

			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-t8-%d", seed)
			flowRunName := fmt.Sprintf("fr-t8-%d", seed)

			// Step 1: transform — maps $(trigger.body.orderId) to result key "orderId".
			// Step 2: http — uses $(steps.transform-step.results.orderId) in URL path.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "transform-step",
					Action: automationv1alpha1.StepAction{
						Type: "transform",
						Transform: &automationv1alpha1.TransformAction{
							Mappings: map[string]string{
								"orderId": "$(trigger.body.orderId)",
							},
						},
					},
				},
				{
					Name:     "http-step",
					RunAfter: []string{"transform-step"},
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    server.URL + "/orders/$(steps.transform_step.results.orderId)",
							Method: "GET",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			// FlowRun carries TriggerData with a JSON body containing orderId.
			flowRun = &automationv1alpha1.FlowRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      flowRunName,
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.FlowRunSpec{
					FlowRef: automationv1alpha1.FlowReference{Name: flowName},
					TriggerData: &automationv1alpha1.TriggerData{
						Body:        `{"orderId":"ORD-42"}`,
						ContentType: "application/json",
					},
				},
			}
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				server.Close()
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("substitutes orderId from trigger body through transform into the HTTP URL", func() {
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 15)
			Expect(err).NotTo(HaveOccurred())

			// Overall FlowRun must succeed.
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))

			// Transform step must have succeeded and emitted the orderId result.
			transformStatus := findStepStatus(updated.Status.Steps, "transform-step")
			Expect(transformStatus).NotTo(BeNil())
			Expect(transformStatus.Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))
			Expect(transformStatus.Results).To(ContainElement(
				automationv1alpha1.ResultValue{Name: "orderId", Value: "ORD-42"},
			))

			// HTTP step must have succeeded.
			httpStatus := findStepStatus(updated.Status.Steps, "http-step")
			Expect(httpStatus).NotTo(BeNil())
			Expect(httpStatus.Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))

			// The server must have received a request whose URL contains the substituted orderId.
			Expect(capturedURL).To(ContainSubstring("ORD-42"))
		})
	})

	Context("wait step timeout and requeue behavior", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-wait-%d", seed)
			flowRunName := fmt.Sprintf("fr-wait-%d", seed)

			// A wait step with a short duration (100ms) so we can assert both the
			// "still waiting / requeue" path and the "wait elapsed / Succeeded" path.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "wait-step",
					Action: automationv1alpha1.StepAction{
						Type: "wait",
						Wait: &automationv1alpha1.WaitAction{Duration: "100ms"},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				// Clear any finalizer so Delete is not blocked.
				var fr automationv1alpha1.FlowRun
				if err := k8sClient.Get(context.Background(),
					types.NamespacedName{Name: flowRunName, Namespace: testNamespace}, &fr); err == nil {
					fr.Finalizers = nil
					_ = k8sClient.Update(context.Background(), &fr)
				}
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("requeues (result.RequeueAfter > 0) on the first reconcile while the wait has not elapsed", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}

			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			// The reconciler must request a requeue because the wait period has not
			// yet elapsed; it stores ResumeAfter in the step status and returns a
			// positive RequeueAfter duration.
			Expect(result.RequeueAfter).To(BeNumerically(">", 0),
				"expected RequeueAfter > 0 while wait period is active")

			// The step status must be Waiting, not Succeeded yet.
			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			waitStatus := findStepStatus(updated.Status.Steps, "wait-step")
			Expect(waitStatus).NotTo(BeNil())
			Expect(waitStatus.Phase).To(Equal(automationv1alpha1.StepPhaseWaiting))
			Expect(waitStatus.ResumeAfter).NotTo(BeNil())
		})

		It("completes the step with phase=Succeeded after the wait duration elapses", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}

			// First reconcile: transitions Pending → Running, hits the wait step,
			// stores ResumeAfter, and requeues.
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			// Verify the step is in Waiting state before sleeping.
			var midRun automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &midRun)).To(Succeed())
			waitStatus := findStepStatus(midRun.Status.Steps, "wait-step")
			Expect(waitStatus).NotTo(BeNil())
			Expect(waitStatus.Phase).To(Equal(automationv1alpha1.StepPhaseWaiting))

			// Sleep long enough for the 100ms wait to elapse.
			time.Sleep(150 * time.Millisecond)

			// Second reconcile: the wait has elapsed, step should now complete
			// (Succeeded) and return Requeue: true so the next reconcile can
			// finalize the FlowRun. Under §12b one-step-per-reconcile, the step
			// completion and the FlowRun terminal transition happen in separate calls.
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			// Verify the wait step is now Succeeded before driving to terminal phase.
			var midRun2 automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &midRun2)).To(Succeed())
			doneStep := findStepStatus(midRun2.Status.Steps, "wait-step")
			Expect(doneStep).NotTo(BeNil())
			Expect(doneStep.Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))
			Expect(doneStep.CompletionTime).NotTo(BeNil())

			// Third reconcile: all steps are terminal, FlowRun transitions to Succeeded.
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var finished automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &finished)).To(Succeed())
			Expect(finished.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))
		})
	})

	Context("step retry with exponential backoff (T7)", func() {
		var (
			server  *httptest.Server
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
			calls   atomic.Int32
		)

		BeforeEach(func() {
			calls.Store(0)
			// Mock HTTP server: returns 503 for first 2 requests, 200 on the 3rd.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n <= 2 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"service unavailable"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"result":"ok"}`))
			}))

			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-retry-exp-%d", seed)
			flowRunName := fmt.Sprintf("fr-retry-exp-%d", seed)

			tenMs := metav1.Duration{Duration: 10 * time.Millisecond}
			hundredMs := metav1.Duration{Duration: 100 * time.Millisecond}

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "retry-step",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    server.URL,
							Method: "POST",
						},
					},
					RetryPolicy: &automationv1alpha1.RetryPolicy{
						MaxRetries:   3,
						BackoffType:  "Exponential",
						InitialDelay: &tenMs,
						MaxDelay:     &hundredMs,
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

		It("succeeds after retrying through 503s with exponential backoff", func() {
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())

			// The FlowRun should succeed because the 3rd attempt returns 200.
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseSucceeded))

			// Verify the step itself succeeded.
			Expect(updated.Status.Steps).NotTo(BeEmpty())
			retryStep := findStepStatus(updated.Status.Steps, "retry-step")
			Expect(retryStep).NotTo(BeNil())
			Expect(retryStep.Phase).To(Equal(automationv1alpha1.StepPhaseSucceeded))

			// The mock server should have received exactly 3 calls (2 x 503, 1 x 200).
			Expect(calls.Load()).To(BeNumerically("==", 3))

			// Verify the actual attempt count is reflected in step status.
			// With MaxRetries=3 and the mock returning 503 twice then 200, we expect 3 total attempts.
			Expect(retryStep.Attempts).To(BeNumerically("==", 3))
		})
	})

	// §12c — Secret value redaction (P0)
	// Verifies that raw secret values are never persisted to StepRunStatus.Message.
	Context("secret value redaction in failed HTTP step (§12c)", func() {
		var (
			secret  *corev1.Secret
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		const secretValue = "super-secret-token-12345"

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			secretName := fmt.Sprintf("test-secret-%d", seed)
			flowName := fmt.Sprintf("flow-secret-redact-%d", seed)
			flowRunName := fmt.Sprintf("fr-secret-redact-%d", seed)

			// Create a Kubernetes Secret containing a known value.
			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"token": []byte(secretValue),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create a Flow whose HTTP step URL embeds a $(secrets.*) reference.
			// The target URL uses port 1 on loopback, which returns an immediate
			// connection-refused. The executor reaches the target address but gets
			// an application-level error, giving us a failed step whose Message we
			// can inspect for secret redaction — without a long network timeout.
			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "http-with-secret",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							// Embed the secret in the URL path so it would appear in
							// the error message if not redacted.
							URL:    fmt.Sprintf("http://127.0.0.1:1/api/$(secrets.%s.token)", secretName),
							Method: "GET",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), secret)
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("does NOT store the raw secret value in StepRunStatus.Message after failure", func() {
			// Use reconcileUntilTerminal; the executor will get an immediate
			// connection-refused from the target (127.0.0.1:1) and return an
			// application-level error — this is not a transport error so the
			// FlowRun fails rather than requeuing.
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())

			// The FlowRun must have failed (unreachable target host).
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseFailed),
				"expected FlowRun to fail because target host is unreachable")

			// Find the failed step status.
			stepStatus := findStepStatus(updated.Status.Steps, "http-with-secret")
			Expect(stepStatus).NotTo(BeNil(), "expected step status to be recorded")
			Expect(stepStatus.Phase).To(Equal(automationv1alpha1.StepPhaseFailed))

			// THE SECURITY ASSERTION: the raw secret value must not appear in the
			// persisted step message.
			Expect(stepStatus.Message).NotTo(ContainSubstring(secretValue),
				"secret value must not be stored in StepRunStatus.Message")

			// The message must contain [REDACTED] so it is clear a secret was present.
			Expect(stepStatus.Message).To(ContainSubstring("[REDACTED]"),
				"[REDACTED] marker must appear in the error message in place of the secret value")
		})
	})

	// §18 P2 TECH DEBT — executor RPC transport failure → requeue with backoff
	// Verifies that when the http-executor pod is unreachable (connection refused),
	// the reconciler returns RequeueAfter instead of marking the step as Failed.
	Context("executor pod unreachable (transport error) → requeue with backoff (§18)", func() {
		var (
			flow    *automationv1alpha1.Flow
			flowRun *automationv1alpha1.FlowRun
		)

		BeforeEach(func() {
			seed := GinkgoRandomSeed()
			flowName := fmt.Sprintf("flow-transport-backoff-%d", seed)
			flowRunName := fmt.Sprintf("fr-transport-backoff-%d", seed)

			flow = makeFlow(flowName, []automationv1alpha1.FlowStep{
				{
					Name: "http-step",
					Action: automationv1alpha1.StepAction{
						Type: "http",
						HTTP: &automationv1alpha1.HTTPAction{
							URL:    "http://example.com/api",
							Method: "POST",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, flow)).To(Succeed())

			flowRun = makeFlowRun(flowRunName, flowName)
			Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), flowRun)
				_ = k8sClient.Delete(context.Background(), flow)
			})
		})

		It("returns RequeueAfter and does not mark the step as Failed", func() {
			// Build a reconciler that points to a guaranteed-dead executor address
			// (port 1 on loopback is conventionally unreachable).
			env, err := cel.NewEnv(
				cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
				cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
				cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
			)
			Expect(err).NotTo(HaveOccurred())

			r := &FlowRunReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				HTTPClient:       http.DefaultClient,
				TTLSucceeded:     24 * time.Hour,
				TTLFailed:        72 * time.Hour,
				celEnv:           env,
				SSRFBlockedCIDRs: []*net.IPNet{},
				ExecutorBaseURL:  "http://127.0.0.1:1", // port 1 is unreachable
			}

			nn := types.NamespacedName{Name: flowRun.Name, Namespace: testNamespace}

			// First reconcile: Pending → Running.
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			// Re-reconcile until we either hit a RequeueAfter or a terminal phase.
			for i := 0; i < 5; i++ {
				if result.RequeueAfter > 0 {
					break
				}
				var current automationv1alpha1.FlowRun
				Expect(k8sClient.Get(ctx, nn, &current)).To(Succeed())
				if current.Status.Phase == automationv1alpha1.FlowRunPhaseSucceeded || current.Status.Phase == automationv1alpha1.FlowRunPhaseFailed {
					break
				}
				result, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())
			}

			// The reconciler must have requested a backoff requeue.
			Expect(result.RequeueAfter).To(Equal(executorTransportBackoff),
				"expected RequeueAfter=%s for transport error, got %s", executorTransportBackoff, result.RequeueAfter)

			// The FlowRun must NOT be marked Failed — it should still be Running.
			var updated automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(automationv1alpha1.FlowRunPhaseRunning),
				"FlowRun must remain Running on executor transport failure, not Failed")

			// The step must NOT appear as Failed in the status (it should be absent
			// or still Running — not persisted as a failure).
			stepStatus := findStepStatus(updated.Status.Steps, "http-step")
			if stepStatus != nil {
				Expect(stepStatus.Phase).NotTo(Equal(automationv1alpha1.StepPhaseFailed),
					"step must not be marked Failed on executor transport error")
			}
		})
	})

	Describe("Kafka producer cache", func() {
		newMock := func() *mockSyncProducer { return &mockSyncProducer{} }

		// buildMinimalReconciler creates a FlowRunReconciler with only the fields
		// needed for getOrCreateKafkaProducer — no envtest k8s client required.
		buildMinimalReconciler := func() *FlowRunReconciler {
			return &FlowRunReconciler{
				kafkaProducers:        make(map[string]sarama.SyncProducer),
				kafkaProducerLastUsed: make(map[string]time.Time),
			}
		}

		Context("cache hit — producer still within TTL", func() {
			It("returns the cached producer without closing it or creating a new one", func() {
				r := buildMinimalReconciler()
				mock := newMock()

				const brokerKey = "broker1:9092"
				r.kafkaProducers[brokerKey] = mock
				r.kafkaProducerLastUsed[brokerKey] = time.Now() // fresh timestamp

				integration := &automationv1alpha1.Integration{
					Spec: automationv1alpha1.IntegrationSpec{
						Kafka: &automationv1alpha1.KafkaIntegrationSpec{
							BootstrapServers: []string{brokerKey},
						},
					},
				}

				got, err := r.getOrCreateKafkaProducer(context.Background(), brokerKey, integration)
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(BeIdenticalTo(mock), "cached producer should be returned as-is")
				Expect(mock.closeCalled).To(BeFalse(), "Close must not be called on a fresh producer")
				Expect(r.kafkaProducers).To(HaveKey(brokerKey), "cache entry must still be present after hit")
			})
		})

		Context("TTL eviction — producer idle past 10 minutes", func() {
			It("closes the old producer, evicts it from the cache, and returns an error (no broker available)", func() {
				r := buildMinimalReconciler()
				mock := newMock()

				const brokerKey = "localhost:19999" // nothing listening here

				r.kafkaProducers[brokerKey] = mock
				// Set last-used 11 minutes in the past — just past the 10-minute TTL.
				r.kafkaProducerLastUsed[brokerKey] = time.Now().Add(-11 * time.Minute)

				integration := &automationv1alpha1.Integration{
					Spec: automationv1alpha1.IntegrationSpec{
						Kafka: &automationv1alpha1.KafkaIntegrationSpec{
							BootstrapServers: []string{brokerKey},
						},
					},
				}

				_, err := r.getOrCreateKafkaProducer(context.Background(), brokerKey, integration)

				// The function must fail because there is no real broker at brokerKey.
				Expect(err).To(HaveOccurred(), "expected error creating producer against unreachable broker")

				// Most importantly: the stale producer must have been closed and evicted.
				Expect(mock.closeCalled).To(BeTrue(), "old producer must be closed on TTL eviction")
				Expect(r.kafkaProducers).NotTo(HaveKey(brokerKey),
					"evicted producer must be removed from the cache map")
				Expect(r.kafkaProducerLastUsed).NotTo(HaveKey(brokerKey),
					"evicted producer's last-used entry must be removed from the cache map")
			})
		})
	})

	Describe("executePublishStep — Kafka topic interpolation", func() {
		It("resolves $(...) placeholders in the topic before publishing, not just in body/headers", func() {
			const brokerKey = "broker1:9092"
			mock := &mockSyncProducer{}

			r := &FlowRunReconciler{
				kafkaProducers:        map[string]sarama.SyncProducer{brokerKey: mock},
				kafkaProducerLastUsed: map[string]time.Time{brokerKey: time.Now()},
			}

			integration := &automationv1alpha1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
				Spec: automationv1alpha1.IntegrationSpec{
					Kafka: &automationv1alpha1.KafkaIntegrationSpec{
						BootstrapServers: []string{brokerKey},
					},
				},
			}

			flowRun := &automationv1alpha1.FlowRun{
				ObjectMeta: metav1.ObjectMeta{Name: "run-1", Namespace: "default"},
			}
			step := &automationv1alpha1.FlowStep{
				Name: "publish-step",
				Action: automationv1alpha1.StepAction{
					Type: "publish",
					Publish: &automationv1alpha1.PublishAction{
						IntegrationRef: corev1.LocalObjectReference{Name: "kafka-integ"},
						Topic:          "orders.$(trigger.body.region)",
						Body:           "hello",
					},
				},
			}
			triggerData := &automationv1alpha1.TriggerData{Body: `{"region":"us-east"}`}

			integCache := map[string]*automationv1alpha1.Integration{
				"default/kafka-integ": integration,
			}
			ctx := context.WithValue(context.Background(), integrationCacheKey, integCache)

			_, attempts, err := r.executePublishStep(ctx, flowRun, step, triggerData, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(attempts).To(Equal(1))

			Expect(mock.lastMessage).NotTo(BeNil())
			Expect(mock.lastMessage.Topic).To(Equal("orders.us-east"),
				"topic must be resolved through substituteVars, not published as the raw $(...) template")
		})
	})

	Describe("FlowRun terminal-phase immutability", func() {
		// See docs/architecture/flowrun-state-model.md "Invalid / Dangerous
		// Transitions": Succeeded→Running, Failed→Running, Cancelled→Running, and
		// Succeeded→Failed must never occur. A FlowRun already in a terminal phase
		// must return early on reconcile without touching Status.Steps or dispatching
		// any step, regardless of which terminal phase it's in.
		DescribeTable("never re-enters Running or dispatches a step from a terminal phase",
			func(terminalPhase automationv1alpha1.FlowRunPhase) {
				var requestCount int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&requestCount, 1)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer server.Close()

				seed := GinkgoRandomSeed()
				flowName := fmt.Sprintf("flow-terminal-%s-%d", strings.ToLower(string(terminalPhase)), seed)
				flowRunName := fmt.Sprintf("fr-terminal-%s-%d", strings.ToLower(string(terminalPhase)), seed)

				flow := makeFlow(flowName, []automationv1alpha1.FlowStep{
					{
						Name: "call-backend",
						Action: automationv1alpha1.StepAction{
							Type: "http",
							HTTP: &automationv1alpha1.HTTPAction{URL: server.URL, Method: "POST"},
						},
					},
				})
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())

				flowRun := makeFlowRun(flowRunName, flowName)
				Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

				now := metav1.Now()
				flowRun.Status.Phase = terminalPhase
				flowRun.Status.CompletionTime = &now
				Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())
				DeferCleanup(func() {
					_ = k8sClient.Delete(context.Background(), flowRun)
					_ = k8sClient.Delete(context.Background(), flow)
				})

				r := newReconciler()
				nn := types.NamespacedName{Name: flowRunName, Namespace: testNamespace}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())

				var updated automationv1alpha1.FlowRun
				Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(terminalPhase),
					"a FlowRun in a terminal phase must never transition to any other phase, including Running")
				Expect(updated.Status.Steps).To(BeEmpty(),
					"no step should be dispatched once the FlowRun is terminal")
				Expect(atomic.LoadInt32(&requestCount)).To(Equal(int32(0)),
					"the step's backend must never be called once the FlowRun is terminal")
			},
			Entry("Succeeded", automationv1alpha1.FlowRunPhaseSucceeded),
			Entry("Failed", automationv1alpha1.FlowRunPhaseFailed),
			Entry("Cancelled", automationv1alpha1.FlowRunPhaseCancelled),
		)
	})

	Describe("step terminal-phase immutability (Running FlowRun, one step already terminal)", func() {
		// See docs/architecture/flowrun-state-model.md: "A step in a terminal phase
		// (Succeeded, Failed, Skipped) must not be re-dispatched." Unlike the
		// FlowRun-level guard above, this must hold even while the FlowRun itself is
		// still Running and other steps are still pending — the guard is per-step,
		// not just a single early-return at the top of Reconcile.
		DescribeTable("does not re-dispatch a step already in a terminal phase",
			func(terminalStepPhase automationv1alpha1.StepPhase) {
				var terminalStepRequests, secondStepRequests int32
				terminalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&terminalStepRequests, 1)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer terminalServer.Close()
				secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					atomic.AddInt32(&secondStepRequests, 1)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer secondServer.Close()

				seed := GinkgoRandomSeed()
				flowName := fmt.Sprintf("flow-step-terminal-%s-%d", strings.ToLower(string(terminalStepPhase)), seed)
				flowRunName := fmt.Sprintf("fr-step-terminal-%s-%d", strings.ToLower(string(terminalStepPhase)), seed)

				flow := makeFlow(flowName, []automationv1alpha1.FlowStep{
					{
						Name: "already-done",
						Action: automationv1alpha1.StepAction{
							Type: "http",
							HTTP: &automationv1alpha1.HTTPAction{URL: terminalServer.URL, Method: "POST"},
						},
					},
					{
						Name:        "second-step",
						RunAfter:    []string{"already-done"},
						RetryPolicy: &automationv1alpha1.RetryPolicy{MaxRetries: 0},
						Action: automationv1alpha1.StepAction{
							Type: "http",
							HTTP: &automationv1alpha1.HTTPAction{URL: secondServer.URL, Method: "POST"},
						},
					},
				})
				// failurePolicy: Continue so a Failed "already-done" still satisfies
				// second-step's runAfter — this test cares about the terminal-step
				// re-dispatch guard, not failurePolicy propagation (covered elsewhere).
				flow.Spec.FailurePolicy = automationv1alpha1.FailurePolicyContinue
				Expect(k8sClient.Create(ctx, flow)).To(Succeed())

				flowRun := makeFlowRun(flowRunName, flowName)
				Expect(k8sClient.Create(ctx, flowRun)).To(Succeed())

				fixedTime := metav1.Now()
				flowRun.Status.Phase = automationv1alpha1.FlowRunPhaseRunning
				flowRun.Status.StartTime = &fixedTime
				flowRun.Status.Steps = []automationv1alpha1.StepRunStatus{
					{
						Name:           "already-done",
						Phase:          terminalStepPhase,
						Attempts:       1,
						StartTime:      &fixedTime,
						CompletionTime: &fixedTime,
					},
				}
				Expect(k8sClient.Status().Update(ctx, flowRun)).To(Succeed())
				DeferCleanup(func() {
					_ = k8sClient.Delete(context.Background(), flowRun)
					_ = k8sClient.Delete(context.Background(), flow)
				})

				r := newReconciler()
				_, err := reconcileUntilTerminal(r, flowRunName, 10)
				Expect(err).NotTo(HaveOccurred())

				var updated automationv1alpha1.FlowRun
				nn := types.NamespacedName{Name: flowRunName, Namespace: testNamespace}
				Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())

				firstStep := findStepStatus(updated.Status.Steps, "already-done")
				Expect(firstStep).NotTo(BeNil())
				Expect(firstStep.Phase).To(Equal(terminalStepPhase),
					"an already-terminal step's phase must not change")
				Expect(firstStep.Attempts).To(Equal(int32(1)),
					"an already-terminal step must not be re-attempted")
				Expect(atomic.LoadInt32(&terminalStepRequests)).To(Equal(int32(0)),
					"an already-terminal step's backend must never be called again")

				Expect(atomic.LoadInt32(&secondStepRequests)).To(Equal(int32(1)),
					"the dependent step must still be dispatched exactly once")
			},
			Entry("Succeeded", automationv1alpha1.StepPhaseSucceeded),
			Entry("Failed", automationv1alpha1.StepPhaseFailed),
		)
	})

	Describe("doPluginPublish — plugin publisher envelope", func() {
		// See docs/design/2026-09-10-plugin-publish-envelope.md: the controller must
		// send the documented {integration, namespace, destination, headers, body}
		// JSON envelope, not raw body/headers, and must parse the documented
		// {messageId}/{error} response shapes.
		var receivedBody []byte
		var receivedContentType string
		var server *httptest.Server
		var r *FlowRunReconciler

		BeforeEach(func() {
			receivedBody = nil
			receivedContentType = ""
			r = &FlowRunReconciler{HTTPClient: http.DefaultClient}
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("sends the full documented envelope, not the raw body/headers", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				receivedContentType = req.Header.Get("Content-Type")
				receivedBody, _ = io.ReadAll(req.Body)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))

			_, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "the message body",
				map[string]string{"X-Correlation-Id": "corr-1"}, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())

			Expect(receivedContentType).To(Equal("application/json"))

			var envelope map[string]interface{}
			Expect(json.Unmarshal(receivedBody, &envelope)).To(Succeed())
			Expect(envelope["integration"]).To(Equal("my-integ"))
			Expect(envelope["namespace"]).To(Equal("my-ns"))
			Expect(envelope["destination"]).To(Equal("orders.processed"))
			Expect(envelope["body"]).To(Equal("the message body"))
			Expect(envelope["headers"]).To(Equal(map[string]interface{}{"X-Correlation-Id": "corr-1"}))
		})

		It("does not set message headers as literal HTTP request headers", func() {
			var sawCustomHeader bool
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				sawCustomHeader = req.Header.Get("X-Correlation-Id") != ""
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))

			_, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "body",
				map[string]string{"X-Correlation-Id": "corr-1"}, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(sawCustomHeader).To(BeFalse(),
				"message headers belong in the envelope's \"headers\" field, not as literal HTTP headers on the /publish call")
		})

		It("surfaces messageId from a successful response as a step result", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"messageId":"broker-msg-42"}`))
			}))

			result, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "body", nil, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(map[string]string{"messageId": "broker-msg-42"}))
		})

		It("treats a success response with no messageId as success with empty results", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))

			result, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "body", nil, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(map[string]string{}))
		})

		It("parses the documented {error} field out of a failure response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"broker unavailable"}`))
			}))

			_, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "body", nil, 5*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("broker unavailable"))
			Expect(err.Error()).NotTo(ContainSubstring(`{"error"`),
				"the parsed message should be used, not the raw JSON envelope")
		})

		It("falls back to the raw response body when a failure response isn't the documented JSON shape", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("plain text failure, not JSON"))
			}))

			_, err := r.doPluginPublish(context.Background(), server.URL,
				"my-integ", "my-ns", "orders.processed", "body", nil, 5*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("plain text failure, not JSON"))
		})
	})

	Describe("substituteVars dot-path body access", func() {
		It("resolves a top-level field", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"name":"alice"}`}
			Expect(substituteVars("hello $(trigger.body.name)", nil, td, nil)).To(Equal("hello alice"))
		})
		It("resolves a nested field", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"order":{"id":"42","customer":"bob"}}`}
			Expect(substituteVars("order=$(trigger.body.order.id) by=$(trigger.body.order.customer)", nil, td, nil)).
				To(Equal("order=42 by=bob"))
		})
		It("resolves deep nesting", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"a":{"b":{"c":"deep"}}}`}
			Expect(substituteVars("$(trigger.body.a.b.c)", nil, td, nil)).To(Equal("deep"))
		})
		It("resolves array index", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"arr":["x","y","z"]}`}
			Expect(substituteVars("$(trigger.body.arr.1)", nil, td, nil)).To(Equal("y"))
		})
		It("returns empty string for missing path", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"a":{"b":"val"}}`}
			Expect(substituteVars("$(trigger.body.a.c)", nil, td, nil)).To(Equal(""))
		})
		It("returns empty string for non-object traversal", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"name":"alice"}`}
			Expect(substituteVars("$(trigger.body.name.foo)", nil, td, nil)).To(Equal(""))
		})
		It("handles out-of-bounds array index gracefully", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"arr":["x"]}`}
			Expect(substituteVars("$(trigger.body.arr.5)", nil, td, nil)).To(Equal(""))
		})
	})
})
