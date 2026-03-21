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
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// NOTE: ResourceWatcher requires RBAC permissions to watch arbitrary resource types.
// Unlike other RBAC markers in this project, the exact permissions depend on the user's
// Trigger configurations (e.g., watching Pods requires get/list/watch on pods).
// Rather than granting wildcard RBAC (*/*), users must grant the controller's
// ServiceAccount appropriate permissions for the resource types they wish to watch.
// See docs/api/trigger.md for guidance.

// ResourceWatcher manages per-Trigger dynamic informers for type:resource triggers.
// It mirrors the CronScheduler pattern: the TriggerReconciler calls Register/Deregister
// and ResourceWatcher owns the informer lifecycle.
type ResourceWatcher struct {
	client        client.Client
	dynamicClient dynamic.Interface
	log           logr.Logger

	mu       sync.Mutex
	watchers map[string]context.CancelFunc // key: "namespace/name"
}

// NewResourceWatcher creates a new ResourceWatcher.
func NewResourceWatcher(c client.Client, dynClient dynamic.Interface, log logr.Logger) *ResourceWatcher {
	return &ResourceWatcher{
		client:        c,
		dynamicClient: dynClient,
		log:           log.WithName("resource-watcher"),
		watchers:      make(map[string]context.CancelFunc),
	}
}

// Register starts watching resources for the given Trigger. If a watcher already
// exists for this trigger key, it is stopped and replaced.
func (rw *ResourceWatcher) Register(trigger *automationv1alpha1.Trigger) {
	key := trigger.Namespace + "/" + trigger.Name
	rw.Deregister(key)

	if trigger.Spec.Resource == nil {
		rw.log.Info("resource trigger has no resource spec, skipping", "trigger", key)
		return
	}

	if rw.dynamicClient == nil {
		rw.log.Info("dynamic client not configured, skipping resource watcher", "trigger", key)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	rw.mu.Lock()
	rw.watchers[key] = cancel
	rw.mu.Unlock()

	go rw.runWatcher(ctx, trigger)
}

// Deregister stops the informer for the given trigger key.
func (rw *ResourceWatcher) Deregister(key string) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if cancel, ok := rw.watchers[key]; ok {
		cancel()
		delete(rw.watchers, key)
	}
}

func (rw *ResourceWatcher) runWatcher(ctx context.Context, trigger *automationv1alpha1.Trigger) {
	spec := trigger.Spec.Resource
	triggerKey := trigger.Namespace + "/" + trigger.Name

	// Parse GVR from apiVersion + kind.
	group, version := parseAPIVersion(spec.APIVersion)
	// Use lowercased + pluralised kind as resource name.
	// For well-known types this is correct; custom resources may need the exact plural.
	resource := strings.ToLower(spec.Kind) + "s"

	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	watchNS := spec.Namespace
	if watchNS == "" {
		watchNS = trigger.Namespace
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		rw.dynamicClient, 30*time.Second, watchNS, nil,
	)
	informer := factory.ForResource(gvr).Informer()

	allowedEvents := allowedEventSet(spec.Events)

	_, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !allowedEvents["create"] {
				return
			}
			rw.handleEvent(ctx, trigger, obj, watch.Added)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if !allowedEvents["update"] {
				return
			}
			if len(spec.WatchFields) > 0 && !fieldsChanged(oldObj, newObj, spec.WatchFields) {
				return
			}
			rw.handleEvent(ctx, trigger, newObj, watch.Modified)
		},
		DeleteFunc: func(obj interface{}) {
			if !allowedEvents["delete"] {
				return
			}
			rw.handleEvent(ctx, trigger, obj, watch.Deleted)
		},
	})
	if err != nil {
		rw.log.Error(err, "failed to add event handler", "trigger", triggerKey)
		return
	}

	factory.Start(ctx.Done())
	syncResults := factory.WaitForCacheSync(ctx.Done())
	if synced, ok := syncResults[gvr]; !ok || !synced {
		rw.log.Error(fmt.Errorf("cache sync failed"), "cache sync failed for resource watcher",
			"trigger", triggerKey, "gvr", gvr.String())
		return
	}

	rw.log.Info("resource watcher started", "trigger", triggerKey, "gvr", gvr, "namespace", watchNS)
	<-ctx.Done()
	rw.log.Info("resource watcher stopped", "trigger", triggerKey)
}

