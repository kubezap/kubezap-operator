/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package executorhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	// maxTimeoutSeconds is the maximum per-request timeout the executor will honour.
	maxTimeoutSeconds = 300

	// defaultTimeoutSeconds is used when the caller does not specify a timeout.
	defaultTimeoutSeconds = 30

	// allowedMethods is the set of HTTP methods accepted by the executor.
	allowedMethods = "GET POST PUT PATCH DELETE"
)

// Handler implements POST /execute and GET /healthz for the HTTP executor.
type Handler struct {
	// BlockedCIDRs is the combined default + operator-configured SSRF blocklist.
	// Constructed via ParseCIDRList.
	BlockedCIDRs []*net.IPNet

	// BodyLimitBytes is the maximum number of bytes read from the upstream
	// response body. Responses larger than this are truncated and Truncated is
	// set to true in the ExecuteResponse. Default: 4096.
	BodyLimitBytes int64

	// AllowTLSSkipVerify controls whether the executor honours the
	// tlsSkipVerify field in ExecuteRequest. When false, TLS verification is
	// always enforced regardless of the request field value.
	AllowTLSSkipVerify bool

	// AllowClusterInternal disables the .svc.cluster.local hostname block, and the
	// CIDR blocklist for such targets only (a Service's ClusterIP legitimately falls
	// in RFC1918 space). Other targets — IP literals and non-cluster hostnames —
	// remain subject to the full CIDR blocklist regardless of this flag. Intended
	// for development and testing environments where in-cluster service calls are
	// required. NOT recommended in production — only enable when the executor has
	// appropriate NetworkPolicy restrictions in place.
	AllowClusterInternal bool

	// HTTPClient is the client used for outbound requests. When nil a default
	// client is used. Callers may inject a custom client for testing.
	HTTPClient *http.Client
}

// ServeExecute handles POST /execute. It always returns HTTP 200; transport
// errors are embedded in the ExecuteResponse.Error field.
func (h *Handler) ServeExecute(w http.ResponseWriter, r *http.Request) {
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid_request: failed to decode JSON body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate method before any network activity.
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if !strings.Contains(allowedMethods, method) || method == "" {
		http.Error(w, fmt.Sprintf("invalid_request: method %q is not allowed; must be one of GET, POST, PUT, PATCH, DELETE", req.Method), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.URL) == "" {
		http.Error(w, "invalid_request: url is required", http.StatusBadRequest)
		return
	}

	// Determine timeout.
	timeoutSecs := req.TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = defaultTimeoutSeconds
	}
	if timeoutSecs > maxTimeoutSeconds {
		timeoutSecs = maxTimeoutSeconds
	}

	// Extract W3C trace context propagated by the controller over this
	// internal RPC (see callExecutor in internal/controller/flowrun_controller.go)
	// so that http_call — the real outbound call this handler makes below —
	// nests correctly under the controller's flowrun.step span instead of
	// starting a disconnected trace.
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	// SSRF pre-flight check — defence-in-depth: the controller also checks
	// before sending. This only validates structural properties (hostname
	// present, .svc.cluster.local policy) for a fast, clear error before any
	// network I/O. The actual DNS-resolution-and-CIDR-check step happens once,
	// at dial time, in Handler.dialContext (installed on the transport built by
	// httpClient below) — see checkSSRFPreflight's doc comment for why.
	if err := checkSSRFPreflight(req.URL, h.AllowClusterInternal); err != nil {
		resp := ExecuteResponse{
			Error: "ssrf_blocked: " + err.Error(),
		}
		h.writeJSON(w, resp)
		return
	}

	// http_call is the span for the real outbound HTTP request — the actual
	// network call this executor exists to make (as opposed to the
	// controller's internal RPC dispatch to this process, which is not
	// itself an outbound call).
	ctx, httpCallSpan := otel.Tracer("kubezap.io/http-executor").Start(ctx, "http_call",
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.url", req.URL),
		))
	defer httpCallSpan.End()

	// Build outbound request.
	outReq, err := http.NewRequestWithContext(ctx, method, req.URL, strings.NewReader(req.Body))
	if err != nil {
		resp := ExecuteResponse{
			Error: fmt.Sprintf("invalid_request: failed to build HTTP request: %v", err),
		}
		h.writeJSON(w, resp)
		return
	}
	for k, v := range req.Headers {
		outReq.Header.Set(k, v)
	}

	// Build or reuse HTTP client.
	client, err := h.httpClient(req)
	if err != nil {
		resp := ExecuteResponse{
			Error: fmt.Sprintf("invalid_request: %v", err),
		}
		h.writeJSON(w, resp)
		return
	}

	// Execute request.
	upstream, err := client.Do(outReq)
	if err != nil {
		classified := h.classifyTransportError(err)
		httpCallSpan.RecordError(err)
		httpCallSpan.SetStatus(otelcodes.Error, classified)
		resp := ExecuteResponse{
			Error: classified,
		}
		h.writeJSON(w, resp)
		return
	}
	defer upstream.Body.Close()
	httpCallSpan.SetAttributes(attribute.Int("http.status_code", upstream.StatusCode))

	// Read body up to the limit.
	limitedReader := io.LimitReader(upstream.Body, h.BodyLimitBytes+1)
	rawBody, err := io.ReadAll(limitedReader)
	if err != nil {
		resp := ExecuteResponse{
			StatusCode: upstream.StatusCode,
			Error:      fmt.Sprintf("upstream_error: failed to read response body: %v", err),
		}
		h.writeJSON(w, resp)
		return
	}

	truncated := false
	if int64(len(rawBody)) > h.BodyLimitBytes {
		rawBody = rawBody[:h.BodyLimitBytes]
		truncated = true
	}

	// Collect response headers (last value wins for duplicate names).
	respHeaders := make(map[string]string, len(upstream.Header))
	for name, values := range upstream.Header {
		if len(values) > 0 {
			respHeaders[name] = values[len(values)-1]
		}
	}

	resp := ExecuteResponse{
		StatusCode: upstream.StatusCode,
		Headers:    respHeaders,
		Body:       string(rawBody),
		Truncated:  truncated,
	}
	h.writeJSON(w, resp)
}

