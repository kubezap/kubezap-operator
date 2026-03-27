package webhook

import (
	"net/http"
	"testing"
)

// TestBuildRedactedTriggerData_BuiltInHeaders verifies that built-in sensitive headers
// are always replaced with "[REDACTED]", regardless of the redactExtra list.
func TestBuildRedactedTriggerData_BuiltInHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization":        {"Bearer secret-token"},
		"X-Api-Key":            {"my-api-key"},
		"X-Webhook-Secret":     {"my-webhook-secret"},
		"X-Hub-Signature":      {"sha1=abc123"},
		"X-Hub-Signature-256":  {"sha256=def456"},
		"X-Amz-Security-Token": {"amz-token"},
		"Cookie":               {"session=abc"},
		"Set-Cookie":           {"session=abc; Path=/"},
		"X-Auth-Token":         {"auth-token"},
		"Proxy-Authorization":  {"Basic xyz"},
		"Content-Type":         {"application/json"},
		"X-Request-Id":         {"req-123"},
	}

	out, body := buildRedactedTriggerData(headers, `{"key":"value"}`, nil, false)

	sensitiveKeys := []string{
		"Authorization",
		"X-Api-Key",
		"X-Webhook-Secret",
		"X-Hub-Signature",
		"X-Hub-Signature-256",
		"X-Amz-Security-Token",
		"Cookie",
		"Set-Cookie",
		"X-Auth-Token",
		"Proxy-Authorization",
	}
	for _, k := range sensitiveKeys {
		if out[k] != "[REDACTED]" {
			t.Errorf("expected header %q to be [REDACTED], got %q", k, out[k])
		}
	}

	// Non-sensitive headers should pass through.
	if out["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type to pass through, got %q", out["Content-Type"])
	}
	if out["X-Request-Id"] != "req-123" {
		t.Errorf("expected X-Request-Id to pass through, got %q", out["X-Request-Id"])
	}

	// Body should pass through when redactBody=false.
	if body != `{"key":"value"}` {
		t.Errorf("expected body to pass through, got %q", body)
	}
}

// TestBuildRedactedTriggerData_CustomHeaders verifies that additional headers listed in
// redactExtra are redacted in addition to the built-in list.
func TestBuildRedactedTriggerData_CustomHeaders(t *testing.T) {
	headers := http.Header{
		"X-Custom-Token": {"custom-secret"},
		"X-My-Api-Key":   {"my-key"},
		"Content-Type":   {"application/json"},
	}

	out, _ := buildRedactedTriggerData(headers, `{}`, []string{"X-Custom-Token", "X-My-Api-Key"}, false)

	if out["X-Custom-Token"] != "[REDACTED]" {
		t.Errorf("expected X-Custom-Token to be [REDACTED], got %q", out["X-Custom-Token"])
	}
	if out["X-My-Api-Key"] != "[REDACTED]" {
		t.Errorf("expected X-My-Api-Key to be [REDACTED], got %q", out["X-My-Api-Key"])
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type to pass through, got %q", out["Content-Type"])
	}
}

// TestBuildRedactedTriggerData_CaseInsensitive verifies that header name matching is
// case-insensitive for both built-in and custom entries.
func TestBuildRedactedTriggerData_CaseInsensitive(t *testing.T) {
	// Go's net/http canonicalises headers to title-case, but we test lower-case input
	// in redactExtra to confirm our strings.ToLower handling works.
	headers := http.Header{
		"Authorization":  {"secret"},
		"X-Custom-Token": {"custom"},
	}

	// Pass the custom header in all-lowercase in redactExtra.
	out, _ := buildRedactedTriggerData(headers, `{}`, []string{"x-custom-token"}, false)

	if out["Authorization"] != "[REDACTED]" {
		t.Errorf("expected Authorization to be [REDACTED], got %q", out["Authorization"])
	}
	if out["X-Custom-Token"] != "[REDACTED]" {
		t.Errorf("expected X-Custom-Token to be [REDACTED] (case-insensitive match), got %q", out["X-Custom-Token"])
	}
}

// TestBuildRedactedTriggerData_BodyPassthrough verifies that body is returned unchanged
// when redactBody is false.
func TestBuildRedactedTriggerData_BodyPassthrough(t *testing.T) {
	body := `{"username":"alice","action":"login"}`
	_, outBody := buildRedactedTriggerData(http.Header{}, body, nil, false)
	if outBody != body {
		t.Errorf("expected body to pass through unchanged, got %q", outBody)
	}
}

