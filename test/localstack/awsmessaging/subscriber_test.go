//go:build localstack

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

package awsmessaging_test

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/plugin/awsmessaging"
)

const (
	testFlow        = "orders-flow"
	testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	// firstDeliveryAnnotation marks a FlowRun the test itself created to
	// simulate a consumer that crashed between create and DeleteMessage.
	firstDeliveryAnnotation = "localstack.test.kubezap.io/first-delivery"
)

var _ = Describe("SQS subscriber against LocalStack", func() {
	var (
		namespace string
		queueURL  string
	)

	BeforeEach(func() {
		namespace = uniqueName("ls-sub")
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).To(Succeed())

		out, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(uniqueName("kz-sub"))})
		Expect(err).NotTo(HaveOccurred())
		queueURL = aws.ToString(out.QueueUrl)
		DeferCleanup(func() {
			_, _ = sqsClient.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		})
	})

	It("creates exactly one FlowRun per SQS message and deletes the message", func() {
		triggerName := uniqueName("orders")
		startPlugin(namespace)
		createPluginTrigger(namespace, triggerName, queueURL)

		By("sending a message with attributes (incl. traceparent) to the queue")
		sent, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    aws.String(queueURL),
			MessageBody: aws.String(`{"orderId":"42"}`),
			MessageAttributes: map[string]sqstypes.MessageAttributeValue{
				"x-order-id":  {DataType: aws.String("String"), StringValue: aws.String("42")},
				"traceparent": {DataType: aws.String("String"), StringValue: aws.String(testTraceparent)},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		msgID := aws.ToString(sent.MessageId)
		expectedName := awsmessaging.FlowRunName(triggerName, msgID)

		By("waiting for FlowRun " + expectedName)
		fr := &automationv1alpha1.FlowRun{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: expectedName}, fr)
		}).WithTimeout(30 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())

		Expect(fr.Name).To(Equal(triggerName + "-msg-" + msgID))
		Expect(fr.Labels).To(HaveKeyWithValue("kubezap.io/trigger", triggerName))
		Expect(fr.Labels).To(HaveKeyWithValue("kubezap.io/trigger-type", "plugin"))
		Expect(fr.Labels).To(HaveKeyWithValue("kubezap.io/flow", testFlow))
		Expect(fr.Annotations).To(HaveKeyWithValue("kubezap.io/traceparent", testTraceparent))
		Expect(fr.Spec.FlowRef.Name).To(Equal(testFlow))
		Expect(fr.Spec.TriggerRef).NotTo(BeNil())
		Expect(fr.Spec.TriggerRef.Name).To(Equal(triggerName))
		Expect(fr.Spec.TriggerRef.Type).To(Equal("plugin"))
		Expect(fr.Spec.TriggerData).NotTo(BeNil())
		Expect(fr.Spec.TriggerData.Source).To(Equal("plugin"))
		Expect(fr.Spec.TriggerData.Body).To(Equal(`{"orderId":"42"}`))
		Expect(fr.Spec.TriggerData.BodyEncoding).To(Equal("utf8"))
		Expect(fr.Spec.TriggerData.Headers).To(HaveKeyWithValue("x-order-id", "42"))
		Expect(fr.Spec.TriggerData.Headers).To(HaveKeyWithValue("traceparent", testTraceparent))

		By("asserting the SQS message was deleted (not merely in flight)")
		expectQueueDrained(queueURL)

		By("asserting exactly one FlowRun exists for the message")
		expectOnlyFlowRun(namespace, expectedName)
	})

	It("treats a visibility-timeout redelivery of the same MessageId as 409-is-success", func() {
		triggerName := uniqueName("redeliver")

		By("sending a message before any plugin is subscribed")
		sent, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    aws.String(queueURL),
			MessageBody: aws.String("redelivery-body"),
		})
		Expect(err).NotTo(HaveOccurred())
		msgID := aws.ToString(sent.MessageId)
		expectedName := awsmessaging.FlowRunName(triggerName, msgID)

		By("simulating a first consumer that created the FlowRun and crashed before DeleteMessage")
		var first sqstypes.Message
		Eventually(func(g Gomega) {
			out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:              aws.String(queueURL),
				MaxNumberOfMessages:   1,
				WaitTimeSeconds:       1,
				VisibilityTimeout:     2, // expires quickly -> SQS redelivers
				MessageAttributeNames: []string{"All"},
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out.Messages).To(HaveLen(1))
			first = out.Messages[0]
		}).WithTimeout(15 * time.Second).Should(Succeed())
		Expect(aws.ToString(first.MessageId)).To(Equal(msgID))

		// Same builder the subscriber uses, so the name is the real dedup key.
		pre := awsmessaging.BuildFlowRun(namespace, triggerName, testFlow, &first)
		Expect(pre.Name).To(Equal(expectedName))
		pre.Annotations = map[string]string{firstDeliveryAnnotation: "true"}
		Expect(k8sClient.Create(ctx, pre)).To(Succeed())
		preUID := pre.UID
		// Deliberately no DeleteMessage: the message comes back after 2s.

		By("starting the real subscriber, which receives the redelivered message")
		startPlugin(namespace)
		createPluginTrigger(namespace, triggerName, queueURL)

		By("asserting the redelivered message was deleted after the 409 AlreadyExists")
		// Only the subscriber can drain the queue here: the test never deletes,
		// and the subscriber deletes only after Create returned success or 409.
		expectQueueDrained(queueURL)

		By("asserting still exactly one FlowRun, the original object untouched")
		expectOnlyFlowRun(namespace, expectedName)
		got := &automationv1alpha1.FlowRun{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: expectedName}, got)).To(Succeed())
		Expect(got.UID).To(Equal(preUID))
		Expect(got.Annotations).To(HaveKeyWithValue(firstDeliveryAnnotation, "true"))
	})
})

