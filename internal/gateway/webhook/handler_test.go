package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

func newWebhookTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = automationv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// newTestHandler creates a WebhookHandler with a fake k8s client and a registry
// containing a single route at /hooks/test with no auth and POST method.
func newTestHandler(t *testing.T) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/test", RouteEntry{
		TriggerName:      "test-trigger",
		TriggerNamespace: "default",
		FlowRef:          "test-flow",
		AllowedMethod:    "POST",
	})
	h := NewWebhookHandler(fakeClient, registry, log)
	return h, fakeClient
}

// lastCreatedFlowRun returns the most recently created FlowRun from the fake client.
func lastCreatedFlowRun(t *testing.T, k8s client.Client) *automationv1alpha1.FlowRun {
	t.Helper()
	list := &automationv1alpha1.FlowRunList{}
	if err := k8s.List(t.Context(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatal("expected at least one FlowRun, got none")
	}
	return &list.Items[len(list.Items)-1]
}

// TestBodyTruncated_SmallBody verifies that a body under 4096 bytes is stored in full
// with BodyTruncated=false.
func TestBodyTruncated_SmallBody(t *testing.T) {
	h, k8s := newTestHandler(t)

	body := strings.Repeat("a", 100)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=false for small body")
	}
	if fr.Spec.TriggerData.Body != body {
		t.Errorf("expected full body stored, got length %d", len(fr.Spec.TriggerData.Body))
	}
}

// TestBodyTruncated_Exactly4096 verifies that a body of exactly 4096 bytes is stored in full
// with BodyTruncated=false.
func TestBodyTruncated_Exactly4096(t *testing.T) {
	h, k8s := newTestHandler(t)

	body := strings.Repeat("b", 4096)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=false for 4096-byte body")
	}
	if len(fr.Spec.TriggerData.Body) != 4096 {
		t.Errorf("expected body length 4096, got %d", len(fr.Spec.TriggerData.Body))
	}
}

// TestBodyTruncated_MidRange verifies that a body between 4097 bytes and the 4MB HTTP limit
// is stored truncated to 4096 bytes with BodyTruncated=true. This is the bug scenario from
// the R2 critical bug hunt: prior to the fix, BodyTruncated was false for mid-range bodies.
func TestBodyTruncated_MidRange(t *testing.T) {
	h, k8s := newTestHandler(t)

	// 8192 bytes — well above 4096 but far below 4MB
	body := strings.Repeat("c", 8192)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if !fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=true for 8192-byte body stored in 4096-char field")
	}
	if len(fr.Spec.TriggerData.Body) != 4096 {
		t.Errorf("expected body truncated to 4096 chars, got %d", len(fr.Spec.TriggerData.Body))
	}
}

// TestBodyTruncated_4097Boundary verifies the exact boundary: 4097 bytes triggers truncation.
func TestBodyTruncated_4097Boundary(t *testing.T) {
	h, k8s := newTestHandler(t)

	body := strings.Repeat("d", 4097)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if !fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=true for 4097-byte body")
	}
	if len(fr.Spec.TriggerData.Body) != 4096 {
		t.Errorf("expected body truncated to 4096 chars, got %d", len(fr.Spec.TriggerData.Body))
	}
}

// TestBodyTruncated_Over4MB verifies that bodies exceeding 4MB are rejected with 413.
func TestBodyTruncated_Over4MB(t *testing.T) {
	h, _ := newTestHandler(t)

	maxBody := 4 << 20 // 4 MB
	body := strings.Repeat("e", maxBody+1)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for body over 4MB, got %d", rr.Code)
	}
}

// TestBodyTruncated_EmptyBody verifies that an empty body is stored correctly.
func TestBodyTruncated_EmptyBody(t *testing.T) {
	h, k8s := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=false for empty body")
	}
	if fr.Spec.TriggerData.Body != "" {
		t.Errorf("expected empty body, got %q", fr.Spec.TriggerData.Body)
	}
}

// TestWebhookHandler_ResponseBody verifies the JSON response structure.
func TestWebhookHandler_ResponseBody(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(`{"test":true}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["flowRun"] == "" {
		t.Error("expected non-empty flowRun in response")
	}
	if resp["namespace"] != "default" {
		t.Errorf("expected namespace 'default', got %q", resp["namespace"])
	}
}