// TestBuildRedactedTriggerData_BodyRedacted verifies that body is replaced with
// "[REDACTED]" when redactBody is true.
func TestBuildRedactedTriggerData_BodyRedacted(t *testing.T) {
	body := `{"password":"super-secret"}`
	_, outBody := buildRedactedTriggerData(http.Header{}, body, nil, true)
	if outBody != "[REDACTED]" {
		t.Errorf("expected body to be [REDACTED], got %q", outBody)
	}
}

// TestBuildRedactedTriggerData_EmptyBody verifies that an empty body is returned as-is
// when redactBody is false.
func TestBuildRedactedTriggerData_EmptyBody(t *testing.T) {
	_, outBody := buildRedactedTriggerData(http.Header{}, "", nil, false)
	if outBody != "" {
		t.Errorf("expected empty body to pass through, got %q", outBody)
	}
}

// TestBuildRedactedTriggerData_NoHeaders verifies that an empty header map produces an
// empty output map.
func TestBuildRedactedTriggerData_NoHeaders(t *testing.T) {
	out, _ := buildRedactedTriggerData(http.Header{}, `{}`, nil, false)
	if len(out) != 0 {
		t.Errorf("expected empty output map, got %v", out)
	}
}

// TestBuildRedactedTriggerData_MultiValueHeader verifies that multi-value headers are
// joined with commas and not redacted when not in the sensitive list.
func TestBuildRedactedTriggerData_MultiValueHeader(t *testing.T) {
	headers := http.Header{
		"Accept": {"text/html", "application/json"},
	}

	out, _ := buildRedactedTriggerData(headers, `{}`, nil, false)
	if out["Accept"] != "text/html,application/json" {
		t.Errorf("expected multi-value header joined with comma, got %q", out["Accept"])
	}
}

// TestBuildRedactedTriggerData_EndToEnd exercises the helper via table-driven cases
// that mirror realistic RouteEntry configurations.
func TestBuildRedactedTriggerData_EndToEnd(t *testing.T) {
	makeHeaders := func(kvs map[string]string) http.Header {
		h := http.Header{}
		for k, v := range kvs {
			h.Set(k, v)
		}
		return h
	}

	tests := []struct {
		name         string
		redactExtra  []string
		redactBody   bool
		inputHeaders map[string]string
		inputBody    string
		wantHeaders  map[string]string // key → expected value
		wantBody     string
	}{
		{
			name:        "built-in authorization header is redacted",
			redactExtra: nil,
			redactBody:  false,
			inputHeaders: map[string]string{
				"Authorization": "Bearer my-token",
				"Content-Type":  "application/json",
			},
			inputBody: `{"event":"push"}`,
			wantHeaders: map[string]string{
				"Authorization": "[REDACTED]",
				"Content-Type":  "application/json",
			},
			wantBody: `{"event":"push"}`,
		},
		{
			name:        "custom header from redactExtra is redacted",
			redactExtra: []string{"X-Custom-Token"},
			redactBody:  false,
			inputHeaders: map[string]string{
				"X-Custom-Token": "should-be-redacted",
				"X-Request-Id":   "req-001",
			},
			inputBody: `{"data":"ok"}`,
			wantHeaders: map[string]string{
				"X-Custom-Token": "[REDACTED]",
				"X-Request-Id":   "req-001",
			},
			wantBody: `{"data":"ok"}`,
		},
		{
			name:        "body is replaced with [REDACTED] when redactBody=true",
			redactExtra: nil,
			redactBody:  true,
			inputHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			inputBody: `{"password":"super-secret"}`,
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			wantBody: "[REDACTED]",
		},
		{
			name:        "custom and built-in headers both redacted together",
			redactExtra: []string{"X-Custom-Token"},
			redactBody:  false,
			inputHeaders: map[string]string{
				"Authorization":  "Bearer tok",
				"X-Custom-Token": "custom",
				"Accept":         "application/json",
			},
			inputBody: `{}`,
			wantHeaders: map[string]string{
				"Authorization":  "[REDACTED]",
				"X-Custom-Token": "[REDACTED]",
				"Accept":         "application/json",
			},
			wantBody: `{}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, outBody := buildRedactedTriggerData(makeHeaders(tc.inputHeaders), tc.inputBody, tc.redactExtra, tc.redactBody)

			for k, want := range tc.wantHeaders {
				if got := out[k]; got != want {
					t.Errorf("header %q: want %q, got %q", k, want, got)
				}
			}
			if outBody != tc.wantBody {
				t.Errorf("body: want %q, got %q", tc.wantBody, outBody)
			}
		})
	}
}
