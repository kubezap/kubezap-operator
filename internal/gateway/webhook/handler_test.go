package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/metrics"
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
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

// newTestHandlerWithBodyLimit is like newTestHandler but with an explicit
// maxStoredBodyBytes, decoupling boundary tests from whatever the production
// default happens to be.
func newTestHandlerWithBodyLimit(t *testing.T, limit int) (*WebhookHandler, client.Client) {
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
	h := NewWebhookHandler(fakeClient, registry, log, nil, limit)
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
	h, k8s := newTestHandlerWithBodyLimit(t, 4096)

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
	h, k8s := newTestHandlerWithBodyLimit(t, 4096)

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
	h, k8s := newTestHandlerWithBodyLimit(t, 4096)

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

// TestBodyEncoding_UTF8 verifies a valid-UTF-8 body is stored verbatim on
// TriggerData.Body with BodyEncoding "utf8".
func TestBodyEncoding_UTF8(t *testing.T) {
	h, k8s := newTestHandler(t)

	body := `{"hello":"world"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if fr.Spec.TriggerData.Body != body {
		t.Errorf("Body = %q, want %q", fr.Spec.TriggerData.Body, body)
	}
	if fr.Spec.TriggerData.BodyEncoding != "utf8" {
		t.Errorf("BodyEncoding = %q, want utf8", fr.Spec.TriggerData.BodyEncoding)
	}
	if fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=false")
	}
}

// TestBodyEncoding_Binary verifies a non-UTF-8 binary body is base64-encoded
// on TriggerData.Body with BodyEncoding "base64", rather than being silently
// corrupted to replacement characters when the FlowRun is JSON-marshaled
// (the bug docs/design/trigger-body-encoding-safety.md fixes).
func TestBodyEncoding_Binary(t *testing.T) {
	h, k8s := newTestHandler(t)

	binaryBody := []byte{0xFF, 0xFE, 0x00, 0x01, 0x02}
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewReader(binaryBody))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	want := base64.StdEncoding.EncodeToString(binaryBody)
	if fr.Spec.TriggerData.Body != want {
		t.Errorf("Body = %q, want %q", fr.Spec.TriggerData.Body, want)
	}
	if fr.Spec.TriggerData.BodyEncoding != "base64" {
		t.Errorf("BodyEncoding = %q, want base64", fr.Spec.TriggerData.BodyEncoding)
	}
	if fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=false for a body under the stored-body limit")
	}
}

// TestBodyEncoding_TruncatedAtRuneBoundary verifies that truncating a valid
// UTF-8 body at a byte boundary that splits a multi-byte rune correctly
// falls back to BodyEncoding "base64" rather than producing another
// silently-invalid string — the scenario
// docs/design/trigger-body-encoding-safety.md calls out explicitly as the
// reason encoding must be determined AFTER truncation, not before.
func TestBodyEncoding_TruncatedAtRuneBoundary(t *testing.T) {
	h, k8s := newTestHandlerWithBodyLimit(t, 5)

	// "é" is the 2-byte UTF-8 sequence 0xC3 0xA9; three of them is 6 bytes.
	// Truncating to 5 bytes splits the final rune, leaving a dangling 0xC3
	// with no continuation byte — invalid UTF-8.
	body := strings.Repeat("é", 3)
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if !fr.Spec.TriggerData.BodyTruncated {
		t.Error("expected BodyTruncated=true")
	}
	if fr.Spec.TriggerData.BodyEncoding != "base64" {
		t.Errorf("BodyEncoding = %q, want base64 (truncation split a multi-byte rune)", fr.Spec.TriggerData.BodyEncoding)
	}
	wantBytes := []byte(body)[:5]
	want := base64.StdEncoding.EncodeToString(wantBytes)
	if fr.Spec.TriggerData.Body != want {
		t.Errorf("Body = %q, want %q", fr.Spec.TriggerData.Body, want)
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

// newTestHandlerWithHMAC creates a WebhookHandler with a fake k8s client and a registry
// containing a single route at /hooks/hmac-test with HMAC auth and POST method.
func newTestHandlerWithHMAC(t *testing.T, secret string) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/hmac-test", RouteEntry{
		TriggerName:      "hmac-trigger",
		TriggerNamespace: "default",
		FlowRef:          "hmac-flow",
		AllowedMethod:    "POST",
		AuthType:         "hmac",
		HMACSecret:       secret,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

// newTestHandlerWithSlackHMAC creates a WebhookHandler with a fake k8s client and a
// registry containing a single route at /hooks/slack-test with Slack-flavored HMAC
// auth and POST method.
func newTestHandlerWithSlackHMAC(t *testing.T, secret string, toleranceSeconds int32) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/slack-test", RouteEntry{
		TriggerName:               "slack-trigger",
		TriggerNamespace:          "default",
		FlowRef:                   "slack-flow",
		AllowedMethod:             "POST",
		AuthType:                  "hmac",
		HMACSecret:                secret,
		HMACProvider:              "slack",
		HMACTimestampToleranceSec: toleranceSeconds,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

// computeSlackSignature returns the "v0=<hex>" signature Slack sends, computed over
// "v0:<timestamp>:<body>".
func computeSlackSignature(body []byte, timestamp string, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":" + string(body)))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// flowRunCount returns the number of FlowRun objects in the fake client.
func flowRunCount(t *testing.T, k8s client.Client) int {
	t.Helper()
	list := &automationv1alpha1.FlowRunList{}
	if err := k8s.List(t.Context(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	return len(list.Items)
}

// computeHMACSignature returns the "sha256=<hex>" signature for the given body and secret.
func computeHMACSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHMACAuth(t *testing.T) {
	const secret = "test-webhook-secret"

	tests := []struct {
		name        string
		signature   string // value for X-Hub-Signature-256; empty string means omit the header
		omitHeader  bool   // explicitly omit the header even if signature is non-empty
		wantStatus  int
		wantFlowRun bool
	}{
		{
			name:        "valid HMAC signature",
			wantStatus:  http.StatusAccepted,
			wantFlowRun: true,
			// signature computed dynamically below
		},
		{
			name:        "wrong HMAC signature",
			signature:   "sha256=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
		{
			name:        "missing X-Hub-Signature-256 header",
			omitHeader:  true,
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithHMAC(t, secret)

			body := []byte(`{"event":"push","ref":"refs/heads/main"}`)
			req := httptest.NewRequest(http.MethodPost, "/hooks/hmac-test", bytes.NewReader(body))

			if !tc.omitHeader {
				sig := tc.signature
				if sig == "" {
					// Compute the correct signature for the "valid" case.
					sig = computeHMACSignature(body, secret)
				}
				req.Header.Set("X-Hub-Signature-256", sig)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// decodeErrorMsg decodes a JSON {"error": "..."} response body and returns the message.
func decodeErrorMsg(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding error response body: %v", err)
	}
	return resp[errorJSONKey]
}

func TestBearerAuth(t *testing.T) {
	const token = "test-bearer-token"

	tests := []struct {
		name        string
		authHeader  string // value to send in the Authorization header
		omitHeader  bool   // explicitly omit the header even if authHeader is non-empty
		wantStatus  int
		wantMsg     string
		wantFlowRun bool
	}{
		{
			name:        "valid bearer token",
			authHeader:  "Bearer " + token,
			wantStatus:  http.StatusAccepted,
			wantFlowRun: true,
		},
		{
			name:        "wrong bearer token",
			authHeader:  "Bearer wrong-token",
			wantStatus:  http.StatusUnauthorized,
			wantMsg:     "invalid bearer token",
			wantFlowRun: false,
		},
		{
			name:        "missing Authorization header",
			omitHeader:  true,
			wantStatus:  http.StatusUnauthorized,
			wantMsg:     "missing Authorization header",
			wantFlowRun: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithBearer(t, token)

			req := httptest.NewRequest(http.MethodPost, "/hooks/bearer-test", bytes.NewBufferString(`{}`))
			if !tc.omitHeader {
				req.Header.Set("Authorization", tc.authHeader)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantMsg != "" {
				if msg := decodeErrorMsg(t, rr); msg != tc.wantMsg {
					t.Errorf("expected error message %q, got %q", tc.wantMsg, msg)
				}
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// newTestHandlerWithBearer creates a WebhookHandler with a fake k8s client and a
// registry containing a single route at /hooks/bearer-test with bearer-token auth
// and POST method.
func newTestHandlerWithBearer(t *testing.T, token string) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/bearer-test", RouteEntry{
		TriggerName:      "bearer-trigger",
		TriggerNamespace: "default",
		FlowRef:          "bearer-flow",
		AllowedMethod:    "POST",
		AuthType:         authTypeBearer,
		BearerToken:      token,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

func TestAPIKeyAuth(t *testing.T) {
	const key = "test-api-key"

	tests := []struct {
		name        string
		headerName  string // header name configured on the route; "" uses the default X-Api-Key
		sendHeader  string // header name the request sets
		sendValue   string
		omitHeader  bool
		wantStatus  int
		wantMsg     string
		wantFlowRun bool
	}{
		{
			name:        "valid API key, default header",
			sendHeader:  "X-Api-Key",
			sendValue:   key,
			wantStatus:  http.StatusAccepted,
			wantFlowRun: true,
		},
		{
			name:        "wrong API key",
			sendHeader:  "X-Api-Key",
			sendValue:   "wrong-key",
			wantStatus:  http.StatusUnauthorized,
			wantMsg:     "invalid API key",
			wantFlowRun: false,
		},
		{
			name:        "missing API key header",
			omitHeader:  true,
			wantStatus:  http.StatusUnauthorized,
			wantMsg:     "missing API key header",
			wantFlowRun: false,
		},
		{
			name:        "valid API key via custom header",
			headerName:  "X-Custom-Key",
			sendHeader:  "X-Custom-Key",
			sendValue:   key,
			wantStatus:  http.StatusAccepted,
			wantFlowRun: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithAPIKey(t, key, tc.headerName)

			req := httptest.NewRequest(http.MethodPost, "/hooks/apikey-test", bytes.NewBufferString(`{}`))
			if !tc.omitHeader {
				req.Header.Set(tc.sendHeader, tc.sendValue)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantMsg != "" {
				if msg := decodeErrorMsg(t, rr); msg != tc.wantMsg {
					t.Errorf("expected error message %q, got %q", tc.wantMsg, msg)
				}
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// newTestHandlerWithAPIKey creates a WebhookHandler with a fake k8s client and a
// registry containing a single route at /hooks/apikey-test with API-key auth and
// POST method. headerName configures RouteEntry.APIKeyHeader; "" exercises the
// production default of "X-Api-Key".
func newTestHandlerWithAPIKey(t *testing.T, key, headerName string) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/apikey-test", RouteEntry{
		TriggerName:      "apikey-trigger",
		TriggerNamespace: "default",
		FlowRef:          "apikey-flow",
		AllowedMethod:    "POST",
		AuthType:         authTypeAPIKey,
		APIKey:           key,
		APIKeyHeader:     headerName,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

func TestBasicAuth(t *testing.T) {
	const username = "testuser"
	const password = "testpass"

	tests := []struct {
		name          string
		setBasicAuth  bool
		authUsername  string
		authPassword  string
		malformedAuth string // raw Authorization header value; used instead of SetBasicAuth when non-empty
		wantStatus    int
		wantMsg       string
		wantFlowRun   bool
	}{
		{
			name:         "valid username and password",
			setBasicAuth: true,
			authUsername: username,
			authPassword: password,
			wantStatus:   http.StatusAccepted,
			wantFlowRun:  true,
		},
		{
			name:         "wrong username",
			setBasicAuth: true,
			authUsername: "wrong-user",
			authPassword: password,
			wantStatus:   http.StatusUnauthorized,
			wantMsg:      "invalid Basic auth credentials",
			wantFlowRun:  false,
		},
		{
			name:         "wrong password",
			setBasicAuth: true,
			authUsername: username,
			authPassword: "wrong-pass",
			wantStatus:   http.StatusUnauthorized,
			wantMsg:      "invalid Basic auth credentials",
			wantFlowRun:  false,
		},
		{
			name:        "missing Authorization header",
			wantStatus:  http.StatusUnauthorized,
			wantMsg:     "missing or malformed Basic auth credentials",
			wantFlowRun: false,
		},
		{
			name:          "malformed Authorization header",
			malformedAuth: "Basic not-valid-base64!!!",
			wantStatus:    http.StatusUnauthorized,
			wantMsg:       "missing or malformed Basic auth credentials",
			wantFlowRun:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithBasic(t, username, password)

			req := httptest.NewRequest(http.MethodPost, "/hooks/basic-test", bytes.NewBufferString(`{}`))
			switch {
			case tc.setBasicAuth:
				req.SetBasicAuth(tc.authUsername, tc.authPassword)
			case tc.malformedAuth != "":
				req.Header.Set("Authorization", tc.malformedAuth)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantMsg != "" {
				if msg := decodeErrorMsg(t, rr); msg != tc.wantMsg {
					t.Errorf("expected error message %q, got %q", tc.wantMsg, msg)
				}
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// newTestHandlerWithBasic creates a WebhookHandler with a fake k8s client and a
// registry containing a single route at /hooks/basic-test with Basic auth and
// POST method.
func newTestHandlerWithBasic(t *testing.T, username, password string) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/basic-test", RouteEntry{
		TriggerName:      "basic-trigger",
		TriggerNamespace: "default",
		FlowRef:          "basic-flow",
		AllowedMethod:    "POST",
		AuthType:         authTypeBasic,
		BasicUsername:    username,
		BasicPassword:    password,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

func TestHeaderEqualsAuth(t *testing.T) {
	const headerName = "X-Custom-Auth"
	const headerValue = "expected-value"

	tests := []struct {
		name            string
		routeHeaderName string // header name configured on the route; "" exercises the misconfigured case
		sendValue       string
		omitHeader      bool
		wantStatus      int
		wantMsg         string
		wantFlowRun     bool
	}{
		{
			name:            "valid header value",
			routeHeaderName: headerName,
			sendValue:       headerValue,
			wantStatus:      http.StatusAccepted,
			wantFlowRun:     true,
		},
		{
			name:            "wrong header value",
			routeHeaderName: headerName,
			sendValue:       "wrong-value",
			wantStatus:      http.StatusUnauthorized,
			wantMsg:         "invalid header value",
			wantFlowRun:     false,
		},
		{
			name:            "missing required header",
			routeHeaderName: headerName,
			omitHeader:      true,
			wantStatus:      http.StatusUnauthorized,
			wantMsg:         "missing required header",
			wantFlowRun:     false,
		},
		{
			name:            "misconfigured: no header name",
			routeHeaderName: "",
			sendValue:       headerValue,
			wantStatus:      http.StatusUnauthorized,
			wantMsg:         "header-equals auth misconfigured: no header name",
			wantFlowRun:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithHeaderEquals(t, tc.routeHeaderName, headerValue)

			req := httptest.NewRequest(http.MethodPost, "/hooks/headereq-test", bytes.NewBufferString(`{}`))
			if !tc.omitHeader {
				// The route's configured header name is what the request must match
				// against; in the misconfigured case (no header name configured on
				// the route) the request still sends a header, but the entry has
				// nothing to compare it to and must fail closed.
				sendOn := tc.routeHeaderName
				if sendOn == "" {
					sendOn = headerName
				}
				req.Header.Set(sendOn, tc.sendValue)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantMsg != "" {
				if msg := decodeErrorMsg(t, rr); msg != tc.wantMsg {
					t.Errorf("expected error message %q, got %q", tc.wantMsg, msg)
				}
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// newTestHandlerWithHeaderEquals creates a WebhookHandler with a fake k8s client and
// a registry containing a single route at /hooks/headereq-test with header-equals
// auth and POST method. An empty headerName reproduces the misconfigured case
// (AuthType set but no header name to compare against).
func newTestHandlerWithHeaderEquals(t *testing.T, headerName, headerValue string) (*WebhookHandler, client.Client) {
	t.Helper()
	scheme := newWebhookTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	log := zap.New()
	registry := NewRouteRegistry(log)
	registry.Register("/hooks/headereq-test", RouteEntry{
		TriggerName:        "headereq-trigger",
		TriggerNamespace:   "default",
		FlowRef:            "headereq-flow",
		AllowedMethod:      "POST",
		AuthType:           authTypeHeaderEquals,
		HeaderEqualsHeader: headerName,
		HeaderEqualsValue:  headerValue,
	})
	h := NewWebhookHandler(fakeClient, registry, log, nil, 0)
	return h, fakeClient
}

func TestSlackHMACAuth(t *testing.T) {
	const secret = "test-slack-signing-secret"

	tests := []struct {
		name        string
		timestamp   func() string
		signature   func(body []byte, ts string) string
		omitSig     bool
		omitTS      bool
		wantStatus  int
		wantFlowRun bool
	}{
		{
			name:        "valid Slack signature",
			timestamp:   func() string { return fmt.Sprintf("%d", time.Now().Unix()) },
			signature:   func(body []byte, ts string) string { return computeSlackSignature(body, ts, secret) },
			wantStatus:  http.StatusAccepted,
			wantFlowRun: true,
		},
		{
			name:      "wrong Slack signature",
			timestamp: func() string { return fmt.Sprintf("%d", time.Now().Unix()) },
			signature: func(body []byte, ts string) string {
				return "v0=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
			},
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
		{
			name:        "missing X-Slack-Signature header",
			timestamp:   func() string { return fmt.Sprintf("%d", time.Now().Unix()) },
			omitSig:     true,
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
		{
			name:        "missing X-Slack-Request-Timestamp header",
			timestamp:   func() string { return fmt.Sprintf("%d", time.Now().Unix()) },
			signature:   func(body []byte, ts string) string { return computeSlackSignature(body, ts, secret) },
			omitTS:      true,
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
		{
			name:        "stale timestamp rejected as replay",
			timestamp:   func() string { return fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix()) },
			signature:   func(body []byte, ts string) string { return computeSlackSignature(body, ts, secret) },
			wantStatus:  http.StatusUnauthorized,
			wantFlowRun: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandlerWithSlackHMAC(t, secret, 300)

			body := []byte("command=%2Fkubezap&text=deploy+staging")
			req := httptest.NewRequest(http.MethodPost, "/hooks/slack-test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			ts := tc.timestamp()
			if !tc.omitTS {
				req.Header.Set("X-Slack-Request-Timestamp", ts)
			}
			if !tc.omitSig {
				req.Header.Set("X-Slack-Signature", tc.signature(body, ts))
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}

			count := flowRunCount(t, k8s)
			if tc.wantFlowRun && count == 0 {
				t.Fatal("expected a FlowRun to be created, but none found")
			}
			if !tc.wantFlowRun && count != 0 {
				t.Fatalf("expected no FlowRun to be created, but found %d", count)
			}
		})
	}
}

// TestFlowRunNameUniqueness fires 500 concurrent webhook requests at the same Trigger
// and asserts that all 500 FlowRuns are created with unique names. This validates that
// randomHex(8) provides sufficient entropy to avoid AlreadyExists collisions under load.
func TestFlowRunNameUniqueness(t *testing.T) {
	h, k8s := newTestHandler(t)

	const concurrency = 500
	var wg sync.WaitGroup
	wg.Add(concurrency)

	type result struct {
		status  int
		flowRun string
	}
	results := make([]result, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"seq":%d}`, idx)
			req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(body))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			var resp map[string]string
			_ = json.NewDecoder(rr.Body).Decode(&resp)
			results[idx] = result{status: rr.Code, flowRun: resp["flowRun"]}
		}(i)
	}
	wg.Wait()

	// Assert all responses are 202.
	for i, r := range results {
		if r.status != http.StatusAccepted {
			t.Errorf("request %d: expected 202, got %d", i, r.status)
		}
	}

	// Assert all FlowRun names from responses are unique.
	seen := make(map[string]struct{}, concurrency)
	for i, r := range results {
		if _, dup := seen[r.flowRun]; dup {
			t.Errorf("duplicate FlowRun name in response %d: %s", i, r.flowRun)
		}
		seen[r.flowRun] = struct{}{}
	}

	// Assert the fake client has exactly 500 FlowRuns.
	count := flowRunCount(t, k8s)
	if count != concurrency {
		t.Fatalf("expected %d FlowRuns in fake client, got %d", concurrency, count)
	}

	// Double-check uniqueness from the server side.
	list := &automationv1alpha1.FlowRunList{}
	if err := k8s.List(t.Context(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	serverNames := make(map[string]struct{}, len(list.Items))
	for _, fr := range list.Items {
		if _, dup := serverNames[fr.Name]; dup {
			t.Errorf("duplicate FlowRun name in fake client: %s", fr.Name)
		}
		serverNames[fr.Name] = struct{}{}
	}
}

// TestCooldownWindowSuppression verifies that the cooldown policy limits the number of
// FlowRuns created within a time window and increments the rate-limited metric for
// suppressed requests.
func TestCooldownWindowSuppression(t *testing.T) {
	tests := []struct {
		name              string
		maxInvocations    int32
		window            time.Duration
		totalRequests     int
		wantFlowRuns      int
		wantRateLimited   int
		wantAcceptedCodes int // number of 202 responses
		wantLimitedCodes  int // number of 429 responses
	}{
		{
			name:              "2 allowed out of 5 in 10s window",
			maxInvocations:    2,
			window:            10 * time.Second,
			totalRequests:     5,
			wantFlowRuns:      2,
			wantRateLimited:   3,
			wantAcceptedCodes: 2,
			wantLimitedCodes:  3,
		},
		{
			name:              "1 allowed out of 3 in 60s window",
			maxInvocations:    1,
			window:            60 * time.Second,
			totalRequests:     3,
			wantFlowRuns:      1,
			wantRateLimited:   2,
			wantAcceptedCodes: 1,
			wantLimitedCodes:  2,
		},
		{
			name:              "no limit when maxInvocations is 0",
			maxInvocations:    0,
			window:            10 * time.Second,
			totalRequests:     5,
			wantFlowRuns:      5,
			wantRateLimited:   0,
			wantAcceptedCodes: 5,
			wantLimitedCodes:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newWebhookTestScheme()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			log := zap.New()
			registry := NewRouteRegistry(log)

			registry.Register("/hooks/cooldown-test", RouteEntry{
				TriggerName:      "cooldown-trigger",
				TriggerNamespace: "default",
				FlowRef:          "test-flow",
				AllowedMethod:    "POST",
				MaxInvocations:   tc.maxInvocations,
				CooldownWindow:   tc.window,
			})

			h := NewWebhookHandler(fakeClient, registry, log, nil, 0)

			// Snapshot the rate-limited metric before firing requests.
			rateLimitedBefore := testutil.ToFloat64(
				metrics.WebhookRateLimited.WithLabelValues("cooldown-trigger", "default"),
			)

			acceptedCount := 0
			limitedCount := 0

			for i := 0; i < tc.totalRequests; i++ {
				req := httptest.NewRequest(http.MethodPost, "/hooks/cooldown-test",
					bytes.NewBufferString(`{"seq":`+strings.Repeat("x", i)+`}`))
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)

				switch rr.Code {
				case http.StatusAccepted:
					acceptedCount++
				case http.StatusTooManyRequests:
					limitedCount++
				default:
					t.Fatalf("request %d: unexpected status %d; body: %s", i, rr.Code, rr.Body.String())
				}
			}

			// Assert response code counts.
			if acceptedCount != tc.wantAcceptedCodes {
				t.Errorf("expected %d accepted (202) responses, got %d", tc.wantAcceptedCodes, acceptedCount)
			}
			if limitedCount != tc.wantLimitedCodes {
				t.Errorf("expected %d rate-limited (429) responses, got %d", tc.wantLimitedCodes, limitedCount)
			}

			// Assert FlowRun count.
			count := flowRunCount(t, fakeClient)
			if count != tc.wantFlowRuns {
				t.Errorf("expected %d FlowRuns, got %d", tc.wantFlowRuns, count)
			}

			// Assert rate-limited metric delta.
			rateLimitedAfter := testutil.ToFloat64(
				metrics.WebhookRateLimited.WithLabelValues("cooldown-trigger", "default"),
			)
			rateLimitedDelta := int(rateLimitedAfter - rateLimitedBefore)
			if rateLimitedDelta != tc.wantRateLimited {
				t.Errorf("expected kubezap_webhook_rate_limited_total to increment by %d, got %d",
					tc.wantRateLimited, rateLimitedDelta)
			}
		})
	}
}

