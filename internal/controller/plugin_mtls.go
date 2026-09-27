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
	"fmt"
	"sync"
	"time"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

// PluginMTLSBundle holds an in-memory CA and the derived server/client cert
// pair for a single plugin Integration's /publish channel.
//
// This is deliberately NOT shared across Integrations the way MTLSBundle
// (executor_mtls.go) is shared cluster-wide for the controller<->http-executor
// channel: the executor is first-party code the operator itself ships, so
// trusting the one executor CA everywhere is safe. A plugin is third-party
// code supplied by the Integration author, so trusting one plugin's server
// cert must never imply trusting another Integration's plugin — hence one
// bundle (one CA) per opted-in Integration.
//
// Never persisted to etcd in this Go form; only the PEM bytes derived from it
// are written to the per-Integration Secret (server leaf + CA) that the
// plugin Deployment mounts. The client cert is held only in the controller's
// process memory.
type PluginMTLSBundle struct {
	CACert     *x509.Certificate
	CAKey      crypto.PrivateKey
	ServerCert tls.Certificate // mounted into the plugin Deployment (server)
	ClientCert tls.Certificate // used by the controller's /publish HTTP client
	ExpiresAt  time.Time
}

// GeneratePluginMTLSBundle generates a self-signed CA scoped to one plugin
// Integration and two leaf certs (server + client) signed by it. Uses ECDSA
// P-256 for all keys, mirroring GenerateMTLSBundle's executor-channel
// precedent. Cert lifetime is 24h from now; callers should regenerate once
// NeedsRotation() reports true, which — combined with a ~5-minute recheck
// interval — yields the same ~23h rotation cadence as the executor channel.
//
// serverDNSNames should include the in-cluster DNS names the controller
// actually dials for this Integration's plugin, e.g.
// []string{"kubezap-plugin-<name>.<ns>.svc", "kubezap-plugin-<name>.<ns>.svc.cluster.local"}.
func GeneratePluginMTLSBundle(integrationName string, serverDNSNames []string) (*PluginMTLSBundle, error) {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	caCert, caKey, err := certutil.GenerateCA(pkix.Name{
		Organization:       []string{"kubezap.io"},
		OrganizationalUnit: []string{"plugin-mtls-ca"},
		CommonName:         fmt.Sprintf("kubezap-plugin-%s-ca", integrationName),
	}, now, expiry)
	if err != nil {
		return nil, err
	}

	serverCert, err := certutil.IssueLeafCert(
		caCert, caKey,
		pkix.Name{CommonName: fmt.Sprintf("kubezap-plugin-%s", integrationName)},
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

	return &PluginMTLSBundle{
		CACert:     caCert,
		CAKey:      caKey,
		ServerCert: serverCert,
		ClientCert: clientCert,
		ExpiresAt:  expiry,
	}, nil
}

// ClientTLSConfig returns a *tls.Config for the controller's /publish HTTP
// client that:
//   - presents b.ClientCert on every TLS handshake
//   - verifies the plugin's server certificate against b.CACert only — never
//     another Integration's CA, since RootCAs is set to exactly this bundle's
//     pool and nothing else (not even system roots).
func (b *PluginMTLSBundle) ClientTLSConfig() *tls.Config {
	caPool := x509.NewCertPool()
	caPool.AddCert(b.CACert)
	return &tls.Config{
		Certificates: []tls.Certificate{b.ClientCert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
}

// ServerCertPEM returns the PEM-encoded server certificate chain.
func (b *PluginMTLSBundle) ServerCertPEM() []byte {
	return certutil.CertChainPEM(b.ServerCert)
}

// ServerKeyPEM returns the PEM-encoded server private key.
func (b *PluginMTLSBundle) ServerKeyPEM() []byte {
	pemBytes, err := certutil.PrivateKeyPEM(b.ServerCert.PrivateKey)
	if err != nil {
		return nil
	}
	return pemBytes
}

// CACertPEM returns the PEM-encoded CA certificate.
func (b *PluginMTLSBundle) CACertPEM() []byte {
	return certutil.EncodeCertPEM(b.CACert.Raw)
}

// NeedsRotation returns true when the bundle will expire within 1 hour.
func (b *PluginMTLSBundle) NeedsRotation() bool {
	return time.Now().UTC().Add(time.Hour).After(b.ExpiresAt)
}

// PluginMTLSStore is an in-memory, per-Integration registry of
// PluginMTLSBundles, keyed by "<namespace>/<name>".
//
// IntegrationReconciler owns writes: GetOrGenerate is called on every
// reconcile pass for a plugin Integration with spec.plugin.mtls.enabled=true,
// generating a bundle on first sight and transparently rotating it once
// NeedsRotation() is true — no separate background goroutine is required
// (contrast with the executor channel's cmd/main.go-owned rotation ticker),
// because IntegrationReconciler.Reconcile requeues itself periodically for
// every opted-in Integration (see reconcilePluginMTLS in
// integration_controller.go).
//
// The controller's /publish HTTP client is the intended reader, via
// ClientTLSConfigFor. A single *PluginMTLSStore instance must be shared
// between IntegrationReconciler and whatever issues the /publish call
// (FlowRunReconciler, in internal/controller/flowrun_controller.go) — see
// this story's implementation notes for the deferred wiring step, analogous
// to how cmd/main.go shares one *MTLSBundle between ExecutorReconciler and
// FlowRunReconciler for the executor channel.
type PluginMTLSStore struct {
	mu      sync.RWMutex
	bundles map[string]*PluginMTLSBundle
}

// NewPluginMTLSStore returns an empty, ready-to-use store.
func NewPluginMTLSStore() *PluginMTLSStore {
	return &PluginMTLSStore{bundles: make(map[string]*PluginMTLSBundle)}
}

func pluginMTLSStoreKey(namespace, name string) string {
	return namespace + "/" + name
}

// GetOrGenerate returns the current bundle for the given Integration,
// generating a fresh one if none exists yet, or if the existing one's
// NeedsRotation() is true. serverDNSNames is only consulted when a new
// bundle must actually be generated.
func (s *PluginMTLSStore) GetOrGenerate(namespace, name string, serverDNSNames []string) (*PluginMTLSBundle, error) {
	key := pluginMTLSStoreKey(namespace, name)

	s.mu.RLock()
	existing := s.bundles[key]
	s.mu.RUnlock()
	if existing != nil && !existing.NeedsRotation() {
		return existing, nil
	}

	bundle, err := GeneratePluginMTLSBundle(name, serverDNSNames)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.bundles[key] = bundle
	s.mu.Unlock()
	return bundle, nil
}

// ClientTLSConfigFor returns the *tls.Config the controller should use to
// present its client cert and verify the plugin's server cert for the named
// Integration's /publish calls. Returns nil if no bundle is registered for
// that Integration (mTLS not enabled, or not yet generated).
func (s *PluginMTLSStore) ClientTLSConfigFor(namespace, name string) *tls.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bundle := s.bundles[pluginMTLSStoreKey(namespace, name)]
	if bundle == nil {
		return nil
	}
	return bundle.ClientTLSConfig()
}

// Remove deletes any tracked bundle for the given Integration. Called when
// mTLS is disabled on an Integration or the Integration is deleted, so a
// stale bundle is not retained in memory forever.
func (s *PluginMTLSStore) Remove(namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bundles, pluginMTLSStoreKey(namespace, name))
}
