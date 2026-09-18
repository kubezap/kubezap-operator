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

// Package webhookcerts self-provisions the TLS certificate served by the
// controller-manager's own admission webhook server: a self-signed CA and
// serving cert are generated on first run, stored in a Secret in the
// operator's own namespace, written to disk for certwatcher.CertWatcher to
// serve, and the CA is kept patched into the ValidatingWebhookConfiguration's
// caBundle fields. See docs/design/2026-09-18-self-managed-webhook-certs.md.
package webhookcerts

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"os"
	"path/filepath"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

// Secret data keys.
const (
	KeyCACert  = "ca.crt"
	KeyCAKey   = "ca.key"
	KeyTLSCert = "tls.crt"
	KeyTLSKey  = "tls.key"
)

// Options configures Ensure and Rotator. Zero-value duration fields get sane
// defaults via setDefaults.
type Options struct {
	// Namespace is the operator's own namespace; the Secret is created there.
	Namespace string
	// SecretName holds ca.crt/ca.key/tls.crt/tls.key.
	SecretName string
	// ServiceName is the Service fronting the webhook server; used to compute
	// the DNS SANs the serving cert must cover ({ServiceName}.{Namespace}.svc[.cluster.local]).
	ServiceName string
	// WebhookConfigurationName is the ValidatingWebhookConfiguration whose
	// webhooks[].clientConfig.caBundle is kept in sync with the CA in SecretName.
	// If the object doesn't exist (e.g. an install path that doesn't register
	// admission webhooks), Ensure logs and skips the patch rather than failing —
	// cert provisioning (which prevents the webhook server crash-looping with no
	// cert at all) must not depend on the webhook registration also being present.
	WebhookConfigurationName string
	// CertDir/CertFileName/KeyFileName: where the serving cert/key are written on
	// disk, for consumption by certwatcher.CertWatcher.
	CertDir      string
	CertFileName string
	KeyFileName  string

	// CAValidity is how long the self-signed CA is valid for. Defaults to 10 years.
	CAValidity time.Duration
	// CertValidity is how long each serving leaf cert is valid for. Defaults to 1 year.
	CertValidity time.Duration
	// RotateBefore is how far ahead of expiry a leaf cert is renewed. Defaults to 30 days.
	RotateBefore time.Duration
}

func (o *Options) setDefaults() {
	if o.CertFileName == "" {
		o.CertFileName = "tls.crt"
	}
	if o.KeyFileName == "" {
		o.KeyFileName = "tls.key"
	}
	if o.CAValidity == 0 {
		o.CAValidity = 10 * 365 * 24 * time.Hour
	}
	if o.CertValidity == 0 {
		o.CertValidity = 365 * 24 * time.Hour
	}
	if o.RotateBefore == 0 {
		o.RotateBefore = 30 * 24 * time.Hour
	}
}

func (o *Options) dnsNames() []string {
	return []string{
		fmt.Sprintf("%s.%s.svc", o.ServiceName, o.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", o.ServiceName, o.Namespace),
	}
}

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;update;patch

// Ensure makes sure a CA + serving cert exist in opts.SecretName (creating or
// rotating the leaf as needed), writes the serving cert/key to opts.CertDir,
// and patches the CA into opts.WebhookConfigurationName's caBundle fields.
//
// It is safe to call concurrently from multiple replicas: Secret creation and
// updates use optimistic concurrency (resourceVersion), so a replica that
// loses a create/update race simply re-reads and converges to whatever won —
// there is no leader-only assumption anywhere in this function.
//
// Call it once, synchronously, before the webhook server starts (from main()
// with an uncached client, since the manager's cache isn't running yet), and
// repeatedly thereafter via Rotator on every replica — a rotation performed by
// any one replica must still be picked up by every other replica's own local
// certwatcher, which only reads its own on-disk files.
func Ensure(ctx context.Context, c client.Client, opts Options) error {
	opts.setDefaults()
	log := logf.FromContext(ctx).WithName("webhookcerts")

	secret, err := ensureSecret(ctx, c, opts)
	if err != nil {
		return fmt.Errorf("ensuring cert secret: %w", err)
	}

	if err := writeCertFiles(opts, secret); err != nil {
		return fmt.Errorf("writing cert files to disk: %w", err)
	}

	if err := patchCABundle(ctx, c, opts, secret.Data[KeyCACert]); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ValidatingWebhookConfiguration not found; skipping caBundle patch",
				"name", opts.WebhookConfigurationName)
			return nil
		}
		return fmt.Errorf("patching webhook caBundle: %w", err)
	}

	return nil
}

// ensureSecret gets the cert Secret, creating it (CA + leaf) if absent, or
// rotating the leaf cert in place if it's within opts.RotateBefore of expiry.
func ensureSecret(ctx context.Context, c client.Client, opts Options) (*corev1.Secret, error) {
	key := client.ObjectKey{Namespace: opts.Namespace, Name: opts.SecretName}
	secret := &corev1.Secret{}
	err := c.Get(ctx, key, secret)
	switch {
	case apierrors.IsNotFound(err):
		created, cerr := createSecret(ctx, c, opts)
		if cerr == nil {
			return created, nil
		}
		if !apierrors.IsAlreadyExists(cerr) {
			return nil, cerr
		}
		// Lost the create race to another replica starting concurrently; the
		// winner's Secret is just as valid, so read it back instead of erroring.
		if gerr := c.Get(ctx, key, secret); gerr != nil {
			return nil, gerr
		}
	case err != nil:
		return nil, err
	}

	if needsLeafRotation(secret, opts) {
		return rotateLeaf(ctx, c, opts, secret)
	}
	return secret, nil
}

