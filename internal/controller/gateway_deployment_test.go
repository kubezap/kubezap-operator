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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("desiredWebhookGatewayRole", func() {
	It("grants get access to secrets, required to resolve webhook auth secretRefs", func() {
		role := desiredWebhookGatewayRole("default")

		hasSecretsGet := false
		for _, rule := range role.Rules {
			if containsString(rule.APIGroups, "") && containsString(rule.Resources, "secrets") && containsString(rule.Verbs, "get") {
				hasSecretsGet = true
			}
		}
		Expect(hasSecretsGet).To(BeTrue(), "webhook gateway Role must grant get on secrets, or hmac/bearer/basic/apiKey/header-equals auth can never resolve their secretRef against a live cluster")
	})
})

var _ = Describe("desiredWebhookGatewayHPA", func() {
	It("is byte-identical to desiredWebhookGatewayHPAFromConfig with a nil config", func() {
		// Proves the pre-existing, no-WebhookGatewayConfig call path is unaffected by
		// adding WebhookGatewayConfig support: desiredWebhookGatewayHPA is now just a
		// thin wrapper, so its output must equal calling the new function with cfg=nil.
		Expect(desiredWebhookGatewayHPA("default")).To(Equal(desiredWebhookGatewayHPAFromConfig("default", nil)))
	})

	It("keeps today's hardcoded defaults: min=1, max=10, target-CPU=70%", func() {
		hpa := desiredWebhookGatewayHPA("default")

		Expect(hpa.Spec.MinReplicas).NotTo(BeNil())
		Expect(*hpa.Spec.MinReplicas).To(Equal(int32(1)))
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(10)))
		Expect(hpa.Spec.Metrics).To(HaveLen(1))
		Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(70)))
	})
})

var _ = Describe("desiredWebhookGatewayHPAFromConfig", func() {
	It("falls back to today's hardcoded defaults when cfg is nil", func() {
		hpa := desiredWebhookGatewayHPAFromConfig("default", nil)

		Expect(*hpa.Spec.MinReplicas).To(Equal(int32(1)))
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(10)))
		Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(70)))
	})

	It("falls back to today's hardcoded defaults when cfg.Spec.HPA is nil", func() {
		cfg := &automationv1alpha1.WebhookGatewayConfig{}

		hpa := desiredWebhookGatewayHPAFromConfig("default", cfg)

		Expect(*hpa.Spec.MinReplicas).To(Equal(int32(1)))
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(10)))
		Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(70)))
	})

	It("overrides only the fields set on cfg.Spec.HPA, defaulting the rest per-field", func() {
		cfg := &automationv1alpha1.WebhookGatewayConfig{
			Spec: automationv1alpha1.WebhookGatewayConfigSpec{
				HPA: &automationv1alpha1.WebhookGatewayHPASpec{
					MinReplicas: ptr.To(int32(3)),
				},
			},
		}

		hpa := desiredWebhookGatewayHPAFromConfig("default", cfg)

		Expect(*hpa.Spec.MinReplicas).To(Equal(int32(3)), "MinReplicas should be overridden")
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(10)), "MaxReplicas left unset on cfg should keep the default")
		Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(70)), "TargetCPUUtilization left unset on cfg should keep the default")
	})

	It("overrides all three fields when all are set on cfg.Spec.HPA", func() {
		cfg := &automationv1alpha1.WebhookGatewayConfig{
			Spec: automationv1alpha1.WebhookGatewayConfigSpec{
				HPA: &automationv1alpha1.WebhookGatewayHPASpec{
					MinReplicas:          ptr.To(int32(2)),
					MaxReplicas:          ptr.To(int32(20)),
					TargetCPUUtilization: ptr.To(int32(60)),
				},
			},
		}

		hpa := desiredWebhookGatewayHPAFromConfig("team-a", cfg)

		Expect(*hpa.Spec.MinReplicas).To(Equal(int32(2)))
		Expect(hpa.Spec.MaxReplicas).To(Equal(int32(20)))
		Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(60)))
	})
})

var _ = Describe("getWebhookGatewayConfig", func() {
	const getCfgTestNamespace = "default"

	AfterEach(func() {
		var list automationv1alpha1.WebhookGatewayConfigList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(getCfgTestNamespace))).To(Succeed())
		for i := range list.Items {
			Expect(k8sClient.Delete(ctx, &list.Items[i])).To(Succeed())
		}
	})

	It("returns nil, nil when no WebhookGatewayConfig exists in the namespace", func() {
		cfg, err := getWebhookGatewayConfig(ctx, k8sClient, "namespace-with-no-config-object")

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(BeNil())
	})

	It("returns the object when exactly one exists in the namespace", func() {
		created := &automationv1alpha1.WebhookGatewayConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gw-cfg-test",
				Namespace: getCfgTestNamespace,
			},
			Spec: automationv1alpha1.WebhookGatewayConfigSpec{
				HPA: &automationv1alpha1.WebhookGatewayHPASpec{MinReplicas: ptr.To(int32(4))},
			},
		}
		Expect(k8sClient.Create(ctx, created)).To(Succeed())

		got, err := getWebhookGatewayConfig(ctx, k8sClient, getCfgTestNamespace)

		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got.Name).To(Equal("gw-cfg-test"))
		Expect(got.Spec.HPA.MinReplicas).NotTo(BeNil())
		Expect(*got.Spec.HPA.MinReplicas).To(Equal(int32(4)))
	})
})
