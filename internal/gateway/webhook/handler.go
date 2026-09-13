package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/redact"
	"github.com/kubezap/kubezap-operator/internal/metrics"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=create

// cooldownTracker tracks invocation timestamps per route path for cooldown enforcement.
type cooldownTracker struct {
	mu          sync.Mutex
	invocations map[string][]time.Time // key: route path
}

func newCooldownTracker() *cooldownTracker {
	return &cooldownTracker{invocations: make(map[string][]time.Time)}
}

// allow checks whether a request to the given path is within the cooldown budget.
// If maxInvocations <= 0, all requests are allowed (no limit). Returns true if
// the request should proceed, false if it should be suppressed.
func (ct *cooldownTracker) allow(path string, maxInvocations int32, window time.Duration) bool {
	if maxInvocations <= 0 {
		return true
	}

	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	// Prune timestamps outside the window.
	timestamps := ct.invocations[path]
	pruned := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}

	if int32(len(pruned)) >= maxInvocations {
		ct.invocations[path] = pruned
		return false
	}

	ct.invocations[path] = append(pruned, now)
	return true
}

// defaultMaxStoredBodyBytes is the default cap on how much of a webhook body
// is stored in TriggerData.Body / made available to Flow processing, independent
// of (and much smaller than) the body read limit that exists purely to bound
// memory use. Raised from an earlier 4096 default: truncating mid-JSON leaves
// an invalid document, which makes trigger.bodyFields and every
// $(trigger.body.<field>) placeholder resolve as if the field didn't exist —
// not just truncate its visible portion — and real JSON webhook payloads
// (GitHub, Slack, Stripe) routinely exceed 4KB. 64KB is still comfortably
// below the 4MB read limit while covering the common case out of the box. See
// docs/design/2026-09-11-webhook-gateway-trust-boundary.md.
const defaultMaxStoredBodyBytes = 65536

// errorJSONKey is the map key used for {"error": "..."} JSON error responses.
const errorJSONKey = "error"

// triggerTypeWebhook is the TriggerSpec.Type value "webhook".
const triggerTypeWebhook = "webhook"

type WebhookHandler struct {
	k8sClient          client.Client
	registry           *RouteRegistry
	log                logr.Logger
	cooldown           *cooldownTracker
	trustedProxies     []*net.IPNet
	maxStoredBodyBytes int
}

// NewWebhookHandler constructs a WebhookHandler. trustedProxies is forwarded to
// realClientIP (empty means X-Forwarded-For/X-Real-IP are never trusted — see
// its doc comment); maxStoredBodyBytes overrides defaultMaxStoredBodyBytes when
// positive, otherwise the default applies.
func NewWebhookHandler(
	k8sClient client.Client,
	registry *RouteRegistry,
	log logr.Logger,
	trustedProxies []*net.IPNet,
	maxStoredBodyBytes int,
) *WebhookHandler {
	if maxStoredBodyBytes <= 0 {
		maxStoredBodyBytes = defaultMaxStoredBodyBytes
	}
	return &WebhookHandler{
		k8sClient:          k8sClient,
		registry:           registry,
		log:                log,
		cooldown:           newCooldownTracker(),
		trustedProxies:     trustedProxies,
		maxStoredBodyBytes: maxStoredBodyBytes,
	}
}

func randomHex(length int) string {
	if length <= 0 {
		return ""
	}
	b := make([]byte, (length+1)/2)
	_, err := rand.Read(b)
	if err != nil {
		return strings.Repeat("0", length)
	}
	hexString := hex.EncodeToString(b)
	if len(hexString) > length {
		hexString = hexString[:length]
	}
	return hexString
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// sourceRange returns the /24 CIDR bucket for IPv4 addresses and the /48 CIDR bucket for IPv6
// addresses. This is used as the source_range Prometheus label to bound label cardinality.
// The full source IP is never exposed as a Prometheus label value.
func sourceRange(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "unknown"
	}
	if ip4 := ip.To4(); ip4 != nil {
		// IPv4: mask to /24
		mask := net.CIDRMask(24, 32)
		return ip4.Mask(mask).String() + "/24"
	}
	// IPv6: mask to /48
	mask := net.CIDRMask(48, 128)
	return ip.Mask(mask).String() + "/48"
}

