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
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

// ---- Response types ---------------------------------------------------------

// FlowRunSummary is a condensed view of a FlowRun for list responses.
type FlowRunSummary struct {
	Name              string   `json:"name"`
	Namespace         string   `json:"namespace"`
	TriggerName       string   `json:"triggerName"`
	FlowName          string   `json:"flowName"`
	Phase             string   `json:"phase"`
	CreationTimestamp string   `json:"creationTimestamp"`
	StartTime         *string  `json:"startTime,omitempty"`
	CompletionTime    *string  `json:"completionTime,omitempty"`
	DurationSeconds   *float64 `json:"durationSeconds,omitempty"`
	StepCount         int      `json:"stepCount"`
	StepsSucceeded    int      `json:"stepsSucceeded"`
	StepsFailed       int      `json:"stepsFailed"`
}

// StepRunSummary is a condensed view of a single step execution.
type StepRunSummary struct {
	Name            string            `json:"name"`
	Phase           string            `json:"phase"`
	Attempts        int32             `json:"attempts"`
	Message         string            `json:"message,omitempty"`
	StartTime       *string           `json:"startTime,omitempty"`
	CompletionTime  *string           `json:"completionTime,omitempty"`
	DurationSeconds *float64          `json:"durationSeconds,omitempty"`
	Results         map[string]string `json:"results,omitempty"`
}

