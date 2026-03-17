package amqp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"sync"
	"time"

	goamqp "github.com/Azure/go-amqp"
	"github.com/go-logr/logr"
	amqp091 "github.com/rabbitmq/amqp091-go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// subscription tracks an active AMQP consumer for a Trigger.
type subscription struct {
	cancel          context.CancelFunc
	integrationName string
	topic           string
	routingKey      string
}

// Watcher watches Trigger CRDs and manages AMQP topic subscriptions.
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

// Start begins watching Trigger CRDs and managing AMQP subscriptions.
// Blocks until ctx is cancelled.
//
// TODO(HIGH): switch to informer-based watch to avoid 30s reaction delay — see tech debt backlog
func (w *Watcher) Start(ctx context.Context) error {
	w.log.Info("amqp watcher started", "namespace", w.namespace)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run once immediately before the first tick.
	w.reconcileTriggers(ctx)

	for {
		select {
		case <-ticker.C:
			w.reconcileTriggers(ctx)
		case <-ctx.Done():
			w.log.Info("amqp watcher stopped")
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

// reconcileTriggers lists all AMQP Triggers and reconciles subscriptions.
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
		if trigger.Spec.PubSub == nil || trigger.Spec.PubSub.Type != "amqp" {
			continue
		}
		if !trigger.Spec.Enabled {
			continue
		}

		key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
		desired[key] = true

		topic := trigger.Spec.PubSub.Topic
		routingKey := trigger.Spec.PubSub.RoutingKey
		integrationName := trigger.Spec.PubSub.IntegrationRef.Name

		if existing, ok := w.subscriptions.Load(key); ok {
			sub := existing.(*subscription)
			// Restart if topic, routingKey, or integrationRef changed.
			if sub.topic == topic && sub.routingKey == routingKey && sub.integrationName == integrationName {
				continue
			}
			w.log.Info("amqp subscription config changed, restarting",
				"trigger", key,
				"topic", topic,
				"routingKey", routingKey,
			)
			w.stopSubscription(key)
		}

		if err := w.startSubscription(ctx, trigger); err != nil {
			w.log.Error(err, "failed to start amqp subscription",
				"trigger", key,
				"topic", topic,
			)
		}
	}

	// Stop subscriptions for triggers that no longer exist or are disabled.
	w.subscriptions.Range(func(k, _ any) bool {
		key := k.(types.NamespacedName)
		if !desired[key] {
			w.log.Info("stopping amqp subscription, trigger removed or disabled", "trigger", key)
			w.stopSubscription(key)
		}
		return true
	})
}

// startSubscription creates an AMQP consumer for the given Trigger.
func (w *Watcher) startSubscription(ctx context.Context, trigger *automationv1alpha1.Trigger) error {
	pubsub := trigger.Spec.PubSub
	integrationName := pubsub.IntegrationRef.Name
	topic := pubsub.Topic
	routingKey := pubsub.RoutingKey

	// Fetch the Integration CRD.
	integration := &automationv1alpha1.Integration{}
	if err := w.client.Get(ctx, types.NamespacedName{
		Namespace: trigger.Namespace,
		Name:      integrationName,
	}, integration); err != nil {
		return fmt.Errorf("get integration %s/%s: %w", trigger.Namespace, integrationName, err)
	}

	if integration.Spec.Amqp == nil {
		return fmt.Errorf("integration %s/%s has no amqp spec", trigger.Namespace, integrationName)
	}

	amqpSpec := integration.Spec.Amqp

	subCtx, cancel := context.WithCancel(context.Background())
	sub := &subscription{
		cancel:          cancel,
		integrationName: integrationName,
		topic:           topic,
		routingKey:      routingKey,
	}

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.subscriptions.Store(key, sub)

	flowRefName := ""
	if trigger.Spec.FlowRef != nil {
		flowRefName = trigger.Spec.FlowRef.Name
	}

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
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("opening amqp channel: %w", err)
	}
	defer ch.Close()

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
	defer conn.Close()

	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		return fmt.Errorf("creating amqp 1.0 session: %w", err)
	}

	receiver, err := session.NewReceiver(ctx, topic, nil)
	if err != nil {
		return fmt.Errorf("creating amqp 1.0 receiver for %q: %w", topic, err)
	}
	defer receiver.Close(ctx)

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
			rest := strings.TrimPrefix(rawURL, scheme)
			return scheme + username + ":" + password + "@" + rest
		}
	}
	return rawURL
}
