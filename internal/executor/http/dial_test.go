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

// This file is an internal (package executorhttp, not executorhttp_test) test
// file because it needs to override the unexported lookupIPAddr and
// rawDialContext package variables to inject a fake resolver/dialer — see
// their doc comments in ssrf.go / handler.go. Ginkgo collects the specs
// registered here into the same suite that handler_test.go's
// TestExecutorHTTP entry point runs; no separate `go test` entry point is
// needed in this file.
package executorhttp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // dot-import matches existing test convention in this package
	. "github.com/onsi/gomega"    //nolint:revive // dot-import matches existing test convention in this package
)

// genTestCAAndLeaf issues a self-signed CA and a leaf server certificate
// (PEM-encoded) whose only identity is DNSNames: []string{hostname} — no IP
// SAN. It exists to prove TLS certificate hostname verification checks the
// original request hostname (which this cert covers) rather than whatever IP
// dialContext actually pinned the connection to (which this cert does NOT
// cover): if verification ever used the pinned IP instead, this cert would
// fail to verify and the test relying on it would fail.
func genTestCAAndLeaf(hostname string) (caCertPEM, serverCertPEM, serverKeyPEM []byte) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kubezap-dial-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).NotTo(HaveOccurred())
	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	serverCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	serverKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return caCertPEM, serverCertPEM, serverKeyPEM
}

