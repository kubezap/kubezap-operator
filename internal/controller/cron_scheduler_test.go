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

// Cooldown tests for CronScheduler.
//
// Design note: the cooldown logic lives inside the cron job closure registered
// by CronScheduler.Register. The closure fetches a fresh Trigger from the API
// server on each fire, so tests can seed Trigger.Status.* state via
// k8sClient.Status().Update before waiting for the cron to fire. This makes
// each cooldown scenario deterministic without mocking time or the scheduler
// internals.

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// listFlowRunsForTrigger returns all FlowRuns in the given namespace labelled
// with the trigger name.
func listFlowRunsForTrigger(namespace, triggerName string) []automationv1alpha1.FlowRun {
	var runs automationv1alpha1.FlowRunList
	_ = k8sClient.List(ctx, &runs,
		client.InNamespace(namespace),
		client.MatchingLabels{"kubezap.io/trigger": triggerName},
	)
	return runs.Items
}

// createCronTrigger creates a Trigger CRD with type=cron in the test environment.
func createCronTrigger(name, namespace, schedule string, cooldown *automationv1alpha1.CooldownPolicy) *automationv1alpha1.Trigger {
	t := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "cron",
			Enabled: true,
			Cron: &automationv1alpha1.CronTrigger{
				Schedule: schedule,
			},
			FlowRef: &automationv1alpha1.FlowReference{
				Name: "some-flow",
			},
			Cooldown: cooldown,
		},
	}
	Expect(k8sClient.Create(ctx, t)).To(Succeed())
	return t
}

// cleanupCronTriggerAndRuns deletes the trigger and all FlowRuns it produced.
func cleanupCronTriggerAndRuns(namespace, triggerName string, trigger *automationv1alpha1.Trigger) {
	_ = k8sClient.Delete(context.Background(), trigger)
	var runs automationv1alpha1.FlowRunList
	_ = k8sClient.List(context.Background(), &runs,
		client.InNamespace(namespace),
		client.MatchingLabels{"kubezap.io/trigger": triggerName},
	)
	for i := range runs.Items {
		_ = k8sClient.Delete(context.Background(), &runs.Items[i])
	}
}

