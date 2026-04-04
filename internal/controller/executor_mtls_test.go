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

package controller

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MTLSBundle concurrent access", func() {
	Describe("concurrent reads", func() {
		It("allows concurrent reads of ClientTLSConfig, ServerCertPEM, ServerKeyPEM, CACertPEM without data races", func() {
			b, err := GenerateMTLSBundle([]string{"localhost"})
			Expect(err).NotTo(HaveOccurred())
			Expect(b).NotTo(BeNil())

			const goroutines = 100
			const iterations = 50

			var wg sync.WaitGroup
			wg.Add(goroutines)

			for i := 0; i < goroutines; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < iterations; j++ {
						cfg := b.ClientTLSConfig()
						Expect(cfg).NotTo(BeNil())

						serverPEM := b.ServerCertPEM()
						Expect(serverPEM).NotTo(BeEmpty())

						keyPEM := b.ServerKeyPEM()
						Expect(keyPEM).NotTo(BeEmpty())

						caPEM := b.CACertPEM()
						Expect(caPEM).NotTo(BeEmpty())
					}
				}()
			}

			wg.Wait()
		})
	})

	Describe("NeedsRotation", func() {
		It("returns false for a freshly generated bundle", func() {
			b, err := GenerateMTLSBundle([]string{"localhost"})
			Expect(err).NotTo(HaveOccurred())
			Expect(b.NeedsRotation()).To(BeFalse())
		})

		It("returns true when ExpiresAt is in the past", func() {
			b, err := GenerateMTLSBundle([]string{"localhost"})
			Expect(err).NotTo(HaveOccurred())

			b.ExpiresAt = time.Now().Add(-1 * time.Minute)
			Expect(b.NeedsRotation()).To(BeTrue())
		})
	})

	Describe("TLS handshake", func() {
		It("ClientTLSConfig verifies the server cert issued by the same bundle", func() {
			b, err := GenerateMTLSBundle([]string{"localhost"})
			Expect(err).NotTo(HaveOccurred())

			// Build a tls.Certificate from the bundle's server PEM.
			serverTLSCert, err := tls.X509KeyPair(b.ServerCertPEM(), b.ServerKeyPEM())
			Expect(err).NotTo(HaveOccurred())

			// Start a TLS test server using the bundle's server cert.
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			srv.TLS = &tls.Config{
				Certificates: []tls.Certificate{serverTLSCert},
				MinVersion:   tls.VersionTLS13,
			}
			srv.StartTLS()
			defer srv.Close()

			// The httptest server listens on 127.0.0.1:<port>. The bundle cert has "localhost"
			// as a DNS SAN but no IP SAN. Override ServerName so Go's TLS verifier matches
			// the cert's DNS SAN instead of the raw IP in the URL.
			clientCfg := b.ClientTLSConfig()
			clientCfg.ServerName = "localhost"

			httpClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: clientCfg,
				},
			}

			resp, err := httpClient.Get(srv.URL)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})
})