var _ = Describe("Handler.dialContext", func() {
	var (
		origLookup func(ctx context.Context, host string) ([]net.IPAddr, error)
		origDial   func(ctx context.Context, network, addr string) (net.Conn, error)
	)

	BeforeEach(func() {
		origLookup = lookupIPAddr
		origDial = rawDialContext
	})

	AfterEach(func() {
		lookupIPAddr = origLookup
		rawDialContext = origDial
	})

	Describe("DNS-rebinding scenario", func() {
		It("never dials the blocked address returned by a second, rebound DNS lookup for the same hostname", func() {
			const hostname = "rebind.example.test"
			safeIP := net.ParseIP("203.0.113.10")       // TEST-NET-3 — not in the default blocklist
			blockedIP := net.ParseIP("169.254.169.254") // cloud metadata IP — blocked by default

			callCount := 0
			lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
				Expect(host).To(Equal(hostname))
				callCount++
				if callCount == 1 {
					// First connection's resolution (e.g. the initial request):
					// resolves to a safe address, so validation passes.
					return []net.IPAddr{{IP: safeIP}}, nil
				}
				// Second connection's resolution for the SAME hostname
				// (e.g. a redirect hop reconnecting, or the low-TTL DNS
				// answer changing between connections) — the attacker's
				// rebind: now resolves to a blocked, internal address.
				return []net.IPAddr{{IP: blockedIP}}, nil
			}

			var mu sync.Mutex
			var dialedAddrs []string
			rawDialContext = func(_ context.Context, _ string, addr string) (net.Conn, error) {
				mu.Lock()
				dialedAddrs = append(dialedAddrs, addr)
				mu.Unlock()
				// No real network I/O: hand back one end of an in-memory
				// pipe so the caller sees a connection without this test
				// depending on the network or a real listener.
				client, server := net.Pipe()
				_ = server.Close()
				return client, nil
			}

			h := &Handler{} // nil BlockedCIDRs -> defaultSSRFBlockedCIDRs, which covers 169.254.0.0/16

			// Connection 1: the resolver's "safe" answer. Must succeed and
			// dial the pinned, validated address.
			conn1, err := h.dialContext(context.Background(), "tcp", net.JoinHostPort(hostname, "443"))
			Expect(err).NotTo(HaveOccurred())
			Expect(conn1).NotTo(BeNil())
			_ = conn1.Close()

			// Connection 2: same hostname, but the resolver now answers with
			// the blocked address (the rebind). This is the attack this
			// story closes: a naive "resolve once to validate, let the
			// transport resolve again to connect" design would validate
			// connection 1's safe answer and then connect using connection
			// 2's blocked answer without ever checking it. Here, every
			// connection goes through this same validated dial path, so the
			// blocked answer is checked and rejected — it must never reach
			// rawDialContext.
			conn2, err := h.dialContext(context.Background(), "tcp", net.JoinHostPort(hostname, "443"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(HavePrefix("ssrf_blocked:"))
			Expect(err.Error()).To(ContainSubstring(blockedIP.String()))
			Expect(conn2).To(BeNil())

			Expect(callCount).To(Equal(2), "the second dial must trigger its own resolution, not reuse the first")

			mu.Lock()
			defer mu.Unlock()
			Expect(dialedAddrs).To(HaveLen(1), "only the first, validated connection may reach the real dialer")
			Expect(dialedAddrs[0]).To(Equal(net.JoinHostPort(safeIP.String(), "443")))
			for _, a := range dialedAddrs {
				Expect(a).NotTo(ContainSubstring(blockedIP.String()), "the blocked address must never be dialed")
			}
		})

		It("fails closed when only one of several resolved addresses is blocked", func() {
			const hostname = "multi-addr.example.test"
			safeIP := net.ParseIP("203.0.113.20")
			blockedIP := net.ParseIP("10.0.0.5")

			lookupIPAddr = func(_ context.Context, _ string) ([]net.IPAddr, error) {
				// Order matters for this test: the safe address resolves
				// first, to prove a single blocked address anywhere in the
				// set still rejects the whole hostname (fail-closed), not
				// just "first address wins".
				return []net.IPAddr{{IP: safeIP}, {IP: blockedIP}}, nil
			}

			dialed := false
			rawDialContext = func(_ context.Context, _ string, _ string) (net.Conn, error) {
				dialed = true
				return nil, nil
			}

			h := &Handler{}
			conn, err := h.dialContext(context.Background(), "tcp", net.JoinHostPort(hostname, "443"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(HavePrefix("ssrf_blocked:"))
			Expect(conn).To(BeNil())
			Expect(dialed).To(BeFalse(), "no address may be dialed when any resolved address is blocked")
		})
	})

	Describe("AllowClusterInternal scoping", func() {
		It("bypasses validation for a .svc.cluster.local hostname when AllowClusterInternal is true", func() {
			var dialedAddr string
			rawDialContext = func(_ context.Context, _ string, addr string) (net.Conn, error) {
				dialedAddr = addr
				client, server := net.Pipe()
				_ = server.Close()
				return client, nil
			}
			lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
				Fail("lookupIPAddr must not be called for a bypassed .svc.cluster.local hostname")
				return nil, nil
			}

			h := &Handler{AllowClusterInternal: true}
			conn, err := h.dialContext(context.Background(), "tcp", "my-svc.default.svc.cluster.local:443")
			Expect(err).NotTo(HaveOccurred())
			Expect(conn).NotTo(BeNil())
			Expect(dialedAddr).To(Equal("my-svc.default.svc.cluster.local:443"),
				"the bypass dials the original hostname:port directly, with no CIDR validation performed")
		})

		It("rejects a .svc.cluster.local hostname when AllowClusterInternal is false", func() {
			h := &Handler{AllowClusterInternal: false}
			_, err := h.dialContext(context.Background(), "tcp", "my-svc.default.svc.cluster.local:443")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(HavePrefix("ssrf_blocked:"))
			Expect(err.Error()).To(ContainSubstring(".svc.cluster.local"))
		})

		It("does NOT bypass validation for an IP literal or non-cluster hostname, even when AllowClusterInternal is true", func() {
			h := &Handler{AllowClusterInternal: true}

			_, err := h.dialContext(context.Background(), "tcp", "169.254.169.254:443")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(HavePrefix("ssrf_blocked:"))
			Expect(err.Error()).To(ContainSubstring("169.254.169.254"))
		})
	})

	Describe("TLS certificate hostname verification after IP pinning", func() {
		It("verifies the server certificate against the original request hostname, not the pinned/dialed IP", func() {
			const hostname = "pinned.example.test"

			caCertPEM, serverCertPEM, serverKeyPEM := genTestCAAndLeaf(hostname)
			serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
			Expect(err).NotTo(HaveOccurred())

			// The server's cert only covers DNSNames: [hostname] — it has no
			// IP SAN at all, so verification against the connection's actual
			// IP (127.0.0.1, where httptest always binds) would fail. Success
			// here is only possible if verification used ServerName ==
			// hostname, which net/http's Transport derives from the original
			// request host — independent of what dialContext actually dialed.
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			upstream.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}}
			upstream.StartTLS()
			defer upstream.Close()

			_, portStr, err := net.SplitHostPort(upstream.Listener.Addr().String())
			Expect(err).NotTo(HaveOccurred())

			// Point the fake resolver at wherever httptest actually listens
			// (127.0.0.1) so dialContext pins the connection there, while the
			// request itself — and therefore the TLS ServerName — uses
			// hostname, never that pinned IP.
			lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
				Expect(host).To(Equal(hostname))
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}

			h := &Handler{
				BlockedCIDRs: []*net.IPNet{}, // empty: this test targets loopback, which is blocked by default
			}
			client, err := h.httpClient(ExecuteRequest{TLSCABundle: string(caCertPEM)})
			Expect(err).NotTo(HaveOccurred())

			resp, err := client.Get("https://" + net.JoinHostPort(hostname, portStr) + "/")
			Expect(err).NotTo(HaveOccurred(), "TLS verification must succeed against the original hostname's certificate")
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})
})