// TriggerDataSummary is a redacted view of the trigger payload.
type TriggerDataSummary struct {
	EventType string            `json:"eventType,omitempty"`
	Body      string            `json:"body,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// FlowRunDetail extends FlowRunSummary with step details and trigger data.
type FlowRunDetail struct {
	FlowRunSummary
	Steps       []StepRunSummary    `json:"steps"`
	TriggerData *TriggerDataSummary `json:"triggerData,omitempty"`
}

// TriggerSummary is a condensed view of a Trigger.
type TriggerSummary struct {
	Name           string  `json:"name"`
	Namespace      string  `json:"namespace"`
	Type           string  `json:"type"`
	Ready          bool    `json:"ready"`
	LastFiredTime  *string `json:"lastFiredTime,omitempty"`
	ActiveFlowRuns int     `json:"activeFlowRuns"`
	Schedule       string  `json:"schedule,omitempty"`
	Endpoint       string  `json:"endpoint,omitempty"`
}

// FlowSummary is a condensed view of a Flow.
type FlowSummary struct {
	Name         string  `json:"name"`
	Namespace    string  `json:"namespace"`
	StepCount    int     `json:"stepCount"`
	Ready        bool    `json:"ready"`
	LastUsedTime *string `json:"lastUsedTime,omitempty"`
}

// ---- Helper functions -------------------------------------------------------

// formatTime returns an RFC3339 string pointer or nil.
func formatTime(t *metav1.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// durationSeconds computes elapsed seconds between two times, or nil.
func durationSeconds(start, end *metav1.Time) *float64 {
	if start == nil || start.IsZero() || end == nil || end.IsZero() {
		return nil
	}
	d := end.Sub(start.Time).Seconds()
	return &d
}

// isReady checks whether a Ready condition with status True exists.
func isReady(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// writeJSON writes v as JSON with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// toFlowRunSummary converts a FlowRun CRD to a FlowRunSummary.
func toFlowRunSummary(fr automationv1alpha1.FlowRun) FlowRunSummary {
	triggerName := ""
	if fr.Spec.TriggerRef != nil {
		triggerName = fr.Spec.TriggerRef.Name
	}

	stepsSucceeded := 0
	stepsFailed := 0
	for _, s := range fr.Status.Steps {
		switch s.Phase {
		case "Succeeded":
			stepsSucceeded++
		case "Failed":
			stepsFailed++
		}
	}

	return FlowRunSummary{
		Name:              fr.Name,
		Namespace:         fr.Namespace,
		TriggerName:       triggerName,
		FlowName:          fr.Spec.FlowRef.Name,
		Phase:             fr.Status.Phase,
		CreationTimestamp: fr.CreationTimestamp.UTC().Format(time.RFC3339),
		StartTime:         formatTime(fr.Status.StartTime),
		CompletionTime:    formatTime(fr.Status.CompletionTime),
		DurationSeconds:   durationSeconds(fr.Status.StartTime, fr.Status.CompletionTime),
		StepCount:         len(fr.Status.Steps),
		StepsSucceeded:    stepsSucceeded,
		StepsFailed:       stepsFailed,
	}
}

// toStepRunSummary converts a StepRunStatus to a StepRunSummary.
func toStepRunSummary(s automationv1alpha1.StepRunStatus) StepRunSummary {
	results := make(map[string]string, len(s.Results))
	for _, r := range s.Results {
		results[r.Name] = r.Value
	}
	var resultsOut map[string]string
	if len(results) > 0 {
		resultsOut = results
	}
	return StepRunSummary{
		Name:            s.Name,
		Phase:           s.Phase,
		Attempts:        s.Attempts,
		Message:         s.Message,
		StartTime:       formatTime(s.StartTime),
		CompletionTime:  formatTime(s.CompletionTime),
		DurationSeconds: durationSeconds(s.StartTime, s.CompletionTime),
		Results:         resultsOut,
	}
}

// watchedNamespaces returns the list of namespaces from WATCH_NAMESPACES env var.
// Returns nil (empty slice) when the variable is unset or empty (all namespaces).
func watchedNamespaces() []string {
	v := os.Getenv("WATCH_NAMESPACES")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- Handlers ---------------------------------------------------------------

// handleNamespaces lists visible Kubernetes namespaces.
// Falls back to WATCH_NAMESPACES if set, or the default namespace on RBAC denial.
func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	if ns := watchedNamespaces(); len(ns) > 0 {
		writeJSON(w, http.StatusOK, map[string][]string{"namespaces": ns})
		return
	}

	var list corev1.NamespaceList
	if err := s.client.List(r.Context(), &list); err != nil {
		// On RBAC denial fall back to single default namespace.
		writeJSON(w, http.StatusOK, map[string][]string{"namespaces": {"default"}})
		return
	}

	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, map[string][]string{"namespaces": names})
}

// handleFlowRunList lists FlowRuns with optional filtering.
func (s *Server) handleFlowRunList(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")

	q := r.URL.Query()
	phaseFilter := q.Get("phase")
	triggerFilter := q.Get("trigger")
	flowFilter := q.Get("flow")

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}

	var sinceTime *time.Time
	if since := q.Get("since"); since != "" {
		if d, err := time.ParseDuration(since); err == nil {
			t := time.Now().Add(-d)
			sinceTime = &t
		}
	}

	var list automationv1alpha1.FlowRunList
	opts := []client.ListOption{client.InNamespace(ns)}
	if err := s.client.List(r.Context(), &list, opts...); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list FlowRuns: "+err.Error())
		return
	}

	// Filter
	filtered := make([]automationv1alpha1.FlowRun, 0, len(list.Items))
	for _, fr := range list.Items {
		if phaseFilter != "" && fr.Status.Phase != phaseFilter {
			continue
		}
		if triggerFilter != "" {
			trigName := ""
			if fr.Spec.TriggerRef != nil {
				trigName = fr.Spec.TriggerRef.Name
			}
			if trigName != triggerFilter {
				continue
			}
		}
		if flowFilter != "" && fr.Spec.FlowRef.Name != flowFilter {
			continue
		}
		if sinceTime != nil && fr.CreationTimestamp.Time.Before(*sinceTime) {
			continue
		}
		filtered = append(filtered, fr)
	}

	// Sort newest first
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreationTimestamp.After(filtered[j].CreationTimestamp.Time)
	})

	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	summaries := make([]FlowRunSummary, len(filtered))
	for i, fr := range filtered {
		summaries[i] = toFlowRunSummary(fr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": summaries,
		"total": total,
	})
}

// handleFlowRunDetail returns a single FlowRun with full step and trigger data.
func (s *Server) handleFlowRunDetail(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	var fr automationv1alpha1.FlowRun
	if err := s.client.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &fr); err != nil {
		writeError(w, http.StatusNotFound, "FlowRun not found: "+err.Error())
		return
	}

	steps := make([]StepRunSummary, len(fr.Status.Steps))
	for i, s := range fr.Status.Steps {
		steps[i] = toStepRunSummary(s)
	}

	var trigData *TriggerDataSummary
	if fr.Spec.TriggerData != nil {
		trigData = &TriggerDataSummary{
			EventType: fr.Spec.TriggerData.EventType,
			Body:      fr.Spec.TriggerData.Body,
			Headers:   fr.Spec.TriggerData.Headers,
		}
	}

	detail := FlowRunDetail{
		FlowRunSummary: toFlowRunSummary(fr),
		Steps:          steps,
		TriggerData:    trigData,
	}

	writeJSON(w, http.StatusOK, detail)
}

// handleTriggerList lists Triggers in a namespace with derived metadata.
func (s *Server) handleTriggerList(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")

	var trigList automationv1alpha1.TriggerList
	if err := s.client.List(r.Context(), &trigList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Triggers: "+err.Error())
		return
	}

	// List all FlowRuns once so we can count active runs per trigger efficiently.
	var frList automationv1alpha1.FlowRunList
	if err := s.client.List(r.Context(), &frList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list FlowRuns: "+err.Error())
		return
	}

	// Build active FlowRun count map: triggerName -> count.
	activeCount := make(map[string]int)
	for _, fr := range frList.Items {
		if fr.Spec.TriggerRef == nil {
			continue
		}
		if fr.Status.Phase == "Running" || fr.Status.Phase == "Pending" {
			activeCount[fr.Spec.TriggerRef.Name]++
		}
	}

	summaries := make([]TriggerSummary, 0, len(trigList.Items))
	for _, t := range trigList.Items {
		ts := TriggerSummary{
			Name:           t.Name,
			Namespace:      t.Namespace,
			Type:           t.Spec.Type,
			Ready:          isReady(t.Status.Conditions),
			ActiveFlowRuns: activeCount[t.Name],
		}

		if t.Status.LastTriggeredTime != nil {
			ts.LastFiredTime = formatTime(t.Status.LastTriggeredTime)
		}

		if t.Spec.Type == "cron" && t.Spec.Cron != nil {
			ts.Schedule = t.Spec.Cron.Schedule
		}

		if t.Spec.Type == "webhook" {
			ts.Endpoint = "/hooks/" + t.Name
		}

		summaries = append(summaries, ts)
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": summaries})
}

// handleFlowList lists Flows in a namespace with derived metadata.
func (s *Server) handleFlowList(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")

	var flowList automationv1alpha1.FlowList
	if err := s.client.List(r.Context(), &flowList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Flows: "+err.Error())
		return
	}

	// List all FlowRuns to find lastUsedTime per flow.
	var frList automationv1alpha1.FlowRunList
	if err := s.client.List(r.Context(), &frList, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list FlowRuns: "+err.Error())
		return
	}

	// Build lastUsed map: flowName -> most recent CreationTimestamp.
	lastUsed := make(map[string]time.Time)
	for _, fr := range frList.Items {
		flowName := fr.Spec.FlowRef.Name
		if t, ok := lastUsed[flowName]; !ok || fr.CreationTimestamp.After(t) {
			lastUsed[flowName] = fr.CreationTimestamp.Time
		}
	}

	summaries := make([]FlowSummary, 0, len(flowList.Items))
	for _, fl := range flowList.Items {
		fs := FlowSummary{
			Name:      fl.Name,
			Namespace: fl.Namespace,
			StepCount: len(fl.Spec.Steps),
			Ready:     isReady(fl.Status.Conditions),
		}

		if t, ok := lastUsed[fl.Name]; ok {
			mt := metav1.NewTime(t)
			fs.LastUsedTime = formatTime(&mt)
		}

		summaries = append(summaries, fs)
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": summaries})
}

// listFlowRunsForContext is a helper used by SSE to build summaries.
func listFlowRunsForContext(ctx context.Context, c client.Client, ns string) ([]FlowRunSummary, error) {
	var list automationv1alpha1.FlowRunList
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	out := make([]FlowRunSummary, len(list.Items))
	for i, fr := range list.Items {
		out[i] = toFlowRunSummary(fr)
	}
	return out, nil
}
