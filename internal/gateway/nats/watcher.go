package natsgateway

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	natsio "github.com/nats-io/nats.go"
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

// subscription tracks an active NATS subscription for a Trigger.
type subscription struct {
	cancel                context.CancelFunc
	integrationName       string
	subject               string
	isJetStream           bool
	credentialFingerprint string
}

// Watcher watches Trigger CRDs via an informer and manages NATS subscriptions.
type Watcher struct {
	client        client.Client
	cache         crcache.Cache
	namespace     string
	log           logr.Logger
	subscriptions sync.Map // key: types.NamespacedName, value: *subscription

	// secretIndex and triggers together let a Secret change (see
	// docs/design/secret-rotation-watches.md) reprocess exactly the
	// Triggers whose Integration references it, without an extra API call:
	// triggers holds the most recently seen object for each Trigger key, and
	// secretIndex maps a Secret key to the set of Trigger keys that currently
	// depend on it (via their Integration's TLS/credentials/username secretRefs).
	secretIndex *secretindex.Index
	// integrationIndex maps an Integration key to the set of Trigger keys
	// whose IntegrationRef currently points at it, mirroring secretIndex —
	// see docs/design/integration-secretref-change-detection.md. This closes
	// the gap where an Integration's secretRef is repointed to a different
	// Secret without the referencing Trigger itself being touched: nothing
	// previously watched Integration objects, so reconcileTrigger (and thus
	// secretIndex.Update, which would have picked up the new Secret) never
	// ran again for that Trigger.
	integrationIndex *secretindex.Index
	triggersMu       sync.Mutex
	triggers         map[types.NamespacedName]*automationv1alpha1.Trigger
}

// NewWatcher creates a new Watcher backed by an informer cache.
func NewWatcher(c client.Client, cfg *rest.Config, namespace string, log logr.Logger) (*Watcher, error) {
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

	return &Watcher{
		client:           c,
		cache:            watchCache,
		namespace:        namespace,
		log:              log,
		secretIndex:      secretindex.New(),
		integrationIndex: secretindex.New(),
		triggers:         make(map[types.NamespacedName]*automationv1alpha1.Trigger),
	}, nil
}

// Start launches the informer cache and stays running until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	triggerInformer, err := w.cache.GetInformer(ctx, &automationv1alpha1.Trigger{})
	if err != nil {
		return fmt.Errorf("unable to get trigger informer: %w", err)
	}

	_, err = triggerInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.onTriggerAdd(ctx, obj) },
		UpdateFunc: func(_, newObj interface{}) { w.onTriggerUpdate(ctx, newObj) },
		DeleteFunc: func(obj interface{}) { w.onTriggerDelete(obj) },
	})
	if err != nil {
		return fmt.Errorf("adding trigger event handler: %w", err)
	}

	secretInformer, err := w.cache.GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return fmt.Errorf("unable to get secret informer: %w", err)
	}
	if _, err := secretInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.handleSecretChange(ctx, obj) },
		UpdateFunc: func(_, newObj interface{}) { w.handleSecretChange(ctx, newObj) },
	}); err != nil {
		return fmt.Errorf("adding secret event handler: %w", err)
	}

	integrationInformer, err := w.cache.GetInformer(ctx, &automationv1alpha1.Integration{})
	if err != nil {
		return fmt.Errorf("unable to get integration informer: %w", err)
	}
	if _, err := integrationInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { w.handleIntegrationChange(ctx, obj) },
		UpdateFunc: func(_, newObj interface{}) { w.handleIntegrationChange(ctx, newObj) },
	}); err != nil {
		return fmt.Errorf("adding integration event handler: %w", err)
	}

	go func() {
		if err := w.cache.Start(ctx); err != nil && err != context.Canceled {
			w.log.Error(err, "trigger cache stopped with error")
		}
	}()

	if !w.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("timed out waiting for initial cache sync")
	}
	w.log.Info("nats watcher started", "namespace", w.namespace)

	<-ctx.Done()
	w.log.Info("nats watcher stopped")

	// Cancel all active subscriptions on shutdown.
	w.subscriptions.Range(func(key, value any) bool {
		sub := value.(*subscription)
		sub.cancel()
		w.subscriptions.Delete(key)
		return true
	})
	return ctx.Err()
}

func (w *Watcher) onTriggerAdd(ctx context.Context, obj interface{}) {
	trigger, ok := obj.(*automationv1alpha1.Trigger)
	if !ok {
		return
	}
	w.reconcileTrigger(ctx, trigger)
}

