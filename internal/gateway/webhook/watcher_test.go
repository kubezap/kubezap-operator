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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
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
		k8sClient: fakeClient,
		registry:  registry,
		namespace: "default",
		log:       log,
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
			FlowRef: &automationv1alpha1.FlowReference{Name: "my-flow"},
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
	h := NewWebhookHandler(fakeClient, registry, log)

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
