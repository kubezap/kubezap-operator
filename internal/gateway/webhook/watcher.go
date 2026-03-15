package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

// TriggerWatcher watches Trigger and MockEndpoint resources and updates the registries.
type TriggerWatcher struct {
	k8sClient    client.Client
	cache        crcache.Cache
	registry     *RouteRegistry
	mockRegistry *MockRegistry
	namespace    string
	log          logr.Logger
}

// NewTriggerWatcher creates a new TriggerWatcher with an informer cache.
func NewTriggerWatcher(k8sClient client.Client, registry *RouteRegistry, mockRegistry *MockRegistry, namespace string, log logr.Logger) (*TriggerWatcher, error) {
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

	return &TriggerWatcher{k8sClient: k8sClient, cache: watchCache, registry: registry, mockRegistry: mockRegistry, namespace: namespace, log: log}, nil
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

	if w.mockRegistry != nil {
		mockInformer, err := w.cache.GetInformer(ctx, &automationv1alpha1.MockEndpoint{})
		if err != nil {
			return fmt.Errorf("unable to get mock endpoint informer: %w", err)
		}
		_, err = mockInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { w.handleMockEndpoint(obj) },
			UpdateFunc: func(_, newObj interface{}) { w.handleMockEndpoint(newObj) },
			DeleteFunc: func(obj interface{}) { w.handleMockEndpointDelete(obj) },
		})
		if err != nil {
			return fmt.Errorf("unable to add mock endpoint event handler: %w", err)
		}
	}

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
		entry, err := w.buildRouteEntry(context.Background(), trigger)
		if err != nil {
			w.log.Error(err, "failed to build route entry; route not registered", "trigger", trigger.Name, "namespace", trigger.Namespace)
			return
		}
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

// buildRouteEntry constructs a RouteEntry from a Trigger, loading auth secrets as needed.
// Returns an error if a required secret cannot be fetched; in that case the route must NOT be registered.
func (w *TriggerWatcher) buildRouteEntry(ctx context.Context, trigger *automationv1alpha1.Trigger) (RouteEntry, error) {
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

	entry := RouteEntry{
		TriggerName:      trigger.Name,
		TriggerNamespace: trigger.Namespace,
		FlowRef:          flowRef,
		FlowNamespace:    flowNamespace,
		AllowedMethod:    method,
	}

	auth := trigger.Spec.Webhook.Auth
	if auth == nil || auth.Type == "" {
		entry.AuthType = ""
		return entry, nil
	}

	entry.AuthType = auth.Type

	switch auth.Type {
	case "hmac":
		if auth.HMACSecretRef == nil {
			return RouteEntry{}, fmt.Errorf("hmac auth requires hmacSecretRef")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.HMACSecretRef.Name, auth.HMACSecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading HMAC secret: %w", err)
		}
		entry.HMACSecret = val

	case "bearer":
		if auth.BearerTokenSecretRef == nil {
			return RouteEntry{}, fmt.Errorf("bearer auth requires bearerTokenSecretRef")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.BearerTokenSecretRef.Name, auth.BearerTokenSecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading bearer token secret: %w", err)
		}
		entry.BearerToken = val

	case "apiKey":
		if auth.APIKeySecretRef == nil {
			return RouteEntry{}, fmt.Errorf("apiKey auth requires apiKeySecretRef")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.APIKeySecretRef.Name, auth.APIKeySecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading API key secret: %w", err)
		}
		entry.APIKey = val
		entry.APIKeyHeader = auth.APIKeyHeader
		if entry.APIKeyHeader == "" {
			entry.APIKeyHeader = "X-Api-Key"
		}

	case "ipAllowlist":
		entry.IPAllowlist = append([]string(nil), auth.IPAllowlist...)
	}

	return entry, nil
}

// readSecretKey fetches a Kubernetes Secret and returns the value for the given key.
func (w *TriggerWatcher) readSecretKey(ctx context.Context, namespace, name, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := w.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret); err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", namespace, name, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s does not contain key %q", namespace, name, key)
	}
	return string(val), nil
}

func (w *TriggerWatcher) handleMockEndpoint(obj interface{}) {
	me, ok := obj.(*automationv1alpha1.MockEndpoint)
	if !ok {
		w.log.Error(fmt.Errorf("wrong object type"), "expected MockEndpoint")
		return
	}

	path := strings.TrimSpace(me.Spec.Path)
	if path == "" {
		w.log.Info("MockEndpoint has empty path, skipping registration", "name", me.Name)
		return
	}
	mockPath := "/mock/" + strings.TrimPrefix(path, "/")

	entry := MockEntry{
		Name:             me.Name,
		Namespace:        me.Namespace,
		Response:         me.Spec.Response,
		ResponseSequence: me.Spec.ResponseSequence,
		MaxHistory:       me.Spec.MaxRequestHistory,
	}
	w.mockRegistry.Register(mockPath, entry)
	w.log.Info("registered mock route", "path", mockPath, "name", me.Name)
}

func (w *TriggerWatcher) handleMockEndpointDelete(obj interface{}) {
	me, ok := obj.(*automationv1alpha1.MockEndpoint)
	if !ok {
		tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected delete object type"), "obj", obj)
			return
		}
		me, ok = tombstone.Obj.(*automationv1alpha1.MockEndpoint)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected tombstone object type"), "obj", tombstone.Obj)
			return
		}
	}

	path := strings.TrimSpace(me.Spec.Path)
	if path == "" {
		return
	}
	mockPath := "/mock/" + strings.TrimPrefix(path, "/")
	w.mockRegistry.Deregister(mockPath)
	w.log.Info("deregistered mock route", "path", mockPath, "name", me.Name)
}