func (w *Watcher) onTriggerUpdate(ctx context.Context, obj interface{}) {
	trigger, ok := obj.(*automationv1alpha1.Trigger)
	if !ok {
		return
	}
	w.reconcileTrigger(ctx, trigger)
}

func (w *Watcher) onTriggerDelete(obj interface{}) {
	trigger, ok := obj.(*automationv1alpha1.Trigger)
	if !ok {
		tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected delete object type"), "expected Trigger or tombstone")
			return
		}
		trigger, ok = tombstone.Obj.(*automationv1alpha1.Trigger)
		if !ok {
			w.log.Error(fmt.Errorf("unexpected tombstone object type"), "expected Trigger")
			return
		}
	}
	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.triggersMu.Lock()
	delete(w.triggers, key)
	w.triggersMu.Unlock()
	w.secretIndex.Remove(key)
	w.integrationIndex.Remove(key)
	w.stopSubscription(key)
}

// reconcileTrigger ensures the subscription state for a single Trigger matches its spec.
func (w *Watcher) reconcileTrigger(ctx context.Context, trigger *automationv1alpha1.Trigger) {
	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.triggersMu.Lock()
	w.triggers[key] = trigger
	w.triggersMu.Unlock()

	// Stop subscription if trigger is not a nats trigger or is disabled.
	if trigger.Spec.Type != triggerTypeNats || trigger.Spec.Nats == nil || !trigger.Spec.Enabled {
		w.secretIndex.Remove(key)
		w.integrationIndex.Remove(key)
		w.stopSubscription(key)
		return
	}

	subject := trigger.Spec.Nats.Subject
	integrationName := trigger.Spec.Nats.IntegrationRef.Name
	integrationKey := types.NamespacedName{Namespace: trigger.Namespace, Name: integrationName}
	// Index the intended Integration ref regardless of read success below, so
	// a Trigger whose Integration doesn't exist yet still gets reprocessed
	// once it's created, and so a repointed IntegrationRef is picked up on
	// the next Trigger event even if the old Integration read had failed.
	w.integrationIndex.Update(key, []types.NamespacedName{integrationKey})

	integration := &automationv1alpha1.Integration{}
	if err := w.client.Get(ctx, integrationKey, integration); err != nil {
		w.log.Error(err, "failed to get integration for nats trigger", "trigger", key, "integration", integrationName)
		return
	}
	if integration.Spec.Nats == nil {
		w.log.Error(fmt.Errorf("integration has no nats spec"), "cannot reconcile nats trigger", "trigger", key, "integration", integrationName)
		return
	}

	creds, credErr := w.resolveNatsCredentials(ctx, trigger.Namespace, integration.Spec.Nats)
	// Index intended secret refs regardless of read success, so a Trigger whose
	// secret doesn't exist yet still gets reprocessed once it's created.
	w.secretIndex.Update(key, creds.secretRefs)
	if credErr != nil {
		w.log.Error(credErr, "failed to resolve nats credentials", "trigger", key, "integration", integrationName)
		return
	}

	if existing, ok := w.subscriptions.Load(key); ok {
		sub := existing.(*subscription)
		if sub.subject == subject && sub.integrationName == integrationName && sub.credentialFingerprint == creds.fingerprint {
			return // no change, not even to credentials
		}
		w.log.Info("nats subscription config changed, restarting",
			"trigger", key, "subject", subject)
		w.stopSubscription(key)
	}

	if err := w.startSubscription(ctx, trigger, integration.Spec.Nats, creds); err != nil {
		w.log.Error(err, "failed to start nats subscription", "trigger", key, "subject", subject)
	}
}

// natsCredentials holds the Secret-derived values needed to configure a NATS
// connection for one Trigger's Integration, plus bookkeeping for change
// detection (fingerprint) and secret-rotation reprocessing (secretRefs).
type natsCredentials struct {
	caPEM        string
	credsContent string // NATS .creds file content (JWT+seed), if CredentialsSecretRef is set
	username     string
	password     string
	secretRefs   []types.NamespacedName
	fingerprint  string
}

