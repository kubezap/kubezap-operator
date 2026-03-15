package webhook

import (
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

const mockBodyLimit = 64 * 1024 // 64 KB

// CapturedRequestEvent carries the details of a request received by a mock endpoint.
type CapturedRequestEvent struct {
	Namespace string
	Name      string
	Method    string
	Path      string
	Headers   map[string]string
	Body      string
}

// MockHandler is an http.Handler that serves /mock/* routes.
type MockHandler struct {
	registry *MockRegistry
	log      logr.Logger
	events   chan CapturedRequestEvent
}

// NewMockHandler creates a MockHandler backed by the given registry.
func NewMockHandler(registry *MockRegistry, log logr.Logger) *MockHandler {
	return &MockHandler{
		registry: registry,
		log:      log,
		events:   make(chan CapturedRequestEvent, 100),
	}
}

// Events returns the read-only channel of captured request events.
func (h *MockHandler) Events() <-chan CapturedRequestEvent {
	return h.events
}

func (h *MockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	entry, ok := h.registry.Lookup(path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	resp := h.registry.NextResponse(path)

	// Apply artificial delay.
	if resp != nil && resp.DelayMs > 0 {
		time.Sleep(time.Duration(resp.DelayMs) * time.Millisecond)
	}

	// Write configured response headers.
	if resp != nil {
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
	}

	// Write status code.
	statusCode := http.StatusOK
	if resp != nil && resp.StatusCode != 0 {
		statusCode = int(resp.StatusCode)
	}
	w.WriteHeader(statusCode)

	// Write body.
	if resp != nil && resp.Body != "" {
		_, _ = w.Write([]byte(resp.Body))
	}

	// Capture request details.
	bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, mockBodyLimit))
	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		headers[k] = redactHeader(k, v)
	}

	event := CapturedRequestEvent{
		Namespace: entry.Namespace,
		Name:      entry.Name,
		Method:    r.Method,
		Path:      r.URL.Path,
		Headers:   headers,
		Body:      string(bodyBytes),
	}

	// Non-blocking send — drop if channel is full.
	select {
	case h.events <- event:
	default:
		h.log.Info("mock event channel full, dropping captured request", "path", path)
	}
}
