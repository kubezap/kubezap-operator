package amqp

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	goamqp "github.com/Azure/go-amqp"
	"github.com/go-logr/logr"
	amqp091 "github.com/rabbitmq/amqp091-go"
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

// subscription tracks an active AMQP consumer for a Trigger.
type subscription struct {
	cancel                context.CancelFunc
	integrationName       string
	topic                 string
	routingKey            string
	credentialFingerprint string
}

// Watcher watches Trigger CRDs via an informer and manages AMQP topic subscriptions.
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
	// depend on it (via their Integration's username/password/CA secretRefs).
	secretIndex *secretindex.Index
	triggersMu  sync.Mutex
	triggers    map[types.NamespacedName]*automationv1alpha1.Trigger

	// allNamespacesMode is true when the operator (and therefore this gateway,
	// which inherits WATCH_NAMESPACES from it) is running with WATCH_NAMESPACES=*.
	// When true, readSecretKey additionally requires the Secret's namespace to
	// carry kubezap.io/managed=true. See
	// docs/design/allnamespaces-secrets-label-restriction.md.
	allNamespacesMode bool
}

// NewWatcher creates a new Watcher backed by an informer cache.
func NewWatcher(c client.Client, cfg *rest.Config, namespace string, log logr.Logger, allNamespacesMode bool) (*Watcher, error) {
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
		client:            c,
		cache:             watchCache,
		namespace:         namespace,
		log:               log,
		secretIndex:       secretindex.New(),
		triggers:          make(map[types.NamespacedName]*automationv1alpha1.Trigger),
		allNamespacesMode: allNamespacesMode,
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

	go func() {
		if err := w.cache.Start(ctx); err != nil && err != context.Canceled {
			w.log.Error(err, "trigger cache stopped with error")
		}
	}()

	if !w.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("timed out waiting for initial cache sync")
	}
	w.log.Info("amqp watcher started", "namespace", w.namespace)

	<-ctx.Done()
	w.log.Info("amqp watcher stopped")

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
	w.stopSubscription(key)
}

// reconcileTrigger ensures the subscription state for a single Trigger matches its spec.
func (w *Watcher) reconcileTrigger(ctx context.Context, trigger *automationv1alpha1.Trigger) {
	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.triggersMu.Lock()
	w.triggers[key] = trigger
	w.triggersMu.Unlock()

	// Stop subscription if trigger is not an amqp trigger or is disabled.
	if trigger.Spec.Type != triggerTypeAMQP || trigger.Spec.Amqp == nil || !trigger.Spec.Enabled {
		w.secretIndex.Remove(key)
		w.stopSubscription(key)
		return
	}

	topic := trigger.Spec.Amqp.Topic
	routingKey := trigger.Spec.Amqp.RoutingKey
	integrationName := trigger.Spec.Amqp.IntegrationRef.Name

	integration := &automationv1alpha1.Integration{}
	if err := w.client.Get(ctx, types.NamespacedName{Namespace: trigger.Namespace, Name: integrationName}, integration); err != nil {
		w.log.Error(err, "failed to get integration for amqp trigger", "trigger", key, "integration", integrationName)
		return
	}
	if integration.Spec.Amqp == nil {
		w.log.Error(fmt.Errorf("integration has no amqp spec"), "cannot reconcile amqp trigger", "trigger", key, "integration", integrationName)
		return
	}

	creds, credErr := w.resolveAmqpCredentials(ctx, trigger.Namespace, integration.Spec.Amqp)
	// Index intended secret refs regardless of read success, so a Trigger whose
	// secret doesn't exist yet still gets reprocessed once it's created.
	w.secretIndex.Update(key, creds.secretRefs)
	if credErr != nil {
		w.log.Error(credErr, "failed to resolve amqp credentials", "trigger", key, "integration", integrationName)
		return
	}

	if existing, ok := w.subscriptions.Load(key); ok {
		sub := existing.(*subscription)
		if sub.topic == topic && sub.routingKey == routingKey && sub.integrationName == integrationName && sub.credentialFingerprint == creds.fingerprint {
			return // no change, not even to credentials
		}
		w.log.Info("amqp subscription config changed, restarting",
			"trigger", key, "topic", topic, "routingKey", routingKey)
		w.stopSubscription(key)
	}

	if err := w.startSubscription(ctx, trigger, integration.Spec.Amqp, creds.fingerprint); err != nil {
		w.log.Error(err, "failed to start amqp subscription", "trigger", key, "topic", topic)
	}
}

// amqpCredentials holds bookkeeping for change detection (fingerprint) and
// secret-rotation reprocessing (secretRefs) for an AMQP Integration's
// username/password/CA secrets. The actual values are re-read independently
// by connect091/connect10 — this is deliberately not plumbed through to avoid
// touching the existing connection-establishment code paths.
type amqpCredentials struct {
	secretRefs  []types.NamespacedName
	fingerprint string
}