// authenticateRequest validates the incoming request against the route entry's auth configuration.
// triggerName is used only for metric labelling when a request is blocked by IP allowlist.
// trustedProxies is forwarded to realClientIP for the ipAllowlist case — see its doc comment.
// Returns (http.StatusOK, "") on success, or (statusCode, errorMessage) on failure.
//
// nolint:gocyclo // one switch case per auth type, each a few lines; the most
// mechanically splittable of these if ever revisited, but owner decided
// (2026-09-11) to keep all of them as one function each for now.
func authenticateRequest(r *http.Request, body []byte, entry RouteEntry, triggerName string, trustedProxies []*net.IPNet) (int, string) {
	switch entry.AuthType {
	case authTypeHMAC:
		if entry.HMACProvider == "slack" {
			return verifySlackHMAC(r, body, entry.HMACSecret, entry.HMACTimestampToleranceSec)
		}
		sigHeader := r.Header.Get("X-Hub-Signature-256")
		if sigHeader == "" {
			return http.StatusUnauthorized, "missing X-Hub-Signature-256 header"
		}
		expected := "sha256=" + hex.EncodeToString(func() []byte {
			mac := hmac.New(sha256.New, []byte(entry.HMACSecret))
			mac.Write(body)
			return mac.Sum(nil)
		}())
		if !hmac.Equal([]byte(sigHeader), []byte(expected)) {
			return http.StatusUnauthorized, "HMAC signature mismatch"
		}

	case authTypeBearer:
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			return http.StatusUnauthorized, "missing Authorization header"
		}
		if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+entry.BearerToken)) != 1 {
			return http.StatusUnauthorized, "invalid bearer token"
		}

	case authTypeAPIKey:
		headerName := entry.APIKeyHeader
		if headerName == "" {
			headerName = "X-Api-Key"
		}
		val := r.Header.Get(headerName)
		if val == "" {
			return http.StatusUnauthorized, "missing API key header"
		}
		if subtle.ConstantTimeCompare([]byte(val), []byte(entry.APIKey)) != 1 {
			return http.StatusUnauthorized, "invalid API key"
		}

	case authTypeOIDC:
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return http.StatusUnauthorized, `{"error":"unauthorized","reason":"missing Bearer token"}`
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if err := entry.OIDCValidator.validate(r.Context(), token); err != nil {
			return http.StatusUnauthorized, fmt.Sprintf(`{"error":"unauthorized","reason":"%s"}`, err.Error())
		}

	case authTypeBasic:
		username, password, ok := r.BasicAuth()
		if !ok {
			return http.StatusUnauthorized, "missing or malformed Basic auth credentials"
		}
		if subtle.ConstantTimeCompare([]byte(username), []byte(entry.BasicUsername)) != 1 ||
			subtle.ConstantTimeCompare([]byte(password), []byte(entry.BasicPassword)) != 1 {
			return http.StatusUnauthorized, "invalid Basic auth credentials"
		}

	case authTypeIPAllowlist:
		clientIP := realClientIP(r, trustedProxies)
		ip := net.ParseIP(clientIP)
		allowed := false
		for _, cidr := range entry.IPAllowlist {
			_, ipNet, parseErr := net.ParseCIDR(cidr)
			if parseErr != nil {
				// treat as a plain IP address
				if allowedIP := net.ParseIP(cidr); allowedIP != nil && allowedIP.Equal(ip) {
					allowed = true
					break
				}
				continue
			}
			if ipNet.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed {
			metrics.WebhookIPBlocked.WithLabelValues(triggerName, sourceRange(clientIP)).Inc()
			return http.StatusForbidden, "source IP not in allowlist"
		}

	case authTypeHeaderEquals:
		headerName := entry.HeaderEqualsHeader
		if headerName == "" {
			return http.StatusUnauthorized, "header-equals auth misconfigured: no header name"
		}
		val := r.Header.Get(headerName)
		if val == "" {
			return http.StatusUnauthorized, "missing required header"
		}
		if subtle.ConstantTimeCompare([]byte(val), []byte(entry.HeaderEqualsValue)) != 1 {
			return http.StatusUnauthorized, "invalid header value"
		}

	case "":
		// no auth configured — allow

	default:
		// unimplemented auth type — fail closed
		return http.StatusUnauthorized, "authentication type not implemented"
	}

	return http.StatusOK, ""
}

