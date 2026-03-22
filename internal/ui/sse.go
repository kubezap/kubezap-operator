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

package ui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// handleSSE streams FlowRun watch events to the client via Server-Sent Events.
//
// Query parameters:
//
//	namespace — filter to a single namespace; omit for all namespaces.
//
// The client must reconnect after disconnect; the retry hint is sent on connect.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Require the underlying client to support Watch.
	wc, ok := s.client.(client.WithWatch)
	if !ok {
		writeError(w, http.StatusNotImplemented,
			"watch not supported: controller-runtime client does not implement client.WithWatch")
		return
	}

	ns := r.URL.Query().Get("namespace")

	// Set SSE headers before writing any body.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Tell the browser to retry in 3 seconds on disconnect.
	fmt.Fprintf(w, "retry: 3000\n\n")
	if canFlush {
		flusher.Flush()
	}

	// Start a watch on FlowRunList.
	listOpts := &client.ListOptions{}
	if ns != "" {
		listOpts.Namespace = ns
	}

	watcher, err := wc.Watch(r.Context(), &automationv1alpha1.FlowRunList{}, listOpts)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
		if canFlush {
			flusher.Flush()
		}
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected.
			return

		case event, open := <-watcher.ResultChan():
			if !open {
				// Watch channel closed (e.g. API server restart). Client will reconnect.
				return
			}

			// Only process Added/Modified/Deleted events for FlowRun objects.
			if event.Type == watch.Error {
				continue
			}

			fr, ok := event.Object.(*automationv1alpha1.FlowRun)
			if !ok {
				continue
			}

			summary := toFlowRunSummary(*fr)
			data, err := json.Marshal(summary)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "event: flowrun\ndata: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}
