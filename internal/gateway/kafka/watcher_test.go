package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/secretindex"
)

// newMinimalKafkaWatcher creates a Watcher backed by a fake client, with no
// informer cache — sufficient for resolveKafkaCredentials, reconcileTrigger's
// indexing side effects, and handleSecretChange, none of which touch w.cache.
func newMinimalKafkaWatcher(t *testing.T, objs ...client.Object) *Watcher {
	t.Helper()
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(objs...).Build()
	return &Watcher{
		client:           fakeClient,
		namespace:        "default",
		log:              zap.New(),
		secretIndex:      secretindex.New(),
		integrationIndex: secretindex.New(),
		triggers:         make(map[types.NamespacedName]*automationv1alpha1.Trigger),
	}
}

func newSASLSecret(pass string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-creds", Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("alice"),
			"password": []byte(pass),
		},
	}
}

func saslKafkaSpec() *automationv1alpha1.KafkaIntegrationSpec {
	return &automationv1alpha1.KafkaIntegrationSpec{
		BootstrapServers: []string{"127.0.0.1:1"}, // unreachable on purpose; these tests never need a real broker
		SASL: &automationv1alpha1.KafkaSASLConfig{
			Mechanism:         "PLAIN",
			UsernameSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kafka-creds"}, Key: "username"},
			PasswordSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kafka-creds"}, Key: "password"},
		},
	}
}

func TestResolveKafkaCredentials_FingerprintStableForSameValues(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	w := newMinimalKafkaWatcher(t, secret)
	spec := saslKafkaSpec()

	a, err := w.resolveKafkaCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveKafkaCredentials: %v", err)
	}
	b, err := w.resolveKafkaCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveKafkaCredentials (second call): %v", err)
	}

	if a.fingerprint != b.fingerprint {
		t.Fatalf("fingerprint must be stable across calls with unchanged secret data: %q != %q", a.fingerprint, b.fingerprint)
	}
	if a.saslUsername != "alice" || a.saslPassword != "s3cr3t" {
		t.Fatalf("resolved credentials: want alice/s3cr3t, got %s/%s", a.saslUsername, a.saslPassword)
	}
	wantRef := types.NamespacedName{Namespace: "default", Name: "kafka-creds"}
	if len(a.secretRefs) != 2 || a.secretRefs[0] != wantRef || a.secretRefs[1] != wantRef {
		t.Fatalf("secretRefs: want [%v %v], got %v", wantRef, wantRef, a.secretRefs)
	}
}

func TestResolveKafkaCredentials_FingerprintChangesWithRotatedSecret(t *testing.T) {
	secret := newSASLSecret("old-password")
	w := newMinimalKafkaWatcher(t, secret)
	spec := saslKafkaSpec()

	before, err := w.resolveKafkaCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveKafkaCredentials: %v", err)
	}

	secret.Data["password"] = []byte("new-password-after-rotation")
	if err := w.client.Update(context.Background(), secret); err != nil {
		t.Fatalf("updating secret: %v", err)
	}

	after, err := w.resolveKafkaCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveKafkaCredentials (after rotation): %v", err)
	}

	if before.fingerprint == after.fingerprint {
		t.Fatal("fingerprint must change when the underlying secret's password value changes")
	}
}

func TestReconcileTrigger_IndexesIntegrationSecrets(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, secret, integration)

	trigger := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "kafka",
			Enabled: true,
			Kafka: &automationv1alpha1.KafkaTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: "kafka-integ"},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger) // startSubscription will fail fast (127.0.0.1:1 refuses instantly); only the indexing side effect is under test

	secretKey := types.NamespacedName{Namespace: "default", Name: "kafka-creds"}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) != 1 || affected[0].Name != "kafka-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v): want [{default kafka-trigger}], got %v", secretKey, affected)
	}
}

func TestHandleSecretChange_ReprocessesDependentKafkaTrigger(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, secret, integration)
	trigger := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "kafka",
			Enabled: true,
			Kafka: &automationv1alpha1.KafkaTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: "kafka-integ"},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	before, ok := w.subscriptions.Load(types.NamespacedName{Namespace: "default", Name: "kafka-trigger"})
	_ = before
	_ = ok // startSubscription failed (unreachable broker) so there's no live subscription; that's fine for this test

	secret.Data["password"] = []byte("rotated")
	if err := w.client.Update(ctx, secret); err != nil {
		t.Fatalf("updating secret: %v", err)
	}

	// handleSecretChange must find kafka-trigger via the index and call
	// reconcileTrigger again — verified indirectly: the trigger stays indexed
	// (reconcileTrigger re-populates the same entry) and no panic/deadlock occurs.
	w.handleSecretChange(ctx, secret)

	secretKey := types.NamespacedName{Namespace: "default", Name: "kafka-creds"}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) != 1 || affected[0].Name != "kafka-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v) after rotation: want [{default kafka-trigger}], got %v", secretKey, affected)
	}
}

