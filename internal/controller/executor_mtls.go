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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
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

	// --- CA ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			Organization:       []string{"kubezap.io"},
			OrganizationalUnit: []string{"executor-ca"},
			CommonName:         "kubezap-executor-ca",
		},
		NotBefore:             now,
		NotAfter:              expiry,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	// --- server cert (for the executor pod) ---
	serverCert, err := issueLeafCert(
		now, expiry,
		pkix.Name{CommonName: "kubezap-http-executor"},
		serverDNSNames,
		x509.ExtKeyUsageServerAuth,
		caCert, caKey,
	)
	if err != nil {
		return nil, err
	}

	// --- client cert (for the controller) ---
	clientCert, err := issueLeafCert(
		now, expiry,
		pkix.Name{CommonName: "kubezap-controller"},
		nil,
		x509.ExtKeyUsageClientAuth,
		caCert, caKey,
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

// issueLeafCert creates a signed leaf certificate for either server or client auth.
func issueLeafCert(
	notBefore, notAfter time.Time,
	subject pkix.Name,
	dnsNames []string,
	extKeyUsage x509.ExtKeyUsage,
	caCert *x509.Certificate,
	caKey crypto.PrivateKey,
) (tls.Certificate, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{extKeyUsage},
		DNSNames:              dnsNames,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
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
	return certChainPEM(b.ServerCert)
}

// ServerKeyPEM returns the PEM-encoded server private key.
func (b *MTLSBundle) ServerKeyPEM() []byte {
	return privateKeyPEM(b.ServerCert.PrivateKey)
}

// CACertPEM returns the PEM-encoded CA certificate.
func (b *MTLSBundle) CACertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b.CACert.Raw})
}

// NeedsRotation returns true when the bundle will expire within 1 hour.
func (b *MTLSBundle) NeedsRotation() bool {
	return time.Now().UTC().Add(time.Hour).After(b.ExpiresAt)
}

// certChainPEM encodes the leaf (and any intermediate) certificates in a tls.Certificate
// as a PEM block sequence.
func certChainPEM(cert tls.Certificate) []byte {
	buf := make([]byte, 0, len(cert.Certificate))
	for _, der := range cert.Certificate {
		buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return buf
}

// privateKeyPEM encodes the private key stored in a tls.Certificate as PEM.
// Supports ECDSA keys (the only type generated by this package).
func privateKeyPEM(key crypto.PrivateKey) []byte {
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil
	}
	der, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