// resolveAmqpCredentials reads whichever username/password/CA secrets amqpSpec
// references and computes a fingerprint over their values, so callers can
// detect a credential-only rotation without comparing full secret contents.
func (w *Watcher) resolveAmqpCredentials(ctx context.Context, namespace string, amqpSpec *automationv1alpha1.AmqpIntegrationSpec) (amqpCredentials, error) {
	var creds amqpCredentials
	h := sha256.New()

	if amqpSpec.UsernameSecretRef != nil {
		username, err := w.readSecretKey(ctx, namespace, amqpSpec.UsernameSecretRef.Name, amqpSpec.UsernameSecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading username secret: %w", err)
		}
		creds.secretRefs = append(creds.secretRefs, types.NamespacedName{Namespace: namespace, Name: amqpSpec.UsernameSecretRef.Name})
		_, _ = h.Write([]byte(username))
	}
	if amqpSpec.PasswordSecretRef != nil {
		password, err := w.readSecretKey(ctx, namespace, amqpSpec.PasswordSecretRef.Name, amqpSpec.PasswordSecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading password secret: %w", err)
		}
		creds.secretRefs = append(creds.secretRefs, types.NamespacedName{Namespace: namespace, Name: amqpSpec.PasswordSecretRef.Name})
		_, _ = h.Write([]byte(password))
	}
	if amqpSpec.TLS != nil && amqpSpec.TLS.CASecretRef != nil {
		caPEM, err := w.readSecretKey(ctx, namespace, amqpSpec.TLS.CASecretRef.Name, amqpSpec.TLS.CASecretRef.Key)
		if err != nil {
			return creds, fmt.Errorf("reading CA cert secret: %w", err)
		}
		creds.secretRefs = append(creds.secretRefs, types.NamespacedName{Namespace: namespace, Name: amqpSpec.TLS.CASecretRef.Name})
		_, _ = h.Write([]byte(caPEM))
	}

	creds.fingerprint = hex.EncodeToString(h.Sum(nil))
	return creds, nil
}

// handleSecretChange reprocesses every Trigger currently known to depend on
// the changed Secret (via its Integration's username/password/CA config),
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

// startSubscription creates an AMQP consumer for the given Trigger, using an
// already-resolved credential fingerprint (see reconcileTrigger).
func (w *Watcher) startSubscription(ctx context.Context, trigger *automationv1alpha1.Trigger, amqpSpec *automationv1alpha1.AmqpIntegrationSpec, credentialFingerprint string) error {
	amqp := trigger.Spec.Amqp
	integrationName := amqp.IntegrationRef.Name
	topic := amqp.Topic
	routingKey := amqp.RoutingKey

	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		cancel:                cancel,
		integrationName:       integrationName,
		topic:                 topic,
		routingKey:            routingKey,
		credentialFingerprint: credentialFingerprint,
	}

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.subscriptions.Store(key, sub)

	flowRefName := trigger.Spec.FlowRef.Name

	version := amqpSpec.Version
	if version == "" {
		version = "0-9-1"
	}

	switch version {
	case "0-9-1":
		go w.runSubscription091(subCtx, key, trigger, amqpSpec, topic, flowRefName)
	case "1.0":
		go w.runSubscription10(subCtx, key, trigger, amqpSpec, topic, flowRefName)
	default:
		cancel()
		w.subscriptions.Delete(key)
		return fmt.Errorf("unsupported amqp version %q for trigger %s/%s", version, trigger.Namespace, trigger.Name)
	}

	return nil
}

// runSubscription091 runs an AMQP 0-9-1 subscription with exponential backoff retry.
func (w *Watcher) runSubscription091(
	ctx context.Context,
	key types.NamespacedName,
	trigger *automationv1alpha1.Trigger,
	amqpSpec *automationv1alpha1.AmqpIntegrationSpec,
	topic string,
	flowRefName string,
) {
	w.log.Info("amqp 0-9-1 subscription started", "trigger", key, "topic", topic)
	defer w.log.Info("amqp 0-9-1 subscription stopped", "trigger", key)

	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		if err := w.connect091(ctx, key, trigger, amqpSpec, topic, flowRefName); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Error(err, "amqp 0-9-1 connection error, retrying", "trigger", key, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			// Reset backoff on clean exit.
			backoff = 5 * time.Second
		}
	}
}

