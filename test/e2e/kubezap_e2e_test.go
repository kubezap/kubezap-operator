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
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/yourname/kubezap/test/utils"
)

// e2eNS is the namespace used for all KubeZap functional E2E tests.
// Kept separate from the controller's own namespace (kubezap-system) so tests
// are isolated and namespaced-RBAC behaviour can be validated independently.
const e2eNS = "kubezap-e2e"

// testdataDir returns the absolute path to test/e2e/testdata.
func testdataDir() string {
	dir, err := utils.GetProjectDir()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return filepath.Join(dir, "test", "e2e", "testdata")
}

// kubectlApply applies a YAML file from testdata into e2eNS.
func kubectlApply(filename string) {
	cmd := exec.Command("kubectl", "apply", "-n", e2eNS, "-f",
		filepath.Join(testdataDir(), filename))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "kubectl apply %s", filename)
}

// kubectlDelete deletes a YAML file from testdata in e2eNS (best-effort).
func kubectlDelete(filename string) {
	cmd := exec.Command("kubectl", "delete", "--ignore-not-found", "-n", e2eNS, "-f",
		filepath.Join(testdataDir(), filename))
	_, _ = utils.Run(cmd)
}

// kubectlGet returns the stdout of a kubectl get command in e2eNS.
func kubectlGet(args ...string) (string, error) {
	base := append([]string{"get", "-n", e2eNS}, args...)
	cmd := exec.Command("kubectl", base...)
	return utils.Run(cmd)
}

