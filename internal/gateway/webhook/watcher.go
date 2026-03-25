package webhook

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/lestrrat-go/jwx/v2/jwk"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var controllerScheme = runtime.NewScheme()

func init() {
	_ = automationv1alpha1.AddToScheme(controllerScheme)
}

// TriggerWatcher watches Trigger resources and updates the route registry.
type TriggerWatcher struct {
	k8sClient client.Client
	cache     crcache.Cache
	registry  *RouteRegistry
	namespace string
	log       logr.Logger
	jwksCache *jwk.Cache
}

// NewTriggerWatcher creates a new TriggerWatcher with an informer cache.
// cfg must be a valid *rest.Config; callers typically obtain it via ctrl.GetConfigOrDie()
// and pass it here to avoid redundant API server round-trips.
// jwksCache is the shared JWKS key cache; callers create it via jwk.NewCache(ctx) in main.
func NewTriggerWatcher(cfg *rest.Config, k8sClient client.Client, registry *RouteRegistry, namespace string, log logr.Logger, jwksCache *jwk.Cache) (*TriggerWatcher, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create HTTP client for REST config: %w", err)
	}

	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
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

	return &TriggerWatcher{k8sClient: k8sClient, cache: watchCache, registry: registry, namespace: namespace, log: log, jwksCache: jwksCache}, nil
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		entry, err := w.buildRouteEntry(ctx, trigger)
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
	if path == "" {
		return ""
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

// buildRouteEntry constructs a RouteEntry from a Trigger, loading auth secrets as needed.
// Returns an error if a required secret cannot be fetched; in that case the route must NOT be registered.
func (w *TriggerWatcher) buildRouteEntry(ctx context.Context, trigger *automationv1alpha1.Trigger) (RouteEntry, error) {
	// FlowRef.Namespace was removed in v1alpha1; Flow is always in the same namespace as the Trigger.
	flowNamespace := trigger.Namespace

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
		if auth.HMAC == nil {
			return RouteEntry{}, fmt.Errorf("hmac auth requires hmac config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.HMAC.SecretRef.Name, auth.HMAC.SecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading HMAC secret: %w", err)
		}
		entry.HMACSecret = val

	case "bearer":
		if auth.Bearer == nil {
			return RouteEntry{}, fmt.Errorf("bearer auth requires bearer config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.Bearer.TokenSecretRef.Name, auth.Bearer.TokenSecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading bearer token secret: %w", err)
		}
		entry.BearerToken = val

	case "apiKey":
		if auth.APIKey == nil {
			return RouteEntry{}, fmt.Errorf("apiKey auth requires apiKey config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.APIKey.SecretRef.Name, auth.APIKey.SecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading API key secret: %w", err)
		}
		entry.APIKey = val
		entry.APIKeyHeader = auth.APIKey.Header
		if entry.APIKeyHeader == "" {
			entry.APIKeyHeader = "X-Api-Key"
		}

	case "basic":
		if auth.Basic == nil {
			return RouteEntry{}, fmt.Errorf("basic auth requires basic.secretRef")
		}
		usernameKey := auth.Basic.UsernameKey
		if usernameKey == "" {
			usernameKey = "username"
		}
		passwordKey := auth.Basic.PasswordKey
		if passwordKey == "" {
			passwordKey = "password"
		}
		username, err := w.readSecretKey(ctx, trigger.Namespace, auth.Basic.SecretRef.Name, usernameKey)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading Basic auth username from secret: %w", err)
		}
		password, err := w.readSecretKey(ctx, trigger.Namespace, auth.Basic.SecretRef.Name, passwordKey)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading Basic auth password from secret: %w", err)
		}
		entry.BasicUsername = username
		entry.BasicPassword = password

	case "ipAllowlist":
		if auth.IPAllowlist != nil {
			entry.IPAllowlist = append([]string(nil), auth.IPAllowlist.CIDRs...)
		}

	case "oidc":
		if auth.OIDC == nil {
			return RouteEntry{}, fmt.Errorf("oidc auth requires oidc config")
		}
		// OIDC.Audience is optional; OIDC.Issuer is optional but recommended.
		// JWKS URL is derived from the issuer using the standard well-known path.
		jwksURL := auth.OIDC.Issuer + "/.well-known/jwks.json"
		if err := RegisterJWKSURL(ctx, w.jwksCache, jwksURL); err != nil {
			return RouteEntry{}, fmt.Errorf("registering OIDC JWKS URL: %w", err)
		}
		entry.OIDCValidator = newOIDCValidator(jwksURL, auth.OIDC.Issuer, auth.OIDC.Audience, w.jwksCache)

	case "header-equals":
		if auth.HeaderEquals == nil {
			return RouteEntry{}, fmt.Errorf("header-equals auth requires headerEquals config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.HeaderEquals.SecretRef.Name, auth.HeaderEquals.SecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading header-equals secret: %w", err)
		}
		entry.HeaderEqualsHeader = auth.HeaderEquals.Header
		entry.HeaderEqualsValue = val
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
