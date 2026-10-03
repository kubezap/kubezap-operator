package awsmessaging

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

const (
	testNS    = "team-a"
	testInteg = "aws"
	queueA    = "http://localstack:4566/000000000000/queue-a"
	queueB    = "http://localstack:4566/000000000000/queue-b"
)

// eventLog records the global order of create/delete calls.
type eventLog struct {
	mu sync.Mutex
	ev []string
}

func (l *eventLog) add(s string) { l.mu.Lock(); l.ev = append(l.ev, s); l.mu.Unlock() }
func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ev...)
}

// fakeSQS is an in-memory SQSAPI. ReceiveMessage returns queued messages or,
// when empty, behaves like a short long-poll that honors ctx cancellation.
type fakeSQS struct {
	mu        sync.Mutex
	pending   map[string][]sqstypes.Message
	deleted   []string
	log       *eventLog
	attrErr   error
	attrCalls int
	recvCalls map[string]int
	cancelled map[string]int // ReceiveMessage calls ended by ctx cancellation, per queue
}

func newFakeSQS(l *eventLog) *fakeSQS {
	return &fakeSQS{pending: map[string][]sqstypes.Message{}, log: l,
		recvCalls: map[string]int{}, cancelled: map[string]int{}}
}

func (f *fakeSQS) enqueue(queue string, msgs ...sqstypes.Message) {
	f.mu.Lock()
	f.pending[queue] = append(f.pending[queue], msgs...)
	f.mu.Unlock()
}

func (f *fakeSQS) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attrCalls++
	if f.attrErr != nil {
		return nil, f.attrErr
	}
	return &sqs.GetQueueAttributesOutput{}, nil
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	q := aws.ToString(in.QueueUrl)
	f.mu.Lock()
	f.recvCalls[q]++
	if p := f.pending[q]; len(p) > 0 {
		n := min(int(in.MaxNumberOfMessages), len(p))
		out := append([]sqstypes.Message(nil), p[:n]...)
		f.pending[q] = p[n:]
		f.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: out}, nil
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		f.mu.Lock()
		f.cancelled[q]++
		f.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return &sqs.ReceiveMessageOutput{}, nil
	}
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.log.add("delete:" + aws.ToString(in.ReceiptHandle))
	f.mu.Lock()
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	f.mu.Unlock()
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) deletedHandles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *fakeSQS) cancelledCount(q string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled[q]
}

func (f *fakeSQS) receives(q string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recvCalls[q]
}

func msg(id, handle, body string) sqstypes.Message {
	return sqstypes.Message{MessageId: aws.String(id), ReceiptHandle: aws.String(handle), Body: aws.String(body)}
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = automationv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newTrigger(name, queue string) *automationv1alpha1.Trigger {
	return &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    TriggerTypePlugin,
			Enabled: true,
			Plugin: &automationv1alpha1.PluginTrigger{
				IntegrationRef: corev1.LocalObjectReference{Name: testInteg},
				Config:         map[string]string{TriggerConfigQueueURL: queue},
			},
			FlowRef: automationv1alpha1.FlowReference{Name: "process"},
		},
	}
}

type harness struct {
	t      *testing.T
	log    *eventLog
	sqs    *fakeSQS
	k8s    client.Client
	sub    *Subscriber
	health *Health
	ctx    context.Context
	// createErr, when non-nil, is returned by FlowRun Create.
	mu        sync.Mutex
	createErr error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, log: &eventLog{}, health: NewHealth()}
	h.sqs = newFakeSQS(h.log)
	h.k8s = fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			h.mu.Lock()
			cerr := h.createErr
			h.mu.Unlock()
			h.log.add("create:" + obj.GetName())
			if cerr != nil {
				return cerr
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	h.sub = NewSubscriber(h.k8s, h.sqs, h.health, testNS, testInteg,
		Options{WaitTimeSeconds: 0, RetryBase: 2 * time.Millisecond, RetryMax: 10 * time.Millisecond},
		logr.Discard())
	var cancel context.CancelFunc
	h.ctx, cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); h.sub.Stop() })
	return h
}

func (h *harness) setCreateErr(err error) { h.mu.Lock(); h.createErr = err; h.mu.Unlock() }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func (h *harness) getFlowRun(name string) (*automationv1alpha1.FlowRun, error) {
	fr := &automationv1alpha1.FlowRun{}
	err := h.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: name}, fr)
	return fr, err
}

