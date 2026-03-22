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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
	"github.com/borfswitch/kubezap/internal/ui"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := automationv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme core: %v", err)
	}
	return s
}

func makeFlowRun(name, ns, phase, flowName, trigName string, age time.Duration) *automationv1alpha1.FlowRun {
	now := metav1.NewTime(time.Now().Add(-age))
	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			CreationTimestamp: now,
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: flowName},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: trigName,
				Type: "webhook",
			},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase: phase,
		},
	}
	return fr
}

// ---- GET /api/v1/namespaces -------------------------------------------------

func TestHandleNamespaces(t *testing.T) {
	scheme := newScheme(t)
	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	ns2 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "production"}}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1, ns2).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Namespaces) != 2 {
		t.Errorf("expected 2 namespaces, got %d: %v", len(resp.Namespaces), resp.Namespaces)
	}
}

// ---- GET /api/v1/{namespace}/flowruns ---------------------------------------

func TestHandleFlowRunList_TwoNamespaces(t *testing.T) {
	scheme := newScheme(t)
	fr1 := makeFlowRun("fr-1", "default", "Running", "my-flow", "my-trigger", time.Minute)
	fr2 := makeFlowRun("fr-2", "default", "Succeeded", "my-flow", "my-trigger", 2*time.Minute)
	fr3 := makeFlowRun("fr-3", "other", "Running", "other-flow", "other-trigger", time.Minute)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fr1, fr2, fr3).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flowruns", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected total=2, got %d", resp.Total)
	}
	if len(resp.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(resp.Items))
	}
	// Verify namespace filter worked — all items must be in default.
	for _, item := range resp.Items {
		if ns, _ := item["namespace"].(string); ns != "default" {
			t.Errorf("unexpected namespace %q in response", ns)
		}
	}
}

func TestHandleFlowRunList_PhaseFilter(t *testing.T) {
	scheme := newScheme(t)
	fr1 := makeFlowRun("fr-running", "default", "Running", "f", "t", time.Minute)
	fr2 := makeFlowRun("fr-succeeded", "default", "Succeeded", "f", "t", time.Minute)
	fr3 := makeFlowRun("fr-failed", "default", "Failed", "f", "t", time.Minute)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fr1, fr2, fr3).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flowruns?phase=Running", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected total=1 after phase filter, got %d", resp.Total)
	}
	if phase, _ := resp.Items[0]["phase"].(string); phase != "Running" {
		t.Errorf("expected phase=Running, got %q", phase)
	}
}

func TestHandleFlowRunList_Limit(t *testing.T) {
	scheme := newScheme(t)
	fr1 := makeFlowRun("fr-1", "default", "Succeeded", "f", "t", 1*time.Minute)
	fr2 := makeFlowRun("fr-2", "default", "Succeeded", "f", "t", 2*time.Minute)
	fr3 := makeFlowRun("fr-3", "default", "Succeeded", "f", "t", 3*time.Minute)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fr1, fr2, fr3).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flowruns?limit=1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("expected total=3 (before limit), got %d", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Errorf("expected 1 item after limit, got %d", len(resp.Items))
	}
}

// ---- GET /api/v1/{namespace}/flowruns/{name} --------------------------------

func TestHandleFlowRunDetail(t *testing.T) {
	scheme := newScheme(t)
	now := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	later := metav1.NewTime(time.Now().Add(-3 * time.Minute))

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "fr-test",
			Namespace:         "default",
			CreationTimestamp: now,
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: "my-trigger",
				Type: "webhook",
			},
			TriggerData: &automationv1alpha1.TriggerData{
				EventType: "MODIFIED",
				Body:      `{"key":"value"}`,
				Headers:   map[string]string{"X-Foo": "bar"},
			},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase:          "Succeeded",
			StartTime:      &now,
			CompletionTime: &later,
			Steps: []automationv1alpha1.StepRunStatus{
				{
					Name:     "step-1",
					Phase:    "Succeeded",
					Attempts: 1,
					Results: []automationv1alpha1.ResultValue{
						{Name: "output", Value: "hello"},
					},
				},
				{
					Name:  "step-2",
					Phase: "Succeeded",
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fr).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flowruns/fr-test", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if name, _ := resp["name"].(string); name != "fr-test" {
		t.Errorf("expected name=fr-test, got %q", name)
	}

	steps, _ := resp["steps"].([]any)
	if len(steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(steps))
	}

	trigData, _ := resp["triggerData"].(map[string]any)
	if trigData == nil {
		t.Fatal("expected triggerData to be present")
	}
	if body, _ := trigData["body"].(string); body != `{"key":"value"}` {
		t.Errorf("unexpected triggerData.body: %q", body)
	}

	// Verify step results are mapped correctly.
	if len(steps) > 0 {
		step1, _ := steps[0].(map[string]any)
		results, _ := step1["results"].(map[string]any)
		if v, _ := results["output"].(string); v != "hello" {
			t.Errorf("expected results.output=hello, got %q", v)
		}
	}
}

