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

package executorhttp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
)

func TestExecutorHTTP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Executor HTTP Suite")
}

// newHandler builds a Handler with default SSRF blocklist and the provided body limit.
func newHandler() *executorhttp.Handler {
	cidrs, err := executorhttp.ParseCIDRList("")
	Expect(err).NotTo(HaveOccurred())
	return &executorhttp.Handler{
		BlockedCIDRs:       cidrs,
		BodyLimitBytes:     4096,
		AllowTLSSkipVerify: false,
	}
}

// newPassthroughHandler builds a Handler with an empty SSRF blocklist so that
// tests using httptest.NewServer (which binds to 127.0.0.1) are not blocked.
// Only use this for tests that verify behaviour other than SSRF blocking.
func newPassthroughHandler() *executorhttp.Handler {
	return &executorhttp.Handler{
		BlockedCIDRs:       []*net.IPNet{}, // empty — no IPs blocked; used for tests targeting localhost
		BodyLimitBytes:     4096,
		AllowTLSSkipVerify: false,
	}
}

// doExecute posts an ExecuteRequest to the handler and returns the ExecuteResponse.
// The outer HTTP status (from the executor itself) is checked separately.
func doExecute(h *executorhttp.Handler, req executorhttp.ExecuteRequest) (executorhttp.ExecuteResponse, int) {
	body, err := json.Marshal(req)
	Expect(err).NotTo(HaveOccurred())

	httpReq := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeExecute(w, httpReq)

	result := w.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		return executorhttp.ExecuteResponse{}, result.StatusCode
	}

	var resp executorhttp.ExecuteResponse
	err = json.NewDecoder(result.Body).Decode(&resp)
	Expect(err).NotTo(HaveOccurred())
	return resp, result.StatusCode
}

var _ = Describe("Handler", func() {

	Describe("POST /execute — SSRF protection", func() {
		Context("when the target URL contains a blocked IP", func() {
			It("returns HTTP 200 with ssrf_blocked: error for RFC1918 10.x.x.x", func() {
				h := newHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method: "GET",
					URL:    "http://10.0.0.1/path",
				})
				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.StatusCode).To(Equal(0))
				Expect(resp.Error).To(HavePrefix("ssrf_blocked:"))
				Expect(resp.Error).To(ContainSubstring("10.0.0.1"))
			})

			It("returns HTTP 200 with ssrf_blocked: error for loopback 127.0.0.1", func() {
				h := newHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method: "GET",
					URL:    "http://127.0.0.1/path",
				})
				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("ssrf_blocked:"))
			})

			It("returns HTTP 200 with ssrf_blocked: error for cloud metadata IP 169.254.169.254", func() {
				h := newHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method: "GET",
					URL:    "http://169.254.169.254/latest/meta-data/",
				})
				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("ssrf_blocked:"))
				Expect(resp.Error).To(ContainSubstring("169.254.169.254"))
			})
		})

		Context("when the target URL has a .svc.cluster.local hostname", func() {
			It("returns HTTP 200 with ssrf_blocked: error without performing DNS resolution", func() {
				h := newHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method: "GET",
					URL:    "http://my-service.default.svc.cluster.local/api",
				})
				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("ssrf_blocked:"))
				Expect(resp.Error).To(ContainSubstring(".svc.cluster.local"))
			})
		})
	})

	Describe("POST /execute — successful upstream call", func() {
		It("returns the upstream response body and status code", func() {
			// Stand up a local test server to act as the upstream.
			// Use newPassthroughHandler (empty blocklist) because httptest.NewServer
			// binds to 127.0.0.1 which is in the default SSRF blocklist.
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Custom-Header", "test-value")
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprint(w, `{"result":"ok"}`)
			}))
			defer upstream.Close()

			h := newPassthroughHandler()
			resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
				Method: "GET",
				URL:    upstream.URL + "/api",
			})

			Expect(outerStatus).To(Equal(http.StatusOK))
			Expect(resp.Error).To(BeEmpty())
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
			Expect(resp.Body).To(ContainSubstring(`"result":"ok"`))
			Expect(resp.Truncated).To(BeFalse())
			Expect(resp.Headers["X-Custom-Header"]).To(Equal("test-value"))
		})

		It("forwards request headers to the upstream", func() {
			var capturedAuth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			h := newPassthroughHandler()
			_, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
				Method:  "POST",
				URL:     upstream.URL + "/secured",
				Headers: map[string]string{"Authorization": "Bearer token123"},
				Body:    "{}",
			})

			Expect(outerStatus).To(Equal(http.StatusOK))
			Expect(capturedAuth).To(Equal("Bearer token123"))
		})
	})

	Describe("POST /execute — body truncation", func() {
		It("truncates responses exceeding BodyLimitBytes and sets Truncated=true", func() {
			largeBody := strings.Repeat("x", 5000)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, largeBody)
			}))
			defer upstream.Close()

			h := newPassthroughHandler()
			resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
				Method: "GET",
				URL:    upstream.URL + "/large",
			})

			Expect(outerStatus).To(Equal(http.StatusOK))
			Expect(resp.Error).To(BeEmpty())
			Expect(resp.Truncated).To(BeTrue())
			Expect(resp.Body).To(HaveLen(4096))
		})

		It("does not set Truncated when body fits within limit", func() {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, "small response")
			}))
			defer upstream.Close()

			h := newPassthroughHandler()
			resp, _ := doExecute(h, executorhttp.ExecuteRequest{
				Method: "GET",
				URL:    upstream.URL + "/small",
			})

			Expect(resp.Truncated).To(BeFalse())
			Expect(resp.Body).To(Equal("small response"))
		})
	})

	Describe("POST /execute — invalid requests", func() {
		It("returns HTTP 400 for an unsupported HTTP method", func() {
			h := newHandler()
			_, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
				Method: "CONNECT",
				URL:    "http://example.com/",
			})
			Expect(outerStatus).To(Equal(http.StatusBadRequest))
		})

		It("returns HTTP 400 for a missing URL", func() {
			body := `{"method":"GET"}`
			httpReq := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(body))
			httpReq.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h := newHandler()
			h.ServeExecute(w, httpReq)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
			Expect(w.Body.String()).To(ContainSubstring("invalid_request"))
		})

		It("returns HTTP 400 for malformed JSON body", func() {
			httpReq := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader("{not json}"))
			httpReq.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h := newHandler()
			h.ServeExecute(w, httpReq)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("GET /healthz", func() {
		It("returns HTTP 200 with body 'ok'", func() {
			h := newHandler()
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			w := httptest.NewRecorder()

			h.ServeHealthz(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Body.String()).To(Equal("ok"))
		})
	})

	Describe("New (server mux wiring)", func() {
		It("routes POST /execute to ServeExecute", func() {
			h := newPassthroughHandler()
			mux := executorhttp.New(h)

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, "hello")
			}))
			defer upstream.Close()

			body, _ := json.Marshal(executorhttp.ExecuteRequest{
				Method: "GET",
				URL:    upstream.URL,
			})
			req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
		})

		It("routes GET /healthz to ServeHealthz", func() {
			h := newHandler()
			mux := executorhttp.New(h)

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Body.String()).To(Equal("ok"))
		})
	})
})
