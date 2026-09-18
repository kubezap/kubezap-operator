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

// Package certutil holds the self-signed CA/leaf certificate generation and PEM
// encode/decode helpers shared by every in-cluster TLS bootstrap KubeZap does for
// itself (controller<->executor mTLS, the controller's own admission webhook
// serving cert). None of these certs are issued by, or trusted outside of, the
// cluster they're generated in.
package certutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	pemBlockTypeCertificate = "CERTIFICATE"
	pemBlockTypeECKey       = "EC PRIVATE KEY"
)

// GenerateCA creates a new self-signed CA certificate and ECDSA P-256 private key,
// valid over [notBefore, notAfter).
func GenerateCA(subject pkix.Name, notBefore, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, caKey, nil
}

// IssueLeafCert creates a leaf certificate signed by the given CA, valid over
// [notBefore, notAfter). dnsNames may be nil (e.g. for a client-auth-only cert).
func IssueLeafCert(
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	subject pkix.Name,
	dnsNames []string,
	extKeyUsage x509.ExtKeyUsage,
	notBefore, notAfter time.Time,
) (tls.Certificate, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randomSerial()
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

	certPEM := EncodeCertPEM(certDER)
	keyPEM, err := EncodeECPrivateKeyPEM(leafKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

// EncodeCertPEM PEM-encodes a single DER-encoded certificate.
func EncodeCertPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: pemBlockTypeCertificate, Bytes: der})
}

// EncodeECPrivateKeyPEM PEM-encodes an ECDSA private key.
func EncodeECPrivateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemBlockTypeECKey, Bytes: der}), nil
}

// CertChainPEM PEM-encodes every certificate in a tls.Certificate (leaf + any
// intermediates), in order.
func CertChainPEM(cert tls.Certificate) []byte {
	buf := make([]byte, 0, len(cert.Certificate))
	for _, der := range cert.Certificate {
		buf = append(buf, EncodeCertPEM(der)...)
	}
	return buf
}

// PrivateKeyPEM PEM-encodes the private key stored in a tls.Certificate.
// Supports only ECDSA keys (the only type this package generates).
func PrivateKeyPEM(key interface{}) ([]byte, error) {
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("certutil: unsupported private key type %T", key)
	}
	return EncodeECPrivateKeyPEM(ecKey)
}

// ParseCertPEM parses the first PEM-encoded certificate found in pemBytes.
func ParseCertPEM(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != pemBlockTypeCertificate {
		return nil, fmt.Errorf("certutil: no PEM certificate block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParseECPrivateKeyPEM parses a PEM-encoded ECDSA private key.
func ParseECPrivateKeyPEM(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != pemBlockTypeECKey {
		return nil, fmt.Errorf("certutil: no PEM EC PRIVATE KEY block found")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}