func TestHandleFlowRunDetail_NotFound(t *testing.T) {
	scheme := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flowruns/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ---- GET /api/v1/{namespace}/triggers ---------------------------------------

func TestHandleTriggersL(t *testing.T) {
	scheme := newScheme(t)
	trig := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-webhook-trigger",
			Namespace: "default",
		},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "webhook",
			Enabled: true,
			FlowRef: &automationv1alpha1.FlowReference{Name: "my-flow"},
		},
		Status: automationv1alpha1.TriggerStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	cronTrig := &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cron-trigger",
			Namespace: "default",
		},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "cron",
			Enabled: true,
			Cron:    &automationv1alpha1.CronTrigger{Schedule: "*/5 * * * *"},
			FlowRef: &automationv1alpha1.FlowReference{Name: "my-flow"},
		},
	}

	// Add an active FlowRun for the webhook trigger.
	fr := makeFlowRun("fr-active", "default", "Running", "my-flow", "my-webhook-trigger", time.Minute)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(trig, cronTrig, fr).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/triggers", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(resp.Items))
	}

	// Find webhook trigger.
	var webhookItem, cronItem map[string]any
	for _, item := range resp.Items {
		switch item["name"].(string) {
		case "my-webhook-trigger":
			webhookItem = item
		case "my-cron-trigger":
			cronItem = item
		}
	}

	if webhookItem == nil || cronItem == nil {
		t.Fatal("did not find both triggers in response")
	}

	// Webhook trigger: ready=true, endpoint set, activeFlowRuns=1.
	if ready, _ := webhookItem["ready"].(bool); !ready {
		t.Errorf("expected webhook trigger ready=true")
	}
	if ep, _ := webhookItem["endpoint"].(string); ep != "/hooks/my-webhook-trigger" {
		t.Errorf("unexpected endpoint: %q", ep)
	}
	if active, _ := webhookItem["activeFlowRuns"].(float64); active != 1 {
		t.Errorf("expected activeFlowRuns=1, got %v", active)
	}

	// Cron trigger: schedule set.
	if sched, _ := cronItem["schedule"].(string); sched != "*/5 * * * *" {
		t.Errorf("unexpected schedule: %q", sched)
	}
}

// ---- GET /api/v1/{namespace}/flows ------------------------------------------

func TestHandleFlowList(t *testing.T) {
	scheme := newScheme(t)

	flow := &automationv1alpha1.Flow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-flow",
			Namespace: "default",
		},
		Spec: automationv1alpha1.FlowSpec{
			Steps: []automationv1alpha1.FlowStep{
				{Name: "step-1", Action: automationv1alpha1.StepAction{Type: "http"}},
				{Name: "step-2", Action: automationv1alpha1.StepAction{Type: "transform"}},
			},
		},
		Status: automationv1alpha1.FlowStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
		},
	}

	// FlowRun referencing the flow.
	fr := makeFlowRun("fr-1", "default", "Succeeded", "my-flow", "t", time.Hour)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(flow, fr).Build()
	srv := ui.NewServer(c, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/default/flows", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(resp.Items))
	}

	item := resp.Items[0]
	if name, _ := item["name"].(string); name != "my-flow" {
		t.Errorf("unexpected name: %q", name)
	}
	if count, _ := item["stepCount"].(float64); count != 2 {
		t.Errorf("expected stepCount=2, got %v", count)
	}
	if ready, _ := item["ready"].(bool); !ready {
		t.Errorf("expected ready=true")
	}
	// lastUsedTime should be populated from the FlowRun.
	if _, ok := item["lastUsedTime"]; !ok {
		t.Errorf("expected lastUsedTime to be present")
	}
}

// ---- Bearer token auth middleware -------------------------------------------

func TestBearerTokenMiddleware(t *testing.T) {
	scheme := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := ui.NewServer(c, "secret-token")

	// No token — expect 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", w.Code)
	}

	// Wrong token — expect 401.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", w.Code)
	}

	// Correct token — expect non-401.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected non-401 with correct token, got 401")
	}
}
