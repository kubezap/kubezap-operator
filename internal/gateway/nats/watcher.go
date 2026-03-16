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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// subscription tracks an active NATS subscription for a Trigger.
type subscription struct {
	cancel          context.CancelFunc
	integrationName string
	subject         string
	isJetStream     bool
}

// Watcher watches Trigger CRDs and manages NATS subscriptions.
type Watcher struct {
	client        client.Client
	namespace     string
	log           logr.Logger
	subscriptions sync.Map // key: types.NamespacedName, value: *subscription
}

// NewWatcher creates a new Watcher.
func NewWatcher(c client.Client, namespace string, log logr.Logger) *Watcher {
	return &Watcher{
		client:    c,
		namespace: namespace,
		log:       log,
	}
}

// Start begins watching Trigger CRDs and managing NATS subscriptions.
// Blocks until ctx is cancelled.
// TODO(HIGH): switch to informer-based watch to avoid 30s reaction delay — see tech debt backlog
func (w *Watcher) Start(ctx context.Context) error {
	w.log.Info("nats watcher started", "namespace", w.namespace)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run once immediately before the first tick.
	w.reconcileTriggers(ctx)

	for {
		select {
		case <-ticker.C:
			w.reconcileTriggers(ctx)
		case <-ctx.Done():
			w.log.Info("nats watcher stopped")
			// Cancel all active subscriptions.
			w.subscriptions.Range(func(key, value any) bool {
				sub := value.(*subscription)
				sub.cancel()
				w.subscriptions.Delete(key)
				return true
			})
			return ctx.Err()
		}
	}
}

// reconcileTriggers lists all NATS Triggers and reconciles subscriptions.
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

	// Build a set of desired trigger keys for cleanup later.
	desired := make(map[types.NamespacedName]bool)

	for i := range list.Items {
		trigger := &list.Items[i]
		if trigger.Spec.Type != "pubsub" {
			continue
		}
		if trigger.Spec.PubSub == nil || trigger.Spec.PubSub.Type != "nats" {
			continue
		}
		if !trigger.Spec.Enabled {
			continue
		}

		key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
		desired[key] = true

		subject := trigger.Spec.PubSub.Subject
		if subject == "" {
			subject = trigger.Spec.PubSub.Topic
		}
		integrationName := trigger.Spec.PubSub.IntegrationRef.Name

		if existing, ok := w.subscriptions.Load(key); ok {
			sub := existing.(*subscription)
			// Restart if subject or integrationRef changed.
			if sub.subject == subject && sub.integrationName == integrationName {
				continue
			}
			w.log.Info("nats subscription config changed, restarting",
				"trigger", key,
				"subject", subject,
			)
			w.stopSubscription(key)
		}

		if err := w.startSubscription(ctx, trigger); err != nil {
			w.log.Error(err, "failed to start nats subscription",
				"trigger", key,
				"subject", subject,
			)
		}
	}

	// Stop subscriptions for triggers that no longer exist or are disabled.
	w.subscriptions.Range(func(k, _ any) bool {
		key := k.(types.NamespacedName)
		if !desired[key] {
			w.log.Info("stopping nats subscription, trigger removed or disabled", "trigger", key)
			w.stopSubscription(key)
		}
		return true
	})
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
	if spec.CredentialsSecretRef != nil {
		credsContent, err := w.readSecretKey(ctx, trigger.Namespace, spec.CredentialsSecretRef.Name, "nats.creds")
		if err != nil {
			return fmt.Errorf("reading nats credentials secret: %w", err)
		}
		// Write credentials to a temp file as natsio.UserCredentials requires a file path.
		credsFile := fmt.Sprintf("/tmp/nats-creds-%s.creds", trigger.Name)
		if err := os.WriteFile(credsFile, []byte(credsContent), 0600); err != nil {
			return fmt.Errorf("writing nats credentials to temp file: %w", err)
		}
		opts = append(opts, natsio.UserCredentials(credsFile))
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

	subCtx, cancel := context.WithCancel(context.Background())
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