func (rw *ResourceWatcher) handleEvent(
	ctx context.Context,
	trigger *automationv1alpha1.Trigger,
	obj interface{},
	eventType watch.EventType,
) {
	u, ok := toUnstructured(obj)
	if !ok {
		return
	}

	// Apply label selector filtering if specified.
	if trigger.Spec.Resource.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(trigger.Spec.Resource.LabelSelector)
		if err == nil && !sel.Matches(labels.Set(u.GetLabels())) {
			return
		}
	}

	resName := u.GetName()
	resNS := u.GetNamespace()

	bodyJSON, _ := json.Marshal(u.Object)

	flowRef := ""
	flowNS := trigger.Namespace
	if trigger.Spec.FlowRef != nil {
		flowRef = trigger.Spec.FlowRef.Name
		if trigger.Spec.FlowRef.Namespace != "" {
			flowNS = trigger.Spec.FlowRef.Namespace
		}
	}
	if flowRef == "" {
		return
	}

	eventStr := strings.ToLower(string(eventType))
	// FlowRun name: <trigger>-<resource-name>-<eventtype>-<timestamp>
	flowRunName := fmt.Sprintf("%s-%s-%s-%d",
		trigger.Name, sanitizeName(resName), eventStr, time.Now().Unix())

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: trigger.Namespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      trigger.Name,
				"kubezap.io/trigger-type": "resource",
				"kubezap.io/flow":         flowRef,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{
				Name:      flowRef,
				Namespace: flowNS,
			},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: trigger.Name,
				Type: "resource",
			},
			TriggerData: &automationv1alpha1.TriggerData{
				EventType:          string(eventType),
				ResourceName:       resName,
				ResourceNamespace:  resNS,
				ResourceAPIVersion: trigger.Spec.Resource.APIVersion,
				ResourceKind:       trigger.Spec.Resource.Kind,
				Body:               string(bodyJSON),
			},
		},
	}

	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := rw.client.Create(createCtx, flowRun); err != nil {
		rw.log.Error(err, "failed to create FlowRun for resource trigger",
			"trigger", trigger.Namespace+"/"+trigger.Name,
			"resource", resNS+"/"+resName,
			"event", eventType,
		)
	} else {
		rw.log.Info("created FlowRun for resource event",
			"trigger", trigger.Name,
			"flowRun", flowRunName,
			"resource", resNS+"/"+resName,
			"event", eventType,
		)
	}
}

// parseAPIVersion splits "group/version" or "version" into (group, version).
func parseAPIVersion(apiVersion string) (group, version string) {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0] // core group
}

// allowedEventSet converts a []string of event names to a set.
// Defaults to {"create"} if empty.
func allowedEventSet(events []string) map[string]bool {
	if len(events) == 0 {
		return map[string]bool{"create": true}
	}
	set := make(map[string]bool, len(events))
	for _, e := range events {
		set[strings.ToLower(e)] = true
	}
	return set
}

// fieldsChanged checks if any of the given JSON-path fields differ between old and new.
// Uses simple dot-notation: ".status.phase" -> ["status", "phase"]
func fieldsChanged(oldObj, newObj interface{}, fields []string) bool {
	oldU, ok1 := toUnstructured(oldObj)
	newU, ok2 := toUnstructured(newObj)
	if !ok1 || !ok2 {
		return true
	}
	for _, field := range fields {
		path := strings.Split(strings.TrimPrefix(field, "."), ".")
		oldVal, _, _ := unstructured.NestedFieldNoCopy(oldU.Object, path...)
		newVal, _, _ := unstructured.NestedFieldNoCopy(newU.Object, path...)
		if fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			return true
		}
	}
	return false
}

func toUnstructured(obj interface{}) (*unstructured.Unstructured, bool) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u, true
	}
	// Tombstone unwrap
	if ts, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		if u, ok2 := ts.Obj.(*unstructured.Unstructured); ok2 {
			return u, true
		}
	}
	return nil, false
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := b.String()
	if len(result) > 32 {
		result = result[:32]
	}
	return strings.Trim(result, "-")
}
