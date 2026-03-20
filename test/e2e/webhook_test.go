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

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/borfswitch/kubezap/test/utils"
)

// webhookE2ENS is the isolated namespace for the webhook→transform→http→MockEndpoint scenario.
const webhookE2ENS = "kubezap-e2e-webhook"

// webhookKubectlApply applies inline YAML (passed as a string) into webhookE2ENS.
func webhookKubectlApply(yamlContent string) {
	tmpFile, err := os.CreateTemp("", "kubezap-webhook-e2e-*.yaml")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	_, err = tmpFile.WriteString(yamlContent)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, tmpFile.Close()).To(Succeed())
	DeferCleanup(os.Remove, tmpFile.Name())

	cmd := exec.Command("kubectl", "apply", "-f", tmpFile.Name())
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// webhookKubectlGet runs kubectl get in webhookE2ENS and returns stdout.
func webhookKubectlGet(args ...string) (string, error) {
	base := append([]string{"get", "-n", webhookE2ENS}, args...)
	cmd := exec.Command("kubectl", base...)
	return utils.Run(cmd)
}

// Inline CR YAML for the self-contained webhook E2E scenario.
// The Flow has two steps:
//   - transform: produces a fixed JSON payload
//   - http: POSTs that payload to the MockEndpoint path on the webhook gateway
//
// The MockEndpoint records the incoming request so we can verify it.

const mockEndpointYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: MockEndpoint
metadata:
  name: test-mock
  namespace: kubezap-e2e-webhook
spec:
  path: /test-target
  response:
    statusCode: 200
    body: '{"ok":true}'
  maxRequestHistory: 20
`

const webhookFlowYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: test-flow
  namespace: kubezap-e2e-webhook
spec:
  timeout: 120s
  steps:
    - name: transform
      action:
        type: transform
        transform:
          mappings:
            forwarded: "true"
            source: "$(trigger.body)"
    - name: notify
      runAfter:
        - transform
      action:
        type: http
        http:
          url: "http://kubezap-webhook-gateway.kubezap-e2e-webhook.svc.cluster.local:8080/mock/test-target"
          method: POST
          body: '{"forwarded":true}'
          timeoutSeconds: 30
`

const webhookTriggerYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: test-webhook
  namespace: kubezap-e2e-webhook
spec:
  type: webhook
  enabled: true
  webhook:
    path: /hooks/e2e-webhook-test
    method: POST
  flowRef:
    name: test-flow
`

var _ = Describe("Webhook Trigger → Transform → HTTP → MockEndpoint", Ordered, func() {
	BeforeAll(func() {
		if os.Getenv("SKIP_WEBHOOK_E2E") == "true" {
			Skip("SKIP_WEBHOOK_E2E=true; skipping webhook E2E scenario")
		}

		By("creating isolated webhook e2e namespace")
		cmd := exec.Command("kubectl", "create", "ns", webhookE2ENS)
		_, err := utils.Run(cmd)
		if err != nil && strings.Contains(err.Error(), "AlreadyExists") {
			By("namespace already exists, continuing")
		} else {
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", webhookE2ENS)
		}

		By("labelling namespace with restricted pod security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", webhookE2ENS,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("applying MockEndpoint CR")
		webhookKubectlApply(mockEndpointYAML)

		By("applying Flow CR with transform and http steps")
		webhookKubectlApply(webhookFlowYAML)

		By("applying Trigger CR of type webhook")
		webhookKubectlApply(webhookTriggerYAML)
	})

	AfterAll(func() {
		By("deleting curl pod if present")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-webhook-e2e-full",
			"-n", webhookE2ENS, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting webhook e2e namespace")
		cmd = exec.Command("kubectl", "delete", "ns", webhookE2ENS, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	It("should mark the Trigger as Accepted", func() {
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("trigger", "test-webhook",
				"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"), "Trigger not yet Accepted")
		}, 2*time.Minute, 3*time.Second).Should(Succeed())
	})

	It("should deploy a webhook gateway Deployment in the test namespace", func() {
		// The controller creates kubezap-webhook-gateway in the same namespace as the Trigger.
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("deployment", "kubezap-webhook-gateway",
				"-o", "jsonpath={.status.availableReplicas}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).NotTo(BeEmpty(), "gateway deployment not yet available")
			g.Expect(out).NotTo(Equal("0"), "gateway has 0 available replicas")
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should mark the MockEndpoint as Ready", func() {
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("mockendpoint", "test-mock",
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"), "MockEndpoint not yet Ready")
		}, 2*time.Minute, 3*time.Second).Should(Succeed())
	})

	It("should create a FlowRun when a POST reaches the webhook path", func() {
		By("sending a POST to /hooks/e2e-webhook-test via a curl pod in the cluster")
		curlArgs := fmt.Sprintf(
			"curl -s -o /dev/null -w '%%{http_code}' "+
				"-X POST http://kubezap-webhook-gateway.%s.svc.cluster.local:8080/hooks/e2e-webhook-test "+
				"-H 'Content-Type: application/json' -d '{\"source\":\"e2e\"}'",
			webhookE2ENS)

		cmd := exec.Command("kubectl", "run", "curl-webhook-e2e-full",
			"--restart=Never",
			"--namespace", webhookE2ENS,
			"--image=curlimages/curl:latest",
			"--overrides", fmt.Sprintf(`{
				"spec": {
					"containers": [{
						"name": "curl",
						"image": "curlimages/curl:latest",
						"command": ["/bin/sh", "-c"],
						"args": [%q],
						"securityContext": {
							"allowPrivilegeEscalation": false,
							"capabilities": {"drop": ["ALL"]},
							"runAsNonRoot": true,
							"runAsUser": 65532,
							"seccompProfile": {"type": "RuntimeDefault"}
						}
					}],
					"restartPolicy": "Never"
				}
			}`, curlArgs))
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "failed to create curl pod")

		By("waiting for curl pod to complete")
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("pod", "curl-webhook-e2e-full",
				"-o", "jsonpath={.status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("Succeeded"), "curl pod not yet Succeeded; current phase: %s", out)
		}, 2*time.Minute, 3*time.Second).Should(Succeed())

		By("asserting at least one FlowRun exists for test-webhook trigger")
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("flowruns",
				"-l", "kubezap.io/trigger=test-webhook",
				"-o", "jsonpath={.items[*].metadata.name}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "no FlowRun created yet for test-webhook")
		}, 30*time.Second, 2*time.Second).Should(Succeed())
	})

	It("should complete the FlowRun with phase Succeeded", func() {
		// Allow generous timeout: FlowRun picks up, executes transform, then issues the
		// HTTP step to the MockEndpoint. Both steps must complete.
		Eventually(func(g Gomega) {
			// Check the status.phase field directly (FlowRunStatus.Phase).
			out, err := webhookKubectlGet("flowruns",
				"-l", "kubezap.io/trigger=test-webhook",
				"-o", "jsonpath={.items[0].status.phase}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("Succeeded"), "FlowRun phase not yet Succeeded; current: %s", out)
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("should capture the request in the MockEndpoint status", func() {
		// Verify the MockEndpoint recorded at least one request — confirming the
		// http step successfully POSTed to the mock.
		Eventually(func(g Gomega) {
			out, err := webhookKubectlGet("mockendpoint", "test-mock",
				"-o", "jsonpath={.status.requestCount}")
			g.Expect(err).NotTo(HaveOccurred())
			count := strings.TrimSpace(out)
			g.Expect(count).NotTo(BeEmpty(), "no requestCount set yet on MockEndpoint status")
			g.Expect(count).NotTo(Equal("0"), "MockEndpoint has not captured any requests yet")
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	})
})
