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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

var _ = Describe("enforceMaxFlowRunsByPhase", func() {
	const testNamespace = "default"
	const triggerName = "gc-test-trigger"

	// newGCReconciler returns a FlowRunReconciler backed by the envtest k8sClient.
	newGCReconciler := func() *FlowRunReconciler {
		return &FlowRunReconciler{
			Client:       k8sClient,
			Scheme:       k8sClient.Scheme(),
			TTLSucceeded: 24 * time.Hour,
			TTLFailed:    72 * time.Hour,
		}
	}

	// makeTerminalFlowRun creates and persists a FlowRun with the given phase and a
	// completion time offset from now (negative = older). It is labeled with the
	// trigger name so the GC list query can locate it.
	makeTerminalFlowRun := func(name, phase string, completedAgo time.Duration) *automationv1alpha1.FlowRun {
		completionTime := metav1.NewTime(time.Now().Add(-completedAgo))
		fr := &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				Labels: map[string]string{
					"kubezap.io/trigger": triggerName,
				},
			},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef: corev1.LocalObjectReference{Name: "some-flow"},
			},
		}
		Expect(k8sClient.Create(ctx, fr)).To(Succeed())
		fr.Status.Phase = phase
		fr.Status.CompletionTime = &completionTime
		Expect(k8sClient.Status().Update(ctx, fr)).To(Succeed())
		return fr
	}

	// makeRetainedFlowRun is like makeTerminalFlowRun but adds the retain annotation.
	makeRetainedFlowRun := func(name, phase string, completedAgo time.Duration) *automationv1alpha1.FlowRun {
		completionTime := metav1.NewTime(time.Now().Add(-completedAgo))
		fr := &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				Labels: map[string]string{
					"kubezap.io/trigger": triggerName,
				},
				Annotations: map[string]string{
					retainAnnotation: "true",
				},
			},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef: corev1.LocalObjectReference{Name: "some-flow"},
			},
		}
		Expect(k8sClient.Create(ctx, fr)).To(Succeed())
		fr.Status.Phase = phase
		fr.Status.CompletionTime = &completionTime
		Expect(k8sClient.Status().Update(ctx, fr)).To(Succeed())
		return fr
	}

	// seedValue generates a unique suffix per Ginkgo random seed to isolate tests.
	seedValue := func(idx int) string {
		return fmt.Sprintf("%d-%d", GinkgoRandomSeed(), idx)
	}

	cleanupFlowRun := func(fr *automationv1alpha1.FlowRun) {
		_ = k8sClient.Delete(context.Background(), fr)
	}

	Context("when the count is at or below the limit", func() {
		It("deletes nothing", func() {
			fr1 := makeTerminalFlowRun("gc-under-1-"+seedValue(1), "Succeeded", 10*time.Minute)
			fr2 := makeTerminalFlowRun("gc-under-2-"+seedValue(2), "Succeeded", 5*time.Minute)
			DeferCleanup(func() {
				cleanupFlowRun(fr1)
				cleanupFlowRun(fr2)
			})

			r := newGCReconciler()
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Succeeded", 3)).To(Succeed())

			// Both FlowRuns should still exist.
			var got automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr1.Name, Namespace: testNamespace}, &got)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr2.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})

		It("deletes nothing when count exactly equals max", func() {
			fr1 := makeTerminalFlowRun("gc-exact-1-"+seedValue(1), "Succeeded", 10*time.Minute)
			fr2 := makeTerminalFlowRun("gc-exact-2-"+seedValue(2), "Succeeded", 5*time.Minute)
			DeferCleanup(func() {
				cleanupFlowRun(fr1)
				cleanupFlowRun(fr2)
			})

			r := newGCReconciler()
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Succeeded", 2)).To(Succeed())

			var got automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr1.Name, Namespace: testNamespace}, &got)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr2.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})
	})

	Context("when the count exceeds the limit for Succeeded phase", func() {
		It("deletes the oldest FlowRun to bring the count within the cap", func() {
			// oldest → 30 min ago, middle → 20 min, newest → 10 min
			oldest := makeTerminalFlowRun("gc-over-old-"+seedValue(1), "Succeeded", 30*time.Minute)
			middle := makeTerminalFlowRun("gc-over-mid-"+seedValue(2), "Succeeded", 20*time.Minute)
			newest := makeTerminalFlowRun("gc-over-new-"+seedValue(3), "Succeeded", 10*time.Minute)
			DeferCleanup(func() {
				// oldest may already be deleted; ignore not-found.
				cleanupFlowRun(oldest)
				cleanupFlowRun(middle)
				cleanupFlowRun(newest)
			})

			r := newGCReconciler()
			// Max 2 Succeeded → oldest must be deleted.
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Succeeded", 2)).To(Succeed())

			var got automationv1alpha1.FlowRun
			// Oldest should be gone.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: oldest.Name, Namespace: testNamespace}, &got)).
				To(MatchError(ContainSubstring("not found")))
			// Newer two should remain.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: middle.Name, Namespace: testNamespace}, &got)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: newest.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})
	})

	Context("when the count exceeds the limit for Failed phase", func() {
		It("deletes the oldest failed FlowRuns without touching succeeded ones", func() {
			failedOld := makeTerminalFlowRun("gc-fail-old-"+seedValue(1), "Failed", 60*time.Minute)
			failedNew := makeTerminalFlowRun("gc-fail-new-"+seedValue(2), "Failed", 10*time.Minute)
			succeeded := makeTerminalFlowRun("gc-succ-"+seedValue(3), "Succeeded", 5*time.Minute)
			DeferCleanup(func() {
				cleanupFlowRun(failedOld)
				cleanupFlowRun(failedNew)
				cleanupFlowRun(succeeded)
			})

			r := newGCReconciler()
			// Max 1 Failed → oldest failed is deleted; succeeded is untouched.
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Failed", 1)).To(Succeed())

			var got automationv1alpha1.FlowRun
			// Oldest failed should be gone.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: failedOld.Name, Namespace: testNamespace}, &got)).
				To(MatchError(ContainSubstring("not found")))
			// Newer failed and the succeeded one should still be present.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: failedNew.Name, Namespace: testNamespace}, &got)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: succeeded.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})
	})

	Context("retain annotation exemption", func() {
		It("does not delete a FlowRun annotated with kubezap.io/retain=true", func() {
			// 3 runs, max 2 — but the oldest is retained; the second-oldest should be deleted instead.
			retained := makeRetainedFlowRun("gc-retain-"+seedValue(1), "Succeeded", 60*time.Minute)
			toDelete := makeTerminalFlowRun("gc-nodelete-"+seedValue(2), "Succeeded", 30*time.Minute)
			newest := makeTerminalFlowRun("gc-newest-"+seedValue(3), "Succeeded", 10*time.Minute)
			DeferCleanup(func() {
				cleanupFlowRun(retained)
				cleanupFlowRun(toDelete)
				cleanupFlowRun(newest)
			})

			r := newGCReconciler()
			// Max 2 — but retained is exempt, so only non-retained ones are counted and sorted.
			// There are 2 non-retained (toDelete, newest); max is 2 → exactly at limit → no deletion.
			// Change max to 1 to force a deletion of the older non-retained.
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Succeeded", 1)).To(Succeed())

			var got automationv1alpha1.FlowRun
			// Retained FlowRun must survive regardless of its age.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: retained.Name, Namespace: testNamespace}, &got)).To(Succeed())
			// The oldest non-retained (toDelete) should be deleted.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: toDelete.Name, Namespace: testNamespace}, &got)).
				To(MatchError(ContainSubstring("not found")))
			// The newest non-retained should survive.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: newest.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})
	})

	Context("max=0 (delete all non-retained)", func() {
		It("deletes all eligible FlowRuns when max is 0", func() {
			fr1 := makeTerminalFlowRun("gc-zero-1-"+seedValue(1), "Succeeded", 10*time.Minute)
			fr2 := makeTerminalFlowRun("gc-zero-2-"+seedValue(2), "Succeeded", 5*time.Minute)
			retained := makeRetainedFlowRun("gc-zero-retain-"+seedValue(3), "Succeeded", 2*time.Minute)
			DeferCleanup(func() {
				cleanupFlowRun(fr1)
				cleanupFlowRun(fr2)
				cleanupFlowRun(retained)
			})

			r := newGCReconciler()
			Expect(r.enforceMaxFlowRunsByPhase(ctx, triggerName, testNamespace, "Succeeded", 0)).To(Succeed())

			var got automationv1alpha1.FlowRun
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr1.Name, Namespace: testNamespace}, &got)).
				To(MatchError(ContainSubstring("not found")))
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fr2.Name, Namespace: testNamespace}, &got)).
				To(MatchError(ContainSubstring("not found")))
			// Retained should survive even with max=0.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: retained.Name, Namespace: testNamespace}, &got)).To(Succeed())
		})
	})
})
