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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kubezap/kubezap-operator/internal/plugin/awsmessaging"
)

var _ = Describe("SNS publisher against LocalStack", func() {
	var srv *httptest.Server

	BeforeEach(func() {
		pub := awsmessaging.NewPublisher(snsClient, "ls-pub", testIntegration, logf.Log.WithName("publisher"))
		// The real POST /publish handler, served exactly as the plugin serves it.
		srv = httptest.NewServer(pub.Mux())
		DeferCleanup(srv.Close)
	})

	It("publishes to a standard topic and the message, attributes and traceparent land in a subscribed queue", func() {
		topicARN, queueURL := subscribedQueue(uniqueName("kz-std"), false)

		status, resp := postPublish(srv.URL, awsmessaging.PublishRequest{
			Integration:    testIntegration,
			Namespace:      "ls-pub",
			Destination:    topicARN,
			Headers:        map[string]string{"x-order-id": "42"},
			Body:           `{"orderId":"42"}`,
			IdempotencyKey: "ignored-on-standard-topics",
		}, testTraceparent)
		Expect(status).To(Equal(http.StatusOK), "response: %v", resp)
		Expect(resp).To(HaveKeyWithValue("messageId", Not(BeEmpty())))

		msgs := receiveUntil(queueURL, func(got []sqstypes.Message) bool { return len(got) >= 1 })
		Expect(msgs).To(HaveLen(1))
		Expect(aws.ToString(msgs[0].Body)).To(Equal(`{"orderId":"42"}`))
		Expect(stringAttrs(msgs[0])).To(And(
			HaveKeyWithValue("x-order-id", "42"),
			HaveKeyWithValue("traceparent", testTraceparent),
		))
	})

	It("publishes to a .fifo topic and deduplicates a repeated idempotencyKey end to end", func() {
		topicARN, queueURL := subscribedQueue(uniqueName("kz-fifo")+".fifo", true)
		req := awsmessaging.PublishRequest{
			Integration:    testIntegration,
			Namespace:      "ls-pub",
			Destination:    topicARN,
			Body:           "first",
			IdempotencyKey: "flowrun-uid-1/step-publish/attempt-key",
		}

		By("publishing the same idempotencyKey twice (a controller retry)")
		for range 2 {
			status, resp := postPublish(srv.URL, req, testTraceparent)
			Expect(status).To(Equal(http.StatusOK), "response: %v", resp)
			Expect(resp).To(HaveKeyWithValue("messageId", Not(BeEmpty())))
		}

		By("publishing a sentinel with a different key in the same message group")
		// FIFO preserves order within a group, so once the sentinel is received
		// any duplicate of "first" would already have been delivered before it.
		req.Body, req.IdempotencyKey = "sentinel", "flowrun-uid-2/step-publish/attempt-key"
		status, resp := postPublish(srv.URL, req, "")
		Expect(status).To(Equal(http.StatusOK), "response: %v", resp)

		msgs := receiveUntil(queueURL, func(got []sqstypes.Message) bool {
			return len(got) > 0 && aws.ToString(got[len(got)-1].Body) == "sentinel"
		})
		bodies := make([]string, 0, len(msgs))
		for _, m := range msgs {
			bodies = append(bodies, aws.ToString(m.Body))
		}
		Expect(bodies).To(Equal([]string{"first", "sentinel"}), "duplicate idempotencyKey was not deduplicated")
		Expect(stringAttrs(msgs[0])).To(HaveKeyWithValue("traceparent", testTraceparent))
	})
})

// subscribedQueue creates an SNS topic and an SQS queue (both FIFO when fifo is
// set), subscribes the queue with raw message delivery (so the SQS body is the
// published message and SNS message attributes become SQS message attributes)
// and returns the topic ARN and queue URL. Resources are deleted after the spec.
func subscribedQueue(name string, fifo bool) (string, string) {
	qAttrs := map[string]string{}
	tAttrs := map[string]string{}
	if fifo {
		qAttrs["FifoQueue"] = "true"
		tAttrs["FifoTopic"] = "true"
	}
	q, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name), Attributes: qAttrs})
	Expect(err).NotTo(HaveOccurred())
	queueURL := aws.ToString(q.QueueUrl)
	DeferCleanup(func() {
		_, _ = sqsClient.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
	})

	qa, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	Expect(err).NotTo(HaveOccurred())
	queueARN := qa.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	Expect(queueARN).NotTo(BeEmpty())

	t, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name), Attributes: tAttrs})
	Expect(err).NotTo(HaveOccurred())
	topicARN := aws.ToString(t.TopicArn)
	DeferCleanup(func() {
		_, _ = snsClient.DeleteTopic(context.Background(), &sns.DeleteTopicInput{TopicArn: aws.String(topicARN)})
	})

	_, err = snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn:              aws.String(topicARN),
		Protocol:              aws.String("sqs"),
		Endpoint:              aws.String(queueARN),
		Attributes:            map[string]string{"RawMessageDelivery": "true"},
		ReturnSubscriptionArn: true,
	})
	Expect(err).NotTo(HaveOccurred())
	return topicARN, queueURL
}

// postPublish POSTs req to the plugin's /publish and returns the status and
// decoded JSON response.
func postPublish(baseURL string, req awsmessaging.PublishRequest, traceparent string) (int, map[string]string) {
	body, err := json.Marshal(req)
	Expect(err).NotTo(HaveOccurred())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/publish", bytes.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	httpReq.Header.Set("Content-Type", "application/json")
	if traceparent != "" {
		httpReq.Header.Set("traceparent", traceparent)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	out := map[string]string{}
	Expect(json.Unmarshal(raw, &out)).To(Succeed(), "non-JSON response: %s", raw)
	return resp.StatusCode, out
}

// receiveUntil polls the queue (deleting what it receives) until done reports
// true for everything received so far, and returns those messages in order.
func receiveUntil(queueURL string, done func([]sqstypes.Message) bool) []sqstypes.Message {
	var got []sqstypes.Message
	Eventually(func(g Gomega) {
		out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              aws.String(queueURL),
			MaxNumberOfMessages:   10,
			WaitTimeSeconds:       1,
			MessageAttributeNames: []string{"All"},
		})
		g.Expect(err).NotTo(HaveOccurred())
		for _, m := range out.Messages {
			got = append(got, m)
			_, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle,
			})
			g.Expect(err).NotTo(HaveOccurred())
		}
		g.Expect(done(got)).To(BeTrue(), "received so far: %d message(s)", len(got))
	}).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
	return got
}

func stringAttrs(m sqstypes.Message) map[string]string {
	out := make(map[string]string, len(m.MessageAttributes))
	for k, v := range m.MessageAttributes {
		out[k] = aws.ToString(v.StringValue)
	}
	return out
}
