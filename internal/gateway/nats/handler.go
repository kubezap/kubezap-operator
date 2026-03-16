package natsgateway

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	natsio "github.com/nats-io/nats.go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
)

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

// MessageHandler converts raw NATS messages into FlowRun CRDs.
type MessageHandler struct {
	client           client.Client
	log              logr.Logger
	triggerName      string
	triggerNamespace string
	flowRefName      string
	isJetStream      bool
}

// HandleMessage is the nats.MsgHandler callback.
func (h *MessageHandler) HandleMessage(msg *natsio.Msg) {
	if err := h.handleMessage(msg); err != nil {
		h.log.Error(err, "failed to handle NATS message", "subject", msg.Subject)
		if h.isJetStream {
			_ = msg.Nak()
		}
		return
	}
	if h.isJetStream {
		_ = msg.Ack()
	}
}

func (h *MessageHandler) handleMessage(msg *natsio.Msg) error {
	var flowRunName string
	if h.isJetStream {
		meta, err := msg.Metadata()
		if err != nil {
			return fmt.Errorf("get JetStream metadata: %w", err)
		}
		rawName := fmt.Sprintf("%s-seq-%d", h.triggerName, meta.Sequence.Consumer)
		flowRunName = sanitizeFlowRunName(rawName)
	} else {
		// Core NATS: non-deterministic name using timestamp + random bytes.
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		rawName := fmt.Sprintf("%s-%d-%x", h.triggerName, time.Now().UnixNano(), b)
		flowRunName = sanitizeFlowRunName(rawName)
	}

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
				Source: "pubsub",
				Body:   string(msg.Data),
				Topic:  msg.Subject,
			},
		},
	}

	if err := h.client.Create(context.Background(), flowRun); err != nil {
		if apierrors.IsAlreadyExists(err) {
			h.log.V(1).Info("FlowRun already exists, skipping", "flowRun", flowRunName)
			return nil
		}
		return err
	}

	h.log.Info("created FlowRun", "flowRun", flowRunName, "subject", msg.Subject)
	return nil
}