// ServeHealthz handles GET /healthz. Returns 200 ok.
func (h *Handler) ServeHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// httpClient returns the handler's HTTPClient or builds a one-shot client
// with the appropriate TLS configuration. When req carries a resolved CA
// bundle and/or client certificate (populated controller-side; this handler
// never resolves a reference itself), the returned client's transport trusts
// that bundle in addition to the system root pool and/or presents that client
// certificate. This logic is independent of, and does not affect,
// InsecureSkipVerify handling.
func (h *Handler) httpClient(req ExecuteRequest) (*http.Client, error) {
	if h.HTTPClient != nil {
		return h.HTTPClient, nil
	}

	skipVerify := h.AllowTLSSkipVerify && req.TLSSkipVerify

	tlsConfig := &tls.Config{
		InsecureSkipVerify: skipVerify, //nolint:gosec // controlled by operator flag + explicit request field
	}

	if req.TLSCABundle != "" {
		// Start from a clone of the system root pool (x509.SystemCertPool()
		// already returns a fresh clone, safe to mutate here) and append the
		// configured bundle — additive to system trust, never a replacement.
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("loading system CA pool: %w", err)
		}
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(req.TLSCABundle)) {
			return nil, fmt.Errorf("failed to parse TLS CA bundle: no valid PEM certificates found")
		}
		tlsConfig.RootCAs = pool
	}

	switch {
	case req.TLSClientCert != "" && req.TLSClientKey != "":
		cert, err := tls.X509KeyPair([]byte(req.TLSClientCert), []byte(req.TLSClientKey))
		if err != nil {
			return nil, fmt.Errorf("failed to parse TLS client certificate/key: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	case req.TLSClientCert != "" || req.TLSClientKey != "":
		return nil, fmt.Errorf("tlsClientCert and tlsClientKey must both be set for client certificate authentication")
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
		// dialContext performs SSRF validation itself and dials the specific
		// validated address — see its doc comment. It replaces a plain
		// net.Dialer.DialContext so that every connection this Transport
		// establishes (including redirect-triggered ones) is validated at the
		// moment it is actually dialed, with no separate, re-resolvable
		// pre-flight check for an attacker to race.
		DialContext:         h.dialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport}, nil
}

// rawDialContext performs the actual network connection once a target
// address has been validated. It is a package variable — rather than a
// net.Dialer{} literal inlined into dialContext — so tests can inject a fake
// dialer to record/observe dial targets (and avoid real network I/O)
// deterministically. Production code never reassigns this; only *_test.go
// files in this package do.
var rawDialContext = (&net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}).DialContext

