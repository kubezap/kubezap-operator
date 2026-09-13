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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// generateTestCA creates a self-signed CA certificate/key pair, PEM-encoded,
// for use as a private root of trust in outbound-TLS tests.
func generateTestCA() (certPEM []byte, cert *x509.Certificate, key *rsa.PrivateKey) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kubezap-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())

	cert, err = x509.ParseCertificate(der)
	Expect(err).NotTo(HaveOccurred())

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return certPEM, cert, key
}

// generateSignedCert issues a leaf certificate/key pair signed by the given CA,
// PEM-encoded, suitable for either a TLS server certificate (with ips set) or
// a client certificate (ips nil).
func generateSignedCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, cn string, ips []net.IP) (certPEM, keyPEM []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
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

	Describe("POST /execute — outbound TLS (CA bundle / client certificate)", func() {
		Context("when the request configures a CA bundle matching the server's certificate", func() {
			It("trusts the private CA and succeeds", func() {
				caCertPEM, caCert, caKey := generateTestCA()
				serverCertPEM, serverKeyPEM := generateSignedCert(caCert, caKey, "127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")})
				serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
				Expect(err).NotTo(HaveOccurred())

				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprint(w, "trusted-ca")
				}))
				upstream.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}
				upstream.StartTLS()
				defer upstream.Close()

				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:      "GET",
					URL:         upstream.URL,
					TLSCABundle: string(caCertPEM),
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(BeEmpty())
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(resp.Body).To(Equal("trusted-ca"))
			})
		})

		Context("when the CA bundle is not configured", func() {
			It("fails TLS verification against a server signed by an untrusted private CA", func() {
				_, caCert, caKey := generateTestCA()
				serverCertPEM, serverKeyPEM := generateSignedCert(caCert, caKey, "127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")})
				serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
				Expect(err).NotTo(HaveOccurred())

				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprint(w, "should-not-be-reached")
				}))
				upstream.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}
				upstream.StartTLS()
				defer upstream.Close()

				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method: "GET",
					URL:    upstream.URL,
					// No TLSCABundle set — the private CA is not trusted.
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.StatusCode).To(Equal(0))
				Expect(resp.Error).To(HavePrefix("tls_error:"))
			})
		})

		Context("when the request configures a client certificate", func() {
			It("succeeds against a server requiring mTLS", func() {
				caCertPEM, caCert, caKey := generateTestCA()
				serverCertPEM, serverKeyPEM := generateSignedCert(caCert, caKey, "127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")})
				serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
				Expect(err).NotTo(HaveOccurred())

				clientCertPEM, clientKeyPEM := generateSignedCert(caCert, caKey, "kubezap-test-client", nil)

				clientCAPool := x509.NewCertPool()
				Expect(clientCAPool.AppendCertsFromPEM(caCertPEM)).To(BeTrue())

				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprint(w, "mtls-ok")
				}))
				upstream.TLS = &tls.Config{
					Certificates: []tls.Certificate{serverCert},
					ClientCAs:    clientCAPool,
					ClientAuth:   tls.RequireAndVerifyClientCert,
				}
				upstream.StartTLS()
				defer upstream.Close()

				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:        "GET",
					URL:           upstream.URL,
					TLSCABundle:   string(caCertPEM),
					TLSClientCert: string(clientCertPEM),
					TLSClientKey:  string(clientKeyPEM),
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(BeEmpty())
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				Expect(resp.Body).To(Equal("mtls-ok"))
			})

			It("rejects a request against an mTLS server when no client certificate is configured", func() {
				caCertPEM, caCert, caKey := generateTestCA()
				serverCertPEM, serverKeyPEM := generateSignedCert(caCert, caKey, "127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")})
				serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
				Expect(err).NotTo(HaveOccurred())

				clientCAPool := x509.NewCertPool()
				Expect(clientCAPool.AppendCertsFromPEM(caCertPEM)).To(BeTrue())

				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = fmt.Fprint(w, "should-not-be-reached")
				}))
				upstream.TLS = &tls.Config{
					Certificates: []tls.Certificate{serverCert},
					ClientCAs:    clientCAPool,
					ClientAuth:   tls.RequireAndVerifyClientCert,
				}
				upstream.StartTLS()
				defer upstream.Close()

				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:      "GET",
					URL:         upstream.URL,
					TLSCABundle: string(caCertPEM),
					// No client cert configured.
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.StatusCode).To(Equal(0))
				Expect(resp.Error).NotTo(BeEmpty())
			})
		})

		Context("when the CA bundle content is malformed", func() {
			It("returns an invalid_request error rather than silently falling back to system trust only", func() {
				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:      "GET",
					URL:         "https://127.0.0.1:1/",
					TLSCABundle: "not a real PEM certificate",
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("invalid_request:"))
			})
		})

		Context("when only one of client cert / client key is set", func() {
			It("returns an invalid_request error", func() {
				h := newPassthroughHandler()
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:        "GET",
					URL:           "https://127.0.0.1:1/",
					TLSClientCert: "some cert content",
					// TLSClientKey deliberately omitted.
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("invalid_request:"))
			})
		})

		Context("existing InsecureSkipVerify behaviour", func() {
			It("remains unaffected by TLS CA bundle fields being absent", func() {
				// AllowTLSSkipVerify=false at the handler level, tlsSkipVerify=true at the
				// request level — verification must still be enforced (handler flag wins),
				// exactly as before this change; unrelated to the new CA/cert fields.
				caCertPEM, caCert, caKey := generateTestCA()
				serverCertPEM, serverKeyPEM := generateSignedCert(caCert, caKey, "127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")})
				serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
				Expect(err).NotTo(HaveOccurred())
				_ = caCertPEM // not trusted by this handler; only skip-verify semantics under test

				upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				upstream.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}
				upstream.StartTLS()
				defer upstream.Close()

				h := newPassthroughHandler() // AllowTLSSkipVerify: false
				resp, outerStatus := doExecute(h, executorhttp.ExecuteRequest{
					Method:        "GET",
					URL:           upstream.URL,
					TLSSkipVerify: true,
				})

				Expect(outerStatus).To(Equal(http.StatusOK))
				Expect(resp.Error).To(HavePrefix("tls_error:"))
			})
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