// verifySlackHMAC verifies Slack's signature scheme: header X-Slack-Signature
// in the form "v0=<hex>", computed as HMAC-SHA256 over "v0:<timestamp>:<body>"
// where <timestamp> comes from X-Slack-Request-Timestamp. Requests whose
// timestamp is more than toleranceSeconds away from now are rejected as
// potential replays, per Slack's own recommendation (default 300s).
func verifySlackHMAC(r *http.Request, body []byte, secret string, toleranceSeconds int32) (int, string) {
	sigHeader := r.Header.Get("X-Slack-Signature")
	if sigHeader == "" {
		return http.StatusUnauthorized, "missing X-Slack-Signature header"
	}
	tsHeader := r.Header.Get("X-Slack-Request-Timestamp")
	if tsHeader == "" {
		return http.StatusUnauthorized, "missing X-Slack-Request-Timestamp header"
	}
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return http.StatusUnauthorized, "invalid X-Slack-Request-Timestamp header"
	}

	tolerance := int64(toleranceSeconds)
	if tolerance <= 0 {
		tolerance = 300
	}
	if age := time.Now().Unix() - ts; age > tolerance || age < -tolerance {
		return http.StatusUnauthorized, "X-Slack-Request-Timestamp outside tolerance window"
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + tsHeader + ":" + string(body)))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sigHeader), []byte(expected)) {
		return http.StatusUnauthorized, "HMAC signature mismatch"
	}
	return http.StatusOK, ""
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusAccepted
	flowRunName := ""
	triggerName := ""
	triggerNamespace := ""
	// metricResult tracks the label value for the request-duration histogram.
	// It is set to "rejected" or "rate_limited" on non-success paths; the
	// accepted path sets it to "accepted" just before the final write.
	metricResult := "rejected"

	sourceIP := realClientIP(r, h.trustedProxies)

	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			h.log.Error(fmt.Errorf("panic: %v", rec), "webhook handler panic recovered",
				"path", r.URL.Path,
				"stack", string(stack),
			)
			status = http.StatusInternalServerError
			// Write 500 only if headers have not been sent yet. Attempting to write
			// after WriteHeader has been called is a no-op for status but still safe.
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		}
	}()

	defer func() {
		durationMs := time.Since(start).Milliseconds()
		h.log.Info("webhook access",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"trigger", triggerName,
			"namespace", triggerNamespace,
			"flowRun", flowRunName,
			"duration_ms", durationMs,
			"content_type", r.Header.Get("Content-Type"),
			"source_ip", sourceIP,
		)
		// Record request latency. triggerName may be empty for 404 paths; that
		// is acceptable — the histogram label will be an empty string in those
		// rare cases and does not inflate cardinality.
		metrics.WebhookRequestDuration.
			WithLabelValues(triggerName, metricResult).
			Observe(time.Since(start).Seconds())
	}()

	entry, ok := h.registry.Lookup(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{errorJSONKey: "no trigger registered for this path"})
		return
	}
	triggerName = entry.TriggerName
	triggerNamespace = entry.TriggerNamespace

	if strings.ToUpper(r.Method) != entry.AllowedMethod {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", entry.AllowedMethod)
		writeJSON(w, status, map[string]string{errorJSONKey: "method not allowed"})
		return
	}

	maxBody := int64(4 << 20)
	limitReader := io.LimitReader(r.Body, maxBody+1)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		status = http.StatusInternalServerError
		h.log.Error(err, "unable to read request body")
		writeJSON(w, status, map[string]string{errorJSONKey: "unable to read request body"})
		return
	}

	bodyTruncated := int64(len(bodyBytes)) > maxBody || (r.ContentLength > maxBody)
	if len(bodyBytes) > int(maxBody) {
		bodyBytes = bodyBytes[:maxBody]
	}

	if bodyTruncated {
		status = http.StatusRequestEntityTooLarge
		writeJSON(w, status, map[string]string{errorJSONKey: "request body exceeds maximum allowed size"})
		return
	}

	if authStatus, authMsg := authenticateRequest(r, bodyBytes, entry, triggerName, h.trustedProxies); authStatus != http.StatusOK {
		status = authStatus
		writeJSON(w, status, map[string]string{errorJSONKey: authMsg})
		return
	}

	// Cooldown window enforcement: suppress requests that exceed maxInvocations within the window.
	if entry.MaxInvocations > 0 && !h.cooldown.allow(r.URL.Path, entry.MaxInvocations, entry.CooldownWindow) {
		status = http.StatusTooManyRequests
		metricResult = metricResultRateLimited
		metrics.WebhookRateLimited.WithLabelValues(triggerName, triggerNamespace).Inc()
		writeJSON(w, status, map[string]string{errorJSONKey: "cooldown window exceeded"})
		return
	}

	bodyString := string(bodyBytes)
	if len(bodyString) > h.maxStoredBodyBytes {
		bodyTruncated = true
		bodyString = bodyString[:h.maxStoredBodyBytes]
	}

	redactedHeaders := redact.Headers(r.Header, entry.RedactHeaders)
	bodyString = redact.Body(bodyString, entry.RedactBody)

	flowRunName = fmt.Sprintf("%s-%d-%s", entry.TriggerName, time.Now().Unix(), randomHex(8))

	// Build annotations for W3C Trace Context propagation.
	// http.Header.Get performs canonical-form lookup, so "Traceparent" matches
	// both "traceparent" and "Traceparent" sent by the caller.
	// Annotating the FlowRun lets the controller resume the distributed trace
	// when it picks up execution — linking gateway and controller spans into a
	// single end-to-end trace without requiring the controller to parse HTTP headers.
	var annotations map[string]string
	if tp := r.Header.Get("Traceparent"); tp != "" {
		annotations = map[string]string{"kubezap.io/traceparent": tp}
	}
	if ts := r.Header.Get("Tracestate"); ts != "" {
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations["kubezap.io/tracestate"] = ts
	}

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:        flowRunName,
			Namespace:   entry.TriggerNamespace,
			Annotations: annotations,
			Labels: map[string]string{
				"kubezap.io/trigger":      entry.TriggerName,
				"kubezap.io/trigger-type": triggerTypeWebhook,
				"kubezap.io/flow":         entry.FlowRef,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: automationv1alpha1.FlowReference{Name: entry.FlowRef},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: entry.TriggerName,
				Type: triggerTypeWebhook,
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:        triggerTypeWebhook,
				Method:        r.Method,
				Path:          r.URL.Path,
				Headers:       redactedHeaders,
				Body:          bodyString,
				BodyTruncated: bodyTruncated,
				ContentType:   r.Header.Get("Content-Type"),
			},
		},
	}

	// FlowRef in FlowRunSpec is LocalObjectReference and does not support namespace
	// in this scheme. Namespace is implied by FlowRun namespace and Flow controller should resolve.
	//
	// We intentionally do NOT use r.Context() directly: client disconnects cancel that context
	// which would abandon the FlowRun create (losing fire-and-forget semantics). Instead, we
	// derive from context.Background() with a bounded timeout to prevent goroutines from
	// blocking indefinitely if the API server is slow. The request context is used as the
	// parent when it is still alive so OTel span context propagates when possible.
	createParent := context.Background()
	// Propagate OTel span context even when the request context is cancelled
	// (client disconnect), so the FlowRun Create call remains linked to the
	// incoming request's trace.
	if span := trace.SpanFromContext(r.Context()); span.SpanContext().IsValid() {
		createParent = trace.ContextWithSpan(createParent, span)
	}
	if r.Context().Err() == nil {
		createParent = r.Context()
	}
	createCtx, createCancel := context.WithTimeout(createParent, 10*time.Second)
	defer createCancel()
	err = h.k8sClient.Create(createCtx, flowRun)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			metricResult = "accepted"
			status = http.StatusAccepted
			writeJSON(w, status, map[string]string{"flowRun": flowRunName, "namespace": entry.TriggerNamespace})
			return
		}
		status = http.StatusInternalServerError
		h.log.Error(err, "unable to create FlowRun", "flowRun", flowRunName, "trigger", entry.TriggerName, "namespace", entry.TriggerNamespace)
		writeJSON(w, status, map[string]string{errorJSONKey: "unable to create FlowRun"})
		return
	}

	metricResult = "accepted"
	status = http.StatusAccepted
	writeJSON(w, status, map[string]string{"flowRun": flowRunName, "namespace": entry.TriggerNamespace})
}