func createSecret(ctx context.Context, c client.Client, opts Options) (*corev1.Secret, error) {
	now := time.Now().UTC()
	caCert, caKey, err := certutil.GenerateCA(pkix.Name{
		Organization: []string{"kubezap.io"},
		CommonName:   "kubezap-webhook-ca",
	}, now, now.Add(opts.CAValidity))
	if err != nil {
		return nil, err
	}
	leafCert, err := certutil.IssueLeafCert(
		caCert, caKey,
		pkix.Name{CommonName: opts.ServiceName},
		opts.dnsNames(),
		x509.ExtKeyUsageServerAuth,
		now, now.Add(opts.CertValidity),
	)
	if err != nil {
		return nil, err
	}
	data, err := secretData(caCert, caKey, leafCert)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: opts.SecretName, Namespace: opts.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	if err := c.Create(ctx, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// rotateLeaf issues a new leaf cert signed by the CA already in secret (the CA
// itself is never rotated by this path) and updates the Secret in place,
// retrying on a concurrent-update conflict from another replica.
func rotateLeaf(ctx context.Context, c client.Client, opts Options, secret *corev1.Secret) (*corev1.Secret, error) {
	caCert, cerr := certutil.ParseCertPEM(secret.Data[KeyCACert])
	caKey, kerr := certutil.ParseECPrivateKeyPEM(secret.Data[KeyCAKey])
	if cerr != nil || kerr != nil {
		// CA material is missing or unparseable — start over rather than sign
		// against something broken.
		return createSecret(ctx, c, opts)
	}

	var updated corev1.Secret
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(secret), current); err != nil {
			return err
		}
		now := time.Now().UTC()
		leafCert, err := certutil.IssueLeafCert(
			caCert, caKey,
			pkix.Name{CommonName: opts.ServiceName},
			opts.dnsNames(),
			x509.ExtKeyUsageServerAuth,
			now, now.Add(opts.CertValidity),
		)
		if err != nil {
			return err
		}
		data, err := secretData(caCert, caKey, leafCert)
		if err != nil {
			return err
		}
		current.Data = data
		if err := c.Update(ctx, current); err != nil {
			return err
		}
		updated = *current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func needsLeafRotation(secret *corev1.Secret, opts Options) bool {
	certPEM := secret.Data[KeyTLSCert]
	if len(certPEM) == 0 || len(secret.Data[KeyTLSKey]) == 0 || len(secret.Data[KeyCACert]) == 0 {
		return true
	}
	cert, err := certutil.ParseCertPEM(certPEM)
	if err != nil {
		return true
	}
	return time.Now().UTC().Add(opts.RotateBefore).After(cert.NotAfter)
}

func secretData(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, leaf tls.Certificate) (map[string][]byte, error) {
	caKeyPEM, err := certutil.EncodeECPrivateKeyPEM(caKey)
	if err != nil {
		return nil, err
	}
	leafKeyPEM, err := certutil.PrivateKeyPEM(leaf.PrivateKey)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		KeyCACert:  certutil.EncodeCertPEM(caCert.Raw),
		KeyCAKey:   caKeyPEM,
		KeyTLSCert: certutil.CertChainPEM(leaf),
		KeyTLSKey:  leafKeyPEM,
	}, nil
}

// writeCertFiles writes the Secret's serving cert/key to opts.CertDir, only
// touching disk when the content actually changed (avoids spurious fsnotify
// events firing in every replica's certwatcher on every no-op reconcile).
func writeCertFiles(opts Options, secret *corev1.Secret) error {
	if err := os.MkdirAll(opts.CertDir, 0o750); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(opts.CertDir, opts.CertFileName), secret.Data[KeyTLSCert]); err != nil {
		return err
	}
	return writeIfChanged(filepath.Join(opts.CertDir, opts.KeyFileName), secret.Data[KeyTLSKey])
}

func writeIfChanged(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return os.WriteFile(path, data, 0o600)
}

// patchCABundle updates every webhook entry's ClientConfig.CABundle in
// opts.WebhookConfigurationName to caCertPEM, if it isn't already current.
// Returns an apierrors.IsNotFound error, unwrapped, if the object doesn't
// exist — callers decide whether that's fatal.
func patchCABundle(ctx context.Context, c client.Client, opts Options, caCertPEM []byte) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cfg admissionregistrationv1.ValidatingWebhookConfiguration
		if err := c.Get(ctx, client.ObjectKey{Name: opts.WebhookConfigurationName}, &cfg); err != nil {
			return err
		}
		changed := false
		for i := range cfg.Webhooks {
			if !bytes.Equal(cfg.Webhooks[i].ClientConfig.CABundle, caCertPEM) {
				cfg.Webhooks[i].ClientConfig.CABundle = caCertPEM
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return c.Update(ctx, &cfg)
	})
}
