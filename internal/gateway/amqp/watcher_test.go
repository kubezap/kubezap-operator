package amqp

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func newUserPassSecret(name, namespace, user, pass string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data: map[string][]byte{
			"username": []byte(user),
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

func newAmqpTrigger(name, integrationName string) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
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
	secret := newUserPassSecret("amqp-creds", "default", "alice", "old-password")
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
	secret := newUserPassSecret("amqp-creds", "default", "alice", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, secret, integration)
	trigger := newAmqpTrigger("amqp-trigger", "amqp-integ")

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
	oldSecret := newUserPassSecret("amqp-creds-old", "default", "alice", "old-password")
	newSecret := newUserPassSecret("amqp-creds-new", "default", "alice", "new-password")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds-old")},
	}
	w := newMinimalAmqpWatcher(t, oldSecret, newSecret, integration)
	trigger := newAmqpTrigger("amqp-trigger", "amqp-integ")

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

func TestOnTriggerDelete_RemovesAmqpTriggerFromIndex(t *testing.T) {
	secret := newUserPassSecret("amqp-creds", "default", "alice", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "amqp-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Amqp: authAmqpSpec("amqp-creds")},
	}
	w := newMinimalAmqpWatcher(t, secret, integration)
	trigger := newAmqpTrigger("amqp-trigger", "amqp-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	w.onTriggerDelete(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "amqp-creds"}
	if affected := w.secretIndex.ObjectsFor(secretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after delete: want nil, got %v", secretKey, affected)
	}
}
