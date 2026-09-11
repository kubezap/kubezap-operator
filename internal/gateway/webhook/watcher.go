package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	"github.com/kubezap/kubezap-operator/internal/gateway/secretindex"
)

var controllerScheme = runtime.NewScheme()

func init() {
	_ = automationv1alpha1.AddToScheme(controllerScheme)
	_ = corev1.AddToScheme(controllerScheme)
}

// TriggerWatcher watches Trigger resources and updates the route registry.
type TriggerWatcher struct {
	k8sClient client.Client
	cache     crcache.Cache
	registry  *RouteRegistry
	namespace string
	log       logr.Logger
	jwksCache *jwk.Cache

	// secretIndex and triggers together let a Secret change (see
	// docs/design/2026-09-11-secret-rotation-watches.md) reprocess exactly the
	// Triggers that reference it, without an extra API call: triggers holds the
	// most recently seen object for each Trigger key (kept in sync by
	// handleTrigger/handleDelete), and secretIndex maps a Secret key to the set
	// of Trigger keys that currently reference it.
	secretIndex *secretindex.Index
	triggersMu  sync.Mutex
	triggers    map[types.NamespacedName]*automationv1alpha1.Trigger
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

	return &TriggerWatcher{
		k8sClient:   k8sClient,
		cache:       watchCache,
		registry:    registry,
		namespace:   namespace,
		log:         log,
		jwksCache:   jwksCache,
		secretIndex: secretindex.New(),
		triggers:    make(map[types.NamespacedName]*automationv1alpha1.Trigger),
	}, nil
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

	secretInformer, err := w.cache.GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return fmt.Errorf("unable to get secret informer: %w", err)
	}
	if _, err := secretInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.handleSecretChange(obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { w.handleSecretChange(newObj) },
	}); err != nil {
		return fmt.Errorf("adding secret event handler: %w", err)
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

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.triggersMu.Lock()
	w.triggers[key] = trigger
	w.triggersMu.Unlock()
	w.secretIndex.Update(key, secretRefsForTrigger(trigger))

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

// secretRefsForTrigger returns the Secret keys a webhook Trigger's auth config
// references, without fetching them. Computed independently of whether those
// secrets currently exist or can be read, so a Trigger with a missing secret
// still gets reprocessed once that secret is created — see
// docs/design/2026-09-11-secret-rotation-watches.md.
func secretRefsForTrigger(trigger *automationv1alpha1.Trigger) []types.NamespacedName {
	if trigger.Spec.Type != "webhook" || trigger.Spec.Webhook == nil || trigger.Spec.Webhook.Auth == nil {
		return nil
	}
	auth := trigger.Spec.Webhook.Auth
	ns := trigger.Namespace
	switch auth.Type {
	case authTypeHMAC:
		if auth.HMAC != nil {
			return []types.NamespacedName{{Namespace: ns, Name: auth.HMAC.SecretRef.Name}}
		}
	case authTypeBearer:
		if auth.Bearer != nil {
			return []types.NamespacedName{{Namespace: ns, Name: auth.Bearer.TokenSecretRef.Name}}
		}
	case authTypeAPIKey:
		if auth.APIKey != nil {
			return []types.NamespacedName{{Namespace: ns, Name: auth.APIKey.SecretRef.Name}}
		}
	case authTypeBasic:
		if auth.Basic != nil {
			return []types.NamespacedName{{Namespace: ns, Name: auth.Basic.SecretRef.Name}}
		}
	case authTypeHeaderEquals:
		if auth.HeaderEquals != nil {
			return []types.NamespacedName{{Namespace: ns, Name: auth.HeaderEquals.SecretRef.Name}}
		}
	}
	return nil
}

// handleSecretChange reprocesses every Trigger currently known to reference
// the changed Secret, using each Trigger's most recently seen object (no
// extra API call) — the same code path a real Trigger spec change takes.
func (w *TriggerWatcher) handleSecretChange(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return
	}
	secretKey := types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) == 0 {
		return
	}
	w.log.Info("secret changed, reprocessing dependent triggers", "secret", secretKey, "count", len(affected))
	for _, triggerKey := range affected {
		w.triggersMu.Lock()
		trigger := w.triggers[triggerKey]
		w.triggersMu.Unlock()
		if trigger == nil {
			continue
		}
		w.handleTrigger(trigger)
	}
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

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.triggersMu.Lock()
	delete(w.triggers, key)
	w.triggersMu.Unlock()
	w.secretIndex.Remove(key)

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
//
// nolint:gocyclo // single-pass config builder with a case per auth type; deferred
// pending owner decision, see docs/schedule.md §36.
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
		RedactBody:       trigger.Spec.Webhook.RedactBody,
	}

	if len(trigger.Spec.Webhook.RedactHeaders) > 0 {
		entry.RedactHeaders = append([]string(nil), trigger.Spec.Webhook.RedactHeaders...)
	}

	// RateLimit provides local per-replica enforcement. Global cluster-wide enforcement
	// (counting FlowRuns in etcd across all replicas) is implemented separately via
	// admission webhook and is not wired here.
	if trigger.Spec.Webhook.RateLimit != nil {
		entry.MaxInvocations = trigger.Spec.Webhook.RateLimit.MaxRequests
		window := trigger.Spec.Webhook.RateLimit.Window.Duration
		if window == 0 {
			window = 60 * time.Second
		}
		entry.CooldownWindow = window
	}

	auth := trigger.Spec.Webhook.Auth
	if auth == nil || auth.Type == "" {
		entry.AuthType = ""
		return entry, nil
	}

	entry.AuthType = auth.Type

	switch auth.Type {
	case authTypeHMAC:
		if auth.HMAC == nil {
			return RouteEntry{}, fmt.Errorf("hmac auth requires hmac config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.HMAC.SecretRef.Name, auth.HMAC.SecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading HMAC secret: %w", err)
		}
		entry.HMACSecret = val

		provider := auth.HMAC.Provider
		if provider == "" {
			provider = "github"
		}
		entry.HMACProvider = provider

		tolerance := auth.HMAC.TimestampToleranceSeconds
		if tolerance <= 0 {
			tolerance = 300
		}
		entry.HMACTimestampToleranceSec = tolerance

	case authTypeBearer:
		if auth.Bearer == nil {
			return RouteEntry{}, fmt.Errorf("bearer auth requires bearer config")
		}
		val, err := w.readSecretKey(ctx, trigger.Namespace, auth.Bearer.TokenSecretRef.Name, auth.Bearer.TokenSecretRef.Key)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("reading bearer token secret: %w", err)
		}
		entry.BearerToken = val

	case authTypeAPIKey:
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

	case authTypeBasic:
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

	case authTypeIPAllowlist:
		if auth.IPAllowlist != nil {
			entry.IPAllowlist = append([]string(nil), auth.IPAllowlist.CIDRs...)
		}

	case authTypeOIDC:
		if auth.OIDC == nil {
			return RouteEntry{}, fmt.Errorf("oidc auth requires oidc config")
		}
		// Discover the JWKS URL from the OIDC provider's discovery document.
		// This is more reliable than guessing the path from the issuer URL.
		jwksURL, err := discoverJWKSURL(ctx, auth.OIDC.Issuer)
		if err != nil {
			return RouteEntry{}, fmt.Errorf("discovering OIDC JWKS URL: %w", err)
		}
		if err := RegisterJWKSURL(ctx, w.jwksCache, jwksURL); err != nil {
			var preWarmErr *jwksPrewarmError
			if errors.As(err, &preWarmErr) {
				// Pre-warm failed (IdP not yet reachable) — still register the route.
				// The cache will populate on the first incoming request.
				w.log.Info("JWKS pre-warm failed; route registered but cache cold — will retry on first request",
					"trigger", trigger.Name, "jwksURL", jwksURL, "error", preWarmErr.cause)
			} else {
				return RouteEntry{}, fmt.Errorf("registering OIDC JWKS URL: %w", err)
			}
		}
		entry.OIDCValidator = newOIDCValidator(jwksURL, auth.OIDC.Issuer, auth.OIDC.Audience, w.jwksCache)

	case authTypeHeaderEquals:
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

// discoverJWKSURL fetches the OIDC provider discovery document and returns the jwks_uri.
// This is the correct way to find the JWKS endpoint — guessing from the issuer URL is fragile
// because providers like Dex use non-standard paths (e.g. /dex/keys, not /.well-known/jwks.json).
func discoverJWKSURL(ctx context.Context, issuer string) (string, error) {
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("building discovery request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching OIDC discovery document from %s: %w", discoveryURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OIDC discovery at %s returned HTTP %d", discoveryURL, resp.StatusCode)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("decoding OIDC discovery document: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("OIDC discovery document at %s missing jwks_uri", discoveryURL)
	}
	return doc.JWKSURI, nil
}
