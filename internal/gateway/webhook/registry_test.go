package webhook

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// TestRegister_RedactsSecretsFromLogOutput verifies that Register's "registering route"
// log line never contains a raw auth secret value, regardless of auth type.
func TestRegister_RedactsSecretsFromLogOutput(t *testing.T) {
	tests := []struct {
		name  string
		entry RouteEntry
	}{
		{"hmac", RouteEntry{TriggerName: "t1", AuthType: "hmac", HMACSecret: "hmac-secret-value"}},
		{"bearer", RouteEntry{TriggerName: "t1", AuthType: "bearer", BearerToken: "bearer-token-value"}},
		{"basic", RouteEntry{TriggerName: "t1", AuthType: "basic", BasicUsername: "user", BasicPassword: "basic-password-value"}},
		{"apiKey", RouteEntry{TriggerName: "t1", AuthType: "apiKey", APIKey: "api-key-value"}},
		{"header-equals", RouteEntry{TriggerName: "t1", AuthType: "header-equals", HeaderEqualsValue: "header-secret-value"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := zap.New(zap.WriteTo(&buf), zap.UseDevMode(true))
			registry := NewRouteRegistry(log)

			registry.Register("/hooks/test", tc.entry)

			output := buf.String()
			for _, secret := range []string{
				tc.entry.HMACSecret, tc.entry.BearerToken, tc.entry.BasicPassword,
				tc.entry.APIKey, tc.entry.HeaderEqualsValue,
			} {
				if secret != "" && strings.Contains(output, secret) {
					t.Fatalf("log output contains raw secret value %q: %s", secret, output)
				}
			}
			if !strings.Contains(output, "[REDACTED]") {
				t.Fatalf("expected [REDACTED] placeholder in log output, got: %s", output)
			}
		})
	}
}

// TestRegister_UpdateRouteAlsoRedacts verifies the "updating route" log path (an
// already-registered path) redacts the same way as initial registration.
func TestRegister_UpdateRouteAlsoRedacts(t *testing.T) {
	var buf bytes.Buffer
	log := zap.New(zap.WriteTo(&buf), zap.UseDevMode(true))
	registry := NewRouteRegistry(log)

	const secret = "bearer-secret-for-update-test"
	entry := RouteEntry{TriggerName: "t1", AuthType: "bearer", BearerToken: secret}
	registry.Register("/hooks/test", entry)
	buf.Reset() // isolate the "updating route" log line from the initial "registering route" one
	registry.Register("/hooks/test", entry)

	output := buf.String()
	if strings.Contains(output, secret) {
		t.Fatalf("update-route log output contains raw secret value: %s", output)
	}
	if !strings.Contains(output, "updating route") {
		t.Fatalf("expected an \"updating route\" log line, got: %s", output)
	}
}

// TestRedactedForLog_LeavesEmptyFieldsEmpty verifies unset secret fields stay empty
// rather than becoming a spurious "[REDACTED]" for auth types that don't use them.
func TestRedactedForLog_LeavesEmptyFieldsEmpty(t *testing.T) {
	entry := RouteEntry{TriggerName: "t1"}
	redacted := entry.redactedForLog()

	if redacted.HMACSecret != "" || redacted.BearerToken != "" || redacted.BasicPassword != "" ||
		redacted.APIKey != "" || redacted.HeaderEqualsValue != "" {
		t.Fatalf("expected empty secret fields to remain empty, got: %+v", redacted)
	}
}

// TestRedactedForLog_DoesNotMutateOriginal verifies that redacting for logging does not
// affect the entry actually stored in the registry (Lookup must still return the real secret).
func TestRedactedForLog_DoesNotMutateOriginal(t *testing.T) {
	var buf bytes.Buffer
	log := zap.New(zap.WriteTo(&buf), zap.UseDevMode(true))
	registry := NewRouteRegistry(log)

	const secret = "hmac-secret-must-survive-storage"
	registry.Register("/hooks/test", RouteEntry{TriggerName: "t1", AuthType: "hmac", HMACSecret: secret})

	stored, ok := registry.Lookup("/hooks/test")
	if !ok {
		t.Fatal("expected route to be registered")
	}
	if stored.HMACSecret != secret {
		t.Fatalf("expected stored entry to retain the real secret for auth verification, got %q", stored.HMACSecret)
	}
}
