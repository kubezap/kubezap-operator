package kafka

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

// traceParentKey is an unexported context key for carrying W3C traceparent values.
type traceParentKey struct{}

// nonAlphaNumDash matches any character that is not a lowercase letter, digit, or dash.
var nonAlphaNumDash = regexp.MustCompile(`[^a-z0-9-]`)

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
		if err := h.HandleMessage(ctx, msg.Topic, msg.Partition, msg.Offset, msg.Value); err != nil {
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
// payload is the raw message bytes.
func (h *MessageHandler) HandleMessage(ctx context.Context, topic string, partition int32, offset int64, payload []byte) error {
	rawName := fmt.Sprintf("%s-p%d-offset-%d", h.triggerName, partition, offset)
	flowRunName := sanitizeFlowRunName(rawName)

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: h.triggerNamespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      h.triggerName,
				"kubezap.io/trigger-type": "pubsub",
				"kubezap.io/flow":         h.flowRefName,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: corev1.LocalObjectReference{Name: h.flowRefName},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: h.triggerName,
				Type: "pubsub",
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:    "pubsub",
				Body:      string(payload),
				Topic:     topic,
				Partition: partition,
				Offset:    offset,
			},
		},
	}

	// Attach W3C traceparent as an annotation if present in context.
	if tp, ok := ctx.Value(traceParentKey{}).(string); ok && tp != "" {
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
			return nil
		}
		return err
	}

	h.log.Info("created FlowRun",
		"flowRun", flowRunName,
		"topic", topic,
		"partition", partition,
		"offset", offset,
	)
	return nil
}
