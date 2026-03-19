package natsgateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"sigs.k8s.io/controller-runtime/pkg/cache"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

var controllerScheme = runtime.NewScheme()

func init() {
	_ = automationv1alpha1.AddToScheme(controllerScheme)
}

// subscription tracks an active NATS subscription for a Trigger.
type subscription struct {
	cancel          context.CancelFunc
	integrationName string
	subject         string
	isJetStream     bool
}

// Watcher watches Trigger CRDs via an informer and manages NATS subscriptions.
type Watcher struct {
	client        client.Client
	cache         crcache.Cache
	namespace     string
	log           logr.Logger
	subscriptions sync.Map // key: types.NamespacedName, value: *subscription
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

	cacheOpts := cache.Options{Scheme: controllerScheme, Mapper: mapper}
	if namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]crcache.Config{namespace: {}}
	}

	watchCache, err := crcache.New(cfg, cacheOpts)
	if err != nil {
		return nil, fmt.Errorf("unable to create cache: %w", err)
	}

	return &Watcher{client: c, cache: watchCache, namespace: namespace, log: log}, nil
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
	w.stopSubscription(key)
}

// reconcileTrigger ensures the subscription state for a single Trigger matches its spec.
func (w *Watcher) reconcileTrigger(ctx context.Context, trigger *automationv1alpha1.Trigger) {
	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}

	// Stop subscription if trigger is not a nats pubsub trigger or is disabled.
	if trigger.Spec.Type != "pubsub" || trigger.Spec.PubSub == nil || trigger.Spec.PubSub.Type != "nats" || !trigger.Spec.Enabled {
		w.stopSubscription(key)
		return
	}

	subject := trigger.Spec.PubSub.Subject
	if subject == "" {
		subject = trigger.Spec.PubSub.Topic
	}
	integrationName := trigger.Spec.PubSub.IntegrationRef.Name

	if existing, ok := w.subscriptions.Load(key); ok {
		sub := existing.(*subscription)
		if sub.subject == subject && sub.integrationName == integrationName {
			return // no change
		}
		w.log.Info("nats subscription config changed, restarting",
			"trigger", key, "subject", subject)
		w.stopSubscription(key)
	}

	if err := w.startSubscription(ctx, trigger); err != nil {
		w.log.Error(err, "failed to start nats subscription", "trigger", key, "subject", subject)
	}
}

// startSubscription creates a NATS subscription for the given Trigger.
func (w *Watcher) startSubscription(ctx context.Context, trigger *automationv1alpha1.Trigger) error {
	pubsub := trigger.Spec.PubSub
	integrationName := pubsub.IntegrationRef.Name

	// Determine subject: prefer explicit Subject field, fall back to Topic.
	subject := pubsub.Subject
	if subject == "" {
		subject = pubsub.Topic
	}

	// Fetch the Integration CRD.
	integration := &automationv1alpha1.Integration{}
	if err := w.client.Get(ctx, types.NamespacedName{
		Namespace: trigger.Namespace,
		Name:      integrationName,
	}, integration); err != nil {
		return fmt.Errorf("get integration %s/%s: %w", trigger.Namespace, integrationName, err)
	}

	if integration.Spec.Nats == nil {
		return fmt.Errorf("integration %s/%s has no nats spec", trigger.Namespace, integrationName)
	}

	spec := integration.Spec.Nats

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
			caPEM, err := w.readSecretKey(ctx, trigger.Namespace, spec.TLS.CASecretRef.Name, spec.TLS.CASecretRef.Key)
			if err != nil {
				return fmt.Errorf("reading CA cert secret: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(caPEM)) {
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
		credsContent, err := w.readSecretKey(ctx, trigger.Namespace, spec.CredentialsSecretRef.Name, "nats.creds")
		if err != nil {
			return fmt.Errorf("reading nats credentials secret: %w", err)
		}
		// Write credentials to a randomly-named temp file. Using a fixed path is a security
		// risk (predictable name, namespace collisions). The file is removed when the
		// subscription goroutine exits.
		f, err := os.CreateTemp("", "nats-creds-*.creds")
		if err != nil {
			return fmt.Errorf("creating nats credentials temp file: %w", err)
		}
		credsFilePath = f.Name()
		if _, err := f.Write([]byte(credsContent)); err != nil {
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
		username, err := w.readSecretKey(ctx, trigger.Namespace, spec.UsernameSecretRef.Name, spec.UsernameSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading nats username secret: %w", err)
		}
		if spec.PasswordSecretRef == nil {
			return fmt.Errorf("spec.nats.passwordSecretRef must be set when usernameSecretRef is set")
		}
		password, err := w.readSecretKey(ctx, trigger.Namespace, spec.PasswordSecretRef.Name, spec.PasswordSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading nats password secret: %w", err)
		}
		opts = append(opts, natsio.UserInfo(username, password))
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
		flowRefName: func() string {
			if trigger.Spec.FlowRef != nil {
				return trigger.Spec.FlowRef.Name
			}
			return ""
		}(),
		isJetStream: isJetStream,
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
		cancel:          cancel,
		integrationName: integrationName,
		subject:         subject,
		isJetStream:     isJetStream,
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