// connect091 establishes one AMQP 0-9-1 connection and runs the message loop until it closes.
func (w *Watcher) connect091(
	ctx context.Context,
	key types.NamespacedName,
	trigger *automationv1alpha1.Trigger,
	amqpSpec *automationv1alpha1.AmqpIntegrationSpec,
	topic string,
	flowRefName string,
) error {
	url := amqpSpec.URL

	// Build auth URL if credentials are configured.
	if amqpSpec.UsernameSecretRef != nil {
		username, err := w.readSecretKey(ctx, trigger.Namespace, amqpSpec.UsernameSecretRef.Name, amqpSpec.UsernameSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading username secret: %w", err)
		}
		password := ""
		if amqpSpec.PasswordSecretRef != nil {
			password, err = w.readSecretKey(ctx, trigger.Namespace, amqpSpec.PasswordSecretRef.Name, amqpSpec.PasswordSecretRef.Key)
			if err != nil {
				return fmt.Errorf("reading password secret: %w", err)
			}
		}
		// Embed credentials into URL: amqp://user:pass@host/vhost
		url = embedCredsInURL(url, username, password)
	}

	var conn *amqp091.Connection
	var err error

	needsTLS := (amqpSpec.TLS != nil && amqpSpec.TLS.Enabled) || strings.HasPrefix(url, "amqps://")
	if needsTLS {
		tlsCfg, tlsErr := buildTLSConfig091(ctx, w, trigger.Namespace, amqpSpec)
		if tlsErr != nil {
			return fmt.Errorf("building TLS config: %w", tlsErr)
		}
		conn, err = amqp091.DialTLS(url, tlsCfg)
	} else {
		conn, err = amqp091.Dial(url)
	}
	if err != nil {
		return fmt.Errorf("dialing amqp broker: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("opening amqp channel: %w", err)
	}
	defer func() { _ = ch.Close() }()

	// Declare the queue (idempotent).
	_, err = ch.QueueDeclare(
		topic, // name
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("declaring queue %q: %w", topic, err)
	}

	deliveries, err := ch.Consume(
		topic,                   // queue
		"kubezap-"+trigger.Name, // consumer tag
		false,                   // auto-ack (false: manual ack after FlowRun creation)
		false,                   // exclusive
		false,                   // no-local
		false,                   // no-wait
		nil,                     // args
	)
	if err != nil {
		return fmt.Errorf("starting consume on queue %q: %w", topic, err)
	}

	handler := &MessageHandler091{
		client:           w.client,
		log:              w.log,
		triggerName:      trigger.Name,
		triggerNamespace: trigger.Namespace,
		flowRefName:      flowRefName,
	}

	w.log.Info("amqp 0-9-1 consuming", "trigger", key, "topic", topic)
	handler.Run(ctx, deliveries)
	return nil
}

// runSubscription10 runs an AMQP 1.0 subscription with exponential backoff retry.
func (w *Watcher) runSubscription10(
	ctx context.Context,
	key types.NamespacedName,
	trigger *automationv1alpha1.Trigger,
	amqpSpec *automationv1alpha1.AmqpIntegrationSpec,
	topic string,
	flowRefName string,
) {
	w.log.Info("amqp 1.0 subscription started", "trigger", key, "topic", topic)
	defer w.log.Info("amqp 1.0 subscription stopped", "trigger", key)

	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		if err := w.connect10(ctx, key, trigger, amqpSpec, topic, flowRefName); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Error(err, "amqp 1.0 connection error, retrying", "trigger", key, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = 5 * time.Second
		}
	}
}

// connect10 establishes one AMQP 1.0 connection and runs the message loop.
func (w *Watcher) connect10(
	ctx context.Context,
	key types.NamespacedName,
	trigger *automationv1alpha1.Trigger,
	amqpSpec *automationv1alpha1.AmqpIntegrationSpec,
	topic string,
	flowRefName string,
) error {
	connOpts := &goamqp.ConnOptions{}

	needsTLS := (amqpSpec.TLS != nil && amqpSpec.TLS.Enabled) || strings.HasPrefix(amqpSpec.URL, "amqps://")
	if needsTLS {
		tlsCfg, err := buildTLSConfig10(ctx, w, trigger.Namespace, amqpSpec)
		if err != nil {
			return fmt.Errorf("building TLS config: %w", err)
		}
		connOpts.TLSConfig = tlsCfg
	}

	if amqpSpec.UsernameSecretRef != nil {
		username, err := w.readSecretKey(ctx, trigger.Namespace, amqpSpec.UsernameSecretRef.Name, amqpSpec.UsernameSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading username secret: %w", err)
		}
		password := ""
		if amqpSpec.PasswordSecretRef != nil {
			password, err = w.readSecretKey(ctx, trigger.Namespace, amqpSpec.PasswordSecretRef.Name, amqpSpec.PasswordSecretRef.Key)
			if err != nil {
				return fmt.Errorf("reading password secret: %w", err)
			}
		}
		connOpts.SASLType = goamqp.SASLTypePlain(username, password)
	}

	conn, err := goamqp.Dial(ctx, amqpSpec.URL, connOpts)
	if err != nil {
		return fmt.Errorf("dialing amqp 1.0 broker: %w", err)
	}
	defer func() { _ = conn.Close() }()

	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		return fmt.Errorf("creating amqp 1.0 session: %w", err)
	}

	receiver, err := session.NewReceiver(ctx, topic, nil)
	if err != nil {
		return fmt.Errorf("creating amqp 1.0 receiver for %q: %w", topic, err)
	}
	defer func() { _ = receiver.Close(ctx) }()

	handler := &MessageHandler10{
		client:           w.client,
		log:              w.log,
		triggerName:      trigger.Name,
		triggerNamespace: trigger.Namespace,
		flowRefName:      flowRefName,
	}

	w.log.Info("amqp 1.0 consuming", "trigger", key, "topic", topic)
	handler.Run(ctx, receiver)
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
// managedNamespaceLabel/managedNamespaceValue gate secret access in
// AllNamespaces mode. See docs/design/allnamespaces-secrets-label-restriction.md.
// Duplicated per-binary rather than shared: see that record's Decision section.
const (
	managedNamespaceLabel = "kubezap.io/managed"
	managedNamespaceValue = "true"
)

