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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
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

	It("returns the object when it exists in the namespace", func() {
		created := &automationv1alpha1.WebhookGatewayConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      webhookGatewayConfigName,
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
		Expect(got.Name).To(Equal(webhookGatewayConfigName))
		Expect(got.Spec.HPA.MinReplicas).NotTo(BeNil())
		Expect(*got.Spec.HPA.MinReplicas).To(Equal(int32(4)))
	})

	It("rejects a create with any name other than the required one", func() {
		bad := &automationv1alpha1.WebhookGatewayConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "not-default",
				Namespace: getCfgTestNamespace,
			},
		}
		err := k8sClient.Create(ctx, bad)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("the only valid name for a WebhookGatewayConfig is 'default'"))
	})
})

var _ = Describe("validateWebhookGatewayConfigTLS", func() {
	const validateTLSTestNamespace = "default"

	AfterEach(func() {
		var secrets corev1.SecretList
		Expect(k8sClient.List(ctx, &secrets, client.InNamespace(validateTLSTestNamespace))).To(Succeed())
		for i := range secrets.Items {
			Expect(k8sClient.Delete(ctx, &secrets.Items[i])).To(Succeed())
		}
	})

	newCfg := func(tls *automationv1alpha1.WebhookGatewayTLSSpec) *automationv1alpha1.WebhookGatewayConfig {
		return &automationv1alpha1.WebhookGatewayConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: validateTLSTestNamespace},
			Spec:       automationv1alpha1.WebhookGatewayConfigSpec{TLS: tls},
		}
	}

	It("is Ready/NoTLSConfigured when spec.tls is nil", func() {
		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, newCfg(nil), false)

		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("NoTLSConfigured"))
	})

	It("is not-Ready/ServerSecretNotFound when serverSecretRef names a missing Secret", func() {
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ServerSecretRef: &corev1.LocalObjectReference{Name: "does-not-exist"},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ServerSecretNotFound"))
	})

	It("is not-Ready/ServerSecretMissingKeys when the server Secret lacks tls.crt/tls.key", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "incomplete-server-secret", Namespace: validateTLSTestNamespace},
			Data:       map[string][]byte{"tls.crt": []byte("cert-only")},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ServerSecretRef: &corev1.LocalObjectReference{Name: secret.Name},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ServerSecretMissingKeys"))
	})

	It("is Ready/WebhookGatewayConfigReady when the server Secret has both keys and no CA is configured", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "valid-server-secret", Namespace: validateTLSTestNamespace},
			Data:       map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ServerSecretRef: &corev1.LocalObjectReference{Name: secret.Name},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("WebhookGatewayConfigReady"))
	})

	It("is not-Ready/ClientCASecretNotFound when clientCASecretRef names a missing Secret", func() {
		serverSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "server-secret-for-ca-test", Namespace: validateTLSTestNamespace},
			Data:       map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")},
		}
		Expect(k8sClient.Create(ctx, serverSecret)).To(Succeed())
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ServerSecretRef:   &corev1.LocalObjectReference{Name: serverSecret.Name},
			ClientCASecretRef: &corev1.LocalObjectReference{Name: "does-not-exist"},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ClientCASecretNotFound"))
	})

	It("is Ready/WebhookGatewayConfigReady when both server and CA Secrets are valid", func() {
		serverSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "server-secret-full", Namespace: validateTLSTestNamespace},
			Data:       map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")},
		}
		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca-secret-full", Namespace: validateTLSTestNamespace},
			Data:       map[string][]byte{"ca.crt": []byte("ca-cert")},
		}
		Expect(k8sClient.Create(ctx, serverSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, caSecret)).To(Succeed())
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ServerSecretRef:   &corev1.LocalObjectReference{Name: serverSecret.Name},
			ClientCASecretRef: &corev1.LocalObjectReference{Name: caSecret.Name},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("WebhookGatewayConfigReady"))
	})

	It("does not flag clientCASecretRef set without serverSecretRef, since that combination is a documented no-op", func() {
		cfg := newCfg(&automationv1alpha1.WebhookGatewayTLSSpec{
			ClientCASecretRef: &corev1.LocalObjectReference{Name: "does-not-exist"},
		})

		cond := validateWebhookGatewayConfigTLS(ctx, k8sClient, cfg, false)

		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})
})

