package amqp

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
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

// newMinimalAmqpWatcher creates a Watcher backed by a fake client, with no
// informer cache — sufficient for resolveAmqpCredentials, reconcileTrigger's
// indexing side effects, and handleSecretChange, none of which touch w.cache.
func newMinimalAmqpWatcher(t *testing.T, objs ...client.Object) *Watcher {
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

func newUserPassSecret(name, pass string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("alice"),
			"password": []byte(pass),
		},
	}
}

func authAmqpSpec(secretName string) *automationv1alpha1.AmqpIntegrationSpec {
	return &automationv1alpha1.AmqpIntegrationSpec{
		URL: "amqp://127.0.0.1:1/", // unreachable on purpose; these tests never need a real broker
		UsernameSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "username",
		},
		PasswordSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "password",
		},
	}
}

func newAmqpTrigger(integrationName string) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-trigger", Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "amqp",
			Enabled: true,
			Amqp: &automationv1alpha1.AmqpTrigger{
				Topic:          "orders",
				IntegrationRef: corev1.LocalObjectReference{Name: integrationName},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}
}

func TestResolveAmqpCredentials_FingerprintChangesWithRotatedSecret(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "old-password")
	w := newMinimalAmqpWatcher(t, secret)
	spec := authAmqpSpec("amqp-creds")

	before, err := w.resolveAmqpCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveAmqpCredentials: %v", err)
	}
	wantRef := types.NamespacedName{Namespace: "default", Name: "amqp-creds"}
	if len(before.secretRefs) != 2 || before.secretRefs[0] != wantRef || before.secretRefs[1] != wantRef {
		t.Fatalf("secretRefs: want [%v %v], got %v", wantRef, wantRef, before.secretRefs)
	}

	secret.Data["password"] = []byte("new-password-after-rotation")
	if err := w.client.Update(context.Background(), secret); err != nil {
		t.Fatalf("updating secret: %v", err)
	}

	after, err := w.resolveAmqpCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveAmqpCredentials (after rotation): %v", err)
	}
	if before.fingerprint == after.fingerprint {
		t.Fatal("fingerprint must change when the underlying secret's password value changes")
	}
}

func TestReconcileTrigger_IndexesAmqpIntegrationSecrets(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, secret, integration)
	trigger := newAmqpTrigger("amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger) // the spawned connect091 goroutine will fail to dial 127.0.0.1:1 in the background; only the indexing side effect is under test

	secretKey := types.NamespacedName{Namespace: "default", Name: "amqp-creds"}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) != 1 || affected[0].Name != "amqp-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v): want [{default amqp-trigger}], got %v", secretKey, affected)
	}
	w.stopSubscription(types.NamespacedName{Namespace: "default", Name: "amqp-trigger"}) // stop the background retry goroutine
}

func TestHandleSecretChange_UnrelatedAmqpSecretIgnored(t *testing.T) {
	w := newMinimalAmqpWatcher(t)
	unrelated := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}

	// Must not panic and must not attempt to reconcile anything — the index is empty.
	w.handleSecretChange(context.Background(), unrelated)
}

// TestHandleIntegrationChange_RepointedSecretRefDropsOldSecretFromIndex covers
// STORY-033: repointing an Integration's username/password secretRef to a
// different Secret, without touching the Trigger, must be picked up within
// one resync (here, one handleIntegrationChange call standing in for the
// Integration informer firing) — and the old Secret must be fully dropped
// from secretIndex, not just have the new one added alongside it.
func TestHandleIntegrationChange_RepointedSecretRefDropsOldSecretFromIndex(t *testing.T) {
	oldSecret := newUserPassSecret("amqp-creds-old", "old-password")
	newSecret := newUserPassSecret("amqp-creds-new", "new-password")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds-old")},
	}
	w := newMinimalAmqpWatcher(t, oldSecret, newSecret, integration)
	trigger := newAmqpTrigger("amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)
	defer w.stopSubscription(types.NamespacedName{Namespace: "default", Name: "amqp-trigger"}) // stop the background retry goroutine

	oldSecretKey := types.NamespacedName{Namespace: "default", Name: "amqp-creds-old"}
	newSecretKey := types.NamespacedName{Namespace: "default", Name: "amqp-creds-new"}

	if affected := w.secretIndex.ObjectsFor(oldSecretKey); len(affected) != 1 || affected[0].Name != "amqp-trigger" {
		t.Fatalf("sanity check before repoint: secretIndex.ObjectsFor(%v) want [{default amqp-trigger}], got %v", oldSecretKey, affected)
	}

	// Repoint the Integration's secretRef to the new Secret. The Trigger
	// object itself is never modified.
	integration.Spec.Amqp.UsernameSecretRef.Name = "amqp-creds-new"
	integration.Spec.Amqp.PasswordSecretRef.Name = "amqp-creds-new"
	if err := w.client.Update(ctx, integration); err != nil {
		t.Fatalf("updating integration: %v", err)
	}

	// Simulate the Integration informer's update event firing for this
	// object — the exact code path an add/update event on the new
	// Integration informer invokes.
	w.handleIntegrationChange(ctx, integration)

	// The new credential must actually have been picked up (not just the
	// index touched): resolveAmqpCredentials against the repointed spec
	// resolves against the new Secret without error, and its secretRefs
	// point only at the new Secret.
	creds, err := w.resolveAmqpCredentials(ctx, "default", integration.Spec.Amqp)
	if err != nil {
		t.Fatalf("resolveAmqpCredentials after repoint: %v", err)
	}
	wantNewRef := types.NamespacedName{Namespace: "default", Name: "amqp-creds-new"}
	if len(creds.secretRefs) != 2 || creds.secretRefs[0] != wantNewRef || creds.secretRefs[1] != wantNewRef {
		t.Fatalf("secretRefs after repoint: want [%v %v], got %v", wantNewRef, wantNewRef, creds.secretRefs)
	}

	// The old Secret must be completely gone from the reverse index — not
	// merely have the new Secret added alongside it. This is the crux of the
	// stale-Secret-watch fix: secretIndex.Update's full-replace semantics
	// mean a stale watch doesn't linger.
	if affected := w.secretIndex.ObjectsFor(oldSecretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after repoint: want nil (old secret dropped), got %v", oldSecretKey, affected)
	}
	if affected := w.secretIndex.ObjectsFor(newSecretKey); len(affected) != 1 || affected[0].Name != "amqp-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v) after repoint: want [{default amqp-trigger}], got %v", newSecretKey, affected)
	}
}