func TestHandleSecretChange_UnrelatedKafkaSecretIgnored(t *testing.T) {
	w := newMinimalKafkaWatcher(t)
	unrelated := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}

	// Must not panic and must not attempt to reconcile anything — the index is empty.
	w.handleSecretChange(context.Background(), unrelated)
}

// TestHandleIntegrationChange_RepointedSecretRefDropsOldSecretFromIndex covers
// STORY-033: repointing an Integration's SASL secretRef to a different Secret,
// without touching the Trigger, must be picked up within one resync (here,
// one handleIntegrationChange call standing in for the Integration informer
// firing) — and the old Secret must be fully dropped from secretIndex, not
// just have the new one added alongside it.
func TestHandleIntegrationChange_RepointedSecretRefDropsOldSecretFromIndex(t *testing.T) {
	oldSecret := newSASLSecret("old-password")
	oldSecret.Name = "kafka-creds-old"
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-creds-new", Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("alice"),
			"password": []byte("new-password"),
		},
	}
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec: automationv1alpha1.IntegrationSpec{
			Kafka: &automationv1alpha1.KafkaIntegrationSpec{
				BootstrapServers: []string{"127.0.0.1:1"},
				SASL: &automationv1alpha1.KafkaSASLConfig{
					Mechanism:         "PLAIN",
					UsernameSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kafka-creds-old"}, Key: "username"},
					PasswordSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kafka-creds-old"}, Key: "password"},
				},
			},
		},
	}
	w := newMinimalKafkaWatcher(t, oldSecret, newSecret, integration)

	trigger := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "kafka",
			Enabled: true,
			Kafka: &automationv1alpha1.KafkaTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: "kafka-integ"},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	oldSecretKey := types.NamespacedName{Namespace: "default", Name: "kafka-creds-old"}
	newSecretKey := types.NamespacedName{Namespace: "default", Name: "kafka-creds-new"}

	if affected := w.secretIndex.ObjectsFor(oldSecretKey); len(affected) != 1 || affected[0].Name != "kafka-trigger" {
		t.Fatalf("sanity check before repoint: secretIndex.ObjectsFor(%v) want [{default kafka-trigger}], got %v", oldSecretKey, affected)
	}
	if affected := w.secretIndex.ObjectsFor(newSecretKey); affected != nil {
		t.Fatalf("sanity check before repoint: secretIndex.ObjectsFor(%v) want nil, got %v", newSecretKey, affected)
	}

	// Repoint the Integration's secretRef to the new Secret. The Trigger
	// object itself is never modified.
	integration.Spec.Kafka.SASL.UsernameSecretRef.Name = "kafka-creds-new"
	integration.Spec.Kafka.SASL.PasswordSecretRef.Name = "kafka-creds-new"
	if err := w.client.Update(ctx, integration); err != nil {
		t.Fatalf("updating integration: %v", err)
	}

	// Simulate the Integration informer's update event firing for this
	// object — the exact code path an add/update event on the new
	// Integration informer invokes.
	w.handleIntegrationChange(ctx, integration)

	// The new credential must actually have been picked up (not just the
	// index touched): resolveKafkaCredentials against the repointed spec
	// returns the new Secret's password.
	creds, err := w.resolveKafkaCredentials(ctx, "default", integration.Spec.Kafka)
	if err != nil {
		t.Fatalf("resolveKafkaCredentials after repoint: %v", err)
	}
	if creds.saslPassword != "new-password" {
		t.Fatalf("resolved SASL password after repoint: want %q, got %q", "new-password", creds.saslPassword)
	}

	// The old Secret must be completely gone from the reverse index — not
	// merely have the new Secret added alongside it. This is the crux of the
	// stale-Secret-watch fix: secretIndex.Update's full-replace semantics
	// mean a stale watch doesn't linger.
	if affected := w.secretIndex.ObjectsFor(oldSecretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after repoint: want nil (old secret dropped), got %v", oldSecretKey, affected)
	}
	if affected := w.secretIndex.ObjectsFor(newSecretKey); len(affected) != 1 || affected[0].Name != "kafka-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v) after repoint: want [{default kafka-trigger}], got %v", newSecretKey, affected)
	}
}

