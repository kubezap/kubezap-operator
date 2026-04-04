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
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/cel-go/cel"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
}

func (m *mockSyncProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
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
func (m *mockSyncProducer) AddMessageToTxn(*sarama.ConsumerMessage, string, *string) error {
	return nil
}

var _ = Describe("FlowRunReconciler", func() {
	const testNamespace = "default"

	newReconciler := func() *FlowRunReconciler {
		env, err := cel.NewEnv(
			cel.Variable("trigger", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
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
			case "Succeeded", "Failed", "Cancelled":
				return &updated, nil
			}
			// If no requeue is requested and phase is not terminal, stop.
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
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.Phase).To(Equal("Succeeded"))
		})

		It("records the step result in status.stepStatuses with phase=Succeeded", func() {
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
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
			r := newReconciler()
			updated, err := reconcileUntilTerminal(r, flowRun.Name, 10)
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
			updated, err := reconcileUntilTerminal(r, fastRun.Name, 10)
			Expect(err).NotTo(HaveOccurred())
			_ = nn

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
			flowRun.Status.Phase = "Running"
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
			Expect(updated.Status.Phase).To(Equal("Failed"))
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
			Expect(updated.Status.Phase).To(Equal("Succeeded"))

			// Verify individual step statuses.
			Expect(updated.Status.Steps).To(HaveLen(3))

			stepA := findStepStatus(updated.Status.Steps, "step-a")
			Expect(stepA).NotTo(BeNil())
			Expect(stepA.Phase).To(Equal("Succeeded"))

			stepB := findStepStatus(updated.Status.Steps, "step-b")
			Expect(stepB).NotTo(BeNil())
			Expect(stepB.Phase).To(Equal("Skipped"))
			Expect(stepB.Message).To(ContainSubstring("when condition"))

			stepC := findStepStatus(updated.Status.Steps, "step-c")
			Expect(stepC).NotTo(BeNil())
			Expect(stepC.Phase).To(Equal("Skipped"))
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
			Expect(updated.Status.Phase).To(Equal("Succeeded"))

			// Transform step must have succeeded and emitted the orderId result.
			transformStatus := findStepStatus(updated.Status.Steps, "transform-step")
			Expect(transformStatus).NotTo(BeNil())
			Expect(transformStatus.Phase).To(Equal("Succeeded"))
			Expect(transformStatus.Results).To(ContainElement(
				automationv1alpha1.ResultValue{Name: "orderId", Value: "ORD-42"},
			))

			// HTTP step must have succeeded.
			httpStatus := findStepStatus(updated.Status.Steps, "http-step")
			Expect(httpStatus).NotTo(BeNil())
			Expect(httpStatus.Phase).To(Equal("Succeeded"))

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
			Expect(waitStatus.Phase).To(Equal("Waiting"))
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
			Expect(waitStatus.Phase).To(Equal("Waiting"))

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
			Expect(doneStep.Phase).To(Equal("Succeeded"))
			Expect(doneStep.CompletionTime).NotTo(BeNil())

			// Third reconcile: all steps are terminal, FlowRun transitions to Succeeded.
			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var finished automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, nn, &finished)).To(Succeed())
			Expect(finished.Status.Phase).To(Equal("Succeeded"))
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
			Expect(updated.Status.Phase).To(Equal("Succeeded"))

			// Verify the step itself succeeded.
			Expect(updated.Status.Steps).NotTo(BeEmpty())
			retryStep := findStepStatus(updated.Status.Steps, "retry-step")
			Expect(retryStep).NotTo(BeNil())
			Expect(retryStep.Phase).To(Equal("Succeeded"))

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
			Expect(updated.Status.Phase).To(Equal("Failed"),
				"expected FlowRun to fail because target host is unreachable")

			// Find the failed step status.
			stepStatus := findStepStatus(updated.Status.Steps, "http-with-secret")
			Expect(stepStatus).NotTo(BeNil(), "expected step status to be recorded")
			Expect(stepStatus.Phase).To(Equal("Failed"))

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
				if current.Status.Phase == "Succeeded" || current.Status.Phase == "Failed" {
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
			Expect(updated.Status.Phase).To(Equal("Running"),
				"FlowRun must remain Running on executor transport failure, not Failed")

			// The step must NOT appear as Failed in the status (it should be absent
			// or still Running — not persisted as a failure).
			stepStatus := findStepStatus(updated.Status.Steps, "http-step")
			if stepStatus != nil {
				Expect(stepStatus.Phase).NotTo(Equal("Failed"),
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

				got, err := r.getOrCreateKafkaProducer(brokerKey, integration)
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

				_, err := r.getOrCreateKafkaProducer(brokerKey, integration)

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

	Describe("substituteVars dot-path body access", func() {
		It("resolves a top-level field", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"name":"alice"}`}
			Expect(substituteVars("hello $(trigger.body.name)", nil, td)).To(Equal("hello alice"))
		})
		It("resolves a nested field", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"order":{"id":"42","customer":"bob"}}`}
			Expect(substituteVars("order=$(trigger.body.order.id) by=$(trigger.body.order.customer)", nil, td)).
				To(Equal("order=42 by=bob"))
		})
		It("resolves deep nesting", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"a":{"b":{"c":"deep"}}}`}
			Expect(substituteVars("$(trigger.body.a.b.c)", nil, td)).To(Equal("deep"))
		})
		It("resolves array index", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"arr":["x","y","z"]}`}
			Expect(substituteVars("$(trigger.body.arr.1)", nil, td)).To(Equal("y"))
		})
		It("returns empty string for missing path", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"a":{"b":"val"}}`}
			Expect(substituteVars("$(trigger.body.a.c)", nil, td)).To(Equal(""))
		})
		It("returns empty string for non-object traversal", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"name":"alice"}`}
			Expect(substituteVars("$(trigger.body.name.foo)", nil, td)).To(Equal(""))
		})
		It("handles out-of-bounds array index gracefully", func() {
			td := &automationv1alpha1.TriggerData{Body: `{"arr":["x"]}`}
			Expect(substituteVars("$(trigger.body.arr.5)", nil, td)).To(Equal(""))
		})
	})
})
