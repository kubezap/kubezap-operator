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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
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

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	// SSRF check — defence-in-depth: the controller also checks before sending.
	if err := checkSSRF(ctx, req.URL, h.BlockedCIDRs); err != nil {
		resp := ExecuteResponse{
			Error: "ssrf_blocked: " + err.Error(),
		}
		h.writeJSON(w, resp)
		return
	}

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
	client := h.httpClient(req.TLSSkipVerify)

	// Execute request.
	upstream, err := client.Do(outReq)
	if err != nil {
		resp := ExecuteResponse{
			Error: h.classifyTransportError(err),
		}
		h.writeJSON(w, resp)
		return
	}
	defer upstream.Body.Close()

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
// with the appropriate TLS configuration.
func (h *Handler) httpClient(tlsSkipVerify bool) *http.Client {
	if h.HTTPClient != nil {
		return h.HTTPClient
	}

	skipVerify := false
	if h.AllowTLSSkipVerify && tlsSkipVerify {
		skipVerify = true
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: skipVerify, //nolint:gosec // controlled by operator flag + explicit request field
		},
		// Use sensible connection timeouts to avoid goroutine leaks.
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// classifyTransportError maps a net/http transport error to an error-prefix string.
func (h *Handler) classifyTransportError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

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
