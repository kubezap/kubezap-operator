package kafka

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"hash"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// subscription tracks an active Kafka consumer group for a Trigger.
type subscription struct {
	cancel          context.CancelFunc
	integrationName string
	topic           string
	consumerGroup   string
}

// Watcher watches Trigger CRDs and manages Kafka topic subscriptions.
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

// Start begins watching Trigger CRDs and managing Kafka subscriptions.
// Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	w.log.Info("kafka watcher started", "namespace", w.namespace)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run once immediately before the first tick.
	w.reconcileTriggers(ctx)

	for {
		select {
		case <-ticker.C:
			w.reconcileTriggers(ctx)
		case <-ctx.Done():
			w.log.Info("kafka watcher stopped")
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

// reconcileTriggers lists all Kafka Triggers and reconciles subscriptions.
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
		if trigger.Spec.PubSub == nil || trigger.Spec.PubSub.Type != "kafka" {
			continue
		}
		if !trigger.Spec.Enabled {
			continue
		}

		key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
		desired[key] = true

		topic := trigger.Spec.PubSub.Topic
		cgID := trigger.Spec.PubSub.ConsumerGroup
		if cgID == "" {
			cgID = "kubezap-" + trigger.Name
		}
		integrationName := trigger.Spec.PubSub.IntegrationRef.Name

		if existing, ok := w.subscriptions.Load(key); ok {
			sub := existing.(*subscription)
			// Restart if topic, consumerGroup, or integrationRef changed.
			if sub.topic == topic && sub.consumerGroup == cgID && sub.integrationName == integrationName {
				continue
			}
			w.log.Info("kafka subscription config changed, restarting",
				"trigger", key,
				"topic", topic,
				"consumerGroup", cgID,
			)
			w.stopSubscription(key)
		}

		if err := w.startSubscription(ctx, trigger); err != nil {
			w.log.Error(err, "failed to start kafka subscription",
				"trigger", key,
				"topic", topic,
			)
		}
	}

	// Stop subscriptions for triggers that no longer exist or are disabled.
	w.subscriptions.Range(func(k, _ any) bool {
		key := k.(types.NamespacedName)
		if !desired[key] {
			w.log.Info("stopping kafka subscription, trigger removed or disabled", "trigger", key)
			w.stopSubscription(key)
		}
		return true
	})
}

