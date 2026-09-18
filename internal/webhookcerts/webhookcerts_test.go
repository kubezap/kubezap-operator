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

package webhookcerts

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

// uniqueOptions returns Options pointing at a fresh Secret name, webhook
// configuration name, and on-disk cert dir, so tests don't interfere with
// each other or with prior runs' state.
func uniqueOptions(suffix string) Options {
	dir, err := os.MkdirTemp("", "webhookcerts-test-"+suffix)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })

	return Options{
		Namespace:                "default",
		SecretName:               "cert-secret-" + suffix,
		ServiceName:              "test-webhook-service",
		WebhookConfigurationName: "test-validating-webhook-" + suffix,
		CertDir:                  dir,
		CertFileName:             "tls.crt",
		KeyFileName:              "tls.key",
	}
}

func readSecret(opts Options) *corev1.Secret {
	secret := &corev1.Secret{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.SecretName}, secret)).To(Succeed())
	return secret
}

var _ = Describe("Ensure", func() {
	It("creates a CA + serving cert secret and writes the cert files to disk when none exist", func() {
		opts := uniqueOptions("create")

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())

		secret := readSecret(opts)
		Expect(secret.Data[KeyCACert]).NotTo(BeEmpty())
		Expect(secret.Data[KeyCAKey]).NotTo(BeEmpty())
		Expect(secret.Data[KeyTLSCert]).NotTo(BeEmpty())
		Expect(secret.Data[KeyTLSKey]).NotTo(BeEmpty())

		leafCert, err := certutil.ParseCertPEM(secret.Data[KeyTLSCert])
		Expect(err).NotTo(HaveOccurred())
		Expect(leafCert.DNSNames).To(ConsistOf(
			"test-webhook-service.default.svc",
			"test-webhook-service.default.svc.cluster.local",
		))

		onDiskCert, err := os.ReadFile(filepath.Join(opts.CertDir, opts.CertFileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(onDiskCert).To(Equal(secret.Data[KeyTLSCert]))

		onDiskKey, err := os.ReadFile(filepath.Join(opts.CertDir, opts.KeyFileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(onDiskKey).To(Equal(secret.Data[KeyTLSKey]))
	})

	It("does not fail when the ValidatingWebhookConfiguration doesn't exist", func() {
		opts := uniqueOptions("no-webhookconfig")

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())
	})

	It("patches every webhook entry's caBundle to match the secret's CA", func() {
		opts := uniqueOptions("patch-cabundle")

		webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: opts.WebhookConfigurationName},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{
				newTestWebhook("first.example.com"),
				newTestWebhook("second.example.com"),
			},
		}
		Expect(k8sClient.Create(ctx, webhookCfg)).To(Succeed())

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())

		secret := readSecret(opts)

		updated := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: opts.WebhookConfigurationName}, updated)).To(Succeed())
		for _, wh := range updated.Webhooks {
			Expect(wh.ClientConfig.CABundle).To(Equal(secret.Data[KeyCACert]))
		}
	})

	It("is idempotent: a second call with no rotation due leaves the leaf cert unchanged", func() {
		opts := uniqueOptions("idempotent")

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())
		first := readSecret(opts)

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())
		second := readSecret(opts)

		Expect(second.Data[KeyTLSCert]).To(Equal(first.Data[KeyTLSCert]))
		Expect(second.Data[KeyCACert]).To(Equal(first.Data[KeyCACert]))
	})

	It("rotates the leaf cert but keeps the same CA once within RotateBefore of expiry", func() {
		opts := uniqueOptions("rotate")
		opts.CertValidity = time.Hour
		opts.RotateBefore = 2 * time.Hour // always "due" relative to a 1h-valid cert

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())
		first := readSecret(opts)

		Expect(Ensure(ctx, k8sClient, opts)).To(Succeed())
		second := readSecret(opts)

		Expect(second.Data[KeyCACert]).To(Equal(first.Data[KeyCACert]), "CA must not be regenerated on a leaf rotation")
		Expect(second.Data[KeyTLSCert]).NotTo(Equal(first.Data[KeyTLSCert]), "leaf cert should have been rotated")

		onDiskCert, err := os.ReadFile(filepath.Join(opts.CertDir, opts.CertFileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(onDiskCert).To(Equal(second.Data[KeyTLSCert]), "on-disk cert must reflect the rotated leaf")
	})
})

var _ = Describe("Rotator", func() {
	It("never requires leader election", func() {
		r := &Rotator{}
		Expect(r.NeedLeaderElection()).To(BeFalse())
	})
})

func newTestWebhook(name string) admissionregistrationv1.ValidatingWebhook {
	sideEffects := admissionregistrationv1.SideEffectClassNone
	path := "/validate"
	return admissionregistrationv1.ValidatingWebhook{
		Name: name,
		ClientConfig: admissionregistrationv1.WebhookClientConfig{
			Service: &admissionregistrationv1.ServiceReference{
				Namespace: "default",
				Name:      "test-webhook-service",
				Path:      &path,
			},
		},
		SideEffects:             &sideEffects,
		AdmissionReviewVersions: []string{"v1"},
	}
}