// TestTraceparentPropagation verifies that the W3C traceparent header is stored as an
// annotation on the created FlowRun, and that requests without it create FlowRuns
// with no traceparent annotation.
func TestTraceparentPropagation(t *testing.T) {
	const sampleTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const sampleTracestate = "rojo=00f067aa0ba902b7,congo=t61rcWkgMzE"

	tests := []struct {
		name              string
		traceparentHeader string
		tracestateHeader  string
		wantTraceparent   string
		wantTracestate    string
	}{
		{
			name:              "traceparent and tracestate both present",
			traceparentHeader: sampleTraceparent,
			tracestateHeader:  sampleTracestate,
			wantTraceparent:   sampleTraceparent,
			wantTracestate:    sampleTracestate,
		},
		{
			name:              "traceparent only",
			traceparentHeader: sampleTraceparent,
			wantTraceparent:   sampleTraceparent,
			wantTracestate:    "",
		},
		{
			name:            "no trace headers",
			wantTraceparent: "",
			wantTracestate:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, k8s := newTestHandler(t)

			req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(`{}`))
			if tc.traceparentHeader != "" {
				req.Header.Set("Traceparent", tc.traceparentHeader)
			}
			if tc.tracestateHeader != "" {
				req.Header.Set("Tracestate", tc.tracestateHeader)
			}

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusAccepted {
				t.Fatalf("expected 202, got %d; body: %s", rr.Code, rr.Body.String())
			}

			fr := lastCreatedFlowRun(t, k8s)

			gotTraceparent := fr.Annotations["kubezap.io/traceparent"]
			if gotTraceparent != tc.wantTraceparent {
				t.Errorf("kubezap.io/traceparent: want %q, got %q", tc.wantTraceparent, gotTraceparent)
			}

			gotTracestate := fr.Annotations["kubezap.io/tracestate"]
			if gotTracestate != tc.wantTracestate {
				t.Errorf("kubezap.io/tracestate: want %q, got %q", tc.wantTracestate, gotTracestate)
			}
		})
	}
}

