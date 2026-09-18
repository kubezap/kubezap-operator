package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/secretindex"
)

// newMinimalWatcher creates a TriggerWatcher with only the fields required by
// buildRouteEntry. The cache and jwksCache are intentionally nil — buildRouteEntry
// does not use them directly; it only calls readSecretKey (k8sClient) for auth configs.
// Rate-limit wiring requires no secret reads, so nil cache is safe for these tests.
func newMinimalWatcher(t *testing.T) *TriggerWatcher {
	t.Helper()
	s := runtime.NewScheme()
	_ = automationv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	return &TriggerWatcher{
		k8sClient:   fakeClient,
		registry:    registry,
		namespace:   "default",
		log:         log,
		secretIndex: secretindex.New(),
		triggers:    make(map[types.NamespacedName]*automationv1alpha1.Trigger),
	}
}

// newWebhookTrigger builds a minimal Trigger of type webhook.
func newWebhookTrigger(name string, rl *automationv1alpha1.WebhookRateLimit) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "webhook",
			Enabled: true,
			Webhook: &automationv1alpha1.WebhookTrigger{
				Path:      "/hooks/" + name,
				Method:    "POST",
				RateLimit: rl,
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}
}

// TestBuildRouteEntry_RateLimit_Populated verifies that buildRouteEntry correctly
// populates MaxInvocations and CooldownWindow from spec.webhook.rateLimit.
func TestBuildRouteEntry_RateLimit_Populated(t *testing.T) {
	w := newMinimalWatcher(t)

	trigger := newWebhookTrigger("rl-trigger", &automationv1alpha1.WebhookRateLimit{
		MaxRequests: 2,
		Window:      metav1.Duration{Duration: 60 * time.Second},
	})

	entry, err := w.buildRouteEntry(context.Background(), trigger)
	if err != nil {
		t.Fatalf("buildRouteEntry returned unexpected error: %v", err)
	}

	if entry.MaxInvocations != 2 {
		t.Errorf("MaxInvocations: want 2, got %d", entry.MaxInvocations)
	}
	if entry.CooldownWindow != 60*time.Second {
		t.Errorf("CooldownWindow: want 60s, got %v", entry.CooldownWindow)
	}
}

// TestBuildRouteEntry_RateLimit_DefaultWindow verifies that a zero Window value in
// WebhookRateLimit is normalised to 60s by buildRouteEntry.
func TestBuildRouteEntry_RateLimit_DefaultWindow(t *testing.T) {
	w := newMinimalWatcher(t)

	// Window is omitted — metav1.Duration zero value has Duration == 0.
	trigger := newWebhookTrigger("rl-default-window", &automationv1alpha1.WebhookRateLimit{
		MaxRequests: 5,
	})

	entry, err := w.buildRouteEntry(context.Background(), trigger)
	if err != nil {
		t.Fatalf("buildRouteEntry returned unexpected error: %v", err)
	}

	if entry.MaxInvocations != 5 {
		t.Errorf("MaxInvocations: want 5, got %d", entry.MaxInvocations)
	}
	if entry.CooldownWindow != 60*time.Second {
		t.Errorf("CooldownWindow: want 60s (default), got %v", entry.CooldownWindow)
	}
}

// TestBuildRouteEntry_RateLimit_Nil verifies that omitting spec.webhook.rateLimit leaves
// MaxInvocations=0 and CooldownWindow=0, meaning no rate-limit enforcement.
func TestBuildRouteEntry_RateLimit_Nil(t *testing.T) {
	w := newMinimalWatcher(t)

	trigger := newWebhookTrigger("no-rl-trigger", nil)

	entry, err := w.buildRouteEntry(context.Background(), trigger)
	if err != nil {
		t.Fatalf("buildRouteEntry returned unexpected error: %v", err)
	}

	if entry.MaxInvocations != 0 {
		t.Errorf("MaxInvocations: want 0 (no limit), got %d", entry.MaxInvocations)
	}
	if entry.CooldownWindow != 0 {
		t.Errorf("CooldownWindow: want 0, got %v", entry.CooldownWindow)
	}
}

