package kafka

import (
	"context"
	"testing"

	"github.com/IBM/sarama"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = automationv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

// TestSanitizeFlowRunName verifies the sanitize helper.
func TestSanitizeFlowRunName(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"my-trigger"},
		{"My_Trigger.name"},
		{"trigger/with:special@chars!"},
		{"UPPER"},
	}
	for _, tc := range cases {
		got := sanitizeFlowRunName(tc.input)
		if len(got) > 253 {
			t.Errorf("sanitizeFlowRunName(%q): result len %d > 253", tc.input, len(got))
		}
		for _, ch := range got {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				t.Errorf("sanitizeFlowRunName(%q): invalid char %q in result %q", tc.input, ch, got)
			}
		}
	}
}

// TestHandleMessage_CreatesFlowRun verifies a basic message creates a correctly
// named FlowRun with the expected labels and trigger data.
func TestHandleMessage_CreatesFlowRun(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "orders-trigger",
		triggerNamespace: "default",
		flowRefName:      "process-order",
	}

	if err := h.HandleMessage(context.Background(), "orders", 0, 42, []byte(`{"order":"abc"}`), nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	fr := list.Items[0]
	expectedName := sanitizeFlowRunName("orders-trigger-p0-offset-42")
	if fr.Name != expectedName {
		t.Errorf("FlowRun name = %q, want %q", fr.Name, expectedName)
	}
	if fr.Spec.FlowRef.Name != "process-order" {
		t.Errorf("FlowRef.Name = %q", fr.Spec.FlowRef.Name)
	}
	if fr.Spec.TriggerData.Body != `{"order":"abc"}` {
		t.Errorf("TriggerData.Body = %q", fr.Spec.TriggerData.Body)
	}
	if fr.Spec.TriggerData.Topic != "orders" {
		t.Errorf("TriggerData.Topic = %q, want orders", fr.Spec.TriggerData.Topic)
	}
	if fr.Labels["kubezap.io/trigger-type"] != "kafka" {
		t.Errorf("trigger-type label = %q", fr.Labels["kubezap.io/trigger-type"])
	}
}

// TestHandleMessage_Duplicate verifies that a duplicate FlowRun (same topic/partition/offset)
// is silently skipped without returning an error.
func TestHandleMessage_Duplicate(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "dup-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	if err := h.HandleMessage(context.Background(), "topic", 0, 1, []byte("payload"), nil); err != nil {
		t.Fatalf("first HandleMessage: %v", err)
	}
	// Second call with same partition+offset — FlowRun already exists.
	if err := h.HandleMessage(context.Background(), "topic", 0, 1, []byte("payload"), nil); err != nil {
		t.Fatalf("duplicate HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	_ = fakeClient.List(context.Background(), list)
	if len(list.Items) != 1 {
		t.Errorf("expected 1 FlowRun after duplicate, got %d", len(list.Items))
	}
}

// TestHandleMessage_AuthHeadersRedacted verifies that auth-like Kafka message headers
// are replaced with "[REDACTED]" in TriggerData.Headers and never stored in plaintext.
func TestHandleMessage_AuthHeadersRedacted(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "auth-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	msgHeaders := []*sarama.RecordHeader{
		{Key: []byte("authorization"), Value: []byte("Bearer secret-token")},
		{Key: []byte("x-api-key"), Value: []byte("my-api-key")},
		{Key: []byte("content-type"), Value: []byte("application/json")},
		{Key: []byte("x-request-id"), Value: []byte("req-42")},
		{Key: []byte("cookie"), Value: []byte("session=abc")},
	}

	if err := h.HandleMessage(context.Background(), "events", 1, 100, []byte(`{"data":"test"}`), msgHeaders); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	hdrs := list.Items[0].Spec.TriggerData.Headers

	sensitiveKeys := []string{"authorization", "x-api-key", "cookie"}
	for _, k := range sensitiveKeys {
		if got := hdrs[k]; got != "[REDACTED]" {
			t.Errorf("header %q: want [REDACTED], got %q", k, got)
		}
	}

	// Non-sensitive headers must pass through.
	if got := hdrs["content-type"]; got != "application/json" {
		t.Errorf("content-type: want application/json, got %q", got)
	}
	if got := hdrs["x-request-id"]; got != "req-42" {
		t.Errorf("x-request-id: want req-42, got %q", got)
	}
}

// TestHandleMessage_NilHeadersInSlice verifies that nil entries in the headers
// slice are skipped gracefully without panic.
func TestHandleMessage_NilHeadersInSlice(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "nil-hdr-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	msgHeaders := []*sarama.RecordHeader{
		nil,
		{Key: []byte("x-request-id"), Value: []byte("req-1")},
		nil,
	}

	if err := h.HandleMessage(context.Background(), "events", 0, 5, []byte("payload"), msgHeaders); err != nil {
		t.Fatalf("HandleMessage with nil header entries: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	_ = fakeClient.List(context.Background(), list)
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	if got := list.Items[0].Spec.TriggerData.Headers["x-request-id"]; got != "req-1" {
		t.Errorf("x-request-id: want req-1, got %q", got)
	}
}
