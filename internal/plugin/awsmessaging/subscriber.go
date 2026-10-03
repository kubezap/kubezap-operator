package awsmessaging

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/redact"
)

const (
	// TriggerTypePlugin is the TriggerSpec.Type this plugin serves.
	TriggerTypePlugin = "plugin"

	traceparentKey        = "traceparent"
	traceparentAnnotation = "kubezap.io/traceparent"
)

var nonAlphaNumDash = regexp.MustCompile(`[^a-z0-9-]`)

// Options tunes the polling loop. Zero values select production defaults.
type Options struct {
	// MaxMessages per ReceiveMessage call (SQS max 10).
	MaxMessages int32
	// WaitTimeSeconds is the long-poll duration (SQS max 20).
	WaitTimeSeconds int32
	// RetryBase / RetryMax bound the exponential backoff applied after SQS
	// errors.
	RetryBase time.Duration
	RetryMax  time.Duration
	// DeleteTimeout bounds the DeleteMessage call made after a FlowRun create,
	// independent of subscription cancellation.
	DeleteTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxMessages <= 0 || o.MaxMessages > 10 {
		o.MaxMessages = 10
	}
	if o.WaitTimeSeconds < 0 || o.WaitTimeSeconds > 20 {
		o.WaitTimeSeconds = 20
	}
	if o.RetryBase <= 0 {
		o.RetryBase = time.Second
	}
	if o.RetryMax <= 0 {
		o.RetryMax = 30 * time.Second
	}
	if o.DeleteTimeout <= 0 {
		o.DeleteTimeout = 5 * time.Second
	}
	return o
}

// Subscriber turns Trigger{type: plugin} objects served by this Integration
// into SQS long-polling subscriptions that create one FlowRun per message.
type Subscriber struct {
	client          client.Client
	sqs             SQSAPI
	health          *Health
	log             logr.Logger
	namespace       string
	integrationName string
	opts            Options

	mu   sync.Mutex
	subs map[types.NamespacedName]*subscription
	wg   sync.WaitGroup
}

// subscription is one running (or config-invalid) per-Trigger poll loop.
type subscription struct {
	cancel      context.CancelFunc
	done        chan struct{}
	fingerprint string
}

// subscriptionSnapshot is the immutable per-Trigger data a poll loop needs.
type subscriptionSnapshot struct {
	key         types.NamespacedName
	queueURL    string
	flowRefName string
}

// NewSubscriber constructs a Subscriber. health may be nil in tests.
func NewSubscriber(c client.Client, q SQSAPI, health *Health, namespace, integrationName string,
	opts Options, log logr.Logger) *Subscriber {
	if health == nil {
		health = NewHealth()
	}
	return &Subscriber{
		client:          c,
		sqs:             q,
		health:          health,
		log:             log.WithValues("integration", integrationName, "namespace", namespace),
		namespace:       namespace,
		integrationName: integrationName,
		opts:            opts.withDefaults(),
		subs:            map[types.NamespacedName]*subscription{},
	}
}

// Selects implements the plugin contract's Trigger selection rule.
func (s *Subscriber) Selects(t *automationv1alpha1.Trigger) bool {
	return t.Namespace == s.namespace &&
		t.Spec.Type == TriggerTypePlugin &&
		t.Spec.Plugin != nil &&
		t.Spec.Plugin.IntegrationRef.Name == s.integrationName &&
		t.Spec.Enabled
}

