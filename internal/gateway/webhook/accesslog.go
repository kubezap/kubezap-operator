package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// auth.result values for the access log, shared between AccessLogMiddleware
// (which supplies the "skipped" fallback) and WebhookHandler (which sets
// "success"/"failure" once authenticateRequest has run).
const (
	authResultSuccess = "success"
	authResultFailure = "failure"
	authResultSkipped = "skipped"
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

// accessLogFields carries request detail that only becomes known deep inside
// WebhookHandler.ServeHTTP (trace/span IDs, the resolved trigger namespace,
// auth outcome, FlowRun outcome) back out to AccessLogMiddleware, which is
// the sole emitter of the per-request access log line. AccessLogMiddleware
// allocates one of these per request and injects a pointer to it into the
// request context before calling the wrapped handler; WebhookHandler
// retrieves the same pointer and populates fields as each becomes known. A
// nil *accessLogFields (e.g. in tests that invoke WebhookHandler directly,
// bypassing the middleware) is a safe no-op for every setter below.
type accessLogFields struct {
	TraceID          string
	SpanID           string
	TriggerNamespace string
	AuthType         string
	AuthResult       string
	AuthReason       string
	BodyBytes        int
	FlowRunCreated   bool
	FlowRunName      string
	FlowName         string
}

func (f *accessLogFields) setTrace(traceID, spanID string) {
	if f == nil {
		return
	}
	f.TraceID = traceID
	f.SpanID = spanID
}

func (f *accessLogFields) setTrigger(namespace, authType string) {
	if f == nil {
		return
	}
	f.TriggerNamespace = namespace
	f.AuthType = authType
}

func (f *accessLogFields) setAuthResult(result, reason string) {
	if f == nil {
		return
	}
	f.AuthResult = result
	f.AuthReason = reason
}

func (f *accessLogFields) setBodyBytes(n int) {
	if f == nil {
		return
	}
	f.BodyBytes = n
}

func (f *accessLogFields) setFlowRun(created bool, name, flow string) {
	if f == nil {
		return
	}
	f.FlowRunCreated = created
	f.FlowRunName = name
	f.FlowName = flow
}

// accessLogFieldsKey is the unexported context key type under which
// AccessLogMiddleware stores an *accessLogFields for the duration of a request.
type accessLogFieldsKey struct{}

// accessLogFieldsFromContext retrieves the *accessLogFields injected by
// AccessLogMiddleware, or nil if none is present (e.g. a direct ServeHTTP
// call in tests that bypasses the middleware). Every accessLogFields setter
// is nil-safe, so callers do not need to check the result before use.
func accessLogFieldsFromContext(ctx context.Context) *accessLogFields {
	f, _ := ctx.Value(accessLogFieldsKey{}).(*accessLogFields)
	return f
}

// normalizeContentType maps a raw Content-Type header value to one of the
// coarse buckets documented in docs/guides/observability.md's Access Log
// Fields Reference: json, xml, form, text, binary. An empty header (no
// Content-Type sent at all, e.g. a body-less GET) maps to "" rather than
// "binary" since no content type was asserted either way.
func normalizeContentType(raw string) string {
	media := raw
	if idx := strings.IndexByte(media, ';'); idx >= 0 {
		media = media[:idx]
	}
	media = strings.ToLower(strings.TrimSpace(media))

	switch {
	case media == "":
		return ""
	case media == "application/json" || strings.HasSuffix(media, "+json"):
		return "json"
	case media == "application/xml" || media == "text/xml" || strings.HasSuffix(media, "+xml"):
		return "xml"
	case media == "application/x-www-form-urlencoded":
		return "form"
	case strings.HasPrefix(media, "text/"):
		return "text"
	default:
		return "binary"
	}
}

// sourcePortFromRemoteAddr best-effort parses the TCP peer port from
// r.RemoteAddr. It intentionally does not consult X-Forwarded-For/X-Real-IP
// (unlike realClientIP) since a forwarded port number from an untrusted
// header is not meaningful; this always reflects the literal socket peer.
func sourcePortFromRemoteAddr(remoteAddr string) (int, bool) {
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, false
	}
	return port, true
}

