package kafka

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/redact"
	"github.com/kubezap/kubezap-operator/internal/metrics"
)

// triggerTypeKafka is the TriggerSpec.Type value "kafka".
const triggerTypeKafka = "kafka"

// traceParentKey is an unexported context key for carrying W3C traceparent values.
type traceParentKey struct{}

// nonAlphaNumDash matches any character that is not a lowercase letter, digit, or dash.
var nonAlphaNumDash = regexp.MustCompile(`[^a-z0-9-]`)

// extractKafkaTraceContext reads the W3C traceparent value (if any) stashed
// under traceParentKey by ConsumeClaim and, if present, extracts it into the
// returned context via the standard OTel propagator so that
// kafka_message_received becomes a child of the originating trace. Mirrors
// extractTraceContext in internal/controller/flowrun_controller.go, adapted
// for the raw string carried via context.Value rather than a FlowRun
// annotation map.
func extractKafkaTraceContext(ctx context.Context) context.Context {
	tp, ok := ctx.Value(traceParentKey{}).(string)
	if !ok || tp == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": tp})
}

// sanitizeFlowRunName converts a raw name to a valid Kubernetes resource name:
// lowercase, non-alphanumeric-or-dash replaced with '-', truncated to 253 chars.
func sanitizeFlowRunName(name string) string {
	name = strings.ToLower(name)
	name = nonAlphaNumDash.ReplaceAllString(name, "-")
	if len(name) > 253 {
		name = name[:253]
	}
	return name
}

// MessageHandler implements sarama.ConsumerGroupHandler and converts raw broker
// messages into FlowRun CRDs.
type MessageHandler struct {
	client           client.Client
	log              logr.Logger
	triggerName      string
	triggerNamespace string
	flowRefName      string // copied from trigger.Spec.FlowRef.Name at subscription start
}

// Setup is called at the start of a new consumer group session.
func (h *MessageHandler) Setup(_ sarama.ConsumerGroupSession) error {
	return nil
}

// Cleanup is called at the end of a consumer group session.
func (h *MessageHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim processes messages from a single topic/partition claim.
func (h *MessageHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		ctx := session.Context()
		// Extract W3C traceparent from Kafka message headers and propagate via context.
		for _, hdr := range msg.Headers {
			if string(hdr.Key) == "traceparent" {
				ctx = context.WithValue(ctx, traceParentKey{}, string(hdr.Value))
				break
			}
		}
		if err := h.HandleMessage(ctx, msg.Topic, msg.Partition, msg.Offset, msg.Key, msg.Value, msg.Headers); err != nil {
			// Log error but continue consuming — do not stop the claim loop on a single failure.
			h.log.Error(err, "failed to handle kafka message",
				"topic", msg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
			)
		}
		session.MarkMessage(msg, "")
	}
	return nil
}

// HandleMessage creates a FlowRun for the given raw message.
// topic, partition, offset identify the message for dedup key generation.
// key is the raw Kafka record key (nil if the record has no key); it is
// captured on TriggerData.Key verbatim as UTF-8 when valid, or base64-encoded
// otherwise — see TriggerData.KeyEncoding. It is never part of the FlowRun
// dedup-key naming scheme (topic/partition/offset only), only informational
// and available for $(trigger.key) interpolation. payload is the raw message
// bytes. msgHeaders contains the Kafka message headers; auth-like keys are
// redacted before being stored in TriggerData.
func (h *MessageHandler) HandleMessage(ctx context.Context, topic string, partition int32, offset int64, key, payload []byte, msgHeaders []*sarama.RecordHeader) error {
	// kafka_message_received is the root span for Kafka-triggered flows. If
	// the broker message itself carried an upstream W3C traceparent header
	// (extracted into ctx by ConsumeClaim), continue that trace; otherwise
	// this starts a new one.
	ctx = extractKafkaTraceContext(ctx)
	ctx, span := otel.Tracer("kubezap.io/kafka").Start(ctx, "kafka_message_received",
		trace.WithAttributes(
			attribute.String("kubezap.trigger.name", h.triggerName),
			attribute.String("kubezap.trigger.type", triggerTypeKafka),
			attribute.String("kafka.topic", topic),
			attribute.Int64("kafka.partition", int64(partition)),
			attribute.Int64("kafka.offset", offset),
		))
	defer span.End()

	rawName := fmt.Sprintf("%s-p%d-offset-%d", h.triggerName, partition, offset)
	flowRunName := sanitizeFlowRunName(rawName)

	// Convert Kafka record headers to map[string]string and redact sensitive keys.
	hdrs := make(map[string]string, len(msgHeaders))
	for _, rh := range msgHeaders {
		if rh != nil {
			hdrs[string(rh.Key)] = string(rh.Value)
		}
	}
	redact.StringMap(hdrs, nil)

	// Capture the Kafka record key, if any. A nil key leaves both fields at
	// their zero value. A non-nil key is stored verbatim when it decodes as
	// valid UTF-8, or base64-encoded otherwise — see docs/design/kafka-message-key-capture.md.
	var keyStr, keyEncoding string
	if key != nil {
		if utf8.Valid(key) {
			keyStr = string(key)
			keyEncoding = "utf8"
		} else {
			keyStr = base64.StdEncoding.EncodeToString(key)
			keyEncoding = "base64"
		}
	}

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: h.triggerNamespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      h.triggerName,
				"kubezap.io/trigger-type": triggerTypeKafka,
				"kubezap.io/flow":         h.flowRefName,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: h.flowRefName},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: h.triggerName,
				Type: triggerTypeKafka,
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:      triggerTypeKafka,
				Body:        string(payload),
				Headers:     hdrs,
				Topic:       topic,
				Partition:   partition,
				Offset:      offset,
				Key:         keyStr,
				KeyEncoding: keyEncoding,
			},
		},
	}

	// Attach the kafka_message_received span's own W3C traceparent as an
	// annotation so that flowrun.reconcile (running in a separate process)
	// becomes its child, mirroring the webhook handler's pattern in
	// internal/gateway/webhook/handler.go. This must inject the *current*
	// span context (ctx, carrying kafka_message_received started above) —
	// not just forward the raw incoming header captured by ConsumeClaim —
	// otherwise a Kafka message without its own upstream traceparent header
	// (the common case; most producers don't set one) would leave the
	// FlowRun with no annotation at all, breaking trace continuity for
	// nearly every Kafka-triggered flow.
	traceCarrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, traceCarrier)
	if tp := traceCarrier.Get("traceparent"); tp != "" {
		flowRun.Annotations = map[string]string{
			"kubezap.io/traceparent": tp,
		}
	}

	if err := h.client.Create(ctx, flowRun); err != nil {
		if apierrors.IsAlreadyExists(err) {
			h.log.V(1).Info("FlowRun already exists, skipping",
				"flowRun", flowRunName,
				"topic", topic,
				"partition", partition,
				"offset", offset,
			)
			metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeKafka, "success").Inc()
			return nil
		}
		metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeKafka, "error").Inc()
		return err
	}

	metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeKafka, "success").Inc()
	h.log.Info("created FlowRun",
		"flowRun", flowRunName,
		"topic", topic,
		"partition", partition,
		"offset", offset,
	)
	return nil
}