// startPlugin runs the real awsmessaging.Subscriber in-process, wired the same
// way cmd/aws-messaging-plugin does it: a namespace-scoped controller-runtime
// cache whose Trigger informer feeds Subscriber.EventHandler, and a direct
// client for FlowRun creates. It is stopped when the current spec ends.
func startPlugin(namespace string) {
	pluginCtx, stop := context.WithCancel(ctx)

	cache, err := crcache.New(restCfg, crcache.Options{
		Scheme:            scheme,
		DefaultNamespaces: map[string]crcache.Config{namespace: {}},
	})
	Expect(err).NotTo(HaveOccurred())

	// Short long-poll and backoff keep the suite fast and shutdown prompt;
	// the polling logic itself is the production code.
	sub := awsmessaging.NewSubscriber(k8sClient, sqsClient, nil, namespace, testIntegration,
		awsmessaging.Options{WaitTimeSeconds: 1, RetryBase: 200 * time.Millisecond, RetryMax: 2 * time.Second},
		logf.Log.WithName("subscriber").WithValues("ns", namespace))

	informer, err := cache.GetInformer(pluginCtx, &automationv1alpha1.Trigger{})
	Expect(err).NotTo(HaveOccurred())
	_, err = informer.AddEventHandler(sub.EventHandler(pluginCtx))
	Expect(err).NotTo(HaveOccurred())

	cacheDone := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(cacheDone)
		if err := cache.Start(pluginCtx); err != nil && !errors.Is(err, context.Canceled) {
			Fail("trigger cache stopped with error: " + err.Error())
		}
	}()
	Expect(cache.WaitForCacheSync(pluginCtx)).To(BeTrue())

	DeferCleanup(func() {
		stop()
		sub.Stop()
		<-cacheDone
	})
}

func createPluginTrigger(namespace, name, queueURL string) {
	Expect(k8sClient.Create(ctx, &automationv1alpha1.Trigger{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: automationv1alpha1.TriggerSpec{
			Type:    "plugin",
			Enabled: true,
			FlowRef: automationv1alpha1.FlowReference{Name: testFlow},
			Plugin: &automationv1alpha1.PluginTrigger{
				IntegrationRef: corev1.LocalObjectReference{Name: testIntegration},
				Config:         map[string]string{awsmessaging.TriggerConfigQueueURL: queueURL},
			},
		},
	})).To(Succeed())
}

// expectQueueDrained waits until the queue holds no visible AND no in-flight
// messages. An undeleted message would stay in flight until its visibility
// timeout and then become visible again, so both counters must reach zero.
func expectQueueDrained(queueURL string) {
	Eventually(func(g Gomega) {
		out, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL),
			AttributeNames: []sqstypes.QueueAttributeName{
				sqstypes.QueueAttributeNameApproximateNumberOfMessages,
				sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
			},
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(out.Attributes).To(HaveKeyWithValue(
			string(sqstypes.QueueAttributeNameApproximateNumberOfMessages), "0"))
		g.Expect(out.Attributes).To(HaveKeyWithValue(
			string(sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible), "0"))
	}).WithTimeout(30 * time.Second).WithPolling(250 * time.Millisecond).Should(Succeed())
}

func expectOnlyFlowRun(namespace, name string) {
	list := &automationv1alpha1.FlowRunList{}
	Expect(k8sClient.List(ctx, list, client.InNamespace(namespace))).To(Succeed())
	Expect(list.Items).To(HaveLen(1))
	Expect(list.Items[0].Name).To(Equal(name))
}
