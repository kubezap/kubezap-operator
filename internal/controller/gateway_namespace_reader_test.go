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
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// See docs/design/allnamespaces-gateway-namespace-read-rbac.md.
//
// Each case below uses its own distinct, never-reused namespace name literal:
// envtest runs no namespace-lifecycle controller, so a deleted Namespace stays
// in Terminating forever rather than actually going away, making name reuse
// across cases unsafe (this has bitten this exact test suite before, per
// secrets_access_test.go).
var _ = Describe("ensureGatewayNamespaceReaderBinding", func() {
	It("creates the ClusterRoleBinding with a single Subject when it does not yet exist", func() {
		const bindingName = "gwns-reader-test-create"
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, bindingName, sharedGatewayServiceAccountName, "gwns-reader-test-ns-create")).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName}})
		})

		var crb rbacv1.ClusterRoleBinding
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bindingName}, &crb)).To(Succeed())
		Expect(crb.RoleRef).To(Equal(rbacv1.RoleRef{
			APIGroup: apiGroupRBAC,
			Kind:     kindClusterRole,
			Name:     clusterRoleGatewayNamespaceReader,
		}))
		Expect(crb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind:      kindServiceAccount,
			Name:      sharedGatewayServiceAccountName,
			Namespace: "gwns-reader-test-ns-create",
		}))
	})

	It("is idempotent: re-running for the same namespace does not duplicate the Subject", func() {
		const bindingName = "gwns-reader-test-idempotent"
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, bindingName, sharedGatewayServiceAccountName, "gwns-reader-test-ns-idem")).To(Succeed())
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, bindingName, sharedGatewayServiceAccountName, "gwns-reader-test-ns-idem")).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName}})
		})

		var crb rbacv1.ClusterRoleBinding
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bindingName}, &crb)).To(Succeed())
		Expect(crb.Subjects).To(HaveLen(1))
	})

	It("appends a second Subject for a different namespace without removing the first", func() {
		const bindingName = "gwns-reader-test-append"
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, bindingName, sharedGatewayServiceAccountName, "gwns-reader-test-ns-append-1")).To(Succeed())
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, bindingName, sharedGatewayServiceAccountName, "gwns-reader-test-ns-append-2")).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName}})
		})

		var crb rbacv1.ClusterRoleBinding
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bindingName}, &crb)).To(Succeed())
		Expect(crb.Subjects).To(ConsistOf(
			rbacv1.Subject{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: "gwns-reader-test-ns-append-1"},
			rbacv1.Subject{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: "gwns-reader-test-ns-append-2"},
		))
	})

	// clusterRoleBindingWebhookGatewayNamespaceReader's production call site is
	// ensureWebhookGateway in trigger_controller.go (added by STORY-056) — see
	// the "ensureWebhookGateway AllNamespaces gateway namespace-reader
	// ClusterRoleBinding" Describe block below for the integration-level
	// coverage of that call site. This test exercises
	// ensureGatewayNamespaceReaderBinding directly with the webhook gateway's
	// own binding/SA name, confirming the helper's genericity over binding/SA
	// name independent of any one caller.
	It("also works for the reserved webhook gateway binding name and SA", func() {
		Expect(ensureGatewayNamespaceReaderBinding(ctx, k8sClient, clusterRoleBindingWebhookGatewayNamespaceReader, webhookGatewayDeploymentName, "gwns-reader-test-ns-webhook")).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingWebhookGatewayNamespaceReader}})
		})

		var crb rbacv1.ClusterRoleBinding
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterRoleBindingWebhookGatewayNamespaceReader}, &crb)).To(Succeed())
		Expect(crb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind:      kindServiceAccount,
			Name:      webhookGatewayDeploymentName,
			Namespace: "gwns-reader-test-ns-webhook",
		}))
	})
})

