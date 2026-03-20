package webhook

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/borfswitch/kubezap/api/v1alpha1"
)

const mockBodyLimit = 4 * 1024 // 4 KB

// MockHandler is an http.Handler that serves /mock/* routes.
type MockHandler struct {
	registry  *MockRegistry
	k8sClient client.Client
	log       logr.Logger
}

// NewMockHandler creates a MockHandler backed by the given registry.
func NewMockHandler(registry *MockRegistry, k8sClient client.Client, log logr.Logger) *MockHandler {
	return &MockHandler{registry: registry, k8sClient: k8sClient, log: log}
}

func (h *MockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	entry, ok := h.registry.Lookup(path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	resp := h.registry.NextResponse(path)

	// Apply artificial delay.
	if resp != nil && resp.DelayMs > 0 {
		time.Sleep(time.Duration(resp.DelayMs) * time.Millisecond)
	}

	// Write configured response headers.
	if resp != nil {
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
	}

	// Write status code.
	statusCode := http.StatusOK
	if resp != nil && resp.StatusCode != 0 {
		statusCode = int(resp.StatusCode)
	}
	w.WriteHeader(statusCode)

	// Write body.
	if resp != nil && resp.Body != "" {
		_, _ = w.Write([]byte(resp.Body))
	}

	// Capture request details.
	capturedHeaders := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		capturedHeaders[k] = redactHeader(k, v)
	}

	limitedReader := io.LimitReader(r.Body, mockBodyLimit+1)
	bodyBytes, _ := io.ReadAll(limitedReader)
	bodyTruncated := false
	capturedBody := string(bodyBytes)
	if len(bodyBytes) > mockBodyLimit {
		capturedBody = string(bodyBytes[:mockBodyLimit])
		bodyTruncated = true
	}

	go h.updateCapturedRequest(context.Background(), entry.Namespace, entry.Name, automationv1alpha1.CapturedRequest{
		Timestamp:          metav1.Now(),
		Method:             r.Method,
		Path:               r.URL.Path,
		Headers:            capturedHeaders,
		Body:               capturedBody,
		BodyTruncated:      bodyTruncated,
		ResponseStatusCode: int32(statusCode),
	}, entry.MaxHistory)
}

func (h *MockHandler) updateCapturedRequest(ctx context.Context, namespace, name string,
	captured automationv1alpha1.CapturedRequest, maxHistory int32) {

	me := &automationv1alpha1.MockEndpoint{}
	if err := h.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, me); err != nil {
		h.log.Error(err, "failed to fetch MockEndpoint for status update", "name", name)
		return
	}
	base := me.DeepCopy()
	me.Status.RecentRequests = append(me.Status.RecentRequests, captured)
	if maxHistory > 0 && int32(len(me.Status.RecentRequests)) > maxHistory {
		me.Status.RecentRequests = me.Status.RecentRequests[int32(len(me.Status.RecentRequests))-maxHistory:]
	}
	me.Status.RequestCount++
	if err := h.k8sClient.Status().Patch(ctx, me, client.MergeFrom(base)); err != nil {
		h.log.Error(err, "failed to update MockEndpoint status", "name", name)
	}
}
