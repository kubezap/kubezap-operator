package redact_test

import (
	"net/http"
	"testing"

	"github.com/kubezap/kubezap-operator/internal/gateway/redact"
)

// TestHeaders_BuiltInAlwaysRedacted verifies that every header in BuiltInHeaders
// is replaced with "[REDACTED]" regardless of what redactExtra contains.
func TestHeaders_BuiltInAlwaysRedacted(t *testing.T) {
	headers := http.Header{}
	for _, h := range redact.BuiltInHeaders {
		headers.Set(h, "sensitive-value")
	}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Request-Id", "req-123")

	out := redact.Headers(headers, nil)

	for _, h := range redact.BuiltInHeaders {
		// http.Header.Set canonicalises the key; use CanonicalHeaderKey so the
		// map lookup matches what Set wrote.
		key := http.CanonicalHeaderKey(h)
		if got := out[key]; got != "[REDACTED]" {
			t.Errorf("built-in header %q: want [REDACTED], got %q", key, got)
		}
	}

	// Non-sensitive headers must pass through.
	if out["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: want %q, got %q", "application/json", out["Content-Type"])
	}
	if out["X-Request-Id"] != "req-123" {
		t.Errorf("X-Request-Id: want %q, got %q", "req-123", out["X-Request-Id"])
	}
}

// TestHeaders_CustomHeaders verifies that names in redactExtra are also redacted.
func TestHeaders_CustomHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Custom-Token", "custom-secret")
	headers.Set("X-My-Api-Key", "my-key")
	headers.Set("Content-Type", "application/json")

	out := redact.Headers(headers, []string{"X-Custom-Token", "X-My-Api-Key"})

	if out["X-Custom-Token"] != "[REDACTED]" {
		t.Errorf("X-Custom-Token: want [REDACTED], got %q", out["X-Custom-Token"])
	}
	if out["X-My-Api-Key"] != "[REDACTED]" {
		t.Errorf("X-My-Api-Key: want [REDACTED], got %q", out["X-My-Api-Key"])
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", out["Content-Type"])
	}
}

// TestHeaders_CaseInsensitive verifies matching is case-insensitive for both
// built-in and extra header names.
func TestHeaders_CaseInsensitive(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("X-Custom-Token", "custom")

	// Supply extra header in all-lowercase.
	out := redact.Headers(headers, []string{"x-custom-token"})

	if out["Authorization"] != "[REDACTED]" {
		t.Errorf("Authorization: want [REDACTED], got %q", out["Authorization"])
	}
	if out["X-Custom-Token"] != "[REDACTED]" {
		t.Errorf("X-Custom-Token: want [REDACTED] (case-insensitive match), got %q", out["X-Custom-Token"])
	}
}

// TestHeaders_MultiValue verifies that multi-value headers are joined with commas
// when they are not in the redact list.
func TestHeaders_MultiValue(t *testing.T) {
	headers := http.Header{
		"Accept": {"text/html", "application/json"},
	}
	out := redact.Headers(headers, nil)
	if out["Accept"] != "text/html,application/json" {
		t.Errorf("Accept: want %q, got %q", "text/html,application/json", out["Accept"])
	}
}

// TestHeaders_EmptyInput verifies that empty headers produce an empty output map.
func TestHeaders_EmptyInput(t *testing.T) {
	out := redact.Headers(http.Header{}, nil)
	if len(out) != 0 {
		t.Errorf("expected empty map, got %v", out)
	}
}

// TestBody_Passthrough verifies that body is returned unchanged when redactBody is false.
func TestBody_Passthrough(t *testing.T) {
	body := `{"username":"alice","action":"login"}`
	got := redact.Body(body, false)
	if got != body {
		t.Errorf("Body(passthrough): want %q, got %q", body, got)
	}
}

// TestBody_Redacted verifies that body is replaced with "[REDACTED]" when redactBody is true.
func TestBody_Redacted(t *testing.T) {
	got := redact.Body(`{"password":"super-secret"}`, true)
	if got != "[REDACTED]" {
		t.Errorf("Body(redact): want [REDACTED], got %q", got)
	}
}

// TestBody_EmptyPassthrough verifies that an empty body passes through unchanged.
func TestBody_EmptyPassthrough(t *testing.T) {
	got := redact.Body("", false)
	if got != "" {
		t.Errorf("Body(empty, passthrough): want empty string, got %q", got)
	}
}

// TestBody_EmptyRedacted verifies that an empty body with redactBody=true still
// returns "[REDACTED]".
func TestBody_EmptyRedacted(t *testing.T) {
	got := redact.Body("", true)
	if got != "[REDACTED]" {
		t.Errorf("Body(empty, redact): want [REDACTED], got %q", got)
	}
}

