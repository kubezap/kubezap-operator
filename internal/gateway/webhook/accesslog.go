package webhook

import (
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

// realClientIP extracts the client IP from X-Forwarded-For, X-Real-IP, or RemoteAddr (in that order).
func realClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may be a comma-separated list; take the first entry.
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			xff = xff[:idx]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := strings.TrimSpace(xri); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &accessLogWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(lw, r)

		durationMs := time.Since(start).Milliseconds()
		sourceIP := realClientIP(r)
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
