/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/borfswitch/kubezap/internal/ui"
)

// noWatchClient wraps a fake.Client but does NOT implement client.WithWatch.
// Used to verify the 501 fallback path.
type noWatchClient struct {
	client.Client
}

func TestSSE_NoWatchSupport_Returns501(t *testing.T) {
	// Wrap the fake client in noWatchClient so the type assertion fails.
	innerC := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	c := noWatchClient{Client: innerC}

	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 when WithWatch not supported, got %d: %s", w.Code, w.Body.String())
	}
}

// TestSSE_HeadersSet verifies that SSE response headers are set correctly.
// It uses a pre-cancelled context so the handler exits without blocking.
func TestSSE_HeadersSet(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	srv := ui.NewServer(c, "")

	// Build a request with a pre-cancelled context so the SSE loop exits immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request is sent

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// The handler should have set SSE headers even if the context was cancelled
	// before the watch loop started.
	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}
	if conn := w.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %q", conn)
	}
	if buf := w.Header().Get("X-Accel-Buffering"); buf != "no" {
		t.Errorf("expected X-Accel-Buffering no, got %q", buf)
	}
}

// TestSSE_RetryHint verifies that "retry: 3000" is written in the response body.
func TestSSE_RetryHint(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	srv := ui.NewServer(c, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "retry: 3000") {
		t.Errorf("expected 'retry: 3000' in SSE body, got: %q", body)
	}
}

// TestSSE_NamespaceParam verifies the handler accepts an optional namespace query param.
func TestSSE_NamespaceParam(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	srv := ui.NewServer(c, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?namespace=default", nil)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// Should still respond with SSE headers (namespace filter is internal).
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}
}