// TestRateLimit_HandlerEnforces429 sends requests up to and beyond the maxRequests
// budget and verifies the handler returns 429 once the window is exhausted.
func TestRateLimit_HandlerEnforces429(t *testing.T) {
	s := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/rl-enforce", RouteEntry{
		TriggerName:      "rl-enforce-trigger",
		TriggerNamespace: "default",
		FlowRef:          "my-flow",
		AllowedMethod:    "POST",
		MaxInvocations:   2,
		CooldownWindow:   60 * time.Second,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)

	type result struct {
		code int
	}
	var results []result

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/hooks/rl-enforce", bytes.NewBufferString(`{}`))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		results = append(results, result{code: rr.Code})
	}

	if results[0].code != http.StatusAccepted {
		t.Errorf("request 1: want 202, got %d", results[0].code)
	}
	if results[1].code != http.StatusAccepted {
		t.Errorf("request 2: want 202, got %d", results[1].code)
	}
	if results[2].code != http.StatusTooManyRequests {
		t.Errorf("request 3: want 429, got %d", results[2].code)
	}
}

// TestRateLimit_NonZeroWindow verifies that a non-standard window (e.g. 5m) is propagated
// correctly through buildRouteEntry into the RouteEntry.
func TestBuildRouteEntry_RateLimit_NonStandardWindow(t *testing.T) {
	w := newMinimalWatcher(t)

	trigger := newWebhookTrigger("rl-5m", &automationv1alpha1.WebhookRateLimit{
		MaxRequests: 100,
		Window:      metav1.Duration{Duration: 5 * time.Minute},
	})

	entry, err := w.buildRouteEntry(context.Background(), trigger)
	if err != nil {
		t.Fatalf("buildRouteEntry returned unexpected error: %v", err)
	}

	if entry.MaxInvocations != 100 {
		t.Errorf("MaxInvocations: want 100, got %d", entry.MaxInvocations)
	}
	if entry.CooldownWindow != 5*time.Minute {
		t.Errorf("CooldownWindow: want 5m, got %v", entry.CooldownWindow)
	}
}

// newHMACTrigger builds a webhook Trigger secured with HMAC auth backed by the "hmac-secret" Secret.
func newHMACTrigger() *automationv1alpha1.Trigger {
	t := newWebhookTrigger("hmac-trigger", nil)
	t.Spec.Webhook.Auth = &automationv1alpha1.WebhookAuth{
		Type: "hmac",
		HMAC: &automationv1alpha1.HMACConfig{
			SecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "hmac-secret"},
				Key:                  "secret",
			},
		},
	}
	return t
}

// TestSecretRefsForTrigger_HMAC verifies the referenced secret is correctly
// identified without needing to read it.
func TestSecretRefsForTrigger_HMAC(t *testing.T) {
	trigger := newHMACTrigger()
	refs := secretRefsForTrigger(trigger)
	if len(refs) != 1 || refs[0].Name != "hmac-secret" || refs[0].Namespace != "default" {
		t.Fatalf("secretRefsForTrigger: want [{default hmac-secret}], got %v", refs)
	}
}

// TestSecretRefsForTrigger_NoAuth verifies a Trigger with no auth config
// references no secrets.
func TestSecretRefsForTrigger_NoAuth(t *testing.T) {
	trigger := newWebhookTrigger("no-auth-trigger", nil)
	if refs := secretRefsForTrigger(trigger); refs != nil {
		t.Fatalf("secretRefsForTrigger: want nil, got %v", refs)
	}
}

