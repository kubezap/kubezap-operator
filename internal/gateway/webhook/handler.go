package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
	"github.com/yourname/kubezap/internal/metrics"
)

// +kubebuilder:rbac:groups=automation.kubezap.io,resources=flowruns,verbs=create

type WebhookHandler struct {
	k8sClient client.Client
	registry  *RouteRegistry
	log       logr.Logger
}

func NewWebhookHandler(k8sClient client.Client, registry *RouteRegistry, log logr.Logger) *WebhookHandler {
	return &WebhookHandler{k8sClient: k8sClient, registry: registry, log: log}
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

func redactHeader(name string, values []string) string {
	lower := strings.ToLower(name)
	if lower == "authorization" || lower == "x-api-key" {
		return "[redacted]"
	}
	return strings.Join(values, ",")
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
// Returns (http.StatusOK, "") on success, or (statusCode, errorMessage) on failure.
func authenticateRequest(r *http.Request, body []byte, entry RouteEntry, triggerName string) (int, string) {
	switch entry.AuthType {
	case "hmac":
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

	case "bearer":
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			return http.StatusUnauthorized, "missing Authorization header"
		}
		if authHeader != "Bearer "+entry.BearerToken {
			return http.StatusUnauthorized, "invalid bearer token"
		}

	case "apiKey":
		headerName := entry.APIKeyHeader
		if headerName == "" {
			headerName = "X-Api-Key"
		}
		val := r.Header.Get(headerName)
		if val == "" {
			return http.StatusUnauthorized, "missing API key header"
		}
		if val != entry.APIKey {
			return http.StatusUnauthorized, "invalid API key"
		}

	case "oidc":
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return http.StatusUnauthorized, `{"error":"unauthorized","reason":"missing Bearer token"}`
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if err := entry.OIDCValidator.validate(r.Context(), token); err != nil {
			return http.StatusUnauthorized, fmt.Sprintf(`{"error":"unauthorized","reason":"%s"}`, err.Error())
		}

	case "ipAllowlist":
		clientIP := realClientIP(r)
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

	default:
		// no auth or unknown — allow
	}

	return http.StatusOK, ""
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusAccepted
	flowRunName := ""
	triggerName := ""
	triggerNamespace := ""

	sourceIP, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		sourceIP = r.RemoteAddr
	}

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
	}()

	entry, ok := h.registry.Lookup(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{"error": "no trigger registered for this path"})
		return
	}
	triggerName = entry.TriggerName
	triggerNamespace = entry.TriggerNamespace

	if strings.ToUpper(r.Method) != entry.AllowedMethod {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", entry.AllowedMethod)
		writeJSON(w, status, map[string]string{"error": "method not allowed"})
		return
	}

	maxBody := int64(4 << 20)
	limitReader := io.LimitReader(r.Body, maxBody+1)
	bodyBytes, err := io.ReadAll(limitReader)
	if err != nil {
		status = http.StatusInternalServerError
		h.log.Error(err, "unable to read request body")
		writeJSON(w, status, map[string]string{"error": "unable to read request body"})
		return
	}

	bodyTruncated := int64(len(bodyBytes)) > maxBody || (r.ContentLength > maxBody)
	if len(bodyBytes) > int(maxBody) {
		bodyBytes = bodyBytes[:maxBody]
	}

	if authStatus, authMsg := authenticateRequest(r, bodyBytes, entry, triggerName); authStatus != http.StatusOK {
		status = authStatus
		writeJSON(w, status, map[string]string{"error": authMsg})
		return
	}

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		headers[k] = redactHeader(k, v)
	}

	bodyString := string(bodyBytes)
	if len(bodyString) > 4096 {
		bodyString = bodyString[:4096]
	}

	flowRunName = fmt.Sprintf("%s-%d-%s", entry.TriggerName, time.Now().Unix(), randomHex(4))

	flowRun := &automationv1alpha1.FlowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      flowRunName,
			Namespace: entry.TriggerNamespace,
			Labels: map[string]string{
				"kubezap.io/trigger":      entry.TriggerName,
				"kubezap.io/trigger-type": "webhook",
				"kubezap.io/flow":         entry.FlowRef,
			},
		},
		Spec: automationv1alpha1.FlowRunSpec{
			FlowRef: corev1.LocalObjectReference{Name: entry.FlowRef},
			TriggerRef: &automationv1alpha1.TriggerReference{
				Name: entry.TriggerName,
				Type: "webhook",
			},
			TriggerData: &automationv1alpha1.TriggerData{
				Source:        "webhook",
				Method:        r.Method,
				Path:          r.URL.Path,
				Headers:       headers,
				Body:          bodyString,
				BodyTruncated: bodyTruncated,
				ContentType:   r.Header.Get("Content-Type"),
			},
		},
	}

	// FlowRef in FlowRunSpec is LocalObjectReference and does not support namespace
	// in this scheme. Namespace is implied by FlowRun namespace and Flow controller should resolve.
	err = h.k8sClient.Create(context.Background(), flowRun)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			status = http.StatusAccepted
			writeJSON(w, status, map[string]string{"flowRun": flowRunName, "namespace": entry.TriggerNamespace})
			return
		}
		status = http.StatusInternalServerError
		h.log.Error(err, "unable to create FlowRun", "flowRun", flowRunName, "trigger", entry.TriggerName, "namespace", entry.TriggerNamespace)
		writeJSON(w, status, map[string]string{"error": "unable to create FlowRun"})
		return
	}

	status = http.StatusAccepted
	writeJSON(w, status, map[string]string{"flowRun": flowRunName, "namespace": entry.TriggerNamespace})
}