func TestFlowRunName(t *testing.T) {
	cases := map[[2]string]string{
		{"orders", "5fea7756-0ea4-451a-a703-a558b933e274"}: "orders-msg-5fea7756-0ea4-451a-a703-a558b933e274",
		{"Orders_V2", "ABC.def"}:                           "orders-v2-msg-abc-def",
	}
	for in, want := range cases {
		if got := FlowRunName(in[0], in[1]); got != want {
			t.Errorf("FlowRunName(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	long := FlowRunName("t", string(make([]byte, 400)))
	if len(long) > 253 {
		t.Errorf("name too long: %d", len(long))
	}
	// Deterministic: standard and FIFO queues use the same MessageId rule.
	first, second := FlowRunName("t", "id"), FlowRunName("t", "id")
	if first != second {
		t.Error("not deterministic")
	}
}

func TestBuildFlowRun(t *testing.T) {
	m := msg("m-1", "h-1", "hello")
	m.MessageAttributes = map[string]sqstypes.MessageAttributeValue{
		"traceparent":   {StringValue: aws.String("00-abc-def-01"), DataType: aws.String("String")},
		"x-tenant":      {StringValue: aws.String("acme"), DataType: aws.String("String")},
		"blob":          {BinaryValue: []byte("hi"), DataType: aws.String("Binary")},
		"authorization": {StringValue: aws.String("Bearer s3cret"), DataType: aws.String("String")},
	}
	fr := BuildFlowRun(testNS, "orders", "process", &m)
	if fr.Name != "orders-msg-m-1" || fr.Namespace != testNS {
		t.Errorf("name/ns = %s/%s", fr.Namespace, fr.Name)
	}
	wantLabels := map[string]string{
		"kubezap.io/trigger": "orders", "kubezap.io/trigger-type": "plugin", "kubezap.io/flow": "process"}
	for k, v := range wantLabels {
		if fr.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, fr.Labels[k], v)
		}
	}
	if fr.Annotations["kubezap.io/traceparent"] != "00-abc-def-01" {
		t.Errorf("traceparent annotation = %v", fr.Annotations)
	}
	td := fr.Spec.TriggerData
	if td.Source != "plugin" || td.Body != "hello" || td.BodyEncoding != "utf8" {
		t.Errorf("trigger data = %+v", td)
	}
	if td.Headers["x-tenant"] != "acme" || td.Headers["blob"] != base64.StdEncoding.EncodeToString([]byte("hi")) {
		t.Errorf("headers = %v", td.Headers)
	}
	if td.Headers["authorization"] == "Bearer s3cret" {
		t.Error("authorization header must be redacted")
	}
	if fr.Spec.FlowRef.Name != "process" || fr.Spec.TriggerRef.Name != "orders" {
		t.Errorf("refs = %+v %+v", fr.Spec.FlowRef, fr.Spec.TriggerRef)
	}

	plain := msg("m-2", "h-2", "x")
	if fr := BuildFlowRun(testNS, "orders", "process", &plain); fr.Annotations != nil {
		t.Errorf("no traceparent expected, got %v", fr.Annotations)
	}
}

func TestSelects(t *testing.T) {
	h := newHarness(t)
	tr := newTrigger("a", queueA)
	if !h.sub.Selects(tr) {
		t.Fatal("matching trigger should be selected")
	}
	mutations := map[string]func(*automationv1alpha1.Trigger){
		"disabled":          func(x *automationv1alpha1.Trigger) { x.Spec.Enabled = false },
		"other integration": func(x *automationv1alpha1.Trigger) { x.Spec.Plugin.IntegrationRef.Name = "other" },
		"other type":        func(x *automationv1alpha1.Trigger) { x.Spec.Type = "kafka" },
		"nil plugin":        func(x *automationv1alpha1.Trigger) { x.Spec.Plugin = nil },
		"other namespace":   func(x *automationv1alpha1.Trigger) { x.Namespace = "elsewhere" },
	}
	for name, mut := range mutations {
		c := tr.DeepCopy()
		mut(c)
		if h.sub.Selects(c) {
			t.Errorf("%s: should not be selected", name)
		}
	}
}

func TestDeleteAfterCreate_Ordering(t *testing.T) {
	h := newHarness(t)
	h.sqs.enqueue(queueA, msg("id-1", "rh-1", "body1"), msg("id-2", "rh-2", "body2"))
	h.sub.Reconcile(h.ctx, newTrigger("orders", queueA))

	eventually(t, "both messages deleted", func() bool { return len(h.sqs.deletedHandles()) == 2 })
	want := []string{"create:orders-msg-id-1", "delete:rh-1", "create:orders-msg-id-2", "delete:rh-2"}
	got := h.log.snapshot()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	if _, err := h.getFlowRun("orders-msg-id-1"); err != nil {
		t.Errorf("flowrun not created: %v", err)
	}
}

func TestConflictTreatedAsSuccess(t *testing.T) {
	h := newHarness(t)
	existing := BuildFlowRun(testNS, "orders", "process", &sqstypes.Message{
		MessageId: aws.String("dup"), Body: aws.String("original")})
	if err := h.k8s.Create(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	h.log.mu.Lock()
	h.log.ev = nil
	h.log.mu.Unlock()

	h.sqs.enqueue(queueA, msg("dup", "rh-dup", "redelivered"))
	h.sub.Reconcile(h.ctx, newTrigger("orders", queueA))

	eventually(t, "duplicate message deleted", func() bool { return len(h.sqs.deletedHandles()) == 1 })
	if h.sqs.deletedHandles()[0] != "rh-dup" {
		t.Errorf("deleted = %v", h.sqs.deletedHandles())
	}
	fr, err := h.getFlowRun("orders-msg-dup")
	if err != nil || fr.Spec.TriggerData.Body != "original" {
		t.Errorf("existing flowrun must be untouched: %v %+v", err, fr.Spec.TriggerData)
	}
	// Exactly one create attempt: 409 is not retried.
	creates := 0
	for _, e := range h.log.snapshot() {
		if e == "create:orders-msg-dup" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("create attempts = %d, want 1", creates)
	}
}

func TestCreateErrorDoesNotDelete(t *testing.T) {
	h := newHarness(t)
	h.setCreateErr(apierrors.NewInternalError(errors.New("boom")))
	h.sqs.enqueue(queueA, msg("id-1", "rh-1", "b"))
	h.sub.Reconcile(h.ctx, newTrigger("orders", queueA))

	eventually(t, "create attempted", func() bool { return len(h.log.snapshot()) >= 1 })
	// Keep polling for a while; the message must never be deleted.
	eventually(t, "polling continues", func() bool { return h.sqs.receives(queueA) >= 3 })
	if d := h.sqs.deletedHandles(); len(d) != 0 {
		t.Errorf("message deleted despite create failure: %v", d)
	}
	if _, err := h.getFlowRun("orders-msg-id-1"); !apierrors.IsNotFound(err) {
		t.Errorf("flowrun should not exist: %v", err)
	}
}

func TestDynamicSubscriptions(t *testing.T) {
	h := newHarness(t)
	tr := newTrigger("orders", queueA)

	// Added.
	h.sub.Reconcile(h.ctx, tr)
	eventually(t, "polling queue A", func() bool { return h.sqs.receives(queueA) > 0 })
	if len(h.sub.Active()) != 1 {
		t.Fatalf("active = %v", h.sub.Active())
	}

	// Unchanged update does not restart.
	h.sub.Reconcile(h.ctx, tr.DeepCopy())
	if n := h.sqs.cancelledCount(queueA); n != 0 {
		t.Errorf("no-op update restarted the subscription (%d cancellations)", n)
	}

	// Disabled: polling stops, goroutine exits.
	disabled := tr.DeepCopy()
	disabled.Spec.Enabled = false
	h.sub.Reconcile(h.ctx, disabled)
	eventually(t, "queue A poll cancelled", func() bool { return h.sqs.cancelledCount(queueA) == 1 })
	if len(h.sub.Active()) != 0 {
		t.Fatalf("active after disable = %v", h.sub.Active())
	}
	h.sqs.enqueue(queueA, msg("late", "rh-late", "x"))
	time.Sleep(30 * time.Millisecond)
	if len(h.sqs.deletedHandles()) != 0 {
		t.Error("disabled trigger still processed a message")
	}

	// Re-enabled, then modified to a different queue: old cancelled, new polled.
	h.sub.Reconcile(h.ctx, tr.DeepCopy())
	eventually(t, "re-enabled consumes pending", func() bool { return len(h.sqs.deletedHandles()) == 1 })
	moved := tr.DeepCopy()
	moved.Spec.Plugin.Config[TriggerConfigQueueURL] = queueB
	h.sub.Reconcile(h.ctx, moved)
	eventually(t, "queue A poll cancelled again", func() bool { return h.sqs.cancelledCount(queueA) == 2 })
	h.sqs.enqueue(queueB, msg("b-1", "rh-b1", "x"))
	eventually(t, "queue B consumed", func() bool { return len(h.sqs.deletedHandles()) == 2 })

	// Deleted.
	h.sub.Remove(types.NamespacedName{Namespace: testNS, Name: "orders"})
	eventually(t, "queue B poll cancelled", func() bool { return h.sqs.cancelledCount(queueB) == 1 })
	if len(h.sub.Active()) != 0 {
		t.Fatalf("active after delete = %v", h.sub.Active())
	}

	// Stop must return promptly (no leaked goroutines).
	done := make(chan struct{})
	go func() { h.sub.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
}

func TestStopCancelsAll(t *testing.T) {
	h := newHarness(t)
	h.sub.Reconcile(h.ctx, newTrigger("a", queueA))
	h.sub.Reconcile(h.ctx, newTrigger("b", queueB))
	eventually(t, "both polling", func() bool { return h.sqs.receives(queueA) > 0 && h.sqs.receives(queueB) > 0 })
	h.sub.Stop()
	if h.sqs.cancelledCount(queueA) != 1 || h.sqs.cancelledCount(queueB) != 1 {
		t.Errorf("cancelled A=%d B=%d", h.sqs.cancelledCount(queueA), h.sqs.cancelledCount(queueB))
	}
	if len(h.sub.Active()) != 0 {
		t.Error("subscriptions remain after Stop")
	}
}

func TestIgnoresOtherIntegrations(t *testing.T) {
	h := newHarness(t)
	tr := newTrigger("x", queueA)
	tr.Spec.Plugin.IntegrationRef.Name = "someone-else"
	h.sub.Reconcile(h.ctx, tr)
	if len(h.sub.Active()) != 0 || h.sqs.receives(queueA) != 0 {
		t.Error("trigger for another integration must be ignored")
	}
}

func TestInvalidQueueURL_SurfacedInHealth(t *testing.T) {
	h := newHarness(t)
	h.health.SetStarted()
	h.sub.Reconcile(h.ctx, newTrigger("bad", "not-a-url"))
	if err := h.health.Check(); err == nil {
		t.Fatal("health should fail for malformed queueUrl")
	}
	if h.sqs.receives("not-a-url") != 0 || h.sqs.attrCalls != 0 {
		t.Error("no SQS calls expected for malformed queue URL")
	}
	// Fixing the Trigger starts the subscription and clears the failure.
	h.sub.Reconcile(h.ctx, newTrigger("bad", queueA))
	eventually(t, "healthy after fix", func() bool { return h.health.Check() == nil })
	// Removing the broken Trigger clears its health entry.
	h.sub.Reconcile(h.ctx, newTrigger("bad2", ""))
	if h.health.Check() == nil {
		t.Fatal("empty queueUrl should be unhealthy")
	}
	h.sub.Remove(types.NamespacedName{Namespace: testNS, Name: "bad2"})
	if err := h.health.Check(); err != nil {
		t.Errorf("health after removal: %v", err)
	}
}

func TestHealthz(t *testing.T) {
	h := newHarness(t)
	code := func() int {
		rec := httptest.NewRecorder()
		h.health.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		return rec.Code
	}
	if code() != http.StatusServiceUnavailable {
		t.Error("expected 503 before initial sync")
	}
	h.health.SetStarted()
	if code() != http.StatusOK {
		t.Error("idle plugin (no subscriptions) should be ready")
	}

	// SQS unreachable: 503 until a session is established.
	h.sqs.mu.Lock()
	h.sqs.attrErr = errors.New("AccessDenied")
	h.sqs.mu.Unlock()
	h.sub.Reconcile(h.ctx, newTrigger("orders", queueA))
	eventually(t, "503 while SQS failing", func() bool { return code() == http.StatusServiceUnavailable })
	if h.sqs.receives(queueA) != 0 {
		t.Error("must not receive before a session is established")
	}

	h.sqs.mu.Lock()
	h.sqs.attrErr = nil
	h.sqs.mu.Unlock()
	eventually(t, "200 once session established", func() bool { return code() == http.StatusOK })
}

func TestMux_OnlyHealthz(t *testing.T) {
	h := NewHealth()
	h.SetStarted()
	srv := httptest.NewServer(h.Mux())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
