package amqp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	goamqp "github.com/Azure/go-amqp"
	"github.com/go-logr/logr"
	amqp091 "github.com/rabbitmq/amqp091-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/redact"
	"github.com/kubezap/kubezap-operator/internal/metrics"
)

// triggerTypeAMQP is the TriggerSpec.Type value "amqp".
const triggerTypeAMQP = "amqp"

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

// --------------------------------------------------------------------------
// AMQP 0-9-1 handler
// --------------------------------------------------------------------------

// MessageHandler091 converts AMQP 0-9-1 deliveries into FlowRun CRDs.
type MessageHandler091 struct {
	client           client.Client
	log              logr.Logger
	triggerName      string
	triggerNamespace string
	flowRefName      string
}

// Run consumes deliveries until ctx is cancelled or the channel is closed.
func (h *MessageHandler091) Run(ctx context.Context, deliveries <-chan amqp091.Delivery) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				// Channel was closed by the broker or connection drop.
				return
			}
			if err := h.handleDelivery(ctx, d); err != nil {
				h.log.Error(err, "failed to handle amqp 0-9-1 delivery",
					"trigger", h.triggerName,
					"deliveryTag", d.DeliveryTag,
				)
				// Nack and requeue so the broker retries delivery.
				if nackErr := d.Nack(false, true); nackErr != nil {
					h.log.Error(nackErr, "failed to nack delivery", "deliveryTag", d.DeliveryTag)
				}
			}
		}
	}
}

// handleDelivery creates a FlowRun for a single AMQP 0-9-1 delivery.
func (h *MessageHandler091) handleDelivery(ctx context.Context, d amqp091.Delivery) error {
	rawName := fmt.Sprintf("%s-dt%d", h.triggerName, d.DeliveryTag)
	flowRunName := sanitizeFlowRunName(rawName)

	routingKey := d.RoutingKey

	// Convert AMQP 0-9-1 table headers to map[string]string and redact sensitive keys.
	hdrs := make(map[string]string, len(d.Headers))
	for k, v := range d.Headers {
		hdrs[k] = fmt.Sprintf("%v", v)
	}
	redact.StringMap(hdrs, nil)

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: h.triggerNamespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      h.triggerName,
				"kubezap.io/trigger-type": triggerTypeAMQP,
				"kubezap.io/flow":         h.flowRefName,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: h.flowRefName},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: h.triggerName,
				Type: triggerTypeAMQP,
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:  triggerTypeAMQP,
				Body:    string(d.Body),
				Headers: hdrs,
				Topic:   routingKey,
			},
		},
	}

	if err := h.client.Create(ctx, flowRun); err != nil {
		if apierrors.IsAlreadyExists(err) {
			h.log.V(1).Info("FlowRun already exists, skipping",
				"flowRun", flowRunName,
				"routingKey", routingKey,
				"deliveryTag", d.DeliveryTag,
			)
			metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "success").Inc()
			// Ack even on duplicate to prevent infinite redelivery.
			return d.Ack(false)
		}
		metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "error").Inc()
		return err
	}

	metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "success").Inc()
	h.log.Info("created FlowRun",
		"flowRun", flowRunName,
		"routingKey", routingKey,
		"deliveryTag", d.DeliveryTag,
	)

	// Ack after successful FlowRun creation.
	return d.Ack(false)
}

// --------------------------------------------------------------------------
// AMQP 1.0 handler
// --------------------------------------------------------------------------

// MessageHandler10 converts AMQP 1.0 messages into FlowRun CRDs.
type MessageHandler10 struct {
	client           client.Client
	log              logr.Logger
	triggerName      string
	triggerNamespace string
	flowRefName      string
}

// Run receives messages until ctx is cancelled or the receiver is closed.
func (h *MessageHandler10) Run(ctx context.Context, receiver *goamqp.Receiver) {
	for {
		msg, err := receiver.Receive(ctx, nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.log.Error(err, "amqp 1.0 receive error", "trigger", h.triggerName)
			return
		}

		if handleErr := h.handleMessage(ctx, msg); handleErr != nil {
			h.log.Error(handleErr, "failed to handle amqp 1.0 message", "trigger", h.triggerName)
			// Release the message so the broker can redeliver.
			if releaseErr := receiver.ReleaseMessage(ctx, msg); releaseErr != nil {
				h.log.Error(releaseErr, "failed to release amqp 1.0 message")
			}
		} else {
			// Accept the message to acknowledge it.
			if acceptErr := receiver.AcceptMessage(ctx, msg); acceptErr != nil {
				h.log.Error(acceptErr, "failed to accept amqp 1.0 message")
			}
		}
	}
}

// handleMessage creates a FlowRun for a single AMQP 1.0 message.
func (h *MessageHandler10) handleMessage(ctx context.Context, msg *goamqp.Message) error {
	var flowRunName string
	if msg.Properties != nil && msg.Properties.MessageID != nil {
		msgID := fmt.Sprintf("%v", msg.Properties.MessageID)
		flowRunName = sanitizeFlowRunName(h.triggerName + "-" + msgID)
	} else {
		// Fallback: timestamp + short random suffix via UnixNano.
		ts := time.Now().UnixNano()
		flowRunName = sanitizeFlowRunName(fmt.Sprintf("%s-%d", h.triggerName, ts))
	}

	body := string(msg.GetData())

	// Convert AMQP 1.0 application properties to map[string]string and redact sensitive keys.
	hdrs := make(map[string]string, len(msg.ApplicationProperties))
	for k, v := range msg.ApplicationProperties {
		hdrs[k] = fmt.Sprintf("%v", v)
	}
	redact.StringMap(hdrs, nil)

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: h.triggerNamespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      h.triggerName,
				"kubezap.io/trigger-type": triggerTypeAMQP,
				"kubezap.io/flow":         h.flowRefName,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: h.flowRefName},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: h.triggerName,
				Type: triggerTypeAMQP,
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:  triggerTypeAMQP,
				Body:    body,
				Headers: hdrs,
			},
		},
	}

	if err := h.client.Create(ctx, flowRun); err != nil {
		if apierrors.IsAlreadyExists(err) {
			h.log.V(1).Info("FlowRun already exists, skipping", "flowRun", flowRunName)
			metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "success").Inc()
			return nil
		}
		metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "error").Inc()
		return err
	}

	metrics.TriggerFirings.WithLabelValues(h.triggerNamespace, h.triggerName, triggerTypeAMQP, "success").Inc()
	h.log.Info("created FlowRun", "flowRun", flowRunName)
	return nil
}
