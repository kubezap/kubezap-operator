package kafka

import (
	"context"
	"encoding/base64"
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
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
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

	if err := h.HandleMessage(context.Background(), "orders", 0, 42, nil, []byte(`{"order":"abc"}`), nil); err != nil {
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
	if fr.Spec.TriggerData.Key != "" {
		t.Errorf("TriggerData.Key = %q, want empty (nil record key)", fr.Spec.TriggerData.Key)
	}
	if fr.Spec.TriggerData.KeyEncoding != "" {
		t.Errorf("TriggerData.KeyEncoding = %q, want empty (nil record key)", fr.Spec.TriggerData.KeyEncoding)
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

	if err := h.HandleMessage(context.Background(), "topic", 0, 1, nil, []byte("payload"), nil); err != nil {
		t.Fatalf("first HandleMessage: %v", err)
	}
	// Second call with same partition+offset — FlowRun already exists.
	if err := h.HandleMessage(context.Background(), "topic", 0, 1, nil, []byte("payload"), nil); err != nil {
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

	if err := h.HandleMessage(context.Background(), "events", 1, 100, nil, []byte(`{"data":"test"}`), msgHeaders); err != nil {
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

	if err := h.HandleMessage(context.Background(), "events", 0, 5, nil, []byte("payload"), msgHeaders); err != nil {
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

// TestHandleMessage_KeyUTF8 verifies a valid-UTF-8 Kafka record key is stored
// verbatim on TriggerData.Key with KeyEncoding "utf8".
func TestHandleMessage_KeyUTF8(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "key-utf8-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	if err := h.HandleMessage(context.Background(), "orders", 0, 1, []byte("order-123"), []byte("payload"), nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	if td.Key != "order-123" {
		t.Errorf("TriggerData.Key = %q, want %q", td.Key, "order-123")
	}
	if td.KeyEncoding != "utf8" {
		t.Errorf("TriggerData.KeyEncoding = %q, want %q", td.KeyEncoding, "utf8")
	}
}

// TestHandleMessage_KeyBinary verifies a non-UTF-8 Kafka record key is
// base64-encoded on TriggerData.Key with KeyEncoding "base64".
func TestHandleMessage_KeyBinary(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "key-binary-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	// 0xFF, 0xFE is not valid UTF-8.
	binaryKey := []byte{0xFF, 0xFE, 0x00, 0x01}

	if err := h.HandleMessage(context.Background(), "orders", 0, 2, binaryKey, []byte("payload"), nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	wantKey := base64.StdEncoding.EncodeToString(binaryKey)
	if td.Key != wantKey {
		t.Errorf("TriggerData.Key = %q, want %q", td.Key, wantKey)
	}
	if td.KeyEncoding != "base64" {
		t.Errorf("TriggerData.KeyEncoding = %q, want %q", td.KeyEncoding, "base64")
	}
}

// TestHandleMessage_KeyNil verifies a nil Kafka record key leaves both
// TriggerData.Key and TriggerData.KeyEncoding unset.
func TestHandleMessage_KeyNil(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "key-nil-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	if err := h.HandleMessage(context.Background(), "orders", 0, 3, nil, []byte("payload"), nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	if td.Key != "" {
		t.Errorf("TriggerData.Key = %q, want empty", td.Key)
	}
	if td.KeyEncoding != "" {
		t.Errorf("TriggerData.KeyEncoding = %q, want empty", td.KeyEncoding)
	}
}

// TestHandleMessage_BodyUTF8 verifies a valid-UTF-8 payload is stored
// verbatim on TriggerData.Body with BodyEncoding "utf8".
func TestHandleMessage_BodyUTF8(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "body-utf8-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	if err := h.HandleMessage(context.Background(), "orders", 0, 10, nil, []byte(`{"order":"abc"}`), nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	if td.Body != `{"order":"abc"}` {
		t.Errorf("TriggerData.Body = %q", td.Body)
	}
	if td.BodyEncoding != "utf8" {
		t.Errorf("TriggerData.BodyEncoding = %q, want utf8", td.BodyEncoding)
	}
}

// TestHandleMessage_BodyBinary verifies a non-UTF-8 Kafka payload (e.g.
// Avro/Protobuf/schema-registry-encoded) is base64-encoded on
// TriggerData.Body with BodyEncoding "base64", rather than being silently
// corrupted to U+FFFD replacement characters when the FlowRun is
// JSON-marshaled into etcd — the bug
// docs/design/trigger-body-encoding-safety.md fixes.
func TestHandleMessage_BodyBinary(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "body-binary-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	binaryPayload := []byte{0x00, 0x01, 0xFF, 0xFE, 0xAB, 0xCD}

	if err := h.HandleMessage(context.Background(), "orders", 0, 11, nil, binaryPayload, nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	want := base64.StdEncoding.EncodeToString(binaryPayload)
	if td.Body != want {
		t.Errorf("TriggerData.Body = %q, want %q", td.Body, want)
	}
	if td.BodyEncoding != "base64" {
		t.Errorf("TriggerData.BodyEncoding = %q, want base64", td.BodyEncoding)
	}
}

// TestHandleMessage_BodySplitRuneBoundary verifies that a payload whose byte
// sequence is invalid as UTF-8 because it ends mid-rune (the exact shape a
// truncation splitting a multi-byte rune would produce, per
// docs/design/trigger-body-encoding-safety.md) is correctly classified as
// "base64" rather than silently mis-decoded. The Kafka gateway does not
// itself truncate payloads today (unlike the webhook gateway), so this
// exercises the encoding check directly against a payload with a dangling
// lead byte.
func TestHandleMessage_BodySplitRuneBoundary(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "body-split-rune-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	// "é" is the 2-byte UTF-8 sequence 0xC3 0xA9; keeping only the first byte
	// leaves a dangling lead byte with no continuation byte.
	full := []byte("é")
	splitPayload := full[:1]

	if err := h.HandleMessage(context.Background(), "orders", 0, 12, nil, splitPayload, nil); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun, got %d", len(list.Items))
	}

	td := list.Items[0].Spec.TriggerData
	want := base64.StdEncoding.EncodeToString(splitPayload)
	if td.Body != want {
		t.Errorf("TriggerData.Body = %q, want %q", td.Body, want)
	}
	if td.BodyEncoding != "base64" {
		t.Errorf("TriggerData.BodyEncoding = %q, want base64", td.BodyEncoding)
	}
}

// TestHandleMessage_DedupKeyNamingUnchangedByRecordKey verifies the FlowRun
// dedup-key naming scheme (<trigger>-p<partition>-offset-<offset>) does not
// incorporate the Kafka record key — Key is informational/interpolation-only,
// never a dedup input. Two messages with the same topic/partition/offset but
// DIFFERENT record keys must still collide on the same FlowRun name (the
// second is treated as a duplicate and skipped), and the resulting FlowRun
// name must match the pre-existing scheme exactly.
func TestHandleMessage_DedupKeyNamingUnchangedByRecordKey(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	log := zap.New()

	h := &MessageHandler{
		client:           fakeClient,
		log:              log,
		triggerName:      "dedup-trigger",
		triggerNamespace: "default",
		flowRefName:      "my-flow",
	}

	if err := h.HandleMessage(context.Background(), "topic", 2, 7, []byte("key-a"), []byte("payload-a"), nil); err != nil {
		t.Fatalf("first HandleMessage: %v", err)
	}
	// Same topic/partition/offset, different record key — must still be
	// treated as a duplicate of the same FlowRun name.
	if err := h.HandleMessage(context.Background(), "topic", 2, 7, []byte("key-b"), []byte("payload-b"), nil); err != nil {
		t.Fatalf("duplicate HandleMessage returned error: %v", err)
	}

	list := &automationv1alpha1.FlowRunList{}
	if err := fakeClient.List(context.Background(), list); err != nil {
		t.Fatalf("listing FlowRuns: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 FlowRun (key must not affect dedup), got %d", len(list.Items))
	}

	wantName := sanitizeFlowRunName("dedup-trigger-p2-offset-7")
	if list.Items[0].Name != wantName {
		t.Errorf("FlowRun name = %q, want %q (dedup-key naming scheme must be unchanged by record key)", list.Items[0].Name, wantName)
	}
	// The first message's key must be the one retained (Create wins; the
	// duplicate is a no-op), confirming the key is not part of the identity
	// used for dedup and does not overwrite the existing FlowRun.
	if list.Items[0].Spec.TriggerData.Key != "key-a" {
		t.Errorf("TriggerData.Key = %q, want %q (first write should win)", list.Items[0].Spec.TriggerData.Key, "key-a")
	}
}
