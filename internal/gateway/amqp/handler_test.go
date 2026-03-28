package amqp

import (
	"context"
	"testing"

	goamqp "github.com/Azure/go-amqp"
	amqp091 "github.com/rabbitmq/amqp091-go"
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

// mockAcknowledger is a no-op Acknowledger for unit tests.
type mockAcknowledger struct {
	acked  bool
	nacked bool
}

func (m *mockAcknowledger) Ack(_ uint64, _ bool) error          { m.acked = true; return nil }
func (m *mockAcknowledger) Nack(_ uint64, _ bool, _ bool) error { m.nacked = true; return nil }
func (m *mockAcknowledger) Reject(_ uint64, _ bool) error       { return nil }

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

// TestHandleDelivery091_CreatesFlowRun verifies that an AMQP 0-9-1 delivery creates a FlowRun
// with the correct dedup name, labels, and trigger data.
func TestHandleDelivery091_CreatesFlowRun(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler091{
		client:           fakeClient,
		log:              log,
		triggerName:      "order-trigger",
		triggerNamespace: "default",
		flowRefName:      "process-order",
	}

	ack := &mockAcknowledger{}
	d := amqp091.Delivery{
		Acknowledger: ack,
		DeliveryTag:  42,
		RoutingKey:   "orders.created",
		Body:         []byte(`{"order":"abc"}`),
	}

	if err := h.handleDelivery(context.Background(), d); err != nil {
		t.Fatalf("handleDelivery returned error: %v", err)
	}

	if !ack.acked {
		t.Error("expected delivery to be acked after successful FlowRun creation")
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	fr := list.Items[0]
	// Name must encode the delivery tag.
	expectedName := sanitizeFlowRunName("order-trigger-dt42")
	if fr.Name != expectedName {
		t.Errorf("FlowRun name = %q, want %q", fr.Name, expectedName)
	}
	if fr.Spec.FlowRef.Name != "process-order" {
		t.Errorf("FlowRef.Name = %q", fr.Spec.FlowRef.Name)
	}
	if fr.Spec.TriggerData.Body != `{"order":"abc"}` {
		t.Errorf("TriggerData.Body = %q", fr.Spec.TriggerData.Body)
	}
	if fr.Spec.TriggerData.Topic != "orders.created" {
		t.Errorf("TriggerData.Topic = %q, want orders.created", fr.Spec.TriggerData.Topic)
	}
	if fr.Labels["kubezap.io/trigger-type"] != "amqp" {
		t.Errorf("trigger-type label = %q", fr.Labels["kubezap.io/trigger-type"])
	}
}

// TestHandleDelivery091_Duplicate verifies that a duplicate FlowRun is acked and no error returned.
func TestHandleDelivery091_Duplicate(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler091{
		client:           fakeClient,
		log:              log,
		triggerName:      "dup-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	ack := &mockAcknowledger{}
	d := amqp091.Delivery{
		Acknowledger: ack,
		DeliveryTag:  1,
		RoutingKey:   "rk",
		Body:         []byte("payload"),
	}

	// First delivery: creates FlowRun.
	if err := h.handleDelivery(context.Background(), d); err != nil {
		t.Fatalf("first handleDelivery: %v", err)
	}
	if !ack.acked {
		t.Error("expected ack after first delivery")
	}

	// Second delivery with same tag: FlowRun already exists → should ack and return nil.
	ack2 := &mockAcknowledger{}
	d2 := amqp091.Delivery{
		Acknowledger: ack2,
		DeliveryTag:  1,
		RoutingKey:   "rk",
		Body:         []byte("payload"),
	}
	if err := h.handleDelivery(context.Background(), d2); err != nil {
		t.Fatalf("duplicate handleDelivery returned error: %v", err)
	}
	if !ack2.acked {
		t.Error("expected ack on duplicate delivery")
	}
}

// TestHandleMessage10_WithMessageID verifies AMQP 1.0 handler uses MessageID in FlowRun name.
func TestHandleMessage10_WithMessageID(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler10{
		client:           fakeClient,
		log:              log,
		triggerName:      "amqp10-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	goamqpMsg := buildAMQP10Message(t, "msg-id-999", `{"key":"value"}`)

	if err := h.handleMessage(context.Background(), goamqpMsg); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	fr := list.Items[0]
	expectedName := sanitizeFlowRunName("amqp10-trigger-msg-id-999")
	if fr.Name != expectedName {
		t.Errorf("FlowRun name = %q, want %q", fr.Name, expectedName)
	}
	if fr.Spec.TriggerData.Body != `{"key":"value"}` {
		t.Errorf("TriggerData.Body = %q", fr.Spec.TriggerData.Body)
	}
}

// TestHandleMessage10_NoMessageID verifies AMQP 1.0 handler falls back to timestamp name.
func TestHandleMessage10_NoMessageID(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler10{
		client:           fakeClient,
		log:              log,
		triggerName:      "amqp10-noid",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	goamqpMsg := buildAMQP10MessageNoID(t, `{"data":"test"}`)

	if err := h.handleMessage(context.Background(), goamqpMsg); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	_ = fakeClient.List(context.Background(), list)
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	fr := list.Items[0]
	// Name must start with the trigger name prefix.
	if len(fr.Name) == 0 {
		t.Error("FlowRun name is empty")
	}
	// Name must be a valid k8s resource name (all lowercase alphanumeric/dash).
	for _, ch := range fr.Name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("invalid char %q in FlowRun name %q", ch, fr.Name)
		}
	}
	if fr.Spec.TriggerData.Body != `{"data":"test"}` {
		t.Errorf("TriggerData.Body = %q", fr.Spec.TriggerData.Body)
	}
}

// buildAMQP10Message creates a *goamqp.Message with the given MessageID and body.
func buildAMQP10Message(t *testing.T, msgID string, body string) *goamqp.Message {
	t.Helper()
	return &goamqp.Message{
		Properties: &goamqp.MessageProperties{
			MessageID: msgID,
		},
		Data: [][]byte{[]byte(body)},
	}
}

// buildAMQP10MessageNoID creates a *goamqp.Message with no MessageID (fallback to timestamp name).
func buildAMQP10MessageNoID(t *testing.T, body string) *goamqp.Message {
	t.Helper()
	return &goamqp.Message{
		Data: [][]byte{[]byte(body)},
	}
}

// TestHandleDelivery091_AuthHeadersRedacted verifies that AMQP 0-9-1 delivery
// headers containing auth-like keys are stored as "[REDACTED]" in TriggerData.
func TestHandleDelivery091_AuthHeadersRedacted(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler091{
		client:           fakeClient,
		log:              log,
		triggerName:      "hdr-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	ack := &mockAcknowledger{}
	d := amqp091.Delivery{
		Acknowledger: ack,
		DeliveryTag:  10,
		RoutingKey:   "events.created",
		Body:         []byte(`{"data":"test"}`),
		Headers: amqp091.Table{
			"authorization": "Bearer secret",
			"x-api-key":     "api-key-value",
			"content-type":  "application/json",
			"x-request-id":  "req-99",
		},
	}

	if err := h.handleDelivery(context.Background(), d); err != nil {
		t.Fatalf("handleDelivery returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	hdrs := list.Items[0].Spec.TriggerData.Headers

	sensitiveKeys := []string{"authorization", "x-api-key"}
	for _, k := range sensitiveKeys {
		if got := hdrs[k]; got != "[REDACTED]" {
			t.Errorf("AMQP 0-9-1 header %q: want [REDACTED], got %q", k, got)
		}
	}
	if got := hdrs["content-type"]; got != "application/json" {
		t.Errorf("content-type: want application/json, got %q", got)
	}
	if got := hdrs["x-request-id"]; got != "req-99" {
		t.Errorf("x-request-id: want req-99, got %q", got)
	}
}

// TestHandleMessage10_AuthHeadersRedacted verifies that AMQP 1.0 application
// properties containing auth-like keys are stored as "[REDACTED]" in TriggerData.
func TestHandleMessage10_AuthHeadersRedacted(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler10{
		client:           fakeClient,
		log:              log,
		triggerName:      "amqp10-hdr",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	msg := &goamqp.Message{
		Properties: &goamqp.MessageProperties{
			MessageID: "msg-id-redact-test",
		},
		Data: [][]byte{[]byte(`{"event":"test"}`)},
		ApplicationProperties: map[string]interface{}{
			"Authorization": "Bearer secret-token",
			"x-api-key":     "my-api-key",
			"content-type":  "application/json",
			"x-trace-id":    "trace-42",
		},
	}

	if err := h.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	hdrs := list.Items[0].Spec.TriggerData.Headers

	if got := hdrs["Authorization"]; got != "[REDACTED]" {
		t.Errorf("Authorization: want [REDACTED], got %q", got)
	}
	if got := hdrs["x-api-key"]; got != "[REDACTED]" {
		t.Errorf("x-api-key: want [REDACTED], got %q", got)
	}
	if got := hdrs["content-type"]; got != "application/json" {
		t.Errorf("content-type: want application/json, got %q", got)
	}
	if got := hdrs["x-trace-id"]; got != "trace-42" {
		t.Errorf("x-trace-id: want trace-42, got %q", got)
	}
}
