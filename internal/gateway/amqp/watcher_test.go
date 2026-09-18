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
		client:      fakeClient,
		namespace:   "default",
		log:         zap.New(),
		secretIndex: secretindex.New(),
		triggers:    make(map[types.NamespacedName]*automationv1alpha1.Trigger),
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