// realClientIP extracts the client IP, honoring X-Forwarded-For/X-Real-IP only
// when the immediate TCP peer (r.RemoteAddr) is itself within trustedProxies.
// This matters for more than logging: authenticateRequest's ipAllowlist case
// calls this to decide access. Trusting these headers unconditionally lets any
// caller who can reach the gateway directly forge its apparent source IP and
// bypass an IP allowlist outright — see
// docs/design/webhook-gateway-trust-boundary.md.
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

// AccessLogMiddleware wraps an http.Handler and is the sole emitter of the
// structured, single-JSON-line-per-request access log described in
// docs/guides/observability.md ("Structured Access Logs" / "Access Log
// Fields Reference"). It emits msg "webhook_request" with nested
// request/auth/response/flowrun objects.
//
// Handler-side detail (trace/span IDs, trigger namespace, auth outcome,
// FlowRun outcome) is threaded in via an *accessLogFields injected into the
// request context before next.ServeHTTP runs, and read back out afterward —
// see accessLogFields's doc comment. trustedProxies is forwarded to
// realClientIP — see its doc comment.
func AccessLogMiddleware(next http.Handler, trustedProxies []*net.IPNet) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &accessLogWriter{ResponseWriter: w, status: http.StatusOK}

		fields := &accessLogFields{}
		r = r.WithContext(context.WithValue(r.Context(), accessLogFieldsKey{}, fields))

		next.ServeHTTP(lw, r)

		durationMs := float64(time.Since(start).Microseconds()) / 1000.0
		sourceIP := realClientIP(r, trustedProxies)
		trigger := triggerFromPath(r.URL.Path)

		// auth.result defaults to "skipped" when the handler never reached
		// (or never populated) auth evaluation at all — e.g. /healthz,
		// /readyz, an unrecognized /hooks/ path (404 before auth runs), or a
		// wrong-method request (405 before auth runs). A trigger with no
		// auth configured is set explicitly to "skipped" by the handler, not
		// via this fallback.
		authResult := fields.AuthResult
		if authResult == "" {
			authResult = "skipped"
		}

		level := slog.LevelInfo
		switch {
		case lw.status >= http.StatusInternalServerError:
			level = slog.LevelError
		case authResult == authResultFailure || lw.status == http.StatusTooManyRequests:
			level = slog.LevelWarn
		}

		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = randomHex(16)
		}

		requestAttrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("trigger", trigger),
			slog.String("namespace", fields.TriggerNamespace),
			slog.String("source_ip", sourceIP),
		}
		if port, ok := sourcePortFromRemoteAddr(r.RemoteAddr); ok {
			requestAttrs = append(requestAttrs, slog.Int("source_port", port))
		}
		requestAttrs = append(requestAttrs,
			slog.String("forwarded_for", r.Header.Get("X-Forwarded-For")),
			slog.String("user_agent", r.Header.Get("User-Agent")),
			slog.String("request_id", requestID),
			slog.String("content_type", normalizeContentType(r.Header.Get("Content-Type"))),
			slog.Int("body_bytes", fields.BodyBytes),
		)

		authAttrs := []any{
			slog.String("type", fields.AuthType),
			slog.String("result", authResult),
		}
		if authResult == authResultFailure && fields.AuthReason != "" {
			authAttrs = append(authAttrs, slog.String("reason", fields.AuthReason))
		}

		flowrunAttrs := []any{
			slog.Bool("created", fields.FlowRunCreated),
		}
		if fields.FlowRunCreated {
			if fields.FlowRunName != "" {
				flowrunAttrs = append(flowrunAttrs, slog.String("name", fields.FlowRunName))
			}
			if fields.FlowName != "" {
				flowrunAttrs = append(flowrunAttrs, slog.String("flow", fields.FlowName))
			}
		}

		accessLogger.LogAttrs(r.Context(), level, "webhook_request",
			slog.String("trace_id", fields.TraceID),
			slog.String("span_id", fields.SpanID),
			slog.Group("request", requestAttrs...),
			slog.Group("auth", authAttrs...),
			slog.Group("response",
				slog.Int("status_code", lw.status),
				slog.Float64("duration_ms", durationMs),
			),
			slog.Group("flowrun", flowrunAttrs...),
		)
	})
}