// Reconcile brings the subscription for t in line with its spec: starts one
// for a newly selected Trigger, restarts it when the queue or Flow changed,
// and cancels it when the Trigger is no longer selected (disabled, repointed
// to another Integration, ...).
func (s *Subscriber) Reconcile(ctx context.Context, t *automationv1alpha1.Trigger) {
	key := types.NamespacedName{Namespace: t.Namespace, Name: t.Name}
	if !s.Selects(t) {
		s.Remove(key)
		return
	}
	queueURL := t.Spec.Plugin.Config[TriggerConfigQueueURL]
	flowRef := t.Spec.FlowRef.Name
	fp := queueURL + "\x00" + flowRef

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.subs[key]; ok {
		if cur.fingerprint == fp {
			return
		}
		s.log.Info("trigger changed, restarting subscription", "trigger", key.Name)
		cur.cancel()
		delete(s.subs, key)
	}

	snap := subscriptionSnapshot{key: key, queueURL: queueURL, flowRefName: flowRef}
	if err := ParseQueueURL(queueURL); err != nil {
		// The operator never validates plugin config; surface it here.
		s.log.Error(err, "invalid trigger plugin config; subscription not started", "trigger", key.Name)
		s.health.SetSession(key, false, err.Error())
		s.subs[key] = &subscription{cancel: func() {}, done: closedChan(), fingerprint: fp}
		return
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{cancel: cancel, done: make(chan struct{}), fingerprint: fp}
	s.subs[key] = sub
	s.health.SetSession(key, false, "session not yet established")
	s.log.Info("starting subscription", "trigger", key.Name, "queueUrl", queueURL)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(sub.done)
		s.poll(subCtx, snap)
	}()
}

// Remove cancels the subscription for key, if any. It does not wait for the
// poll goroutine; Stop does.
func (s *Subscriber) Remove(key types.NamespacedName) {
	s.mu.Lock()
	sub, ok := s.subs[key]
	delete(s.subs, key)
	s.mu.Unlock()
	if ok {
		sub.cancel()
		s.log.Info("subscription stopped", "trigger", key.Name)
	}
	s.health.Forget(key)
}

// Active returns the keys of Triggers with a subscription entry.
func (s *Subscriber) Active() []types.NamespacedName {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.NamespacedName, 0, len(s.subs))
	for k := range s.subs {
		out = append(out, k)
	}
	return out
}

// Stop cancels every subscription and waits for all poll goroutines to exit.
func (s *Subscriber) Stop() {
	s.mu.Lock()
	for k, sub := range s.subs {
		sub.cancel()
		delete(s.subs, k)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// EventHandler adapts Trigger informer events to Reconcile/Remove. ctx is the
// parent context for all poll goroutines.
func (s *Subscriber) EventHandler(ctx context.Context) toolscache.ResourceEventHandler {
	return toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if t, ok := obj.(*automationv1alpha1.Trigger); ok {
				s.Reconcile(ctx, t)
			}
		},
		UpdateFunc: func(_, obj interface{}) {
			if t, ok := obj.(*automationv1alpha1.Trigger); ok {
				s.Reconcile(ctx, t)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if tomb, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			if t, ok := obj.(*automationv1alpha1.Trigger); ok {
				s.Remove(types.NamespacedName{Namespace: t.Namespace, Name: t.Name})
			}
		},
	}
}

// poll is the per-Trigger loop: establish an SQS session, then long-poll.
func (s *Subscriber) poll(ctx context.Context, snap subscriptionSnapshot) {
	log := s.log.WithValues("trigger", snap.key.Name)
	backoff := s.opts.RetryBase
	established := false

	for ctx.Err() == nil {
		if !established {
			_, err := s.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
				QueueUrl:       aws.String(snap.queueURL),
				AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
			})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error(err, "SQS session check failed", "queueUrl", snap.queueURL)
				s.health.SetSession(snap.key, false, err.Error())
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff, s.opts.RetryMax)
				continue
			}
			established = true
			backoff = s.opts.RetryBase
			s.health.SetSession(snap.key, true, "")
			log.Info("SQS session established", "queueUrl", snap.queueURL)
		}

		out, err := s.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              aws.String(snap.queueURL),
			MaxNumberOfMessages:   s.opts.MaxMessages,
			WaitTimeSeconds:       s.opts.WaitTimeSeconds,
			MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error(err, "SQS ReceiveMessage failed", "queueUrl", snap.queueURL)
			s.health.SetSession(snap.key, false, err.Error())
			established = false
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.opts.RetryMax)
			continue
		}
		backoff = s.opts.RetryBase
		for i := range out.Messages {
			if ctx.Err() != nil {
				return
			}
			s.handleMessage(ctx, snap, &out.Messages[i])
		}
	}
}