func (w *Watcher) readSecretKey(ctx context.Context, namespace, name, key string) (string, error) {
	if w.allNamespacesMode {
		var ns corev1.Namespace
		if err := w.client.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
			return "", fmt.Errorf("checking namespace %q for secret access: %w", namespace, err)
		}
		if ns.Labels[managedNamespaceLabel] != managedNamespaceValue {
			return "", fmt.Errorf("namespace %q is not labeled %s=%s — secrets cannot be read here in AllNamespaces mode",
				namespace, managedNamespaceLabel, managedNamespaceValue)
		}
	}
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

// buildTLSConfig091 constructs a tls.Config for AMQP 0-9-1 connections.
func buildTLSConfig091(ctx context.Context, w *Watcher, namespace string, amqpSpec *automationv1alpha1.AmqpIntegrationSpec) (*tls.Config, error) {
	tlsCfg := &tls.Config{} //nolint:gosec // InsecureSkipVerify set below from user config
	if amqpSpec.TLS != nil {
		tlsCfg.InsecureSkipVerify = amqpSpec.TLS.InsecureSkipVerify //nolint:gosec // user-configured
		if amqpSpec.TLS.CASecretRef != nil {
			caPEM, err := w.readSecretKey(ctx, namespace, amqpSpec.TLS.CASecretRef.Name, amqpSpec.TLS.CASecretRef.Key)
			if err != nil {
				return nil, fmt.Errorf("reading CA cert secret: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(caPEM)) {
				return nil, fmt.Errorf("failed to parse CA certificate from secret %s/%s key %s",
					namespace, amqpSpec.TLS.CASecretRef.Name, amqpSpec.TLS.CASecretRef.Key)
			}
			tlsCfg.RootCAs = pool
		}
	}
	return tlsCfg, nil
}

// buildTLSConfig10 constructs a tls.Config for AMQP 1.0 connections.
func buildTLSConfig10(ctx context.Context, w *Watcher, namespace string, amqpSpec *automationv1alpha1.AmqpIntegrationSpec) (*tls.Config, error) {
	tlsCfg := &tls.Config{} //nolint:gosec // InsecureSkipVerify set below from user config
	if amqpSpec.TLS != nil {
		tlsCfg.InsecureSkipVerify = amqpSpec.TLS.InsecureSkipVerify //nolint:gosec // user-configured
		if amqpSpec.TLS.CASecretRef != nil {
			caPEM, err := w.readSecretKey(ctx, namespace, amqpSpec.TLS.CASecretRef.Name, amqpSpec.TLS.CASecretRef.Key)
			if err != nil {
				return nil, fmt.Errorf("reading CA cert secret: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(caPEM)) {
				return nil, fmt.Errorf("failed to parse CA certificate from secret %s/%s key %s",
					namespace, amqpSpec.TLS.CASecretRef.Name, amqpSpec.TLS.CASecretRef.Key)
			}
			tlsCfg.RootCAs = pool
		}
	}
	return tlsCfg, nil
}

// embedCredsInURL inserts username:password into an AMQP URL.
// e.g. amqp://host/vhost -> amqp://user:pass@host/vhost
func embedCredsInURL(rawURL, username, password string) string {
	for _, scheme := range []string{"amqps://", "amqp://"} {
		if strings.HasPrefix(rawURL, scheme) {
			hostAndPath := strings.TrimPrefix(rawURL, scheme)
			return scheme + username + ":" + password + "@" + hostAndPath
		}
	}
	return rawURL
}
