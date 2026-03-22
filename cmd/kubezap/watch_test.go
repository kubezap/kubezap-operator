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

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/cli"
)

// makeTime returns a pointer to a metav1.Time with the given offset from a
// fixed base time, for deterministic test output.
func makeTime(base time.Time, offset time.Duration) *metav1.Time {
	t := metav1.NewTime(base.Add(offset))
	return &t
}

func TestRenderFlowRunTimeline_Header(t *testing.T) {
	base := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-webhook-20260318-abc4f",
			Namespace: "default",
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "order-processor"},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: "order-webhook",
				Type: "webhook",
			},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase:     "Running",
			StartTime: makeTime(base, 0),
		},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 12400*time.Millisecond, &buf)
	out := buf.String()

	cases := []struct {
		label string
		want  string
	}{
		{"separator", "---"},
		{"flowrun name", "order-webhook-20260318-abc4f"},
		{"trigger name", "order-webhook"},
		{"flow name", "order-processor"},
		{"phase", "Running"},
	}
	for _, tc := range cases {
		if !strings.Contains(out, tc.want) {
			t.Errorf("expected output to contain %q (%s), got:\n%s", tc.want, tc.label, out)
		}
	}
}

func TestRenderFlowRunTimeline_NoTriggerRef(t *testing.T) {
	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "manual-run", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "my-flow"},
		},
		Status: automationv1alpha1.FlowRunStatus{Phase: "Pending"},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 0, &buf)
	out := buf.String()

	if !strings.Contains(out, "-") {
		t.Errorf("expected '-' placeholder for missing trigger, got:\n%s", out)
	}
}

func TestRenderFlowRunTimeline_StepBadges(t *testing.T) {
	base := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "order-webhook-20260318-abc4f", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "order-processor"},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: "order-webhook",
				Type: "webhook",
			},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase:     "Running",
			StartTime: makeTime(base, 0),
			Steps: []automationv1alpha1.StepRunStatus{
				{
					Name:           "validate-order",
					Phase:          "Succeeded",
					StartTime:      makeTime(base, 0),
					CompletionTime: makeTime(base, 1200*time.Millisecond),
				},
				{
					Name:      "enrich-customer",
					Phase:     "Running",
					StartTime: makeTime(base, 1200*time.Millisecond),
				},
				{
					Name:  "notify-warehouse",
					Phase: "Pending",
				},
				{
					Name:  "send-confirmation",
					Phase: "Waiting",
				},
				{
					Name:  "cleanup",
					Phase: "Skipped",
				},
			},
		},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 3*time.Second, &buf)
	out := buf.String()

	badgeCases := []struct {
		badge string
		step  string
	}{
		{"✓", "validate-order"},
		{"●", "enrich-customer"},
		{"○", "notify-warehouse"},
		{"○", "send-confirmation"},
		{"-", "cleanup"},
	}
	for _, tc := range badgeCases {
		if !strings.Contains(out, tc.badge) {
			t.Errorf("expected badge %q for step %q, output:\n%s", tc.badge, tc.step, out)
		}
		if !strings.Contains(out, tc.step) {
			t.Errorf("expected step name %q in output:\n%s", tc.step, out)
		}
	}
}

func TestRenderFlowRunTimeline_CompletedDuration(t *testing.T) {
	base := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	end := base.Add(12400 * time.Millisecond)

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-1", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "flow-a"},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase:          "Succeeded",
			StartTime:      makeTime(base, 0),
			CompletionTime: &metav1.Time{Time: end},
		},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 0, &buf)
	out := buf.String()

	// The rendered elapsed should show "12.4s" from start→completion.
	if !strings.Contains(out, "12.4s") {
		t.Errorf("expected elapsed '12.4s' in output:\n%s", out)
	}
}

func TestRenderFlowRunTimeline_FailedStep(t *testing.T) {
	base := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-fail", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "flow-b"},
		},
		Status: automationv1alpha1.FlowRunStatus{
			Phase:          "Failed",
			StartTime:      makeTime(base, 0),
			CompletionTime: makeTime(base, 3*time.Second),
			Steps: []automationv1alpha1.StepRunStatus{
				{
					Name:           "step-ok",
					Phase:          "Succeeded",
					StartTime:      makeTime(base, 0),
					CompletionTime: makeTime(base, 1*time.Second),
				},
				{
					Name:           "step-fail",
					Phase:          "Failed",
					StartTime:      makeTime(base, 1*time.Second),
					CompletionTime: makeTime(base, 3*time.Second),
					Message:        "connection refused",
				},
			},
		},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 0, &buf)
	out := buf.String()

	if !strings.Contains(out, "✗") {
		t.Errorf("expected '✗' badge for failed step, output:\n%s", out)
	}
	if !strings.Contains(out, "Failed") {
		t.Errorf("expected 'Failed' phase in output:\n%s", out)
	}
}

func TestRenderFlowRunTimeline_NoSteps(t *testing.T) {
	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-nosteps", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "flow-c"},
		},
		Status: automationv1alpha1.FlowRunStatus{Phase: "Pending"},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 0, &buf)
	out := buf.String()

	// Should have header but no step lines (no badges).
	if strings.Contains(out, "✓") || strings.Contains(out, "●") || strings.Contains(out, "✗") {
		t.Errorf("expected no step badges for empty steps, output:\n%s", out)
	}
	if !strings.Contains(out, "run-nosteps") {
		t.Errorf("expected FlowRun name in output:\n%s", out)
	}
}

func TestRenderFlowRunTimeline_EmptyPhaseDefaultsPending(t *testing.T) {
	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-empty-phase", Namespace: "default"},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: "flow-d"},
		},
		Status: automationv1alpha1.FlowRunStatus{},
	}

	var buf bytes.Buffer
	cli.RenderFlowRunTimeline(fr, 0, &buf)
	out := buf.String()

	if !strings.Contains(out, "Pending") {
		t.Errorf("expected empty phase to default to 'Pending', output:\n%s", out)
	}
}
