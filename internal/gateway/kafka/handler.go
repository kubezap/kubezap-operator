package kafka

import (
	"context"

	"github.com/IBM/sarama"
)

// MessageHandler implements sarama.ConsumerGroupHandler and converts raw broker
// messages into FlowRun CRDs. Full FlowRun creation is implemented in KG-3.
type MessageHandler struct {
	// TODO: KG-3 — add client, log, triggerNamespace, triggerName fields
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
		if err := h.HandleMessage(session.Context(), msg.Topic, msg.Partition, msg.Offset, msg.Value); err != nil {
			// Log error but continue consuming — do not stop the claim loop on a single failure.
			_ = err
		}
		session.MarkMessage(msg, "")
	}
	return nil
}

// HandleMessage creates a FlowRun for the given raw message.
// topic, partition, offset identify the message for dedup key generation.
// payload is the raw message bytes.
func (h *MessageHandler) HandleMessage(_ context.Context, _ string, _ int32, _ int64, _ []byte) error {
	// TODO: KG-3 — build FlowRun name from "<trigger>-p<partition>-offset-<offset>" and create via k8s client
	return nil
}