var _ = Describe("reconcileWebhookGatewayConfigStatus", func() {
	const reconcileStatusTestNamespace = "default"

	AfterEach(func() {
		var list automationv1alpha1.WebhookGatewayConfigList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(reconcileStatusTestNamespace))).To(Succeed())
		for i := range list.Items {
			Expect(k8sClient.Delete(ctx, &list.Items[i])).To(Succeed())
		}
	})

	It("writes a Ready condition onto the live object's status", func() {
		created := &automationv1alpha1.WebhookGatewayConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      webhookGatewayConfigName,
				Namespace: reconcileStatusTestNamespace,
			},
		}
		Expect(k8sClient.Create(ctx, created)).To(Succeed())

		reconcileWebhookGatewayConfigStatus(ctx, k8sClient, created, false)

		var got automationv1alpha1.WebhookGatewayConfig
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(created), &got)).To(Succeed())
		Expect(got.Status.Conditions).To(HaveLen(1))
		Expect(got.Status.Conditions[0].Type).To(Equal(conditionTypeReady))
		Expect(got.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
		Expect(got.Status.Conditions[0].Reason).To(Equal("NoTLSConfigured"))
		Expect(got.Status.Conditions[0].ObservedGeneration).To(Equal(got.Generation))
	})
})

var _ = Describe("desiredWebhookGatewayDeployment TLS volumes", func() {
	// Regression test: the mounted Secret volumes' DefaultMode must be
	// world-readable (0444), not owner-only (0400). Kubernetes Secret volume
	// files are owned by root regardless of the pod's securityContext; with
	// readOnlyRootFilesystem+runAsNonRoot (this Deployment's actual
	// securityContext) and no fsGroup set, a 0400 mode means the non-root
	// container user cannot read the file at all — verified live on k3s: the
	// webhook-gateway container crash-looped with "permission denied" opening
	// its own mounted TLS cert until this was fixed to 0444.
	It("mounts the server TLS secret with a world-readable DefaultMode", func() {
		dep := desiredWebhookGatewayDeployment("default", WebhookGatewayTLSConfig{
			TLSSecretName: "webhook-gw-tls",
		})

		var found *corev1.Volume
		for i := range dep.Spec.Template.Spec.Volumes {
			if dep.Spec.Template.Spec.Volumes[i].Name == "webhook-tls" {
				found = &dep.Spec.Template.Spec.Volumes[i]
			}
		}
		Expect(found).NotTo(BeNil(), "expected a webhook-tls volume when TLSSecretName is set")
		Expect(found.Secret).NotTo(BeNil())
		Expect(found.Secret.DefaultMode).To(Equal(ptr.To(int32(0444))))
	})

	It("mounts the client CA secret with a world-readable DefaultMode", func() {
		dep := desiredWebhookGatewayDeployment("default", WebhookGatewayTLSConfig{
			TLSSecretName:    "webhook-gw-tls",
			MTLSCASecretName: "webhook-gw-mtls-ca",
		})

		var found *corev1.Volume
		for i := range dep.Spec.Template.Spec.Volumes {
			if dep.Spec.Template.Spec.Volumes[i].Name == "webhook-mtls-ca" {
				found = &dep.Spec.Template.Spec.Volumes[i]
			}
		}
		Expect(found).NotTo(BeNil(), "expected a webhook-mtls-ca volume when MTLSCASecretName is set")
		Expect(found.Secret).NotTo(BeNil())
		Expect(found.Secret.DefaultMode).To(Equal(ptr.To(int32(0444))))
	})
})

// See STORY-056: desiredWebhookGatewayDeployment previously only set
// Env: otelPassthroughEnv() on the webhook gateway container, never
// propagating WATCH_NAMESPACES — unlike the kafka/amqp/nats gateway
// Deployments in integration_controller.go, which all do. This left
// allNamespacesMode always false in cmd/webhook-gateway/main.go, making the
// managed-namespace check in webhook/watcher.go's readSecretKey dead code for
// the webhook gateway specifically.
var _ = Describe("desiredWebhookGatewayDeployment WATCH_NAMESPACES propagation", func() {
	It("propagates the operator's WATCH_NAMESPACES value into the container Env", func() {
		Expect(os.Setenv("WATCH_NAMESPACES", "team-a,team-b")).To(Succeed())
		DeferCleanup(func() {
			Expect(os.Unsetenv("WATCH_NAMESPACES")).To(Succeed())
		})

		dep := desiredWebhookGatewayDeployment("default", WebhookGatewayTLSConfig{})

		var found *corev1.EnvVar
		env := dep.Spec.Template.Spec.Containers[0].Env
		for i := range env {
			if env[i].Name == envVarWatchNamespaces {
				found = &env[i]
			}
		}
		Expect(found).NotTo(BeNil(), "expected a WATCH_NAMESPACES entry in the webhook gateway container Env")
		Expect(found.Value).To(Equal(os.Getenv("WATCH_NAMESPACES")))
		Expect(found.Value).To(Equal("team-a,team-b"))
	})
})
