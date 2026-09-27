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

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// This file implements the controller-side half of the mechanism described in
// docs/design/gateway-credential-failure-visibility.md: the kafka/amqp/nats
// gateway watchers (internal/gateway/{kafka,amqp,nats}/watcher.go) cannot
// write to a Trigger or its status subresource — they only hold an
// events:create grant (internal/controller/integration_controller.go) — so
// they surface a credential-resolution failure (or a recovery from one) as a
// Kubernetes Event referencing the Trigger instead. TriggerReconciler watches
// for those Events and translates the most recent one into a real
// status.conditions entry, using the same apimeta.SetStatusCondition pattern
// already used for Flow/Integration/WebhookGatewayConfig/FlowRun.
//
// This mechanism applies only to kafka/amqp/nats Triggers — webhook gateway
// auth failures are a different, high-frequency failure class already
// covered by access logs and Prometheus metrics (docs/guides/observability.md)
// and are explicitly out of scope here; the webhook gateway never emits
// either of the two reasons below.

const (
	// triggerTypeKafka / triggerTypeAmqp / triggerTypeNats mirror the
	// triggerTypeKafka/triggerTypeAMQP/triggerTypeNats constants each of
	// internal/gateway/{kafka,amqp,nats}'s own watcher.go files define
	// locally against automationv1alpha1.TriggerSpec.Type's
	// +kubebuilder:validation:Enum=webhook;cron;kafka;amqp;nats;resource.
	// Duplicated here (rather than imported) because the controller package
	// does not import the gateway packages, matching this codebase's existing
	// convention of duplicating such small per-package constants rather than
	// introducing a shared package for them.
	triggerTypeKafka = "kafka"
	triggerTypeAmqp  = "amqp"
	triggerTypeNats  = "nats"

	// conditionTypeCredentialResolutionFailed is the metav1.Condition Type
	// synced onto a kafka/amqp/nats Trigger's status.conditions from the
	// Events described above. Negative-polarity, matching the Kubernetes Node
	// "MemoryPressure"/"DiskPressure" convention: Status=True means the
	// failure was still occurring as of the most recent correlated Event;
	// Status=False means the most recent credential-resolution attempt
	// succeeded. Left entirely absent for any Trigger that has never hit this
	// path — including every webhook/cron/resource Trigger, and any
	// kafka/amqp/nats Trigger whose credentials have always resolved cleanly.
	conditionTypeCredentialResolutionFailed = "CredentialResolutionFailed"

	// reasonCredentialResolutionFailed / reasonCredentialResolutionSucceeded
	// must stay in sync with the matching credentialResolutionFailedReason /
	// credentialResolutionSucceededReason constants duplicated in each of
	// internal/gateway/{kafka,amqp,nats}/watcher.go.
	reasonCredentialResolutionFailed    = "CredentialResolutionFailed"
	reasonCredentialResolutionSucceeded = "CredentialResolutionSucceeded"

	// eventInvolvedObjectTriggerKind is the Kind value the gateway watchers'
	// EventRecorder.Eventf calls populate on a Trigger-referencing Event's
	// involvedObject, via reference.GetReference against the Trigger object.
	eventInvolvedObjectTriggerKind = "Trigger"

	// eventInvolvedObjectNameField is both the field-indexer key registered
	// against the manager's cache in TriggerReconciler.SetupWithManager and a
	// field selector natively supported by the Kubernetes API server for
	// corev1.Event objects. Using the same string for both means
	// syncCredentialResolutionCondition's r.List call works unchanged whether
	// r.Client is a cache-backed manager client (production; requires the
	// registered indexer) or a direct API-server client with no cache at all
	// (envtest in this package's Ginkgo suite; the apiserver honors the
	// selector natively, no indexer needed).
	eventInvolvedObjectNameField = "involvedObject.name"
)

// indexEventsByInvolvedTriggerName is the client.IndexerFunc registered for
// corev1.Event under eventInvolvedObjectNameField in SetupWithManager. Only
// Events whose involvedObject is a Trigger are indexed at all — an Event
// referencing some other Kind that happens to share a name with a Trigger in
// the same namespace is deliberately excluded here rather than relying on
// syncCredentialResolutionCondition's own Kind check alone.
func indexEventsByInvolvedTriggerName(obj client.Object) []string {
	evt, ok := obj.(*corev1.Event)
	if !ok || evt.InvolvedObject.Kind != eventInvolvedObjectTriggerKind {
		return nil
	}
	return []string{evt.InvolvedObject.Name}
}

