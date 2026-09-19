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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// See docs/design/allnamespaces-secrets-label-restriction.md.
//
// Each case uses its own distinct, never-reused namespace name: envtest runs no
// namespace-lifecycle controller, so a deleted Namespace stays in Terminating
// forever rather than actually going away, making name reuse across cases unsafe.
var _ = Describe("checkNamespaceManagedForSecrets", func() {
	newNamespace := func(name string, labels map[string]string) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: labels,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	}

	It("always allows access when allNamespacesMode is false, regardless of labels", func() {
		const ns = "secrets-access-test-mode-off"
		newNamespace(ns, nil)
		Expect(checkNamespaceManagedForSecrets(ctx, k8sClient, false, ns)).To(Succeed())
	})

	It("denies access in AllNamespaces mode when the namespace has no kubezap.io/managed label", func() {
		const ns = "secrets-access-test-no-label"
		newNamespace(ns, nil)
		err := checkNamespaceManagedForSecrets(ctx, k8sClient, true, ns)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubezap.io/managed=true"))
	})

	It("denies access in AllNamespaces mode when the label is present but false", func() {
		const ns = "secrets-access-test-label-false"
		newNamespace(ns, map[string]string{"kubezap.io/managed": "false"})
		Expect(checkNamespaceManagedForSecrets(ctx, k8sClient, true, ns)).NotTo(Succeed())
	})

	It("allows access in AllNamespaces mode when the namespace carries kubezap.io/managed=true", func() {
		const ns = "secrets-access-test-label-true"
		newNamespace(ns, map[string]string{"kubezap.io/managed": "true"})
		Expect(checkNamespaceManagedForSecrets(ctx, k8sClient, true, ns)).To(Succeed())
	})

	It("returns an error (not a silent allow) when the namespace does not exist", func() {
		err := checkNamespaceManagedForSecrets(ctx, k8sClient, true, "secrets-access-test-nonexistent")
		Expect(err).To(HaveOccurred())
	})
})