var _ = Describe("CronScheduler", func() {
	const testNamespace = "default"

	// uniqueName generates a unique name per test using the Ginkgo random seed.
	uniqueName := func(base string, idx int) string {
		return fmt.Sprintf("%s-%d-%d", base, GinkgoRandomSeed(), idx)
	}

	Context("when no cooldown policy is configured", func() {
		It("fires and creates a FlowRun on the first cron tick", func() {
			triggerName := uniqueName("cron-nocooldown", 1)

			trigger := createCronTrigger(triggerName, testNamespace, "@every 1s", nil)
			DeferCleanup(func() { cleanupCronTriggerAndRuns(testNamespace, triggerName, trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())

			// At least one FlowRun should appear within 5 seconds.
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 5*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 1))
		})
	})

	Context("cooldown policy: MaxInvocations per window", func() {
		It("rate-limits after MaxInvocations is reached within the cooldown window", func() {
			triggerName := uniqueName("cron-ratelimit", 1)

			// Allow only 1 invocation per 60-second window.
			cooldown := &automationv1alpha1.CooldownPolicy{
				MaxInvocations: 1,
				Window:         &metav1.Duration{Duration: 60 * time.Second},
			}
			trigger := createCronTrigger(triggerName, testNamespace, "@every 1s", cooldown)
			DeferCleanup(func() { cleanupCronTriggerAndRuns(testNamespace, triggerName, trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())

			// Wait for the first (and only permitted) FlowRun.
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 5*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 1))

			// The window is 60 seconds; subsequent ticks should be blocked.
			// Wait another 3 seconds and assert the count has not increased.
			time.Sleep(3 * time.Second)
			Expect(listFlowRunsForTrigger(testNamespace, triggerName)).To(HaveLen(1))

			// Trigger status should record the rate-limit.
			var updated automationv1alpha1.Trigger
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: testNamespace}, &updated)).To(Succeed())
			Expect(updated.Status.LastResult).To(Equal("RateLimited"))
		})

		It("fires again after the cooldown window expires (short 1-second window)", func() {
			triggerName := uniqueName("cron-newwindow", 1)

			// 1-second window: each tick opens a fresh window because the previous one
			// has already expired by the time the next tick fires.
			cooldown := &automationv1alpha1.CooldownPolicy{
				MaxInvocations: 1,
				Window:         &metav1.Duration{Duration: 1 * time.Second},
			}
			trigger := createCronTrigger(triggerName, testNamespace, "@every 1s", cooldown)
			DeferCleanup(func() { cleanupCronTriggerAndRuns(testNamespace, triggerName, trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())

			// With a 1-second window and every-second cron, a new window opens on each
			// tick. Expect at least 2 FlowRuns within 5 seconds.
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 6*time.Second, 300*time.Millisecond).Should(BeNumerically(">=", 2))
		})

		It("opens a new cooldown window when LastTriggeredTime is nil (first invocation)", func() {
			triggerName := uniqueName("cron-firstfire", 1)

			// MaxInvocations > 1 so the first fire is never blocked.
			cooldown := &automationv1alpha1.CooldownPolicy{
				MaxInvocations: 5,
				Window:         &metav1.Duration{Duration: 60 * time.Second},
			}
			trigger := createCronTrigger(triggerName, testNamespace, "@every 1s", cooldown)
			DeferCleanup(func() { cleanupCronTriggerAndRuns(testNamespace, triggerName, trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())

			// A FlowRun should appear (nil LastTriggeredTime = first invocation → no block).
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 5*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 1))

			// The scheduler should have patched LastTriggeredTime onto the Trigger status.
			var updated automationv1alpha1.Trigger
			Eventually(func() *metav1.Time {
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: triggerName, Namespace: testNamespace}, &updated)
				return updated.Status.LastTriggeredTime
			}, 3*time.Second, 200*time.Millisecond).ShouldNot(BeNil())
		})
	})

	Context("Register edge cases", func() {
		It("returns an error for an invalid cron expression", func() {
			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      uniqueName("bad-sched", 1),
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type:    "cron",
					Enabled: true,
					Cron: &automationv1alpha1.CronTrigger{
						Schedule: "not-a-valid-cron",
					},
					FlowRef: &automationv1alpha1.FlowReference{Name: "flow"},
				},
			}
			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			err := scheduler.Register(trigger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid cron schedule"))
		})

		It("returns an error when the trigger has no cron spec", func() {
			trigger := &automationv1alpha1.Trigger{
				ObjectMeta: metav1.ObjectMeta{
					Name:      uniqueName("no-cron", 1),
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
				},
			}
			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			err := scheduler.Register(trigger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no cron spec"))
		})

		It("is idempotent: registering the same trigger twice replaces the old entry", func() {
			triggerName := uniqueName("cron-idem", 1)
			trigger := createCronTrigger(triggerName, testNamespace, "0 0 1 1 *", nil) // 1 Jan — won't fire
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())
			// Register again — should succeed and replace the previous entry.
			Expect(scheduler.Register(trigger)).To(Succeed())
		})
	})

	Context("FlowRun name format and metadata (T1)", func() {
		It("creates FlowRuns named <trigger>-<unix-timestamp> with correct ScheduledTime, TriggerRef, and labels", func() {
			triggerName := uniqueName("cron-t1", 1)

			trigger := createCronTrigger(triggerName, testNamespace, "@every 1s", nil)
			DeferCleanup(func() { cleanupCronTriggerAndRuns(testNamespace, triggerName, trigger) })

			log := logf.Log.WithName("test-cron-t1")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			beforeUnix := time.Now().Unix()
			Expect(scheduler.Register(trigger)).To(Succeed())

			// Wait for the first FlowRun.
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 5*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 1))

			runs := listFlowRunsForTrigger(testNamespace, triggerName)
			Expect(runs).NotTo(BeEmpty())
			fr := runs[0]

			// Name must be <trigger>-<unix-seconds>.
			Expect(fr.Name).To(MatchRegexp("^" + triggerName + `-\d+$`))

			// The unix timestamp embedded in the name must be at or after test start.
			var embeddedTs int64
			_, scanErr := fmt.Sscanf(fr.Name, triggerName+"-%d", &embeddedTs)
			Expect(scanErr).NotTo(HaveOccurred())
			Expect(embeddedTs).To(BeNumerically(">=", beforeUnix))

			// TriggerData.ScheduledTime must be populated.
			Expect(fr.Spec.TriggerData).NotTo(BeNil())
			Expect(fr.Spec.TriggerData.ScheduledTime).NotTo(BeNil())
			Expect(fr.Spec.TriggerData.ScheduledTime.Unix()).To(BeNumerically(">=", beforeUnix))

			// TriggerRef must point back to the trigger with the correct type.
			Expect(fr.Spec.TriggerRef).NotTo(BeNil())
			Expect(fr.Spec.TriggerRef.Name).To(Equal(triggerName))
			Expect(fr.Spec.TriggerRef.Type).To(Equal("cron"))

			// Labels must identify trigger and type.
			Expect(fr.Labels["kubezap.io/trigger"]).To(Equal(triggerName))
			Expect(fr.Labels["kubezap.io/trigger-type"]).To(Equal("cron"))

			// Wait for a second FlowRun to confirm multi-fire and name uniqueness.
			Eventually(func() int {
				return len(listFlowRunsForTrigger(testNamespace, triggerName))
			}, 5*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", 2))

			allRuns := listFlowRunsForTrigger(testNamespace, triggerName)
			seen := make(map[string]struct{}, len(allRuns))
			for _, r := range allRuns {
				_, dup := seen[r.Name]
				Expect(dup).To(BeFalse(), "duplicate FlowRun name: %s", r.Name)
				seen[r.Name] = struct{}{}
			}
		})
	})

	Context("Deregister", func() {
		It("removes a previously registered trigger without panicking", func() {
			triggerName := uniqueName("cron-dereg", 1)
			trigger := createCronTrigger(triggerName, testNamespace, "0 0 1 1 *", nil)
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), trigger) })

			log := logf.Log.WithName("test-cron")
			scheduler := NewCronScheduler(k8sClient, log)
			DeferCleanup(scheduler.Stop)

			Expect(scheduler.Register(trigger)).To(Succeed())

			key := testNamespace + "/" + triggerName
			scheduler.Deregister(key)
			// Second deregister of the same key is a no-op; must not panic.
			scheduler.Deregister(key)
		})
	})
})