// handleMessage creates the FlowRun for msg and, only once that returned
// success (created or 409 AlreadyExists), deletes the SQS message. On any
// other create error the message is left on the queue so the visibility
// timeout redelivers it.
func (s *Subscriber) handleMessage(ctx context.Context, snap subscriptionSnapshot, msg *sqstypes.Message) {
	log := s.log.WithValues("trigger", snap.key.Name)
	if msg.MessageId == nil || *msg.MessageId == "" {
		log.Error(fmt.Errorf("message has no MessageId"), "skipping message; not deleting")
		return
	}
	fr := BuildFlowRun(snap.key.Namespace, snap.key.Name, snap.flowRefName, msg)
	log = log.WithValues("flowrun", fr.Name, "messageId", *msg.MessageId)

	if err := s.client.Create(ctx, fr); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			if ctx.Err() == nil {
				log.Error(err, "failed to create FlowRun; leaving message for redelivery")
			}
			return
		}
		log.V(1).Info("flowrun already exists, treating as duplicate")
	} else {
		log.Info("flowrun created")
	}

	// Detach from subscription cancellation so a FlowRun that was created just
	// as the Trigger is removed / the pod shuts down still gets its message
	// deleted instead of being redelivered (harmless, but wasteful).
	delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.DeleteTimeout)
	defer cancel()
	if _, err := s.sqs.DeleteMessage(delCtx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(snap.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		log.Error(err, "failed to delete SQS message after FlowRun create; it will be redelivered and deduplicated")
	}
}

// FlowRunName derives the deterministic FlowRun name (dedup key) for an SQS
// message: <trigger>-msg-<messageId>, sanitized to a valid Kubernetes name.
func FlowRunName(triggerName, messageID string) string {
	name := strings.ToLower(triggerName + "-msg-" + messageID)
	name = nonAlphaNumDash.ReplaceAllString(name, "-")
	if len(name) > 253 {
		name = name[:253]
	}
	return strings.TrimRight(name, "-")
}

// BuildFlowRun builds the FlowRun for an SQS message per
// docs/api/plugin-contract.md (labels, TriggerData, traceparent annotation).
func BuildFlowRun(namespace, triggerName, flowRefName string, msg *sqstypes.Message) *automationv1alpha1.FlowRun {
	body := aws.ToString(msg.Body)
	bodyEncoding := "utf8"
	if !utf8.ValidString(body) {
		body = base64.StdEncoding.EncodeToString([]byte(body))
		bodyEncoding = "base64"
	}

	hdrs := make(map[string]string, len(msg.MessageAttributes))
	for k, v := range msg.MessageAttributes {
		switch {
		case v.StringValue != nil:
			hdrs[k] = *v.StringValue
		case len(v.BinaryValue) > 0:
			hdrs[k] = base64.StdEncoding.EncodeToString(v.BinaryValue)
		}
	}
	// Capture traceparent before redaction/headers are handed over.
	tp := hdrs[traceparentKey]
	redact.StringMap(hdrs, nil)

	fr := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      FlowRunName(triggerName, aws.ToString(msg.MessageId)),
			Namespace: namespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      triggerName,
				"kubezap.io/trigger-type": TriggerTypePlugin,
				"kubezap.io/flow":         flowRefName,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef:    automationv1alpha1.FlowReference{Name: flowRefName},
			TriggerRef: &automationv1alpha1.TriggerReference{Name: triggerName, Type: TriggerTypePlugin},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:       TriggerTypePlugin,
				Body:         body,
				BodyEncoding: bodyEncoding,
				Headers:      hdrs,
			},
		},
	}
	if tp != "" {
		fr.Annotations = map[string]string{traceparentAnnotation: tp}
	}
	return fr
}

func nextBackoff(cur, max time.Duration) time.Duration {
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

// sleepCtx sleeps for d, returning false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
