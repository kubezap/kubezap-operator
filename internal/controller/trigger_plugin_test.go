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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

var _ = Describe("TriggerReconciler type=plugin", func() {
	const ns = "default"

	suffix := func() string { return fmt.Sprintf("%d", GinkgoRandomSeed()) }

	reconcileTrigger := func(name string) (reconcile.Result, *automationv1alpha1.Trigger) {
		r := &TriggerReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		nn := types.NamespacedName{Name: name, Namespace: ns}
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
		Expect(err).NotTo(HaveOccurred())
		var got automationv1alpha1.Trigger
		Expect(k8sClient.Get(ctx, nn, &got)).To(Succeed())
		return res, &got
	}

	cond := func(trg *automationv1alpha1.Trigger, t string) *metav1.Condition {
		for i := range trg.Status.Conditions {
			if trg.Status.Conditions[i].Type == t {
				return &trg.Status.Conditions[i]
			}
		}
		return nil
	}

	createIntegration := func(name, typ string) {
		integ := &automationv1alpha1.Integration{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       automationv1alpha1.IntegrationSpec{Type: typ},
		}
		switch typ {
		case "plugin":
			integ.Spec.Plugin = &automationv1alpha1.PluginIntegrationSpec{Image: "example.com/plugin:v1"}
		case "kafka":
			integ.Spec.Kafka = &automationv1alpha1.KafkaIntegrationSpec{BootstrapServers: []string{"k:9092"}}
		}
		Expect(k8sClient.Create(ctx, integ)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), integ) })
	}

	createTrigger := func(name string, spec automationv1alpha1.TriggerSpec) {
		spec.FlowRef = automationv1alpha1.FlowReference{Name: "example-flow"}
		trg := &automationv1alpha1.Trigger{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: spec}
		Expect(k8sClient.Create(ctx, trg)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), trg) })
	}

	pluginSpec := func(integ string) automationv1alpha1.TriggerSpec {
		return automationv1alpha1.TriggerSpec{
			Type:    "plugin",
			Enabled: true,
			Plugin: &automationv1alpha1.PluginTrigger{
				IntegrationRef: corev1.LocalObjectReference{Name: integ},
				Config:         map[string]string{"queueUrl": "opaque-value"},
			},
		}
	}

	It("is admitted by the CRD and reconciles cleanly against an existing type=plugin Integration", func() {
		integ, name := "plug-ok-"+suffix(), "trg-plug-ok-"+suffix()
		createIntegration(integ, "plugin")
		createTrigger(name, pluginSpec(integ))

		res, got := reconcileTrigger(name)
		Expect(res).To(Equal(reconcile.Result{}))
		c := cond(got, conditionTypePluginIntegrationInvalid)
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal(reasonPluginIntegrationValid))
		Expect(cond(got, conditionTypeReady).Status).To(Equal(metav1.ConditionTrue))
		Expect(got.Spec.Plugin.Config).To(HaveKeyWithValue("queueUrl", "opaque-value"))
	})

	It("creates no gateway or other per-Trigger resources and is idempotent", func() {
		integ, name := "plug-noop-"+suffix(), "trg-plug-noop-"+suffix()
		createIntegration(integ, "plugin")
		createTrigger(name, pluginSpec(integ))

		reconcileTrigger(name)
		_, got := reconcileTrigger(name)
		Expect(cond(got, conditionTypePluginIntegrationInvalid).Status).To(Equal(metav1.ConditionFalse))
		Expect(got.Finalizers).To(BeEmpty())

		var deps appsv1.DeploymentList
		Expect(k8sClient.List(ctx, &deps, client.InNamespace(ns))).To(Succeed())
		for _, d := range deps.Items {
			Expect(d.Name).NotTo(ContainSubstring(name))
		}
	})

	It("surfaces a failure condition and requeues when the Integration is missing", func() {
		name := "trg-plug-missing-" + suffix()
		createTrigger(name, pluginSpec("does-not-exist-"+suffix()))

		res, got := reconcileTrigger(name)
		Expect(res.RequeueAfter).To(Equal(pluginIntegrationRecheckInterval))
		c := cond(got, conditionTypePluginIntegrationInvalid)
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionTrue))
		Expect(c.Reason).To(Equal(reasonPluginIntegrationNotFound))
		Expect(cond(got, conditionTypeReady).Status).To(Equal(metav1.ConditionFalse))
	})

	It("surfaces a failure condition when the Integration exists but is not type=plugin", func() {
		integ, name := "plug-kafka-"+suffix(), "trg-plug-wrong-"+suffix()
		createIntegration(integ, "kafka")
		createTrigger(name, pluginSpec(integ))

		res, got := reconcileTrigger(name)
		Expect(res.RequeueAfter).To(Equal(pluginIntegrationRecheckInterval))
		c := cond(got, conditionTypePluginIntegrationInvalid)
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionTrue))
		Expect(c.Reason).To(Equal(reasonPluginIntegrationWrongType))
		Expect(c.Message).To(ContainSubstring("kafka"))
		Expect(cond(got, conditionTypeReady).Status).To(Equal(metav1.ConditionFalse))
	})

	It("recovers once the missing Integration is created", func() {
		integ, name := "plug-late-"+suffix(), "trg-plug-late-"+suffix()
		createTrigger(name, pluginSpec(integ))
		_, got := reconcileTrigger(name)
		Expect(cond(got, conditionTypePluginIntegrationInvalid).Status).To(Equal(metav1.ConditionTrue))

		createIntegration(integ, "plugin")
		res, got := reconcileTrigger(name)
		Expect(res).To(Equal(reconcile.Result{}))
		Expect(cond(got, conditionTypePluginIntegrationInvalid).Status).To(Equal(metav1.ConditionFalse))
		Expect(cond(got, conditionTypeReady).Status).To(Equal(metav1.ConditionTrue))
	})

	It("surfaces a failure when spec.plugin is omitted", func() {
		name := "trg-plug-nospec-" + suffix()
		createTrigger(name, automationv1alpha1.TriggerSpec{Type: "plugin", Enabled: true})

		_, got := reconcileTrigger(name)
		c := cond(got, conditionTypePluginIntegrationInvalid)
		Expect(c).NotTo(BeNil())
		Expect(c.Reason).To(Equal(reasonPluginSpecMissing))
	})

	It("does not validate a disabled plugin Trigger", func() {
		name := "trg-plug-disabled-" + suffix()
		createTrigger(name, pluginSpec("missing-but-disabled"))
		// spec.enabled is omitempty+default=true; a raw merge patch sets false.
		trg := &automationv1alpha1.Trigger{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
		Expect(k8sClient.Patch(ctx, trg, client.RawPatch(types.MergePatchType, []byte(`{"spec":{"enabled":false}}`)))).To(Succeed())

		res, got := reconcileTrigger(name)
		Expect(res).To(Equal(reconcile.Result{}))
		Expect(cond(got, conditionTypePluginIntegrationInvalid)).To(BeNil())
	})

	Context("regression: other trigger types never get the plugin condition", func() {
		DescribeTable("reconciles unchanged",
			func(typ string, spec automationv1alpha1.TriggerSpec) {
				name := fmt.Sprintf("trg-reg-%s-%s", typ, suffix())
				spec.Type, spec.Enabled = typ, true
				createTrigger(name, spec)

				res, got := reconcileTrigger(name)
				Expect(res).To(Equal(reconcile.Result{}))
				Expect(cond(got, conditionTypePluginIntegrationInvalid)).To(BeNil())
				Expect(cond(got, conditionTypeReady).Status).To(Equal(metav1.ConditionTrue))
			},
			Entry("webhook", "webhook", automationv1alpha1.TriggerSpec{
				Webhook: &automationv1alpha1.WebhookTrigger{Path: "/hook/reg-plugin", Method: "POST"}}),
			Entry("cron", "cron", automationv1alpha1.TriggerSpec{
				Cron: &automationv1alpha1.CronTrigger{Schedule: "*/5 * * * *"}}),
			Entry("kafka", "kafka", automationv1alpha1.TriggerSpec{
				Kafka: &automationv1alpha1.KafkaTrigger{Topic: "t", IntegrationRef: corev1.LocalObjectReference{Name: "k"}}}),
			Entry("amqp", "amqp", automationv1alpha1.TriggerSpec{
				Amqp: &automationv1alpha1.AmqpTrigger{Topic: "t", IntegrationRef: corev1.LocalObjectReference{Name: "a"}}}),
			Entry("nats", "nats", automationv1alpha1.TriggerSpec{
				Nats: &automationv1alpha1.NatsTrigger{Subject: "s", IntegrationRef: corev1.LocalObjectReference{Name: "n"}}}),
			Entry("resource", "resource", automationv1alpha1.TriggerSpec{
				Resource: &automationv1alpha1.ResourceTrigger{APIVersion: "v1", Kind: "Pod"}}),
		)
	})
})
