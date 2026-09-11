package natsgateway

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

// newMinimalNatsWatcher creates a Watcher backed by a fake client, with no
// informer cache — sufficient for resolveNatsCredentials, reconcileTrigger's
// indexing side effects, and handleSecretChange, none of which touch w.cache.
func newMinimalNatsWatcher(t *testing.T, objs ...client.Object) *Watcher {
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

func newNatsUserPassSecret(name, namespace, user, pass string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data: map[string][]byte{
			"username": []byte(user),
			"password": []byte(pass),
		},
	}
}

func authNatsSpec(secretName string) *automationv1alpha1.NatsIntegrationSpec {
	return &automationv1alpha1.NatsIntegrationSpec{
		Servers: []string{"nats://127.0.0.1:1"}, // unreachable on purpose; these tests never need a real broker
		UsernameSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "username",
		},
		PasswordSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName}, Key: "password",
		},
	}
}

func newNatsTrigger(name, integrationName string) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "nats",
			Enabled: true,
			Nats: &automationv1alpha1.NatsTrigger{
				Subject:        "orders.created",
				IntegrationRef: corev1.LocalObjectReference{Name: integrationName},
			},
			FlowRef: &automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}
}

func TestResolveNatsCredentials_FingerprintChangesWithRotatedSecret(t *testing.T) {
	secret := newNatsUserPassSecret("nats-creds", "default", "alice", "old-password")
	w := newMinimalNatsWatcher(t, secret)
	spec := authNatsSpec("nats-creds")

	before, err := w.resolveNatsCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveNatsCredentials: %v", err)
	}
	wantRef := types.NamespacedName{Namespace: "default", Name: "nats-creds"}
	if len(before.secretRefs) != 2 || before.secretRefs[0] != wantRef || before.secretRefs[1] != wantRef {
		t.Fatalf("secretRefs: want [%v %v], got %v", wantRef, wantRef, before.secretRefs)
	}
	if before.username != "alice" || before.password != "old-password" {
		t.Fatalf("resolved credentials: want alice/old-password, got %s/%s", before.username, before.password)
	}

	secret.Data["password"] = []byte("new-password-after-rotation")
	if err := w.client.Update(context.Background(), secret); err != nil {
		t.Fatalf("updating secret: %v", err)
	}

	after, err := w.resolveNatsCredentials(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("resolveNatsCredentials (after rotation): %v", err)
	}
	if before.fingerprint == after.fingerprint {
		t.Fatal("fingerprint must change when the underlying secret's password value changes")
	}
}

func TestReconcileTrigger_IndexesNatsIntegrationSecrets(t *testing.T) {
	secret := newNatsUserPassSecret("nats-creds", "default", "alice", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "nats-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Nats: authNatsSpec("nats-creds")},
	}
	w := newMinimalNatsWatcher(t, secret, integration)
	trigger := newNatsTrigger("nats-trigger", "nats-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger) // startSubscription will fail fast (nats://127.0.0.1:1 refuses instantly); only the indexing side effect is under test

	secretKey := types.NamespacedName{Namespace: "default", Name: "nats-creds"}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) != 1 || affected[0].Name != "nats-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v): want [{default nats-trigger}], got %v", secretKey, affected)
	}
}

func TestHandleSecretChange_UnrelatedNatsSecretIgnored(t *testing.T) {
	w := newMinimalNatsWatcher(t)
	unrelated := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}}

	// Must not panic and must not attempt to reconcile anything — the index is empty.
	w.handleSecretChange(context.Background(), unrelated)
}

func TestOnTriggerDelete_RemovesNatsTriggerFromIndex(t *testing.T) {
	secret := newNatsUserPassSecret("nats-creds", "default", "alice", "s3cr3t")
	integration := &automationv1alpha1.Integration{
		ObjectMeta: metav1.ObjectMeta{Name: "nats-integ", Namespace: "default"},
		Spec:       automationv1alpha1.IntegrationSpec{Nats: authNatsSpec("nats-creds")},
	}
	w := newMinimalNatsWatcher(t, secret, integration)
	trigger := newNatsTrigger("nats-trigger", "nats-integ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w.reconcileTrigger(ctx, trigger)

	w.onTriggerDelete(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "nats-creds"}
	if affected := w.secretIndex.ObjectsFor(secretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after delete: want nil, got %v", secretKey, affected)
	}
}