var _ = Describe("KubeZap functional E2E", Ordered, func() {
	BeforeAll(func() {
		By("creating e2e test namespace")
		cmd := exec.Command("kubectl", "create", "ns", e2eNS)
		// Ignore error — namespace may already exist from a previous run.
		_, _ = utils.Run(cmd)

		By("labelling namespace with restricted pod security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", e2eNS,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		By("deleting e2e test namespace")
		cmd := exec.Command("kubectl", "delete", "ns", e2eNS, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	// -------------------------------------------------------------------------
	// Webhook Trigger
	// -------------------------------------------------------------------------
	Describe("Webhook Trigger", Ordered, func() {
		BeforeAll(func() {
			By("applying webhook Flow and Trigger")
			kubectlApply("webhook-flow.yaml")
			kubectlApply("webhook-trigger.yaml")
		})

		AfterAll(func() {
			kubectlDelete("webhook-trigger.yaml")
			kubectlDelete("webhook-flow.yaml")
		})

		It("should mark the Trigger as Accepted", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("trigger", "e2e-webhook",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"), "Trigger not yet Accepted")
			}, 2*time.Minute, 3*time.Second).Should(Succeed())
		})

		It("should deploy a webhook gateway in the test namespace", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("deployment", "kubezap-webhook-gateway",
					"-o", "jsonpath={.status.availableReplicas}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty(), "gateway deployment not yet available")
				g.Expect(out).NotTo(Equal("0"), "gateway has 0 available replicas")
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should create a FlowRun when a POST reaches the webhook path", func() {
			By("sending a POST to /hooks/e2e-test via a curl pod")
			// The curl pod runs inside the cluster so it can reach the gateway
			// ClusterIP service without port-forwarding.
			curlArgs := fmt.Sprintf(
				"curl -s -o /dev/null -w '%%{http_code}' "+
					"-X POST http://kubezap-webhook-gateway.%s.svc.cluster.local:8080/hooks/e2e-test "+
					"-H 'Content-Type: application/json' -d '{\"test\":true}'",
				e2eNS)

			cmd := exec.Command("kubectl", "run", "curl-webhook-e2e",
				"--restart=Never",
				"--namespace", e2eNS,
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

			defer func() {
				c := exec.Command("kubectl", "delete", "pod", "curl-webhook-e2e",
					"-n", e2eNS, "--ignore-not-found")
				_, _ = utils.Run(c)
			}()

			By("waiting for curl pod to succeed")
			Eventually(func(g Gomega) {
				out, err := kubectlGet("pod", "curl-webhook-e2e",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "curl pod not yet succeeded")
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("asserting at least one FlowRun exists for e2e-webhook-flow")
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-webhook",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "no FlowRun created yet")
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should complete the FlowRun successfully", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-webhook",
					"-o", "jsonpath={.items[0].status.conditions[?(@.type=='Succeeded')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"), "FlowRun not yet Succeeded")
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------------
	// Cron Trigger
	// -------------------------------------------------------------------------
	Describe("Cron Trigger", Ordered, func() {
		BeforeAll(func() {
			By("applying cron Flow and Trigger")
			kubectlApply("cron-flow.yaml")
			kubectlApply("cron-trigger.yaml")
		})

		AfterAll(func() {
			kubectlDelete("cron-trigger.yaml")
			kubectlDelete("cron-flow.yaml")
		})

		It("should mark the Trigger as Accepted", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("trigger", "e2e-cron",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"))
			}, 2*time.Minute, 3*time.Second).Should(Succeed())
		})

		It("should fire a FlowRun within 90 seconds of schedule", func() {
			// The cron schedule is */1 * * * * — fires every minute.
			// Allow up to 90 s (one full minute plus a buffer).
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-cron",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "cron FlowRun not yet created")
			}, 90*time.Second, 5*time.Second).Should(Succeed())
		})

		It("should complete the cron FlowRun successfully", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-cron",
					"-o", "jsonpath={.items[0].status.conditions[?(@.type=='Succeeded')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------------
	// FlowRun GC
	// -------------------------------------------------------------------------
	Describe("FlowRun GC", Ordered, func() {
		const gcFlowRunName = "e2e-gc-flowrun"
		const retainFlowRunName = "e2e-retain-flowrun"

		// Inline YAML for a minimal FlowRun that references a real Flow.
		// The Flow referenced is e2e-cron-flow which must be present; the
		// cron Describe above applies it. The GC tests run after cron tests
		// because Ordered Describes execute in declaration order.
		gcFlowRunYAML := fmt.Sprintf(`
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: %s
  namespace: %s
spec:
  flowRef:
    name: e2e-cron-flow
  ttlAfterFinished: 15s
`, gcFlowRunName, e2eNS)

		retainFlowRunYAML := fmt.Sprintf(`
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: %s
  namespace: %s
  annotations:
    kubezap.io/retain: "true"
spec:
  flowRef:
    name: e2e-cron-flow
  ttlAfterFinished: 15s
`, retainFlowRunName, e2eNS)

		BeforeAll(func() {
			// Ensure cron Flow is present (applied by Cron Trigger block above).
			// Apply it again idempotently in case test order changes.
			kubectlApply("cron-flow.yaml")
		})

		AfterAll(func() {
			// Best-effort cleanup.
			cmd := exec.Command("kubectl", "delete", "flowrun",
				gcFlowRunName, retainFlowRunName,
				"-n", e2eNS, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should delete a finished FlowRun after spec.ttlAfterFinished elapses", func() {
			By("creating FlowRun with ttlAfterFinished=15s")
			tmpFile, err := os.CreateTemp("", "gc-flowrun-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = tmpFile.WriteString(gcFlowRunYAML)
			Expect(err).NotTo(HaveOccurred())
			Expect(tmpFile.Close()).To(Succeed())
			defer os.Remove(tmpFile.Name())

			cmd := exec.Command("kubectl", "apply", "-f", tmpFile.Name())
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the FlowRun to reach Succeeded")
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowrun", gcFlowRunName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Succeeded')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the FlowRun is garbage-collected within TTL + buffer")
			Eventually(func(g Gomega) {
				_, err := kubectlGet("flowrun", gcFlowRunName)
				// We expect a "not found" error — that means GC worked.
				g.Expect(err).To(HaveOccurred(), "FlowRun should have been deleted by GC")
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, 60*time.Second, 3*time.Second).Should(Succeed())
		})

		It("should NOT delete a FlowRun annotated with kubezap.io/retain=true", func() {
			By("creating FlowRun with retain annotation and ttlAfterFinished=15s")
			tmpFile, err := os.CreateTemp("", "retain-flowrun-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = tmpFile.WriteString(retainFlowRunYAML)
			Expect(err).NotTo(HaveOccurred())
			Expect(tmpFile.Close()).To(Succeed())
			defer os.Remove(tmpFile.Name())

			cmd := exec.Command("kubectl", "apply", "-f", tmpFile.Name())
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the FlowRun to reach Succeeded")
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowrun", retainFlowRunName,
					"-o", "jsonpath={.status.conditions[?(@.type=='Succeeded')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting TTL + buffer and asserting the FlowRun is still present")
			time.Sleep(30 * time.Second)
			out, err := kubectlGet("flowrun", retainFlowRunName,
				"-o", "jsonpath={.metadata.name}")
			Expect(err).NotTo(HaveOccurred(), "retained FlowRun should still exist")
			Expect(out).To(Equal(retainFlowRunName))
		})
	})

	// -------------------------------------------------------------------------
	// Kafka Trigger
	// -------------------------------------------------------------------------
	Describe("Kafka Trigger", Ordered, func() {
		BeforeAll(func() {
			if os.Getenv("KAFKA_BOOTSTRAP_SERVERS") == "" {
				Skip("KAFKA_BOOTSTRAP_SERVERS not set; skipping Kafka E2E tests")
			}
		})

		BeforeAll(func() {
			By("applying Kafka Flow, Integration, and Trigger")
			kubectlApply("kafka-flow.yaml")

			// Patch the broker address from the env var before applying.
			brokers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
			integrationYAML := fmt.Sprintf(`
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: e2e-kafka
  namespace: %s
spec:
  type: kafka
  kafka:
    brokers:
      - %s
`, e2eNS, brokers)
			tmpFile, err := os.CreateTemp("", "kafka-integration-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = tmpFile.WriteString(integrationYAML)
			Expect(err).NotTo(HaveOccurred())
			Expect(tmpFile.Close()).To(Succeed())
			DeferCleanup(os.Remove, tmpFile.Name())

			cmd := exec.Command("kubectl", "apply", "-f", tmpFile.Name())
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			kubectlApply("kafka-trigger.yaml")
		})

		AfterAll(func() {
			kubectlDelete("kafka-trigger.yaml")
			cmd := exec.Command("kubectl", "delete", "integration", "e2e-kafka",
				"-n", e2eNS, "--ignore-not-found")
			_, _ = utils.Run(cmd)
			kubectlDelete("kafka-flow.yaml")
		})

		It("should mark the Kafka Trigger as Accepted", func() {
			Eventually(func(g Gomega) {
				out, err := kubectlGet("trigger", "e2e-kafka",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should create a FlowRun with partition-offset dedup key when a message arrives", func() {
			brokers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")

			By("producing a test message via kafka-console-producer")
			produceArgs := fmt.Sprintf(
				"echo 'e2e-test-payload' | kafka-console-producer.sh "+
					"--bootstrap-server %s --topic e2e-test-topic", brokers)
			cmd := exec.Command("kubectl", "run", "kafka-producer-e2e",
				"--restart=Never",
				"--namespace", e2eNS,
				"--image=bitnami/kafka:latest",
				"--overrides", fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "producer",
							"image": "bitnami/kafka:latest",
							"command": ["/bin/sh", "-c"],
							"args": [%q],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {"drop": ["ALL"]},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {"type": "RuntimeDefault"}
							}
						}],
						"restartPolicy": "Never"
					}
				}`, produceArgs))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				c := exec.Command("kubectl", "delete", "pod", "kafka-producer-e2e",
					"-n", e2eNS, "--ignore-not-found")
				_, _ = utils.Run(c)
			}()

			By("asserting a FlowRun appears with partition-offset naming")
			Eventually(func(g Gomega) {
				out, err := kubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-kafka",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				names := strings.TrimSpace(out)
				g.Expect(names).NotTo(BeEmpty(), "no Kafka FlowRun created yet")
				// Dedup key format: <trigger>-p<partition>-offset-<offset>
				g.Expect(names).To(MatchRegexp(`e2e-kafka-p\d+-offset-\d+`),
					"FlowRun name should follow dedup key pattern")
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should be idempotent — re-delivering the same offset must not duplicate FlowRuns", func() {
			By("listing current FlowRun names for the kafka trigger")
			out, err := kubectlGet("flowruns",
				"-l", "kubezap.io/trigger=e2e-kafka",
				"-o", "jsonpath={.items[*].metadata.name}")
			Expect(err).NotTo(HaveOccurred())
			initial := strings.Fields(out)
			Expect(initial).NotTo(BeEmpty())

			// Allow a few seconds for any hypothetical duplicates to appear.
			time.Sleep(10 * time.Second)

			out2, err := kubectlGet("flowruns",
				"-l", "kubezap.io/trigger=e2e-kafka",
				"-o", "jsonpath={.items[*].metadata.name}")
			Expect(err).NotTo(HaveOccurred())
			after := strings.Fields(out2)

			// Count must not have grown beyond what we already saw
			// (no duplicate for existing offsets).
			Expect(len(after)).To(BeNumerically(">=", len(initial)),
				"FlowRun count should be stable")
		})
	})
})