// resolveNatsCredentials reads whichever TLS/credentials/username secrets spec
// references and computes a fingerprint over their values, so callers can
// detect a credential-only rotation without comparing full secret contents.
// Unlike startSubscription, this never writes a credentials temp file.
func (w *Watcher) resolveNatsCredentials(ctx context.Context, namespace string, spec *automationv1alpha1.NatsIntegrationSpec) (natsCredentials, error) {
	var creds natsCredentials
	h := sha256.New()

	if spec.TLS != nil && spec.TLS.CASecretRef != nil {
		caPEM, err := w.readSecretKey(ctx, namespace, spec.TLS.CASecretRef.Name, spec.TLS.CASecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading CA cert secret: %w", err)
		}
		creds.caPEM = caPEM
		creds.secretRefs = append(creds.secretRefs, types.NamespacedName{Namespace: namespace, Name: spec.TLS.CASecretRef.Name})
		_, _ = h.Write([]byte(caPEM))
	}

	if spec.CredentialsSecretRef != nil {
		credsContent, err := w.readSecretKey(ctx, namespace, spec.CredentialsSecretRef.Name, "nats.creds")
		if err != nil {
			return creds, fmt.Errorf("reading nats credentials secret: %w", err)
		}
		creds.credsContent = credsContent
		creds.secretRefs = append(creds.secretRefs, types.NamespacedName{Namespace: namespace, Name: spec.CredentialsSecretRef.Name})
		_, _ = h.Write([]byte(credsContent))
	} else if spec.UsernameSecretRef != nil {
		username, err := w.readSecretKey(ctx, namespace, spec.UsernameSecretRef.Name, spec.UsernameSecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading nats username secret: %w", err)
		}
		if spec.PasswordSecretRef == nil {
			return creds, fmt.Errorf("spec.nats.passwordSecretRef must be set when usernameSecretRef is set")
		}
		password, err := w.readSecretKey(ctx, namespace, spec.PasswordSecretRef.Name, spec.PasswordSecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading nats password secret: %w", err)
		}
		creds.username = username
		creds.password = password
		creds.secretRefs = append(creds.secretRefs,
			types.NamespacedName{Namespace: namespace, Name: spec.UsernameSecretRef.Name},
			types.NamespacedName{Namespace: namespace, Name: spec.PasswordSecretRef.Name},
		)
		_, _ = h.Write([]byte(username))
		_, _ = h.Write([]byte(password))
	}

	creds.fingerprint = hex.EncodeToString(h.Sum(nil))
	return creds, nil
}

// handleSecretChange reprocesses every Trigger currently known to depend on
// the changed Secret (via its Integration's TLS/credentials/username config),
// using each Trigger's most recently seen object — the same code path a real
// Trigger spec change takes.
func (w *Watcher) handleSecretChange(ctx context.Context, obj interface{}) {
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
		w.reconcileTrigger(ctx, trigger)
	}
}

// handleIntegrationChange reprocesses every Trigger currently known to
// reference the changed Integration (via its IntegrationRef), using each
// Trigger's most recently seen object — the same code path a real Trigger
// spec change takes. This is what closes the stale-credential and
// stale-Secret-watch gap left by handleSecretChange alone: reconcileTrigger
// re-resolves credentials from the Integration's (possibly repointed)
// secretRefs and calls w.secretIndex.Update with the current, complete set,
// which naturally drops the old Secret and starts tracking the new one as a
// side effect of running again — see
// docs/design/integration-secretref-change-detection.md.
func (w *Watcher) handleIntegrationChange(ctx context.Context, obj interface{}) {
	integration, ok := obj.(*automationv1alpha1.Integration)
	if !ok {
		return
	}
	integrationKey := types.NamespacedName{Name: integration.Name, Namespace: integration.Namespace}
	affected := w.integrationIndex.ObjectsFor(integrationKey)
	if len(affected) == 0 {
		return
	}
	w.log.Info("integration changed, reprocessing dependent triggers", "integration", integrationKey, "count", len(affected))
	for _, triggerKey := range affected {
		w.triggersMu.Lock()
		trigger := w.triggers[triggerKey]
		w.triggersMu.Unlock()
		if trigger == nil {
			continue
		}
		w.reconcileTrigger(ctx, trigger)
	}
}