// TestStringMap_BuiltInRedacted verifies that StringMap redacts built-in header
// names from a plain map[string]string regardless of case.
func TestStringMap_BuiltInRedacted(t *testing.T) {
	cases := []struct {
		key string
	}{
		{"authorization"},
		{"Authorization"},
		{"AUTHORIZATION"},
		{"X-Api-Key"},
		{"x-api-key"},
		{"Cookie"},
		{"X-Hub-Signature-256"},
	}

	for _, tc := range cases {
		hdrs := map[string]string{tc.key: "should-be-redacted"}
		redact.StringMap(hdrs, nil)
		if hdrs[tc.key] != "[REDACTED]" {
			t.Errorf("StringMap key %q: want [REDACTED], got %q", tc.key, hdrs[tc.key])
		}
	}
}

// TestStringMap_NonSensitivePassthrough verifies that non-sensitive keys are
// left unchanged by StringMap.
func TestStringMap_NonSensitivePassthrough(t *testing.T) {
	hdrs := map[string]string{
		"content-type": "application/json",
		"x-request-id": "req-42",
	}
	redact.StringMap(hdrs, nil)
	if hdrs["content-type"] != "application/json" {
		t.Errorf("content-type: want application/json, got %q", hdrs["content-type"])
	}
	if hdrs["x-request-id"] != "req-42" {
		t.Errorf("x-request-id: want req-42, got %q", hdrs["x-request-id"])
	}
}

// TestStringMap_ExtraHeaders verifies that redactExtra names are also redacted by StringMap.
func TestStringMap_ExtraHeaders(t *testing.T) {
	hdrs := map[string]string{
		"x-tenant-secret": "secret-value",
		"content-type":    "text/plain",
	}
	redact.StringMap(hdrs, []string{"x-tenant-secret"})
	if hdrs["x-tenant-secret"] != "[REDACTED]" {
		t.Errorf("x-tenant-secret: want [REDACTED], got %q", hdrs["x-tenant-secret"])
	}
	if hdrs["content-type"] != "text/plain" {
		t.Errorf("content-type: want text/plain, got %q", hdrs["content-type"])
	}
}

// TestStringMap_Empty verifies that StringMap on an empty map is a no-op.
func TestStringMap_Empty(t *testing.T) {
	hdrs := map[string]string{}
	redact.StringMap(hdrs, nil)
	if len(hdrs) != 0 {
		t.Errorf("expected empty map, got %v", hdrs)
	}
}

// TestHeaders_TableDriven exercises Headers via realistic RouteEntry-like scenarios.
func TestHeaders_TableDriven(t *testing.T) {
	makeHeaders := func(kvs map[string]string) http.Header {
		h := http.Header{}
		for k, v := range kvs {
			h.Set(k, v)
		}
		return h
	}

	tests := []struct {
		name        string
		redactExtra []string
		redactBody  bool
		inputHdrs   map[string]string
		inputBody   string
		wantHdrs    map[string]string
		wantBody    string
	}{
		{
			name:      "built-in authorization header is redacted",
			inputHdrs: map[string]string{"Authorization": "Bearer my-token", "Content-Type": "application/json"},
			inputBody: `{"event":"push"}`,
			wantHdrs:  map[string]string{"Authorization": "[REDACTED]", "Content-Type": "application/json"},
			wantBody:  `{"event":"push"}`,
		},
		{
			name:        "custom header from redactExtra is redacted",
			redactExtra: []string{"X-Custom-Token"},
			inputHdrs:   map[string]string{"X-Custom-Token": "should-be-redacted", "X-Request-Id": "req-001"},
			inputBody:   `{"data":"ok"}`,
			wantHdrs:    map[string]string{"X-Custom-Token": "[REDACTED]", "X-Request-Id": "req-001"},
			wantBody:    `{"data":"ok"}`,
		},
		{
			name:       "body is replaced when redactBody=true",
			inputHdrs:  map[string]string{"Content-Type": "application/json"},
			inputBody:  `{"password":"super-secret"}`,
			redactBody: true,
			wantHdrs:   map[string]string{"Content-Type": "application/json"},
			wantBody:   "[REDACTED]",
		},
		{
			name:        "custom and built-in headers both redacted together",
			redactExtra: []string{"X-Custom-Token"},
			inputHdrs:   map[string]string{"Authorization": "Bearer tok", "X-Custom-Token": "custom", "Accept": "application/json"},
			inputBody:   `{}`,
			wantHdrs:    map[string]string{"Authorization": "[REDACTED]", "X-Custom-Token": "[REDACTED]", "Accept": "application/json"},
			wantBody:    `{}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := redact.Headers(makeHeaders(tc.inputHdrs), tc.redactExtra)
			outBody := redact.Body(tc.inputBody, tc.redactBody)

			for k, want := range tc.wantHdrs {
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
