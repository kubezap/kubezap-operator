package kafka

import "context"

// MessageHandler converts a raw broker message into a FlowRun CRD.
// Implemented in KG-3 when sarama consumer group is wired.
type MessageHandler struct {
	// TODO: KG-3
}

// HandleMessage creates a FlowRun for the given raw message.
// topic, partition, offset identify the message for dedup key generation.
// payload is the raw message bytes.
func (h *MessageHandler) HandleMessage(ctx context.Context, topic string, partition int32, offset int64, payload []byte) error {
	// TODO: KG-3
	return nil
}