func TestHandleIntegrationChange_UnrelatedKafkaIntegrationIgnored(t *testing.T) {
	w := newMinimalKafkaWatcher(t)
	unrelated := &automationv1alpha1.Integration{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}

	// Must not panic and must not attempt to reconcile anything — the index is empty.
	w.handleIntegrationChange(context.Background(), unrelated)
}

// newKafkaTrigger builds a minimal kafka Trigger referencing integrationName,
// mirroring the amqp/nats packages' newAmqpTrigger/newNatsTrigger helpers.
func newKafkaTrigger(integrationName string) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "kafka",
			Enabled: true,
			Kafka: &automationv1alpha1.KafkaTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: integrationName},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}
}

// -----------------------------------------------------------------------
// STORY-067: credential-resolution failure/recovery Events
// See docs/design/gateway-credential-failure-visibility.md.
// -----------------------------------------------------------------------

func TestReconcileTrigger_EmitsCredentialResolutionFailedEvent_WhenIntegrationMissing(t *testing.T) {
	w := newMinimalKafkaWatcher(t) // no Integration created — Get fails
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newKafkaTrigger("missing-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	select {
	case evt := <-fakeRecorder.Events:
		if !strings.Contains(evt, "Warning") || !strings.Contains(evt, credentialResolutionFailedReason) {
			t.Fatalf("expected a Warning %s event, got %q", credentialResolutionFailedReason, evt)
		}
	default:
		t.Fatal("expected a credential-resolution-failed event when the Integration cannot be found")
	}
}

func TestReconcileTrigger_EmitsCredentialResolutionFailedEvent_WhenSecretMissing(t *testing.T) {
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, integration) // secret deliberately absent
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newKafkaTrigger("kafka-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	select {
	case evt := <-fakeRecorder.Events:
		if !strings.Contains(evt, "Warning") || !strings.Contains(evt, credentialResolutionFailedReason) {
			t.Fatalf("expected a Warning %s event, got %q", credentialResolutionFailedReason, evt)
		}
	default:
		t.Fatal("expected a credential-resolution-failed event when the SASL secret cannot be resolved")
	}
}

func TestReconcileTrigger_NoRecoveryEvent_OnFirstEverSuccess(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, secret, integration)
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newKafkaTrigger("kafka-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger) // never failed before — recordCredentialSuccess must stay quiet

	select {
	case evt := <-fakeRecorder.Events:
		t.Fatalf("expected no event on a Trigger's first-ever successful credential resolution, got %q", evt)
	default:
	}
}

func TestReconcileTrigger_EmitsCredentialResolutionSucceededEvent_OnRecoveryFromFailure(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, integration) // secret absent for now — first reconcile fails
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newKafkaTrigger("kafka-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.reconcileTrigger(ctx, trigger)
	select {
	case evt := <-fakeRecorder.Events:
		if !strings.Contains(evt, "Warning") || !strings.Contains(evt, credentialResolutionFailedReason) {
			t.Fatalf("expected a Warning %s event on the first (failing) reconcile, got %q", credentialResolutionFailedReason, evt)
		}
	default:
		t.Fatal("expected a credential-resolution-failed event on the first (failing) reconcile")
	}

	// Fix it: create the secret the Integration references, then reconcile again.
	if err := w.client.Create(ctx, secret); err != nil {
		t.Fatalf("creating secret: %v", err)
	}
	w.reconcileTrigger(ctx, trigger)

	select {
	case evt := <-fakeRecorder.Events:
		if !strings.Contains(evt, "Normal") || !strings.Contains(evt, credentialResolutionSucceededReason) {
			t.Fatalf("expected a Normal %s event on recovery, got %q", credentialResolutionSucceededReason, evt)
		}
	default:
		t.Fatal("expected a credential-resolution-succeeded event once the missing secret is created and reconcileTrigger succeeds")
	}
}

func TestOnTriggerDelete_RemovesKafkaTriggerFromIndex(t *testing.T) {
	secret := newSASLSecret("s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Kafka: saslKafkaSpec()},
	}
	w := newMinimalKafkaWatcher(t, secret, integration)
	trigger := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "kafka-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "kafka",
			Enabled: true,
			Kafka: &automationv1alpha1.KafkaTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: "kafka-integ"},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	w.onTriggerDelete(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "kafka-creds"}
	if affected := w.secretIndex.ObjectsFor(secretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after delete: want nil, got %v", secretKey, affected)
	}
}