// TestHandleTrigger_IndexesReferencedSecret verifies that processing a Trigger
// records it in the secret index, so a later secret change can find it.
func TestHandleTrigger_IndexesReferencedSecret(t *testing.T) {
	w := newMinimalWatcher(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hmac-secret", Namespace: "default"},
		Data:       map[string][]byte{"secret": []byte("v1")},
	}
	if err := w.k8sClient.Create(context.Background(), secret); err != nil {
		t.Fatalf("creating secret: %v", err)
	}
	trigger := newHMACTrigger()

	w.handleTrigger(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "hmac-secret"}
	affected := w.secretIndex.ObjectsFor(secretKey)
	if len(affected) != 1 || affected[0].Name != "hmac-trigger" {
		t.Fatalf("secretIndex.ObjectsFor(%v): want [{default hmac-trigger}], got %v", secretKey, affected)
	}

	registered, ok := w.registry.Lookup("/hooks/hmac-trigger")
	if !ok {
		t.Fatal("expected route to be registered")
	}
	if registered.HMACSecret != "v1" {
		t.Fatalf("HMACSecret: want %q, got %q", "v1", registered.HMACSecret)
	}
}

// TestHandleSecretChange_ReprocessesDependentTrigger verifies that changing a
// Secret already referenced by a known Trigger causes that Trigger to be
// rebuilt with the new secret value — the core of the secret-rotation fix in
// docs/design/secret-rotation-watches.md.
func TestHandleSecretChange_ReprocessesDependentTrigger(t *testing.T) {
	w := newMinimalWatcher(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hmac-secret", Namespace: "default"},
		Data:       map[string][]byte{"secret": []byte("v1")},
	}
	ctx := context.Background()
	if err := w.k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("creating secret: %v", err)
	}
	trigger := newHMACTrigger()
	w.handleTrigger(trigger) // initial processing, as the real informer would do on Add

	// Rotate the secret's value, as ESO or a manual kubectl edit would.
	secret.Data["secret"] = []byte("v2-rotated")
	if err := w.k8sClient.Update(ctx, secret); err != nil {
		t.Fatalf("updating secret: %v", err)
	}

	w.handleSecretChange(secret)

	registered, ok := w.registry.Lookup("/hooks/hmac-trigger")
	if !ok {
		t.Fatal("expected route to still be registered")
	}
	if registered.HMACSecret != "v2-rotated" {
		t.Fatalf("HMACSecret after rotation: want %q, got %q — secret change was not picked up", "v2-rotated", registered.HMACSecret)
	}
}

// TestHandleSecretChange_UnrelatedSecretIgnored verifies that a change to a
// Secret nothing references does not touch the registry.
func TestHandleSecretChange_UnrelatedSecretIgnored(t *testing.T) {
	w := newMinimalWatcher(t)
	trigger := newHMACTrigger()
	w.triggers[types.NamespacedName{Namespace: "default", Name: "hmac-trigger"}] = trigger
	w.secretIndex.Update(
		types.NamespacedName{Namespace: "default", Name: "hmac-trigger"},
		secretRefsForTrigger(trigger),
	)

	unrelated := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-secret", Namespace: "default"}}
	w.handleSecretChange(unrelated) // must be a no-op: nothing references this secret

	if _, ok := w.registry.Lookup("/hooks/hmac-trigger"); ok {
		t.Fatal("route must not be registered — handleTrigger was never called for it")
	}
}

// TestHandleDelete_RemovesFromSecretIndex verifies that deleting a Trigger
// removes it from the secret index, so a later rotation of the secret it used
// to reference does not try to reprocess a Trigger that no longer exists.
func TestHandleDelete_RemovesFromSecretIndex(t *testing.T) {
	w := newMinimalWatcher(t)
	trigger := newHMACTrigger()
	w.handleTrigger(trigger)

	w.handleDelete(trigger)

	secretKey := types.NamespacedName{Namespace: "default", Name: "hmac-secret"}
	if affected := w.secretIndex.ObjectsFor(secretKey); affected != nil {
		t.Fatalf("secretIndex.ObjectsFor(%v) after delete: want nil, got %v", secretKey, affected)
	}
	w.triggersMu.Lock()
	_, stillPresent := w.triggers[types.NamespacedName{Namespace: "default", Name: "hmac-trigger"}]
	w.triggersMu.Unlock()
	if stillPresent {
		t.Fatal("trigger must be removed from the triggers map after delete")
	}
}