func TestHandleIntegrationChange_UnrelatedAmqpIntegrationIgnored(t *testing.T) {
	w := newMinimalAmqpWatcher(t)
	unrelated := &automationv1alpha1.Integration{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}

	// Must not panic and must not attempt to reconcile anything — the index is empty.
	w.handleIntegrationChange(context.Background(), unrelated)
}

// -----------------------------------------------------------------------
// STORY-067: credential-resolution failure/recovery Events
// See docs/design/gateway-credential-failure-visibility.md.
// -----------------------------------------------------------------------

func TestReconcileTrigger_EmitsCredentialResolutionFailedEvent_WhenIntegrationMissing(t *testing.T) {
	w := newMinimalAmqpWatcher(t) // no Integration created — Get fails
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newAmqpTrigger("missing-integ")

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

func TestReconcileTrigger_NoRecoveryEvent_OnFirstEverSuccess(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, secret, integration)
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newAmqpTrigger("amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger) // never failed before — recordCredentialSuccess must stay quiet
	defer w.stopSubscription(types.NamespacedName{Namespace: "default", Name: "amqp-trigger"})

	select {
	case evt := <-fakeRecorder.Events:
		t.Fatalf("expected no event on a Trigger's first-ever successful credential resolution, got %q", evt)
	default:
	}
}

func TestReconcileTrigger_EmitsCredentialResolutionSucceededEvent_OnRecoveryFromFailure(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, integration) // secret absent for now — first reconcile fails
	fakeRecorder := record.NewFakeRecorder(10)
	w.recorder = fakeRecorder

	trigger := newAmqpTrigger("amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer w.stopSubscription(types.NamespacedName{Namespace: "default", Name: "amqp-trigger"})

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

func TestOnTriggerDelete_RemovesAmqpTriggerFromIndex(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, secret, integration)
	trigger := newAmqpTrigger("amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	w.onTriggerDelete(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "amqp-creds"}
	if affected := w.secretIndex.ObjectsFor(secretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after delete: want nil, got %v", secretKey, affected)
	}
}

// TestStartSubscription_RejectsExchangeWithVersion10 covers STORY-070: an
// Exchange configured alongside version "1.0" must be rejected with a clear
// error, never silently ignored (the exact bug this story fixes for
// routingKey).
func TestStartSubscription_RejectsExchangeWithVersion10(t *testing.T) {
	w := newMinimalAmqpWatcher(t)
	trigger := newAmqpTrigger("amqp-integ")
	trigger.Spec.Amqp.Exchange = &automationv1alpha1.AmqpExchangeSpec{Name: "orders-exchange", Type: "topic"}
	amqpSpec := authAmqpSpec("amqp-creds")
	amqpSpec.Version = "1.0"

	err := w.startSubscription(context.Background(), trigger, amqpSpec, "fingerprint")
	if err == nil {
		t.Fatal("expected an error rejecting spec.amqp.exchange with version \"1.0\", got nil")
	}
	if !strings.Contains(err.Error(), "0-9-1") || !strings.Contains(err.Error(), `"1.0"`) {
		t.Fatalf("error should clearly name both the required and given version, got: %v", err)
	}

	key := types.NamespacedName{Namespace: "default", Name: "amqp-trigger"}
	if _, ok := w.subscriptions.Load(key); ok {
		t.Fatal("startSubscription must not leave a subscription registered after rejecting the config")
	}
}

// fakeAmqp091Channel implements amqp091Channel, recording the exact call
// sequence declareAndBind091 makes so tests can assert it without a live
// broker.
type fakeAmqp091Channel struct {
	calls []string

	exchangeDeclareErr error
	queueDeclareErr    error
	queueBindErr       error
	consumeErr         error
}

func (f *fakeAmqp091Channel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp091.Table) error {
	f.calls = append(f.calls, fmt.Sprintf("ExchangeDeclare(name=%s,kind=%s,durable=%v)", name, kind, durable))
	return f.exchangeDeclareErr
}

func (f *fakeAmqp091Channel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp091.Table) (amqp091.Queue, error) {
	f.calls = append(f.calls, fmt.Sprintf("QueueDeclare(name=%s,durable=%v)", name, durable))
	if f.queueDeclareErr != nil {
		return amqp091.Queue{}, f.queueDeclareErr
	}
	return amqp091.Queue{Name: name}, nil
}

func (f *fakeAmqp091Channel) QueueBind(name, key, exchange string, noWait bool, args amqp091.Table) error {
	f.calls = append(f.calls, fmt.Sprintf("QueueBind(queue=%s,key=%s,exchange=%s)", name, key, exchange))
	return f.queueBindErr
}

func (f *fakeAmqp091Channel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp091.Table) (<-chan amqp091.Delivery, error) {
	f.calls = append(f.calls, fmt.Sprintf("Consume(queue=%s,consumer=%s)", queue, consumer))
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	return make(chan amqp091.Delivery), nil
}

// TestDeclareAndBind091_NoExchange_UnchangedSequence is the regression test
// for STORY-070: with no Exchange configured, behavior must be byte-for-byte
// unchanged — same QueueDeclare + Consume sequence, no ExchangeDeclare/
// QueueBind calls.
func TestDeclareAndBind091_NoExchange_UnchangedSequence(t *testing.T) {
	ch := &fakeAmqp091Channel{}
	if _, err := declareAndBind091(ch, "orders", "unused-routing-key", nil, "kubezap-amqp-trigger"); err != nil {
		t.Fatalf("declareAndBind091: %v", err)
	}
	want := []string{
		"QueueDeclare(name=orders,durable=true)",
		"Consume(queue=orders,consumer=kubezap-amqp-trigger)",
	}
	if !reflect.DeepEqual(ch.calls, want) {
		t.Fatalf("call sequence: want %v, got %v", want, ch.calls)
	}
}

// TestDeclareAndBind091_WithExchange_DeclaresAndBindsBeforeConsume covers
// STORY-070's core feature: the exchange is declared idempotently before the
// queue, the queue is declared unchanged, and it's bound to the exchange
// using RoutingKey as the binding pattern before Consume starts.
func TestDeclareAndBind091_WithExchange_DeclaresAndBindsBeforeConsume(t *testing.T) {
	ch := &fakeAmqp091Channel{}
	exchange := &automationv1alpha1.AmqpExchangeSpec{Name: "orders-exchange", Type: "topic"}
	if _, err := declareAndBind091(ch, "orders", "orders.*", exchange, "kubezap-amqp-trigger"); err != nil {
		t.Fatalf("declareAndBind091: %v", err)
	}
	want := []string{
		"ExchangeDeclare(name=orders-exchange,kind=topic,durable=true)",
		"QueueDeclare(name=orders,durable=true)",
		"QueueBind(queue=orders,key=orders.*,exchange=orders-exchange)",
		"Consume(queue=orders,consumer=kubezap-amqp-trigger)",
	}
	if !reflect.DeepEqual(ch.calls, want) {
		t.Fatalf("call sequence: want %v, got %v", want, ch.calls)
	}
}

// TestDeclareAndBind091_MismatchedExchangeRedeclare_SurfacesClearError covers
// the case where an exchange pre-exists on the broker with an incompatible
// type: ExchangeDeclare fails, and declareAndBind091 must surface a clear
// error (for the caller's reconnect-backoff logging) rather than proceeding
// to declare/bind/consume the queue.
func TestDeclareAndBind091_MismatchedExchangeRedeclare_SurfacesClearError(t *testing.T) {
	ch := &fakeAmqp091Channel{exchangeDeclareErr: fmt.Errorf("PRECONDITION_FAILED - inequivalent arg 'type' for exchange 'orders-exchange'")}
	exchange := &automationv1alpha1.AmqpExchangeSpec{Name: "orders-exchange", Type: "topic"}

	_, err := declareAndBind091(ch, "orders", "orders.*", exchange, "kubezap-amqp-trigger")
	if err == nil {
		t.Fatal("expected an error when the exchange redeclare fails")
	}
	if !strings.Contains(err.Error(), "orders-exchange") {
		t.Fatalf("error should name the exchange, got: %v", err)
	}
	if len(ch.calls) != 1 {
		t.Fatalf("expected declareAndBind091 to stop after the failed ExchangeDeclare, got calls: %v", ch.calls)
	}
}