// TestTraceparentCaseInsensitive verifies that lowercase "traceparent" header is also accepted,
// since Go's net/http canonicalises header names automatically.
func TestTraceparentCaseInsensitive(t *testing.T) {
	const sampleTraceparent = "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01"

	h, k8s := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(`{}`))
	// Set using lowercase — Go's http package will canonicalise to "Traceparent".
	req.Header.Set("traceparent", sampleTraceparent)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	fr := lastCreatedFlowRun(t, k8s)
	if got := fr.Annotations["kubezap.io/traceparent"]; got != sampleTraceparent {
		t.Errorf("expected kubezap.io/traceparent=%q, got %q", sampleTraceparent, got)
	}
}

// TestWebhookRequestDurationHistogram verifies that the kubezap_webhook_request_duration_seconds
// histogram is observed with the correct result label for accepted and rejected requests.
func TestWebhookRequestDurationHistogram(t *testing.T) {
	tests := []struct {
		name         string
		setupHandler func(t *testing.T) (*WebhookHandler, client.Client)
		buildRequest func() *http.Request
		wantResult   string
		wantStatus   int
	}{
		{
			name:         "accepted request increments accepted histogram",
			setupHandler: newTestHandler,
			buildRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/hooks/test", bytes.NewBufferString(`{}`))
			},
			wantResult: "accepted",
			wantStatus: http.StatusAccepted,
		},
		{
			name: "auth failure increments rejected histogram",
			setupHandler: func(t *testing.T) (*WebhookHandler, client.Client) {
				t.Helper()
				return newTestHandlerWithHMAC(t, "secret")
			},
			buildRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/hooks/hmac-test", bytes.NewBufferString(`{}`))
				req.Header.Set("X-Hub-Signature-256", "sha256=badhash")
				return req
			},
			wantResult: "rejected",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "rate limited request increments rate_limited histogram",
			setupHandler: func(t *testing.T) (*WebhookHandler, client.Client) {
				t.Helper()
				scheme := newWebhookTestScheme()
				fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
				log := zap.New()
				registry := NewRouteRegistry(log)
				registry.Register("/hooks/rl-hist", RouteEntry{
					TriggerName:      "rl-trigger",
					TriggerNamespace: "default",
					FlowRef:          "test-flow",
					AllowedMethod:    "POST",
					MaxInvocations:   1,
					CooldownWindow:   10 * time.Second,
				})
				return NewWebhookHandler(fakeClient, registry, log, nil, 0), fakeClient
			},
			buildRequest: func() *http.Request {
				// Second request will be rate-limited; the test sends two below.
				return httptest.NewRequest(http.MethodPost, "/hooks/rl-hist", bytes.NewBufferString(`{}`))
			},
			wantResult: metricResultRateLimited,
			wantStatus: http.StatusTooManyRequests,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := tc.setupHandler(t)

			// For the rate-limited case, fire a first request (should be accepted) then
			// a second (should be rate-limited). We only care about the second one's label.
			if tc.wantResult == metricResultRateLimited {
				first := httptest.NewRequest(http.MethodPost, "/hooks/rl-hist", bytes.NewBufferString(`{}`))
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, first)
				if rr.Code != http.StatusAccepted {
					t.Fatalf("first request: expected 202, got %d", rr.Code)
				}
			}

			triggerLabel := func() string {
				switch tc.wantResult {
				case metricResultRateLimited:
					return "rl-trigger"
				case "rejected":
					return "hmac-trigger"
				default:
					return "test-trigger"
				}
			}()

			// Snapshot SampleCount before the request under test.
			countBefore := webhookDurationSampleCount(t, triggerLabel, tc.wantResult)

			req := tc.buildRequest()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}

			countAfter := webhookDurationSampleCount(t, triggerLabel, tc.wantResult)
			if countAfter <= countBefore {
				t.Errorf("expected kubezap_webhook_request_duration_seconds{trigger=%q,result=%q} sample_count to increase; before=%d after=%d",
					triggerLabel, tc.wantResult, countBefore, countAfter)
			}
		})
	}
}

// webhookDurationSampleCount gathers the kubezap_webhook_request_duration_seconds histogram
// from the controller-runtime registry and returns the sample_count for the given label pair.
// Returns 0 if the time series does not yet exist.
func webhookDurationSampleCount(t *testing.T, trigger, result string) uint64 {
	t.Helper()
	mfs, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "kubezap_webhook_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var gotTrigger, gotResult string
			for _, lp := range m.GetLabel() {
				switch lp.GetName() {
				case "trigger":
					gotTrigger = lp.GetValue()
				case "result":
					gotResult = lp.GetValue()
				}
			}
			if gotTrigger == trigger && gotResult == result {
				if h := m.GetHistogram(); h != nil {
					return h.GetSampleCount()
				}
			}
		}
	}
	return 0
}
