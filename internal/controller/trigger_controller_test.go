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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("TriggerReconciler", func() {
	const testNamespace = "default"

	// newReconciler returns a TriggerReconciler wired to the envtest client.
	// CronScheduler is intentionally left nil unless a specific test requires it.
	newReconciler := func() *TriggerReconciler {
		return &TriggerReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	}

	// reconcileAndFetch invokes the reconciler then re-fetches the Trigger so
	// callers can assert on the latest persisted status.
	reconcileAndFetch := func(name string) (*automationv1alpha1.Trigger, error) {
		r := newReconciler()
		nn := types.NamespacedName{Name: name, Namespace: testNamespace}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		if err != nil {
			return nil, err
		}
		var updated automationv1alpha1.Trigger
		if fetchErr := k8sClient.Get(ctx, nn, &updated); fetchErr != nil {
			return nil, fetchErr
		}
		return &updated, nil
	}

	// makeTrigger builds a Trigger object without persisting it.
	makeTrigger := func(name string, spec automationv1alpha1.TriggerSpec) *automationv1alpha1.Trigger {
		return &automationv1alpha1.Trigger{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: spec,
		}
	}

	// findCondition returns the named condition from the Trigger status, or nil.
	findCondition := func(trg *automationv1alpha1.Trigger, condType string) *metav1.Condition {
		for i := range trg.Status.Conditions {
			if trg.Status.Conditions[i].Type == condType {
				return &trg.Status.Conditions[i]
			}
		}
		return nil
	}

	// -------------------------------------------------------------------------
	// Accepted condition — enabled Trigger
	// -------------------------------------------------------------------------

	Context("when reconciling an enabled webhook Trigger", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			By("creating an enabled webhook Trigger")
			trigger = makeTrigger(
				fmt.Sprintf("trg-enabled-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/enabled",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("sets the Accepted condition status to True", func() {
			By("invoking the reconciler")
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			By("asserting the Accepted condition is True with reason Enabled")
			cond := findCondition(updated, "Accepted")
			Expect(cond).NotTo(BeNil(), "expected an Accepted condition to be present")
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("Enabled"))
		})

		It("sets status.lastResult to Accepted", func() {
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.LastResult).To(Equal("Accepted"))
		})
	})

	// -------------------------------------------------------------------------
	// Accepted condition — disabled Trigger
	// -------------------------------------------------------------------------

	Context("when reconciling a disabled Trigger", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			By("creating a webhook Trigger and then patching spec.enabled=false")
			// spec.enabled has `+kubebuilder:default=true` and `omitempty`, so the
			// API server will override a Go false zero-value to true.  We must
			// create the object first, then immediately patch it to set the field
			// explicitly to false via a strategic merge patch.
			trigger = makeTrigger(
				fmt.Sprintf("trg-disabled-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type: "webhook",
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/disabled",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())

			// Patch spec.enabled to false — the raw merge patch bypasses omitempty.
			patch := []byte(`{"spec":{"enabled":false}}`)
			Expect(k8sClient.Patch(ctx, trigger, client.RawPatch(types.MergePatchType, patch))).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("sets the Accepted condition to False with reason Disabled", func() {
			By("invoking the reconciler")
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			By("asserting the Accepted condition is False")
			cond := findCondition(updated, "Accepted")
			Expect(cond).NotTo(BeNil(), "expected an Accepted condition to be present")
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("Disabled"))
		})

		It("does not set status.lastResult to Accepted", func() {
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Status.LastResult).NotTo(Equal("Accepted"))
		})
	})

	// -------------------------------------------------------------------------
	// Webhook gateway Deployment created
	// -------------------------------------------------------------------------

	Context("when an enabled webhook Trigger is reconciled", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			By("creating an enabled webhook Trigger")
			trigger = makeTrigger(
				fmt.Sprintf("trg-gw-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/gateway",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("ensures the webhook gateway Deployment exists in the trigger's namespace", func() {
			By("invoking the reconciler")
			_, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			By("fetching the gateway Deployment by its well-known name")
			var deploy appsv1.Deployment
			nn := types.NamespacedName{
				Name:      webhookGatewayDeploymentName,
				Namespace: testNamespace,
			}
			Expect(k8sClient.Get(ctx, nn, &deploy)).To(Succeed())
		})

		It("labels the gateway Deployment with kubezap.io/component=webhook-gateway", func() {
			_, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			By("listing Deployments with the gateway component label")
			var deployList appsv1.DeploymentList
			Expect(k8sClient.List(ctx, &deployList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{"kubezap.io/component": "webhook-gateway"},
			)).To(Succeed())
			Expect(deployList.Items).NotTo(BeEmpty())
		})
	})

	// -------------------------------------------------------------------------
	// lastTriggeredTime NOT set by the reconciler (HIGH bug regression)
	// -------------------------------------------------------------------------

	Context("after reconciling an enabled webhook Trigger", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			By("creating a fresh enabled webhook Trigger with no prior status")
			trigger = makeTrigger(
				fmt.Sprintf("trg-ltt-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/ltt",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("does not populate status.lastTriggeredTime (only set on actual firing)", func() {
			By("invoking the reconciler")
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			By("asserting lastTriggeredTime remains nil — reconciler must not set it")
			Expect(updated.Status.LastTriggeredTime).To(BeNil(),
				"lastTriggeredTime must only be set when the trigger fires, not during reconciliation")
		})
	})

	// -------------------------------------------------------------------------
	// Cron Trigger scheduling
	// -------------------------------------------------------------------------

	Context("when reconciling a cron Trigger with a valid schedule", func() {
		var (
			trigger   *automationv1alpha1.Trigger
			scheduler *CronScheduler
		)

		BeforeEach(func() {
			By("constructing a CronScheduler backed by the envtest client")
			logger := logf.FromContext(ctx)
			scheduler = NewCronScheduler(k8sClient, logger)
			DeferCleanup(func() {
				scheduler.Stop()
			})

			By("creating a cron Trigger with a valid five-field schedule")
			trigger = makeTrigger(
				fmt.Sprintf("trg-cron-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "cron",
					Enabled: true,
					Cron: &automationv1alpha1.CronTrigger{
						Schedule: "*/5 * * * *",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				// Strip any finalizers the reconciler may have added before deleting
				// so that the object is not left in a terminating state between It
				// blocks (both share the same BeforeEach).
				var latest automationv1alpha1.Trigger
				nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
				if err := k8sClient.Get(context.Background(), nn, &latest); err == nil {
					latest.Finalizers = nil
					_ = k8sClient.Update(context.Background(), &latest)
					_ = k8sClient.Delete(context.Background(), &latest)
				}
			})
		})

		It("reconciles without error and sets the Accepted condition to True", func() {
			By("invoking a reconciler with a CronScheduler attached")
			r := &TriggerReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				CronScheduler: scheduler,
			}
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("re-fetching and asserting Accepted condition")
			var updated automationv1alpha1.Trigger
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())

			cond := findCondition(&updated, "Accepted")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("adds the cron finalizer to the Trigger", func() {
			By("invoking a reconciler with a CronScheduler attached")
			r := &TriggerReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				CronScheduler: scheduler,
			}
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("asserting the cron finalizer is present on the Trigger")
			var updated automationv1alpha1.Trigger
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(cronTriggerFinalizer))
		})
	})

	// -------------------------------------------------------------------------
	// Degenerate spec — webhook type with no webhook sub-spec
	// -------------------------------------------------------------------------

	Context("when a webhook Trigger has the webhook sub-spec omitted", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			By("creating a webhook Trigger without a Webhook field")
			trigger = makeTrigger(
				fmt.Sprintf("trg-nowh-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					// spec.webhook is intentionally absent
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("does not panic regardless of whether it returns an error", func() {
			By("invoking the reconciler — a panic is never acceptable")
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			// The reconciler calls ensureWebhookGateway for all enabled webhook
			// Triggers, irrespective of spec.webhook being nil, so this exercises
			// the path where the sub-spec is absent. Returning an error is fine;
			// panicking is not.
			Expect(func() {
				_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			}).NotTo(Panic())
		})
	})

	// -------------------------------------------------------------------------
	// Idempotency — reconciling the same Trigger twice is safe
	// -------------------------------------------------------------------------

	Context("when the same enabled webhook Trigger is reconciled multiple times", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			trigger = makeTrigger(
				fmt.Sprintf("trg-idem-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/idempotent",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("succeeds on the second invocation and preserves the Accepted condition", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}

			By("first reconcile")
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("second reconcile — must be idempotent")
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("asserting the Accepted condition is still True after the second reconcile")
			var updated automationv1alpha1.Trigger
			Expect(k8sClient.Get(ctx, nn, &updated)).To(Succeed())
			cond := findCondition(&updated, "Accepted")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	// -------------------------------------------------------------------------
	// Not-found — reconciling a missing resource is a no-op
	// -------------------------------------------------------------------------

	Context("when the Trigger resource does not exist", func() {
		It("returns no error (IgnoreNotFound behaviour)", func() {
			r := newReconciler()
			nn := types.NamespacedName{
				Name:      "nonexistent-trigger",
				Namespace: testNamespace,
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// -------------------------------------------------------------------------
	// WebhookGatewayConfig — the real reconcile path picks up per-namespace HPA settings
	// -------------------------------------------------------------------------

	Context("when a WebhookGatewayConfig exists in the namespace", func() {
		var trigger *automationv1alpha1.Trigger
		var cfg *automationv1alpha1.WebhookGatewayConfig

		BeforeEach(func() {
			cfg = &automationv1alpha1.WebhookGatewayConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      webhookGatewayConfigName,
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.WebhookGatewayConfigSpec{
					HPA: &automationv1alpha1.WebhookGatewayHPASpec{
						MinReplicas: ptr.To(int32(3)),
						MaxReplicas: ptr.To(int32(15)),
					},
				},
			}
			Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

			trigger = makeTrigger(
				fmt.Sprintf("trg-gwcfg-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/gwcfg",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
				_ = k8sClient.Delete(context.Background(), cfg)
			})
		})

		It("reconciles the webhook gateway HPA using the config's HPA fields, not the hardcoded defaults", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var hpa autoscalingv2.HorizontalPodAutoscaler
			hpaKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, hpaKey, &hpa)).To(Succeed())

			Expect(hpa.Spec.MinReplicas).NotTo(BeNil())
			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(3)), "MinReplicas should come from the WebhookGatewayConfig, not the hardcoded default of 1")
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(15)), "MaxReplicas should come from the WebhookGatewayConfig, not the hardcoded default of 10")
			// TargetCPUUtilization was left unset on the config — should still fall back to the default.
			Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(70)))
		})

		It("does not create a PodDisruptionBudget when podDisruptionBudget is unset on the config", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var pdb policyv1.PodDisruptionBudget
			pdbKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			err = k8sClient.Get(ctx, pdbKey, &pdb)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no PodDisruptionBudget should be created when the config leaves podDisruptionBudget unset — matches today's behavior")
		})
	})

	// -------------------------------------------------------------------------
	// WebhookGatewayConfig — podDisruptionBudget wiring
	// -------------------------------------------------------------------------

	Context("when a WebhookGatewayConfig with podDisruptionBudget.minAvailable exists in the namespace", func() {
		var trigger *automationv1alpha1.Trigger
		var cfg *automationv1alpha1.WebhookGatewayConfig

		BeforeEach(func() {
			minAvailable := intstr.FromInt32(2)
			cfg = &automationv1alpha1.WebhookGatewayConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      webhookGatewayConfigName,
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.WebhookGatewayConfigSpec{
					PodDisruptionBudget: &automationv1alpha1.WebhookGatewayPDBSpec{
						MinAvailable: &minAvailable,
					},
				},
			}
			Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

			trigger = makeTrigger(
				fmt.Sprintf("trg-gwcfg-pdb-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/gwcfg-pdb",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
				_ = k8sClient.Delete(context.Background(), cfg)
			})
		})

		It("reconciles a real PodDisruptionBudget targeting the gateway Deployment's pod selector", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var pdb policyv1.PodDisruptionBudget
			pdbKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, pdbKey, &pdb)).To(Succeed())

			Expect(pdb.Spec.MinAvailable).NotTo(BeNil())
			Expect(*pdb.Spec.MinAvailable).To(Equal(intstr.FromInt32(2)))
			Expect(pdb.Spec.Selector).NotTo(BeNil())
			Expect(pdb.Spec.Selector.MatchLabels).To(Equal(map[string]string{
				"kubezap.io/component": "webhook-gateway",
				"kubezap.io/namespace": testNamespace,
			}))
		})

		It("is idempotent across repeated reconciles", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var pdb policyv1.PodDisruptionBudget
			pdbKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, pdbKey, &pdb)).To(Succeed())
			Expect(*pdb.Spec.MinAvailable).To(Equal(intstr.FromInt32(2)))
		})
	})

	// -------------------------------------------------------------------------
	// WebhookGatewayConfig.spec.tls
	// -------------------------------------------------------------------------

	Context("when a WebhookGatewayConfig with spec.tls exists in the namespace", func() {
		var trigger *automationv1alpha1.Trigger
		var cfg *automationv1alpha1.WebhookGatewayConfig

		BeforeEach(func() {
			cfg = &automationv1alpha1.WebhookGatewayConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      webhookGatewayConfigName,
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.WebhookGatewayConfigSpec{
					TLS: &automationv1alpha1.WebhookGatewayTLSSpec{
						ServerSecretRef:   &corev1.LocalObjectReference{Name: "kubezap-webhook-tls"},
						ClientCASecretRef: &corev1.LocalObjectReference{Name: "webhook-client-ca"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

			trigger = makeTrigger(
				fmt.Sprintf("trg-gwtls-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/gwtls",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
				_ = k8sClient.Delete(context.Background(), cfg)
			})
		})

		It("mounts the TLS and mTLS CA secrets named in spec.tls onto the gateway Deployment", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var deploy appsv1.Deployment
			deployKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, deployKey, &deploy)).To(Succeed())

			var tlsVol, caVol *corev1.Volume
			for i := range deploy.Spec.Template.Spec.Volumes {
				v := &deploy.Spec.Template.Spec.Volumes[i]
				switch v.Name {
				case "webhook-tls":
					tlsVol = v
				case "webhook-mtls-ca":
					caVol = v
				}
			}
			Expect(tlsVol).NotTo(BeNil(), "expected a webhook-tls volume sourced from spec.tls.serverSecretRef")
			Expect(tlsVol.Secret.SecretName).To(Equal("kubezap-webhook-tls"))
			Expect(caVol).NotTo(BeNil(), "expected a webhook-mtls-ca volume sourced from spec.tls.clientCASecretRef")
			Expect(caVol.Secret.SecretName).To(Equal("webhook-client-ca"))

			var svc corev1.Service
			Expect(k8sClient.Get(ctx, deployKey, &svc)).To(Succeed())
			Expect(svc.Spec.Ports[0].Name).To(Equal("https"), "Service port should switch to https once TLS is configured")
		})

		// Regression coverage for STORY-069: crlConfigMapRef is unset here, so
		// wiring it into the Deployment/Role must be a no-op — confirming the
		// RBAC-conditioning choice (grant configmaps access only when a CRL
		// ConfigMap is actually configured) is genuinely safe by default.
		It("does not add --crl-configmap-name or configmaps RBAC when crlConfigMapRef is unset", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var deploy appsv1.Deployment
			deployKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, deployKey, &deploy)).To(Succeed())
			for _, arg := range deploy.Spec.Template.Spec.Containers[0].Args {
				Expect(arg).NotTo(HavePrefix("--crl-configmap-name="), "no --crl-configmap-name arg should be present when spec.tls.crlConfigMapRef is unset")
			}

			var role rbacv1.Role
			roleKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, roleKey, &role)).To(Succeed())
			for _, rule := range role.Rules {
				Expect(containsString(rule.Resources, "configmaps")).To(BeFalse(), "no configmaps RBAC rule should be granted when spec.tls.crlConfigMapRef is unset")
			}
		})
	})

	// -------------------------------------------------------------------------
	// STORY-069: WebhookGatewayConfig.spec.tls.crlConfigMapRef wiring
	// -------------------------------------------------------------------------

	Context("when a WebhookGatewayConfig with spec.tls.crlConfigMapRef exists in the namespace", func() {
		var trigger *automationv1alpha1.Trigger
		var cfg *automationv1alpha1.WebhookGatewayConfig

		BeforeEach(func() {
			cfg = &automationv1alpha1.WebhookGatewayConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      webhookGatewayConfigName,
					Namespace: testNamespace,
				},
				Spec: automationv1alpha1.WebhookGatewayConfigSpec{
					TLS: &automationv1alpha1.WebhookGatewayTLSSpec{
						ServerSecretRef:   &corev1.LocalObjectReference{Name: "kubezap-webhook-tls"},
						ClientCASecretRef: &corev1.LocalObjectReference{Name: "webhook-client-ca"},
						CRLConfigMapRef:   &corev1.LocalObjectReference{Name: "webhook-client-ca-crl"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cfg)).To(Succeed())

			trigger = makeTrigger(
				fmt.Sprintf("trg-gwcrl-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/gwcrl",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
				_ = k8sClient.Delete(context.Background(), cfg)
			})
		})

		It("passes --crl-configmap-name to the gateway Deployment and grants configmaps RBAC on the gateway Role", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var deploy appsv1.Deployment
			deployKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, deployKey, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--crl-configmap-name=webhook-client-ca-crl"))

			var role rbacv1.Role
			roleKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, roleKey, &role)).To(Succeed())

			hasConfigMapsRule := false
			for _, rule := range role.Rules {
				if containsString(rule.APIGroups, "") &&
					containsString(rule.Resources, "configmaps") &&
					containsString(rule.Verbs, "get") &&
					containsString(rule.Verbs, "list") &&
					containsString(rule.Verbs, "watch") {
					hasConfigMapsRule = true
				}
			}
			Expect(hasConfigMapsRule).To(BeTrue(), "webhook gateway Role must grant get;list;watch on configmaps once spec.tls.crlConfigMapRef is set")
		})

		It("is idempotent across repeated reconciles", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var deploy appsv1.Deployment
			deployKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, deployKey, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--crl-configmap-name=webhook-client-ca-crl"))
		})
	})

	Context("when no WebhookGatewayConfig exists in the namespace", func() {
		var trigger *automationv1alpha1.Trigger

		BeforeEach(func() {
			trigger = makeTrigger(
				fmt.Sprintf("trg-notls-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/notls",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("serves plain HTTP with no TLS volumes mounted (absent-config default, not a fallback to Namespace annotations)", func() {
			r := newReconciler()
			nn := types.NamespacedName{Name: trigger.Name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			var deploy appsv1.Deployment
			deployKey := types.NamespacedName{Name: webhookGatewayDeploymentName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, deployKey, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Volumes).To(BeEmpty())

			var svc corev1.Service
			Expect(k8sClient.Get(ctx, deployKey, &svc)).To(Succeed())
			Expect(svc.Spec.Ports[0].Name).To(Equal("http"))
		})
	})

	// -------------------------------------------------------------------------
	// STORY-067: CredentialResolutionFailed condition synced from gateway Events
	// See docs/design/gateway-credential-failure-visibility.md. The
	// kafka/amqp/nats gateway watchers cannot write to Trigger/status, so they
	// signal a credential-resolution failure (and later, a recovery from one)
	// via a Kubernetes Event referencing the Trigger; TriggerReconciler is
	// responsible for translating the most recent such Event into a real
	// status.conditions entry.
	// -------------------------------------------------------------------------

	Context("when a kafka Trigger has a CredentialResolutionFailed Event referencing it", func() {
		var trigger *automationv1alpha1.Trigger

		// makeCredentialEvent builds (but does not persist) a corev1.Event of the
		// shape internal/gateway/kafka/watcher.go's recordCredentialFailure /
		// recordCredentialSuccess emit via record.EventRecorder.Eventf.
		makeCredentialEvent := func(name, reason, eventType, message string, lastTimestamp metav1.Time) *corev1.Event {
			return &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				InvolvedObject: corev1.ObjectReference{
					Kind:       "Trigger",
					Namespace:  testNamespace,
					Name:       trigger.Name,
					APIVersion: "automation.kubezap.io/v1alpha1",
				},
				Reason:         reason,
				Message:        message,
				Type:           eventType,
				FirstTimestamp: lastTimestamp,
				LastTimestamp:  lastTimestamp,
				Count:          1,
				Source:         corev1.EventSource{Component: "kafka-gateway"},
			}
		}

		BeforeEach(func() {
			By("creating an enabled kafka Trigger")
			trigger = makeTrigger(
				fmt.Sprintf("trg-credfail-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "kafka",
					Enabled: true,
					Kafka: &automationv1alpha1.KafkaTrigger{
						Topic:          "orders",
						IntegrationRef: corev1.LocalObjectReference{Name: "some-kafka-integration"},
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), trigger)
			})
		})

		It("has no CredentialResolutionFailed condition before any Event exists", func() {
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(findCondition(updated, "CredentialResolutionFailed")).To(BeNil(),
				"a kafka Trigger that has never hit the credential-resolution path must not get an invented Condition")
		})

		It("sets CredentialResolutionFailed=True once a Warning Event referencing it exists", func() {
			evt := makeCredentialEvent(trigger.Name+".credfail1", "CredentialResolutionFailed",
				corev1.EventTypeWarning, `failed to resolve kafka credentials for integration "some-kafka-integration": secret not found`,
				metav1.Now())
			Expect(k8sClient.Create(ctx, evt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), evt) })

			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())

			cond := findCondition(updated, "CredentialResolutionFailed")
			Expect(cond).NotTo(BeNil(), "expected a CredentialResolutionFailed condition once a matching Event exists")
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("CredentialResolutionFailed"))
			Expect(cond.Message).To(ContainSubstring("secret not found"))
		})

		It("clears (Status=False) once a later CredentialResolutionSucceeded Event exists", func() {
			failedAt := metav1.NewTime(time.Now().Add(-time.Minute))
			failEvt := makeCredentialEvent(trigger.Name+".credfail2", "CredentialResolutionFailed",
				corev1.EventTypeWarning, "failed to resolve kafka credentials for integration \"some-kafka-integration\": secret not found",
				failedAt)
			Expect(k8sClient.Create(ctx, failEvt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), failEvt) })

			By("confirming the condition is True while only the failure Event exists")
			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			cond := findCondition(updated, "CredentialResolutionFailed")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))

			By("simulating the underlying Secret being fixed and reconcileTrigger succeeding")
			succeededAt := metav1.Now() // after failedAt
			succeedEvt := makeCredentialEvent(trigger.Name+".credsucceed1", "CredentialResolutionSucceeded",
				corev1.EventTypeNormal, `resolved kafka credentials for integration "some-kafka-integration"`,
				succeededAt)
			Expect(k8sClient.Create(ctx, succeedEvt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), succeedEvt) })

			updated, err = reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			cond = findCondition(updated, "CredentialResolutionFailed")
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse),
				"the condition must clear once a later CredentialResolutionSucceeded Event is the most recent one")
			Expect(cond.Reason).To(Equal("CredentialResolutionSucceeded"))
		})
	})

	Context("when a webhook Trigger has a stray CredentialResolutionFailed Event referencing it", func() {
		// This mechanism is scoped to kafka/amqp/nats Triggers only — the
		// webhook gateway never emits either reason (docs/design/gateway-credential-failure-visibility.md's
		// Rejected Alternatives: webhook auth failures are a different,
		// high-frequency failure class already covered by access logs/metrics).
		// A stray Event of this shape referencing a webhook Trigger (which
		// should never happen in practice) must still not produce a Condition,
		// since syncCredentialResolutionCondition is gated on trg.Spec.Type.
		It("does not set a CredentialResolutionFailed condition on a webhook Trigger", func() {
			trigger := makeTrigger(
				fmt.Sprintf("trg-webhook-stray-evt-%d", GinkgoRandomSeed()),
				automationv1alpha1.TriggerSpec{
					Type:    "webhook",
					Enabled: true,
					Webhook: &automationv1alpha1.WebhookTrigger{
						Path:   "/hook/stray-evt",
						Method: "POST",
					},
					FlowRef: automationv1alpha1.FlowReference{Name: "example-flow"},
				},
			)
			Expect(k8sClient.Create(ctx, trigger)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), trigger) })

			evt := &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{Name: trigger.Name + ".credfail1", Namespace: testNamespace},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Trigger", Namespace: testNamespace, Name: trigger.Name,
					APIVersion: "automation.kubezap.io/v1alpha1",
				},
				Reason:         "CredentialResolutionFailed",
				Message:        "should never be emitted for a webhook Trigger",
				Type:           corev1.EventTypeWarning,
				FirstTimestamp: metav1.Now(),
				LastTimestamp:  metav1.Now(),
				Count:          1,
				Source:         corev1.EventSource{Component: "webhook-gateway"},
			}
			Expect(k8sClient.Create(ctx, evt)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), evt) })

			updated, err := reconcileAndFetch(trigger.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(findCondition(updated, "CredentialResolutionFailed")).To(BeNil(),
				"webhook Triggers are out of scope for this mechanism regardless of what Events reference them")
		})
	})
})
