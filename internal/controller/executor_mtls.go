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
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"time"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

// MTLSBundle holds an in-memory CA and the derived cert pair for the executor channel.
// The bundle is generated once at startup (or on rotation) and is never persisted to etcd.
// The server cert is mounted into the executor Deployment via a Secret; the client cert
// is used by the controller's HTTP client when calling the executor RPC endpoint.
type MTLSBundle struct {
	CACert     *x509.Certificate
	CAKey      crypto.PrivateKey
	ServerCert tls.Certificate // for executor pod (server)
	ClientCert tls.Certificate // for controller (client)
	ExpiresAt  time.Time
}

// GenerateMTLSBundle generates a self-signed CA and two leaf certs (server + client).
// Uses ECDSA P-256 for all keys. Cert lifetime is 24h from now.
//
// serverDNSNames should include the in-cluster DNS names for the executor Service,
// e.g. []string{"kubezap-http-executor.<ns>.svc.cluster.local", "kubezap-http-executor.<ns>.svc"}.
// Pass []string{"kubezap-http-executor"} as a fallback when the namespace is not yet known;
// the integrator (cmd/main.go) must pass the correct names at startup.
func GenerateMTLSBundle(serverDNSNames []string) (*MTLSBundle, error) {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	caCert, caKey, err := certutil.GenerateCA(pkix.Name{
		Organization:       []string{"kubezap.io"},
		OrganizationalUnit: []string{"executor-ca"},
		CommonName:         "kubezap-executor-ca",
	}, now, expiry)
	if err != nil {
		return nil, err
	}

	serverCert, err := certutil.IssueLeafCert(
		caCert, caKey,
		pkix.Name{CommonName: "kubezap-http-executor"},
		serverDNSNames,
		x509.ExtKeyUsageServerAuth,
		now, expiry,
	)
	if err != nil {
		return nil, err
	}

	clientCert, err := certutil.IssueLeafCert(
		caCert, caKey,
		pkix.Name{CommonName: "kubezap-controller"},
		nil,
		x509.ExtKeyUsageClientAuth,
		now, expiry,
	)
	if err != nil {
		return nil, err
	}

	return &MTLSBundle{
		CACert:     caCert,
		CAKey:      caKey,
		ServerCert: serverCert,
		ClientCert: clientCert,
		ExpiresAt:  expiry,
	}, nil
}

// ClientTLSConfig returns a *tls.Config for the controller's HTTP client that:
//   - presents b.ClientCert on every TLS handshake
//   - verifies the server certificate against b.CACert (no system roots)
func (b *MTLSBundle) ClientTLSConfig() *tls.Config {
	caPool := x509.NewCertPool()
	caPool.AddCert(b.CACert)
	return &tls.Config{
		Certificates: []tls.Certificate{b.ClientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
}

// ServerCertPEM returns the PEM-encoded server certificate.
func (b *MTLSBundle) ServerCertPEM() []byte {
	return certutil.CertChainPEM(b.ServerCert)
}

// ServerKeyPEM returns the PEM-encoded server private key.
func (b *MTLSBundle) ServerKeyPEM() []byte {
	pemBytes, err := certutil.PrivateKeyPEM(b.ServerCert.PrivateKey)
	if err != nil {
		return nil
	}
	return pemBytes
}

// CACertPEM returns the PEM-encoded CA certificate.
func (b *MTLSBundle) CACertPEM() []byte {
	return certutil.EncodeCertPEM(b.CACert.Raw)
}

// NeedsRotation returns true when the bundle will expire within 1 hour.
func (b *MTLSBundle) NeedsRotation() bool {
	return time.Now().UTC().Add(time.Hour).After(b.ExpiresAt)
}