// startSubscription creates a NATS subscription for the given Trigger, using
// already-resolved credentials (see reconcileTrigger).
func (w *Watcher) startSubscription(ctx context.Context, trigger *automationv1alpha1.Trigger, spec *automationv1alpha1.NatsIntegrationSpec, creds natsCredentials) error {
	integrationName := trigger.Spec.Nats.IntegrationRef.Name
	subject := trigger.Spec.Nats.Subject

	// Build NATS connection options.
	opts := []natsio.Option{
		natsio.MaxReconnects(-1),
		natsio.ReconnectWait(5 * time.Second),
	}

	// Apply TLS if configured.
	if spec.TLS != nil {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: spec.TLS.InsecureSkipVerify, //nolint:gosec // user-configured
		}

		if spec.TLS.CASecretRef != nil {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(creds.caPEM)) {
				return fmt.Errorf("failed to parse CA certificate from secret %s/%s key %s",
					trigger.Namespace, spec.TLS.CASecretRef.Name, spec.TLS.CASecretRef.Key)
			}
			tlsCfg.RootCAs = pool
		}

		opts = append(opts, natsio.Secure(tlsCfg))
	}

	// Apply credentials if configured.
	var credsFilePath string // set below if credentials are written; cleaned up on subscription stop
	if spec.CredentialsSecretRef != nil {
		// Write credentials to a randomly-named temp file. Using a fixed path is a security
		// risk (predictable name, namespace collisions). The file is removed when the
		// subscription goroutine exits.
		f, err := os.CreateTemp("", "nats-creds-*.creds")
		if err != nil {
			return fmt.Errorf("creating nats credentials temp file: %w", err)
		}
		credsFilePath = f.Name()
		if _, err := f.Write([]byte(creds.credsContent)); err != nil {
			_ = f.Close()
			_ = os.Remove(credsFilePath)
			return fmt.Errorf("writing nats credentials to temp file: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(credsFilePath)
			return fmt.Errorf("closing nats credentials temp file: %w", err)
		}
		opts = append(opts, natsio.UserCredentials(credsFilePath))
	} else if spec.UsernameSecretRef != nil {
		opts = append(opts, natsio.UserInfo(creds.username, creds.password))
	}

	// Connect to NATS.
	nc, err := natsio.Connect(strings.Join(spec.Servers, ","), opts...)
	if err != nil {
		return fmt.Errorf("connect to nats for trigger %s/%s: %w", trigger.Namespace, trigger.Name, err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	isJetStream := spec.JetStream

	handler := &MessageHandler{
		client:           w.client,
		log:              w.log,
		triggerName:      trigger.Name,
		triggerNamespace: trigger.Namespace,
		flowRefName:      trigger.Spec.FlowRef.Name,
		isJetStream:      isJetStream,
	}

	var nsSub *natsio.Subscription
	if isJetStream {
		js, err := nc.JetStream()
		if err != nil {
			cancel()
			nc.Close()
			return fmt.Errorf("create jetstream context for trigger %s/%s: %w", trigger.Namespace, trigger.Name, err)
		}
		// Durable name must be <= 32 chars.
		durableName := "kubezap-" + trigger.Name
		if len(durableName) > 32 {
			durableName = durableName[:32]
		}
		nsSub, err = js.Subscribe(subject, handler.HandleMessage, natsio.Durable(durableName), natsio.AckExplicit())
		if err != nil {
			cancel()
			nc.Close()
			return fmt.Errorf("jetstream subscribe on trigger %s/%s subject %s: %w", trigger.Namespace, trigger.Name, subject, err)
		}
	} else {
		nsSub, err = nc.Subscribe(subject, handler.HandleMessage)
		if err != nil {
			cancel()
			nc.Close()
			return fmt.Errorf("subscribe on trigger %s/%s subject %s: %w", trigger.Namespace, trigger.Name, subject, err)
		}
	}

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	sub := &subscription{
		cancel:                cancel,
		integrationName:       integrationName,
		subject:               subject,
		isJetStream:           isJetStream,
		credentialFingerprint: creds.fingerprint,
	}
	w.subscriptions.Store(key, sub)

	go func() {
		w.log.Info("nats subscription started",
			"trigger", key,
			"subject", subject,
			"jetStream", isJetStream,
		)
		<-subCtx.Done()
		if err := nsSub.Unsubscribe(); err != nil {
			w.log.Error(err, "error unsubscribing from nats subject", "trigger", key, "subject", subject)
		}
		if err := nc.Drain(); err != nil {
			w.log.Error(err, "error draining nats connection", "trigger", key)
		}
		if credsFilePath != "" {
			if err := os.Remove(credsFilePath); err != nil && !os.IsNotExist(err) {
				w.log.Error(err, "failed to remove nats credentials temp file", "path", credsFilePath)
			}
		}
		w.log.Info("nats subscription stopped", "trigger", key)
	}()

	return nil
}

// stopSubscription cancels the context for a subscription and removes it from the map.
func (w *Watcher) stopSubscription(key types.NamespacedName) {
	if val, ok := w.subscriptions.LoadAndDelete(key); ok {
		sub := val.(*subscription)
		sub.cancel()
	}
}

// readSecretKey fetches a Kubernetes Secret and returns the value for the given key.
func (w *Watcher) readSecretKey(ctx context.Context, namespace, name, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret); err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", namespace, name, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s does not contain key %q", namespace, name, key)
	}
	return string(val), nil
}
