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
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
	"github.com/yourname/kubezap/internal/metrics"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=create
// +kubebuilder:rbac:groups=automation.kubezap.io,resources=triggers/status,verbs=get;update;patch

// CronScheduler manages cron jobs for Trigger resources with type=cron.
// It maintains one cron entry per Trigger and creates a FlowRun on each fire.
type CronScheduler struct {
	mu      sync.Mutex
	cron    *cron.Cron
	entries map[string]cron.EntryID // key: "<namespace>/<name>"
	client  client.Client
	log     logr.Logger
}

// NewCronScheduler creates and starts a CronScheduler.
func NewCronScheduler(c client.Client, log logr.Logger) *CronScheduler {
	cr := cron.New()
	cr.Start()
	return &CronScheduler{
		cron:    cr,
		entries: make(map[string]cron.EntryID),
		client:  c,
		log:     log.WithName("cron-scheduler"),
	}
}

// Register adds or replaces the cron entry for the given Trigger.
// Safe to call repeatedly — idempotent.
func (s *CronScheduler) Register(trigger *automationv1alpha1.Trigger) error {
	if trigger.Spec.Cron == nil {
		return fmt.Errorf("trigger %s/%s has no cron spec", trigger.Namespace, trigger.Name)
	}

	key := trigger.Namespace + "/" + trigger.Name
	flowRef := ""
	if trigger.Spec.FlowRef != nil {
		flowRef = trigger.Spec.FlowRef.Name
	}

	ns := trigger.Namespace
	name := trigger.Name
	schedule := trigger.Spec.Cron.Schedule

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing entry if present
	if id, ok := s.entries[key]; ok {
		s.cron.Remove(id)
		delete(s.entries, key)
	}

	id, err := s.cron.AddFunc(schedule, func() {
		ctx := context.Background()
		now := time.Now()
		scheduledTime := metav1.Time{Time: now}

		// Step 1: Fetch the current Trigger to check cooldown state.
		trigger := &automationv1alpha1.Trigger{}
		if err := s.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, trigger); err != nil { //nolint:govet
			if apierrors.IsNotFound(err) {
				s.log.Info("cron trigger not found, skipping FlowRun creation", "trigger", name, "namespace", ns)
			} else {
				s.log.Error(err, "failed to fetch trigger for cron job", "trigger", name, "namespace", ns)
			}
			return
		}

		// Step 2: Check cooldown policy.
		if trigger.Spec.Cooldown != nil && trigger.Spec.Cooldown.MaxInvocations > 0 {
			windowDuration := 60 * time.Second
			if trigger.Spec.Cooldown.Window != nil {
				windowDuration = trigger.Spec.Cooldown.Window.Duration
			}

			if trigger.Status.LastTriggeredTime != nil {
				windowStart := trigger.Status.LastTriggeredTime.Time
				windowEnd := windowStart.Add(windowDuration)

				if now.Before(windowEnd) {
					// Within the current window.
					if trigger.Status.CurrentInvocationCount >= trigger.Spec.Cooldown.MaxInvocations {
						// Rate limit reached — patch status and skip FlowRun creation.
						base := trigger.DeepCopy()
						trigger.Status.LastResult = "RateLimited"
						if patchErr := s.client.Status().Patch(ctx, trigger, client.MergeFrom(base)); patchErr != nil {
							s.log.Error(patchErr, "failed to patch trigger status for rate limit", "trigger", name, "namespace", ns)
						}
						s.log.Info("cron trigger rate limited by cooldown policy", "trigger", name, "namespace", ns,
							"currentInvocationCount", trigger.Status.CurrentInvocationCount,
							"maxInvocations", trigger.Spec.Cooldown.MaxInvocations)
						return
					}
					// Within window and under limit — increment count.
					base := trigger.DeepCopy()
					trigger.Status.CurrentInvocationCount++
					if patchErr := s.client.Status().Patch(ctx, trigger, client.MergeFrom(base)); patchErr != nil {
						s.log.Error(patchErr, "failed to patch trigger invocation count", "trigger", name, "namespace", ns)
						return
					}
				} else {
					// Window has expired — start a new window.
					base := trigger.DeepCopy()
					trigger.Status.CurrentInvocationCount = 1
					trigger.Status.LastTriggeredTime = &metav1.Time{Time: now}
					if patchErr := s.client.Status().Patch(ctx, trigger, client.MergeFrom(base)); patchErr != nil {
						s.log.Error(patchErr, "failed to reset trigger cooldown window", "trigger", name, "namespace", ns)
						return
					}
				}
				// Refresh local trigger state after patch so Step 4 uses the updated object.
				trigger.Status.LastTriggeredTime = &metav1.Time{Time: now}
			} else {
				// LastTriggeredTime is nil: no window is open yet (first invocation, or a prior Step 4
				// patch failed and left the field unset). Open the window atomically BEFORE creating
				// the FlowRun so that a subsequent patch failure in Step 4 does not leave the window
				// permanently absent and allow unbounded re-firing on every tick.
				base := trigger.DeepCopy()
				trigger.Status.LastTriggeredTime = &metav1.Time{Time: now}
				trigger.Status.CurrentInvocationCount = 1
				if patchErr := s.client.Status().Patch(ctx, trigger, client.MergeFrom(base)); patchErr != nil {
					s.log.Error(patchErr, "failed to open cooldown window before FlowRun creation", "trigger", name, "namespace", ns)
					return
				}
			}
		}

		// Step 3: Create the FlowRun.
		flowRunName := fmt.Sprintf("%s-%d", name, scheduledTime.Unix())

		flowRun := &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      flowRunName,
				Namespace: ns,
				Labels: map[string]string{
					"kubezap.io/trigger":      name,
					"kubezap.io/trigger-type": "cron",
					"kubezap.io/flow":         flowRef,
				},
			},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef: corev1.LocalObjectReference{Name: flowRef},
				TriggerRef: &automationv1alpha1.TriggerReference{
					Name: name,
					Type: "cron",
				},
				TriggerData: &automationv1alpha1.TriggerData{
					ScheduledTime: &scheduledTime,
				},
			},
		}

		if err := s.client.Create(ctx, flowRun); err != nil { //nolint:govet
			if !apierrors.IsAlreadyExists(err) {
				s.log.Error(err, "failed to create FlowRun for cron trigger",
					"trigger", name, "namespace", ns, "flowRun", flowRunName)
				metrics.TriggerFirings.WithLabelValues(ns, name, "cron", "error").Inc()
			}
			return
		}
		s.log.Info("created FlowRun for cron trigger",
			"trigger", name, "namespace", ns, "flowRun", flowRunName)
		metrics.TriggerFirings.WithLabelValues(ns, name, "cron", "success").Inc()

		// Step 4: Update trigger status after successful FlowRun creation.
		base := trigger.DeepCopy()
		trigger.Status.LastTriggeredTime = &metav1.Time{Time: now}
		trigger.Status.LastResult = "Success"
		if patchErr := s.client.Status().Patch(ctx, trigger, client.MergeFrom(base)); patchErr != nil {
			s.log.Error(patchErr, "failed to patch trigger status after FlowRun creation", "trigger", name, "namespace", ns)
		}
	})
	if err != nil {
		return fmt.Errorf("invalid cron schedule %q for trigger %s/%s: %w", schedule, ns, name, err)
	}

	s.entries[key] = id
	s.log.Info("registered cron trigger", "trigger", key, "schedule", schedule)
	return nil
}

// Deregister removes the cron entry for the given key ("<namespace>/<name>").
func (s *CronScheduler) Deregister(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id, ok := s.entries[key]; ok {
		s.cron.Remove(id)
		delete(s.entries, key)
		s.log.Info("deregistered cron trigger", "trigger", key)
	}
}

// Stop shuts down the scheduler gracefully.
func (s *CronScheduler) Stop() {
	s.cron.Stop()
}
