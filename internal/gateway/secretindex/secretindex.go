// Package secretindex provides a small, thread-safe reverse index mapping a
// Kubernetes Secret to the set of objects (Triggers, in every current caller)
// that currently depend on it. Gateway watchers use it to know which objects
// to reprocess when a watched Secret's contents change — see
// docs/design/secret-rotation-watches.md.
//
// It intentionally holds no Kubernetes client, informer, or credential-
// resolution logic: those differ per gateway (webhook's auth config lives
// directly on the Trigger; kafka/amqp/nats resolve through an Integration),
// so each watcher wires its own informer and calls Update/Remove/TriggersFor
// as its own reconcile logic runs.
package secretindex

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// Index is a reverse index from a Secret's NamespacedName to the set of
// dependent objects' NamespacedNames. The zero value is not usable; use New.
type Index struct {
	mu       sync.Mutex
	bySecret map[types.NamespacedName]map[types.NamespacedName]struct{}
	byObject map[types.NamespacedName]map[types.NamespacedName]struct{}
}

// New returns an empty Index.
func New() *Index {
	return &Index{
		bySecret: make(map[types.NamespacedName]map[types.NamespacedName]struct{}),
		byObject: make(map[types.NamespacedName]map[types.NamespacedName]struct{}),
	}
}

// Update replaces the full set of Secrets that objectKey currently depends on.
// Call this every time the dependent object (a Trigger) is reconciled, even
// if its secret references haven't changed — passing the current, complete
// set is what lets stale references (a Trigger edited to drop a secretRef, or
// deleted entirely) get cleaned up automatically, without a separate GC pass.
// Pass a nil or empty secretKeys to fully remove objectKey from the index.
func (idx *Index) Update(objectKey types.NamespacedName, secretKeys []types.NamespacedName) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for s := range idx.byObject[objectKey] {
		delete(idx.bySecret[s], objectKey)
		if len(idx.bySecret[s]) == 0 {
			delete(idx.bySecret, s)
		}
	}
	delete(idx.byObject, objectKey)

	if len(secretKeys) == 0 {
		return
	}

	newSet := make(map[types.NamespacedName]struct{}, len(secretKeys))
	for _, s := range secretKeys {
		newSet[s] = struct{}{}
		if idx.bySecret[s] == nil {
			idx.bySecret[s] = make(map[types.NamespacedName]struct{})
		}
		idx.bySecret[s][objectKey] = struct{}{}
	}
	idx.byObject[objectKey] = newSet
}

// Remove fully removes objectKey from the index. Equivalent to
// Update(objectKey, nil); provided separately to make delete-event call
// sites read clearly.
func (idx *Index) Remove(objectKey types.NamespacedName) {
	idx.Update(objectKey, nil)
}

// ObjectsFor returns the current set of objects that depend on secretKey, in
// no particular order. Returns nil if nothing depends on it.
func (idx *Index) ObjectsFor(secretKey types.NamespacedName) []types.NamespacedName {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	set := idx.bySecret[secretKey]
	if len(set) == 0 {
		return nil
	}
	out := make([]types.NamespacedName, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
