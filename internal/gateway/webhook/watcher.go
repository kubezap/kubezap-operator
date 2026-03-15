package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

var controllerScheme = runtime.NewScheme()

func init() {
	_ = automationv1alpha1.AddToScheme(controllerScheme)
}

// TriggerWatcher watches Trigger resources and updates the RouteRegistry.
type TriggerWatcher struct {
	k8sClient client.Client
	cache     crcache.Cache
	registry  *RouteRegistry
	namespace string
	log       logr.Logger
}

// NewTriggerWatcher creates a new TriggerWatcher with an informer cache.
func NewTriggerWatcher(k8sClient client.Client, registry *RouteRegistry, namespace string, log logr.Logger) (*TriggerWatcher, error) {
	cfg := ctrl.GetConfigOrDie()
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("unable to create REST mapper: %w", err)
	}

	cacheOpts := crcache.Options{Scheme: controllerScheme, Mapper: mapper}
	if namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]crcache.Config{namespace: {}}
	}

	watchCache, err := crcache.New(cfg, cacheOpts)
	if err != nil {
		return nil, fmt.Errorf("unable to create cache: %w", err)
	}

	return &TriggerWatcher{k8sClient: k8sClient, cache: watchCache, registry: registry, namespace: namespace, log: log}, nil
}

// Start launches the cache and informer and stays running until ctx is cancelled.
func (w *TriggerWatcher) Start(ctx context.Context) error {
	triggerInformer, err := w.cache.GetInformer(ctx, &automationv1alpha1.Trigger{})
	if err != nil {
		return fmt.Errorf("unable to get trigger informer: %w", err)
	}

	registration, err := triggerInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.handleTrigger(obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { w.handleTrigger(newObj) },
		DeleteFunc: func(obj interface{}) { w.handleDelete(obj) },
	})
	if err != nil {
		return fmt.Errorf("adding trigger event handler: %w", err)
	}
	_ = registration

	go func() {
		if err := w.cache.Start(ctx); err != nil && err != context.Canceled {
			w.log.Error(err, "trigger cache stopped with error")
		}
	}()

	if !w.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("timed out waiting for initial cache sync")
	}

	w.registry.MarkSynced()
	w.log.Info("trigger cache synced")

	<-ctx.Done()
	w.log.Info("trigger watcher context canceled")
	return nil
}

func (w *TriggerWatcher) handleTrigger(obj interface{}) {
	trigger, ok := obj.(*automationv1alpha1.Trigger)
	if !ok {
		w.log.Error(fmt.Errorf("wrong object type"), "expected Trigger")
		return
	}

	if trigger.Spec.Type == "webhook" && trigger.Spec.Enabled && trigger.Spec.Webhook != nil {
		entry := triggerToRouteEntry(trigger)
		w.registry.Register(entryRoutePath(trigger), entry)
		return
	}
	w.registry.Deregister(entryRoutePath(trigger))
}

func (w *TriggerWatcher) handleDelete(obj interface{}) {
	trigger, ok := obj.(*automationv1alpha1.Trigger)
	if !ok {
		tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected delete object type"), "obj", obj)
			return
		}
		trigger, ok = tombstone.Obj.(*automationv1alpha1.Trigger)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected tombstone object type"), "obj", tombstone.Obj)
			return
		}
	}

	w.registry.Deregister(entryRoutePath(trigger))
}

func entryRoutePath(trigger *automationv1alpha1.Trigger) string {
	if trigger.Spec.Webhook == nil {
		return ""
	}

	path := strings.TrimSpace(trigger.Spec.Webhook.Path)
	path = strings.TrimPrefix(path, "/")
	return "/hooks/" + path
}

func triggerToRouteEntry(trigger *automationv1alpha1.Trigger) RouteEntry {
	flowNamespace := trigger.Namespace
	if trigger.Spec.FlowRef != nil && trigger.Spec.FlowRef.Namespace != "" {
		flowNamespace = trigger.Spec.FlowRef.Namespace
	}

	method := "POST"
	if trigger.Spec.Webhook != nil && trigger.Spec.Webhook.Method != "" {
		method = strings.ToUpper(trigger.Spec.Webhook.Method)
	}

	flowRef := ""
	if trigger.Spec.FlowRef != nil {
		flowRef = trigger.Spec.FlowRef.Name
	}

	return RouteEntry{
		TriggerName:      trigger.Name,
		TriggerNamespace: trigger.Namespace,
		FlowRef:          flowRef,
		FlowNamespace:    flowNamespace,
		AllowedMethod:    method,
	}
}
