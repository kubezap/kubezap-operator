package webhook

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// accessLogWriter wraps http.ResponseWriter to capture the status code.
type accessLogWriter struct {
	http.ResponseWriter
	status int
}

func (w *accessLogWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// accessLogger is a global slog.Logger configured for JSON access logs, written to stdout.
var accessLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// realClientIP extracts the client IP, honoring X-Forwarded-For/X-Real-IP only
// when the immediate TCP peer (r.RemoteAddr) is itself within trustedProxies.
// This matters for more than logging: authenticateRequest's ipAllowlist case
// calls this to decide access. Trusting these headers unconditionally lets any
// caller who can reach the gateway directly forge its apparent source IP and
// bypass an IP allowlist outright — see
// docs/design/2026-09-11-webhook-gateway-trust-boundary.md.
//
// When trustedProxies is empty (the default), the headers are never consulted
// and r.RemoteAddr is always returned. When non-empty and the peer is trusted,
// the right-most X-Forwarded-For entry is used — the address the trusted hop
// itself observed, which a client further down the chain cannot overwrite (it
// can only prepend to the list, which appends rather than replaces).
func realClientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if len(trustedProxies) == 0 || !isTrustedProxy(host, trustedProxies) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := strings.TrimSpace(xri); ip != "" {
			return ip
		}
	}
	return host
}

// ParseTrustedProxyCIDRs parses a comma-separated list of CIDR strings for the
// --trusted-proxy-cidrs flag. An empty string returns a nil slice (the safe
// default: X-Forwarded-For/X-Real-IP are never trusted — see realClientIP).
func ParseTrustedProxyCIDRs(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		cidr := strings.TrimSpace(part)
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q in --trusted-proxy-cidrs: %w", cidr, err)
		}
		out = append(out, ipNet)
	}
	return out, nil
}

// isTrustedProxy reports whether host falls within any of trustedProxies.
func isTrustedProxy(host string, trustedProxies []*net.IPNet) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// triggerFromPath attempts to extract the trigger name from the request context.
// The trigger name is set on the context by WebhookHandler before the deferred log; for the
// middleware we do a best-effort extract from path only (e.g. /hooks/<trigger>).
// The actual trigger name (including namespace look-up) is emitted by the per-handler log;
// the middleware log shows the path-derived name or empty string if the path is not a /hooks/ path.
func triggerFromPath(path string) string {
	const prefix = "/hooks/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	// strip trailing slash and any sub-path
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

// AccessLogMiddleware wraps an http.Handler and emits a structured JSON log line for every request.
// Fields: timestamp, method, path, status, duration_ms, source_ip, trigger.
// trustedProxies is forwarded to realClientIP — see its doc comment.
func AccessLogMiddleware(next http.Handler, trustedProxies []*net.IPNet) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &accessLogWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(lw, r)

		durationMs := time.Since(start).Milliseconds()
		sourceIP := realClientIP(r, trustedProxies)
		trigger := triggerFromPath(r.URL.Path)

		accessLogger.LogAttrs(r.Context(), slog.LevelInfo, "access",
			slog.String("timestamp", start.UTC().Format(time.RFC3339)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", lw.status),
			slog.Int64("duration_ms", durationMs),
			slog.String("source_ip", sourceIP),
			slog.String("trigger", trigger),
		)
	})
}
