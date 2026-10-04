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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
)

// The FlowRun CRD's spec.triggerRef.type enum must accept every Trigger type a
// FlowRun creator can report. Subscriber-role plugins create FlowRuns with
// triggerRef.type "plugin" (docs/api/plugin-contract.md); before this enum
// included it, the API server rejected every plugin-created FlowRun with 422.
var _ = Describe("FlowRun CRD triggerRef.type admission", func() {
	const testNamespace = "default"

	DescribeTable("accepts every Trigger type",
		func(triggerType string) {
			ctx := context.Background()
			fr := &automationv1alpha1.FlowRun{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "triggerref-" + triggerType + "-",
					Namespace:    testNamespace,
				},
				Spec: automationv1alpha1.FlowRunSpec{
					FlowRef:    automationv1alpha1.FlowReference{Name: "some-flow"},
					TriggerRef: &automationv1alpha1.TriggerReference{Name: "some-trigger", Type: triggerType},
				},
			}
			Expect(k8sClient.Create(ctx, fr)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), fr) })
		},
		Entry("webhook", "webhook"),
		Entry("cron", "cron"),
		Entry("kafka", "kafka"),
		Entry("amqp", "amqp"),
		Entry("nats", "nats"),
		Entry("resource", "resource"),
		Entry("plugin", "plugin"),
	)

	It("rejects an unknown trigger type", func() {
		fr := &automationv1alpha1.FlowRun{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "triggerref-bogus-", Namespace: testNamespace},
			Spec: automationv1alpha1.FlowRunSpec{
				FlowRef:    automationv1alpha1.FlowReference{Name: "some-flow"},
				TriggerRef: &automationv1alpha1.TriggerReference{Name: "some-trigger", Type: "bogus"},
			},
		}
		Expect(k8sClient.Create(context.Background(), fr)).NotTo(Succeed())
	})
})