// dialContext is installed as the executor's per-request http.Transport's
// DialContext (see httpClient above). net/http's Transport calls this for
// every TCP connection it establishes to serve a request — the initial
// connection and any connection triggered by following a redirect to a
// different host — so performing SSRF validation here, rather than only
// once before the request is built, means there is exactly one DNS
// resolution per connection and it is always the one that gets checked. This
// closes the DNS-rebinding TOCTOU window structurally: there is no window
// between "resolve and validate" and "resolve again and connect" for a
// low-TTL DNS answer to exploit, because both steps use the same resolution.
//
// TLS certificate hostname verification is unaffected by pinning the dial to
// a specific validated IP: net/http's Transport derives the TLS ServerName
// from addr's original hostname (the connectMethod's target, populated
// before dialContext runs), not from the net.Conn this function returns or
// its remote address. Verification therefore continues to check the
// hostname the caller configured, never the pinned IP.
//
// AllowClusterInternal scoping mirrors checkSSRFPreflight: it bypasses
// validation only for hostnames recognized as in-cluster services (the
// ".svc.cluster.local" suffix), never for IP literals or other hostnames.
// This re-check is necessary here (not just in ServeExecute's pre-flight)
// because a redirect can target a .svc.cluster.local hostname that the
// original request URL never mentioned.
func (h *Handler) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf_blocked: invalid dial address %q: %v", addr, err)
	}

	isClusterInternal := strings.HasSuffix(host, ".svc.cluster.local") ||
		strings.HasSuffix(host, ".svc.cluster.local.")

	switch {
	case isClusterInternal && h.AllowClusterInternal:
		// Bypass CIDR validation only for this in-cluster hostname (its
		// ClusterIP legitimately falls in RFC1918 space); still resolve and
		// dial it through the normal path.
		return rawDialContext(ctx, network, addr)
	case isClusterInternal:
		return nil, fmt.Errorf("ssrf_blocked: requests to in-cluster service endpoints (.svc.cluster.local) are not permitted from HTTP steps; use a plugin Integration instead")
	}

	validated, err := resolveAndValidate(ctx, host, h.BlockedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("ssrf_blocked: %v", err)
	}

	// Dial the specific validated address directly rather than the original
	// hostname, so the connection cannot be re-resolved to a different,
	// unvalidated address between validation and connect.
	pinned := net.JoinHostPort(validated[0].String(), port)
	return rawDialContext(ctx, network, pinned)
}

// classifyTransportError maps a net/http transport error to an error-prefix string.
func (h *Handler) classifyTransportError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

	// Dial-time SSRF validation failures (from dialContext) are surfaced as a
	// distinguishable "ssrf_blocked:"-prefixed error, matching the contract
	// ServeExecute's pre-flight check already uses. This is checked first,
	// ahead of the net.OpError/timeout classification below, because
	// net/http's Transport wraps a DialContext error in its own error types
	// (e.g. *net.OpError) on the way up through client.Do — the ssrf_blocked
	// message text survives that wrapping, but its type does not, so we match
	// on the message rather than relying on errors.As for a specific type.
	if idx := strings.Index(msg, "ssrf_blocked:"); idx >= 0 {
		return msg[idx:]
	}

	// Context deadline / timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout: " + msg
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		// DNS resolution errors.
		var dnsErr *net.DNSError
		if errors.As(netErr, &dnsErr) {
			return "dns_error: " + msg
		}
		return "upstream_error: " + msg
	}

	// TLS errors appear as url.Error wrapping a tls error; check the string
	// as a fallback for cases not covered by net.OpError unwrapping.
	if strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") ||
		strings.Contains(msg, "certificate") {
		return "tls_error: " + msg
	}

	return "upstream_error: " + msg
}

// writeJSON writes v as JSON to w with HTTP 200 and Content-Type application/json.
func (h *Handler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
