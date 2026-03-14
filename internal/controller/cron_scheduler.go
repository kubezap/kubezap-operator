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

	"github.com/go-logr/logr"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=create

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
		scheduledTime := metav1.Now()
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

		if err := s.client.Create(context.Background(), flowRun); err != nil { //nolint:govet
			if !apierrors.IsAlreadyExists(err) {
				s.log.Error(err, "failed to create FlowRun for cron trigger",
					"trigger", name, "namespace", ns, "flowRun", flowRunName)
			}
		} else {
			s.log.Info("created FlowRun for cron trigger",
				"trigger", name, "namespace", ns, "flowRun", flowRunName)
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
