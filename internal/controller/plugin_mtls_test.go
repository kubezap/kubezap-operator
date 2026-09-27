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
	"crypto/x509"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

var _ = Describe("GeneratePluginMTLSBundle", func() {
	It("issues a CA, a server cert with the given DNS SANs, and a client cert", func() {
		dnsNames := []string{"kubezap-plugin-my-plugin.default.svc", "kubezap-plugin-my-plugin.default.svc.cluster.local"}
		bundle, err := GeneratePluginMTLSBundle("my-plugin", dnsNames)
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle.CACert.IsCA).To(BeTrue())

		leaf, err := x509.ParseCertificate(bundle.ServerCert.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(leaf.DNSNames).To(ConsistOf(dnsNames[0], dnsNames[1]))
		Expect(leaf.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))

		clientLeaf, err := x509.ParseCertificate(bundle.ClientCert.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(clientLeaf.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageClientAuth))
	})

	It("sets ExpiresAt roughly 24h in the future and NeedsRotation is false for a fresh bundle", func() {
		bundle, err := GeneratePluginMTLSBundle("my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle.ExpiresAt).To(BeTemporally("~", time.Now().UTC().Add(24*time.Hour), time.Minute))
		Expect(bundle.NeedsRotation()).To(BeFalse())
	})

	It("reports NeedsRotation true once within 1h of expiry", func() {
		bundle, err := GeneratePluginMTLSBundle("my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())
		bundle.ExpiresAt = time.Now().UTC().Add(30 * time.Minute)
		Expect(bundle.NeedsRotation()).To(BeTrue())
	})

	// Acceptance criterion: the controller "verifies the plugin's server cert
	// against that Integration's own CA (never another Integration's)".
	It("scopes trust to exactly one Integration's own CA — another Integration's server cert does not verify", func() {
		bundleA, err := GeneratePluginMTLSBundle("integration-a", []string{"kubezap-plugin-integration-a.default.svc"})
		Expect(err).NotTo(HaveOccurred())
		bundleB, err := GeneratePluginMTLSBundle("integration-b", []string{"kubezap-plugin-integration-b.default.svc"})
		Expect(err).NotTo(HaveOccurred())

		// bundleA's client TLS config trusts only bundleA's CA.
		clientCfgA := bundleA.ClientTLSConfig()

		bServerLeaf, err := x509.ParseCertificate(bundleB.ServerCert.Certificate[0])
		Expect(err).NotTo(HaveOccurred())

		_, err = bServerLeaf.Verify(x509.VerifyOptions{
			Roots:     clientCfgA.RootCAs,
			DNSName:   "kubezap-plugin-integration-b.default.svc",
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		Expect(err).To(HaveOccurred(), "Integration A's client trust store must not verify Integration B's plugin server cert")

		// Sanity check: bundleA's own server cert DOES verify against its own client trust store.
		aServerLeaf, err := x509.ParseCertificate(bundleA.ServerCert.Certificate[0])
		Expect(err).NotTo(HaveOccurred())
		_, err = aServerLeaf.Verify(x509.VerifyOptions{
			Roots:     clientCfgA.RootCAs,
			DNSName:   "kubezap-plugin-integration-a.default.svc",
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("round-trips PEM encodings for server cert, server key, and CA cert", func() {
		bundle, err := GeneratePluginMTLSBundle("my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(bundle.ServerCertPEM()).NotTo(BeEmpty())
		Expect(bundle.ServerKeyPEM()).NotTo(BeEmpty())
		Expect(bundle.CACertPEM()).NotTo(BeEmpty())

		caCert, err := certutil.ParseCertPEM(bundle.CACertPEM())
		Expect(err).NotTo(HaveOccurred())
		Expect(caCert.Subject.CommonName).To(Equal("kubezap-plugin-my-plugin-ca"))
	})
})

var _ = Describe("PluginMTLSStore", func() {
	It("returns nil TLS config for an Integration with no tracked bundle", func() {
		store := NewPluginMTLSStore()
		Expect(store.ClientTLSConfigFor("default", "unknown")).To(BeNil())
	})

	It("generates a bundle on first GetOrGenerate and reuses it on subsequent calls", func() {
		store := NewPluginMTLSStore()
		b1, err := store.GetOrGenerate("default", "my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())

		b2, err := store.GetOrGenerate("default", "my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(b2).To(BeIdenticalTo(b1), "a non-expiring bundle should not be regenerated")

		Expect(store.ClientTLSConfigFor("default", "my-plugin")).NotTo(BeNil())
	})

	It("keys bundles independently per namespace/name so two Integrations never share a CA", func() {
		store := NewPluginMTLSStore()
		_, err := store.GetOrGenerate("ns-a", "shared-name", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = store.GetOrGenerate("ns-b", "shared-name", nil)
		Expect(err).NotTo(HaveOccurred())

		cfgA := store.ClientTLSConfigFor("ns-a", "shared-name")
		cfgB := store.ClientTLSConfigFor("ns-b", "shared-name")
		Expect(cfgA.Certificates[0].Certificate[0]).NotTo(Equal(cfgB.Certificates[0].Certificate[0]))
	})

	It("regenerates a bundle once NeedsRotation is true", func() {
		store := NewPluginMTLSStore()
		original, err := store.GetOrGenerate("default", "my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())

		// Force rotation by mutating the tracked bundle's expiry directly.
		original.ExpiresAt = time.Now().UTC().Add(30 * time.Minute)

		rotated, err := store.GetOrGenerate("default", "my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(rotated).NotTo(BeIdenticalTo(original))
		Expect(rotated.NeedsRotation()).To(BeFalse())
	})

	It("Remove drops a tracked bundle so ClientTLSConfigFor reports nil again", func() {
		store := NewPluginMTLSStore()
		_, err := store.GetOrGenerate("default", "my-plugin", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.ClientTLSConfigFor("default", "my-plugin")).NotTo(BeNil())

		store.Remove("default", "my-plugin")
		Expect(store.ClientTLSConfigFor("default", "my-plugin")).To(BeNil())
	})
})