// enqueueTriggerForCredentialEvent maps a corev1.Event to a reconcile request
// for the Trigger it references, but only for the two credential-resolution
// reasons this mechanism cares about. Any other Event — including one
// referencing a Trigger for an unrelated reason, or one from the webhook
// gateway (which never emits either reason) — is ignored.
func enqueueTriggerForCredentialEvent(_ context.Context, obj client.Object) []reconcile.Request {
	evt, ok := obj.(*corev1.Event)
	if !ok || evt.InvolvedObject.Kind != eventInvolvedObjectTriggerKind {
		return nil
	}
	if evt.Reason != reasonCredentialResolutionFailed && evt.Reason != reasonCredentialResolutionSucceeded {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Name:      evt.InvolvedObject.Name,
			Namespace: evt.InvolvedObject.Namespace,
		},
	}}
}

// syncCredentialResolutionCondition lists the credential-resolution Events
// referencing trg and sets/clears conditionTypeCredentialResolutionFailed on
// trg.Status based on the most recent one. It mutates trg.Status.Conditions
// in place and reports whether anything changed; the caller still owns
// persisting the status change (see TriggerReconciler.Reconcile).
//
// This derives the desired Condition fresh from the current Event list on
// every call rather than depending on any in-memory state, so it is
// idempotent and safe to call on every Trigger reconcile regardless of why
// that reconcile was triggered: one triggered for an unrelated reason (e.g. a
// Trigger spec edit) still converges the Condition, and one triggered by a
// stale/duplicate Event is a no-op.
func (r *TriggerReconciler) syncCredentialResolutionCondition(ctx context.Context, trg *automationv1alpha1.Trigger) (bool, error) {
	var events corev1.EventList
	if err := r.List(ctx, &events,
		client.InNamespace(trg.Namespace),
		client.MatchingFields{eventInvolvedObjectNameField: trg.Name},
	); err != nil {
		return false, fmt.Errorf("listing credential-resolution events for trigger %s/%s: %w", trg.Namespace, trg.Name, err)
	}

	var latest *corev1.Event
	for i := range events.Items {
		evt := &events.Items[i]
		if evt.InvolvedObject.Kind != eventInvolvedObjectTriggerKind ||
			evt.InvolvedObject.Namespace != trg.Namespace ||
			evt.InvolvedObject.Name != trg.Name {
			continue
		}
		if evt.Reason != reasonCredentialResolutionFailed && evt.Reason != reasonCredentialResolutionSucceeded {
			continue
		}
		if latest == nil || eventTimestamp(evt).After(eventTimestamp(latest)) {
			latest = evt
		}
	}

	if latest == nil {
		// This Trigger has never hit the credential-resolution path (or the
		// only Events it ever generated have since expired via the cluster's
		// --event-ttl) — leave the Condition absent/untouched rather than
		// inventing a state we have no signal for.
		return false, nil
	}

	cond := metav1.Condition{
		Type:               conditionTypeCredentialResolutionFailed,
		ObservedGeneration: trg.Generation,
		Message:            latest.Message,
	}
	if latest.Reason == reasonCredentialResolutionFailed {
		cond.Status = metav1.ConditionTrue
		cond.Reason = reasonCredentialResolutionFailed
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonCredentialResolutionSucceeded
	}

	return apimeta.SetStatusCondition(&trg.Status.Conditions, cond), nil
}

// eventTimestamp returns the best available timestamp for ordering Events:
// LastTimestamp (set by the client-go EventRecorder the gateway watchers use)
// falling back to EventTime, then CreationTimestamp, for an Event recorded
// through a different client.
func eventTimestamp(evt *corev1.Event) time.Time {
	if !evt.LastTimestamp.IsZero() {
		return evt.LastTimestamp.Time
	}
	if !evt.EventTime.IsZero() {
		return evt.EventTime.Time
	}
	return evt.CreationTimestamp.Time
}
