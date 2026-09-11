// Package redact provides header and body redaction utilities shared across
// all KubeZap gateways (webhook, Kafka, AMQP, NATS). Redacted values are
// replaced with the literal string Placeholder so that TriggerData stored
// in FlowRun CRDs never contains credentials.
package redact

import (
	"net/http"
	"strings"
)

// Placeholder replaces any redacted header or body value.
const Placeholder = "[REDACTED]"

// BuiltInHeaders is the list of HTTP header names always redacted when building
// TriggerData, regardless of per-Trigger configuration.
// Names are stored in their canonical HTTP form (Title-Case) but matching is
// always performed case-insensitively.
var BuiltInHeaders = []string{
	"Authorization",
	"X-Api-Key",
	"Cookie",
	"Set-Cookie",
	"X-Auth-Token",
	"Proxy-Authorization",
	"X-Webhook-Secret",
	"X-Hub-Signature",
	"X-Hub-Signature-256",
	"X-Amz-Security-Token",
}

// Headers returns a map[string]string built from the given http.Header with
// all built-in sensitive headers and any extra names in redactExtra replaced
// with "[REDACTED]" (Placeholder). Matching is case-insensitive. Multi-value
// headers are joined with commas. Non-sensitive headers pass through unchanged.
func Headers(headers http.Header, redactExtra []string) map[string]string {
	toRedact := make(map[string]bool, len(BuiltInHeaders)+len(redactExtra))
	for _, h := range BuiltInHeaders {
		toRedact[strings.ToLower(h)] = true
	}
	for _, h := range redactExtra {
		toRedact[strings.ToLower(h)] = true
	}

	out := make(map[string]string, len(headers))
	for k, vals := range headers {
		if toRedact[strings.ToLower(k)] {
			out[k] = Placeholder
		} else {
			out[k] = strings.Join(vals, ",")
		}
	}
	return out
}

// Body returns "[REDACTED]" (Placeholder) when redactBody is true; otherwise
// it returns body unchanged. This is a thin helper that makes call sites
// self-documenting.
func Body(body string, redactBody bool) string {
	if redactBody {
		return Placeholder
	}
	return body
}

// StringMap applies redaction to an arbitrary map[string]string (e.g. Kafka or
// AMQP message headers that have already been converted from their native
// representation). Keys are matched case-insensitively against BuiltInHeaders.
// redactExtra allows callers to supply additional names to redact.
func StringMap(hdrs map[string]string, redactExtra []string) {
	toRedact := make(map[string]bool, len(BuiltInHeaders)+len(redactExtra))
	for _, h := range BuiltInHeaders {
		toRedact[strings.ToLower(h)] = true
	}
	for _, h := range redactExtra {
		toRedact[strings.ToLower(h)] = true
	}

	for k := range hdrs {
		if toRedact[strings.ToLower(k)] {
			hdrs[k] = Placeholder
		}
	}
}