// startSubscription creates a sarama consumer group for the given Trigger.
func (w *Watcher) startSubscription(ctx context.Context, trigger *automationv1alpha1.Trigger) error {
	pubsub := trigger.Spec.PubSub
	integrationName := pubsub.IntegrationRef.Name
	topic := pubsub.Topic

	cgID := pubsub.ConsumerGroup
	if cgID == "" {
		cgID = "kubezap-" + trigger.Name
	}

	// Fetch the Integration CRD.
	integration := &automationv1alpha1.Integration{}
	if err := w.client.Get(ctx, types.NamespacedName{
		Namespace: trigger.Namespace,
		Name:      integrationName,
	}, integration); err != nil {
		return fmt.Errorf("get integration %s/%s: %w", trigger.Namespace, integrationName, err)
	}

	if integration.Spec.Kafka == nil {
		return fmt.Errorf("integration %s/%s has no kafka spec", trigger.Namespace, integrationName)
	}

	kafkaSpec := integration.Spec.Kafka

	// Build sarama config.
	config := sarama.NewConfig()
	config.Version = sarama.V2_6_0_0
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}

	// Apply TLS if configured.
	if kafkaSpec.TLS != nil && kafkaSpec.TLS.Enabled {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: kafkaSpec.TLS.InsecureSkipVerify, //nolint:gosec // user-configured
		}

		if kafkaSpec.TLS.CASecretRef != nil {
			caPEM, err := w.readSecretKey(ctx, trigger.Namespace, kafkaSpec.TLS.CASecretRef.Name, kafkaSpec.TLS.CASecretRef.Key)
			if err != nil {
				return fmt.Errorf("reading CA cert secret: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(caPEM)) {
				return fmt.Errorf("failed to parse CA certificate from secret %s/%s key %s",
					trigger.Namespace, kafkaSpec.TLS.CASecretRef.Name, kafkaSpec.TLS.CASecretRef.Key)
			}
			tlsCfg.RootCAs = pool
		}

		config.Net.TLS.Enable = true
		config.Net.TLS.Config = tlsCfg
	}

	// Apply SASL if configured.
	if kafkaSpec.SASL != nil {
		saslCfg := kafkaSpec.SASL

		username, err := w.readSecretKey(ctx, trigger.Namespace,
			saslCfg.UsernameSecretRef.Name, saslCfg.UsernameSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading SASL username secret: %w", err)
		}
		password, err := w.readSecretKey(ctx, trigger.Namespace,
			saslCfg.PasswordSecretRef.Name, saslCfg.PasswordSecretRef.Key)
		if err != nil {
			return fmt.Errorf("reading SASL password secret: %w", err)
		}

		config.Net.SASL.Enable = true
		config.Net.SASL.User = username
		config.Net.SASL.Password = password

		switch saslCfg.Mechanism {
		case "PLAIN":
			config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "SCRAM-SHA-256":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &xdgSCRAMClient{HashGeneratorFcn: sha256.New}
			}
		case "SCRAM-SHA-512":
			config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
				return &xdgSCRAMClient{HashGeneratorFcn: sha512.New}
			}
		default:
			return fmt.Errorf("unsupported SASL mechanism: %s", saslCfg.Mechanism)
		}
	}

	cg, err := sarama.NewConsumerGroup(kafkaSpec.BootstrapServers, cgID, config)
	if err != nil {
		return fmt.Errorf("create consumer group for trigger %s/%s: %w", trigger.Namespace, trigger.Name, err)
	}

	subCtx, cancel := context.WithCancel(context.Background())
	sub := &subscription{
		cancel:          cancel,
		integrationName: integrationName,
		topic:           topic,
		consumerGroup:   cgID,
	}

	key := types.NamespacedName{Name: trigger.Name, Namespace: trigger.Namespace}
	w.subscriptions.Store(key, sub)

	handler := &MessageHandler{}

	go func() {
		w.log.Info("kafka subscription started",
			"trigger", key,
			"topic", topic,
			"consumerGroup", cgID,
		)
		defer func() {
			if err := cg.Close(); err != nil {
				w.log.Error(err, "error closing consumer group", "trigger", key)
			}
			w.log.Info("kafka subscription stopped", "trigger", key)
		}()

		for {
			if subCtx.Err() != nil {
				return
			}
			if err := cg.Consume(subCtx, []string{topic}, handler); err != nil {
				if subCtx.Err() != nil {
					return
				}
				w.log.Error(err, "consumer group error", "trigger", key, "topic", topic)
				// Brief backoff before retrying.
				select {
				case <-subCtx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
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

// --------------------------------------------------------------------------
// Inline SCRAM-SHA-{256,512} client (RFC 5802)
//
// Implements sarama.SCRAMClient without requiring the xdg-go/scram external
// dependency. Uses only Go standard library crypto primitives.
// --------------------------------------------------------------------------

// xdgSCRAMClient implements sarama.SCRAMClient for SCRAM-SHA-256 and SCRAM-SHA-512.
type xdgSCRAMClient struct {
	HashGeneratorFcn func() hash.Hash
	user             string
	pass             string
	clientNonce      string
	saltedPass       []byte
	authMessage      string
	step             int
}

func (x *xdgSCRAMClient) Begin(userName, password, _ string) error {
	x.user = userName
	x.pass = password
	x.step = 0

	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate client nonce: %w", err)
	}
	x.clientNonce = base64.StdEncoding.EncodeToString(nonceBytes)
	return nil
}

// Step implements the RFC 5802 SCRAM exchange.
// Step 1 (challenge ""): produces client-first message.
// Step 2 (server-first): produces client-final message.
// Step 3 (server-final): verifies server signature.
func (x *xdgSCRAMClient) Step(challenge string) (string, error) {
	x.step++
	switch x.step {
	case 1:
		return "n,,n=" + x.user + ",r=" + x.clientNonce, nil

	case 2:
		// Parse server-first: "r=<combinedNonce>,s=<salt-b64>,i=<iterations>"
		attrs := scramParseAttrs(challenge)

		combinedNonce, ok := attrs["r"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'r' in server-first message")
		}
		if !strings.HasPrefix(combinedNonce, x.clientNonce) {
			return "", fmt.Errorf("SCRAM: server nonce does not begin with client nonce")
		}

		saltB64, ok := attrs["s"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 's' in server-first message")
		}
		salt, err := base64.StdEncoding.DecodeString(saltB64)
		if err != nil {
			return "", fmt.Errorf("SCRAM: decode salt: %w", err)
		}

		iterStr, ok := attrs["i"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'i' in server-first message")
		}
		iterations, err := scramParseIter(iterStr)
		if err != nil {
			return "", fmt.Errorf("SCRAM: %w", err)
		}

		x.saltedPass = x.scramHi([]byte(x.pass), salt, iterations)

		clientKey := x.scramHMAC(x.saltedPass, []byte("Client Key"))
		storedKey := x.scramH(clientKey)

		clientFirstBare := "n=" + x.user + ",r=" + x.clientNonce
		cbind := base64.StdEncoding.EncodeToString([]byte("n,,"))
		clientFinalNP := "c=" + cbind + ",r=" + combinedNonce

		x.authMessage = clientFirstBare + "," + challenge + "," + clientFinalNP

		clientSig := x.scramHMAC(storedKey, []byte(x.authMessage))
		proof := scramXorBytes(clientKey, clientSig)

		return clientFinalNP + ",p=" + base64.StdEncoding.EncodeToString(proof), nil

	case 3:
		// Verify server-final: "v=<server-sig-b64>"
		serverKey := x.scramHMAC(x.saltedPass, []byte("Server Key"))
		expected := x.scramHMAC(serverKey, []byte(x.authMessage))

		attrs := scramParseAttrs(challenge)
		sigB64, ok := attrs["v"]
		if !ok {
			return "", fmt.Errorf("SCRAM: missing 'v' in server-final message")
		}
		received, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			return "", fmt.Errorf("SCRAM: decode server signature: %w", err)
		}
		if !bytes.Equal(expected, received) {
			return "", fmt.Errorf("SCRAM: server signature verification failed")
		}
		return "", nil

	default:
		return "", fmt.Errorf("SCRAM: unexpected step %d", x.step)
	}
}

func (x *xdgSCRAMClient) Done() bool {
	return x.step >= 3
}

// scramHi implements the PBKDF2-like Hi function from RFC 5802.
func (x *xdgSCRAMClient) scramHi(password, salt []byte, iterations int) []byte {
	mac := hmac.New(x.HashGeneratorFcn, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	result := make([]byte, len(u))
	copy(result, u)
	for i := 2; i <= iterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}

func (x *xdgSCRAMClient) scramHMAC(key, msg []byte) []byte {
	mac := hmac.New(x.HashGeneratorFcn, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

func (x *xdgSCRAMClient) scramH(data []byte) []byte {
	hh := x.HashGeneratorFcn()
	hh.Write(data)
	return hh.Sum(nil)
}

// scramParseAttrs parses comma-separated "key=value" pairs from a SCRAM message.
func scramParseAttrs(msg string) map[string]string {
	attrs := make(map[string]string)
	for _, part := range strings.Split(msg, ",") {
		if len(part) < 2 || part[1] != '=' {
			continue
		}
		attrs[part[:1]] = part[2:]
	}
	return attrs
}

// scramParseIter parses an ASCII decimal iteration count.
func scramParseIter(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid iteration count: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("iteration count must be positive, got %q", s)
	}
	return n, nil
}

func scramXorBytes(a, b []byte) []byte {
	result := make([]byte, len(a))
	for i := range a {
		result[i] = a[i] ^ b[i]
	}
	return result
}