// See docs/design/allnamespaces-gateway-namespace-read-rbac.md and
// STORY-055's regression-test acceptance criterion.
var _ = Describe("IntegrationReconciler AllNamespaces gateway namespace-reader ClusterRoleBinding", func() {
	const (
		gwnsTestNamespaceModeOff = "gwns-reader-it-ns-mode-off"
		gwnsTestNamespaceFirst   = "gwns-reader-it-ns-first"
		gwnsTestNamespaceSecond  = "gwns-reader-it-ns-second"
	)

	newReconciler := func(allNamespaces bool) *IntegrationReconciler {
		return &IntegrationReconciler{
			Client:            k8sClient,
			Scheme:            scheme.Scheme,
			AllNamespacesMode: allNamespaces,
		}
	}

	newNamespace := func(name string) {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())
	}

	newKafkaIntegration := func(name, ns string) {
		integration := &automationv1alpha1.Integration{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: automationv1alpha1.IntegrationSpec{
				Type:  "kafka",
				Kafka: &automationv1alpha1.KafkaIntegrationSpec{BootstrapServers: []string{"broker:9092"}},
			},
		}
		Expect(k8sClient.Create(ctx, integration)).To(Succeed())
	}

	reconcileIn := func(allNamespaces bool, name, ns string) {
		r := newReconciler(allNamespaces)
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
	}

	getSharedBinding := func() (rbacv1.ClusterRoleBinding, error) {
		var crb rbacv1.ClusterRoleBinding
		err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterRoleBindingGatewayNamespaceReader}, &crb)
		return crb, err
	}

	It("does not create the shared ClusterRoleBinding when AllNamespacesMode is false", func() {
		// Clean slate: the shared binding is not owner-referenced (see the design
		// record's "stale subjects are accepted" tradeoff), so an earlier It in
		// this suite may have left it behind.
		_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingGatewayNamespaceReader}})

		newNamespace(gwnsTestNamespaceModeOff)
		newKafkaIntegration("kafka-gwns-mode-off", gwnsTestNamespaceModeOff)

		reconcileIn(false, "kafka-gwns-mode-off", gwnsTestNamespaceModeOff)

		_, err := getSharedBinding()
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the ClusterRoleBinding must not be touched at all outside AllNamespaces mode")
	})

	It("creates the ClusterRoleBinding on first reconcile, stays idempotent on re-reconcile, and appends (not replaces) a second namespace's subject", func() {
		_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingGatewayNamespaceReader}})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingGatewayNamespaceReader}})
		})

		newNamespace(gwnsTestNamespaceFirst)
		newKafkaIntegration("kafka-gwns-first", gwnsTestNamespaceFirst)

		reconcileIn(true, "kafka-gwns-first", gwnsTestNamespaceFirst)

		crb, err := getSharedBinding()
		Expect(err).NotTo(HaveOccurred())
		Expect(crb.RoleRef).To(Equal(rbacv1.RoleRef{
			APIGroup: apiGroupRBAC,
			Kind:     kindClusterRole,
			Name:     clusterRoleGatewayNamespaceReader,
		}))
		Expect(crb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind:      kindServiceAccount,
			Name:      sharedGatewayServiceAccountName,
			Namespace: gwnsTestNamespaceFirst,
		}))

		// Re-reconciling the same namespace must not duplicate its Subject entry.
		reconcileIn(true, "kafka-gwns-first", gwnsTestNamespaceFirst)
		crb, err = getSharedBinding()
		Expect(err).NotTo(HaveOccurred())
		Expect(crb.Subjects).To(HaveLen(1))

		// A second, different namespace's reconcile must append a second Subject,
		// not remove or replace the first.
		newNamespace(gwnsTestNamespaceSecond)
		newKafkaIntegration("kafka-gwns-second", gwnsTestNamespaceSecond)
		reconcileIn(true, "kafka-gwns-second", gwnsTestNamespaceSecond)

		crb, err = getSharedBinding()
		Expect(err).NotTo(HaveOccurred())
		Expect(crb.Subjects).To(ConsistOf(
			rbacv1.Subject{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: gwnsTestNamespaceFirst},
			rbacv1.Subject{Kind: kindServiceAccount, Name: sharedGatewayServiceAccountName, Namespace: gwnsTestNamespaceSecond},
		))
	})
})

// See STORY-056: ensureWebhookGateway (trigger_controller.go) is the webhook
// gateway's counterpart call site to reconcileKafkaGateway/
// reconcileAmqpGateway/reconcileNatsGateway above, using its own
// ServiceAccount name (webhookGatewayDeploymentName) and its own
// ClusterRoleBinding (clusterRoleBindingWebhookGatewayNamespaceReader) so it
// never shares Subjects with the kafka/amqp/nats gateways' binding.
var _ = Describe("ensureWebhookGateway AllNamespaces gateway namespace-reader ClusterRoleBinding", func() {
	const (
		gwnsWebhookNamespaceModeOff = "gwns-reader-ewg-ns-mode-off"
		gwnsWebhookNamespaceOn      = "gwns-reader-ewg-ns-mode-on"
	)

	newNamespace := func(name string) {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())
	}

	getWebhookBinding := func() (rbacv1.ClusterRoleBinding, error) {
		var crb rbacv1.ClusterRoleBinding
		err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterRoleBindingWebhookGatewayNamespaceReader}, &crb)
		return crb, err
	}

	It("does not create the ClusterRoleBinding when allNamespacesMode is false", func() {
		_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingWebhookGatewayNamespaceReader}})

		newNamespace(gwnsWebhookNamespaceModeOff)

		Expect(ensureWebhookGateway(ctx, k8sClient, gwnsWebhookNamespaceModeOff, false)).To(Succeed())

		_, err := getWebhookBinding()
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the webhook gateway ClusterRoleBinding must not be touched at all outside AllNamespaces mode")
	})

	It("creates the ClusterRoleBinding with the webhook gateway SA as Subject when allNamespacesMode is true, and re-running is idempotent", func() {
		_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingWebhookGatewayNamespaceReader}})
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleBindingWebhookGatewayNamespaceReader}})
		})

		newNamespace(gwnsWebhookNamespaceOn)

		Expect(ensureWebhookGateway(ctx, k8sClient, gwnsWebhookNamespaceOn, true)).To(Succeed())

		crb, err := getWebhookBinding()
		Expect(err).NotTo(HaveOccurred())
		Expect(crb.RoleRef).To(Equal(rbacv1.RoleRef{
			APIGroup: apiGroupRBAC,
			Kind:     kindClusterRole,
			Name:     clusterRoleGatewayNamespaceReader,
		}))
		Expect(crb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind:      kindServiceAccount,
			Name:      webhookGatewayDeploymentName,
			Namespace: gwnsWebhookNamespaceOn,
		}))

		// Re-running ensureWebhookGateway for the same namespace must not
		// duplicate the Subject entry.
		Expect(ensureWebhookGateway(ctx, k8sClient, gwnsWebhookNamespaceOn, true)).To(Succeed())
		crb, err = getWebhookBinding()
		Expect(err).NotTo(HaveOccurred())
		Expect(crb.Subjects).To(HaveLen(1))
	})
})
