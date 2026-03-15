package kafka

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// Watcher watches Trigger CRDs and manages Kafka topic subscriptions.
type Watcher struct {
	client    client.Client
	namespace string
	log       logr.Logger
	// subscriptions map[types.NamespacedName]*subscription  // TODO: KG-2
}

// NewWatcher creates a new Watcher.
func NewWatcher(c client.Client, namespace string, log logr.Logger) *Watcher {
	return &Watcher{
		client:    c,
		namespace: namespace,
		log:       log,
	}
}

// Start begins watching Trigger CRDs and managing Kafka subscriptions.
// Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	w.log.Info("kafka watcher started", "namespace", w.namespace)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run once immediately before the first tick.
	w.reconcileTriggers(ctx)

	for {
		select {
		case <-ticker.C:
			w.reconcileTriggers(ctx)
		case <-ctx.Done():
			w.log.Info("kafka watcher stopped")
			return ctx.Err()
		}
	}
}

// reconcileTriggers lists all Kafka Triggers and logs their names.
// TODO: KG-2 — replace with real subscription management (create/update/delete sarama consumer groups).
func (w *Watcher) reconcileTriggers(ctx context.Context) {
	list := &automationv1alpha1.TriggerList{}
	listOpts := []client.ListOption{}
	if w.namespace != "" {
		listOpts = append(listOpts, client.InNamespace(w.namespace))
	}

	if err := w.client.List(ctx, list, listOpts...); err != nil {
		w.log.Error(err, "failed to list triggers")
		return
	}

	for i := range list.Items {
		trigger := &list.Items[i]
		if trigger.Spec.Type != "pubsub" {
			continue
		}
		if trigger.Spec.PubSub == nil || trigger.Spec.PubSub.Type != "kafka" {
			continue
		}
		w.log.V(1).Info("found kafka trigger",
			"name", trigger.Name,
			"namespace", trigger.Namespace,
		)
	}
}
