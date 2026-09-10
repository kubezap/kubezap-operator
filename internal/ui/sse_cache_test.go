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
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/ui"
)

// TestSSE_WithCache_DoesNotReturn501 verifies that when the Server is constructed with
// WithCache, the SSE endpoint no longer falls back to the 501 client.WithWatch check —
// this is the production path (mgr.GetClient() does not implement client.WithWatch).
func TestSSE_WithCache_DoesNotReturn501(t *testing.T) {
	scheme := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeInformers := &informertest.FakeInformers{Scheme: scheme}

	srv := ui.NewServer(c, "", ui.WithCache(fakeInformers))
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	resp, err := http.Get(httpSrv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with WithCache configured, got %d", resp.StatusCode)
	}
}

// TestSSE_WithCache_StreamsFlowRunEvent verifies that an Add event on the underlying
// informer is streamed to the client as an "event: flowrun" SSE message.
func TestSSE_WithCache_StreamsFlowRunEvent(t *testing.T) {
	scheme := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeInformers := &informertest.FakeInformers{Scheme: scheme}

	// Pre-create the FakeInformer for FlowRun so the test and the handler share the same
	// instance (informertest.FakeInformers caches informers by GVK).
	fakeInformer, err := fakeInformers.FakeInformerFor(context.Background(), &automationv1alpha1.FlowRun{})
	if err != nil {
		t.Fatalf("FakeInformerFor: %v", err)
	}

	srv := ui.NewServer(c, "", ui.WithCache(fakeInformers))
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(httpSrv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()

	fr := makeFlowRun("order-1", "default", "Succeeded", "order-router", "order-webhook", 0)

	// The handler registers its event handler with the informer shortly after the
	// connection opens (after writing SSE headers + the retry hint), racing with this
	// goroutine. Keep retrying Add() on a short interval until it's picked up; the
	// client's Timeout above bounds the overall wait if the handler never registers.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fakeInformer.Add(fr)
			}
		}
	}()

	reader := bufio.NewReader(resp.Body)
	found := false
	for {
		line, readErr := reader.ReadString('\n')
		if strings.HasPrefix(line, "event: flowrun") {
			dataLine, _ := reader.ReadString('\n')
			if strings.Contains(dataLine, `"name":"order-1"`) {
				found = true
			}
			break
		}
		if readErr != nil {
			break
		}
	}

	if !found {
		t.Fatal("expected an \"event: flowrun\" message containing order-1 within the timeout")
	}
}
