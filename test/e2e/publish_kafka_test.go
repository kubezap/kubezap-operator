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

// publish_kafka_test.go — E2E coverage for the publish step → Kafka Integration path.
//
// The entire suite is marked Pending (PDescribe) because it requires an external
// Kafka broker to be reachable from within the Kind cluster. Run these tests in a
// CI environment that provides KAFKA_BOOTSTRAP_SERVERS (e.g. via a Strimzi or
// Redpanda sidecar) by temporarily removing the PDescribe wrapper and adding:
//
//   if os.Getenv("KAFKA_BOOTSTRAP_SERVERS") == "" {
//       Skip("KAFKA_BOOTSTRAP_SERVERS not set")
//   }
//
// Sub-tests 2 and 3 (missing Integration, 4xx body capture) use inline FlowRuns
// that do NOT require a Kafka broker — they test controller-side error handling.
// They are nested inside PDescribe so that they are also skipped by default; move
// them into a separate non-pending Describe if you want them to run in CI without
// a broker.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kubezap/kubezap-operator/test/utils"
)

// publishKafkaE2ENS is the isolated namespace for the publish → Kafka E2E scenario.
// Kept separate from e2eNS and webhookE2ENS to avoid resource name collisions.
const publishKafkaE2ENS = "kubezap-e2e-publish-kafka"

// publishKubectlApply applies a YAML file from testdata into publishKafkaE2ENS.
func publishKubectlApply(filename string) {
	cmd := exec.Command("kubectl", "apply", "-n", publishKafkaE2ENS, "-f",
		fmt.Sprintf("%s/%s", testdataDir(), filename))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "kubectl apply %s in %s", filename, publishKafkaE2ENS)
}

// publishKubectlDelete deletes a YAML file from testdata in publishKafkaE2ENS (best-effort).
func publishKubectlDelete(filename string) {
	cmd := exec.Command("kubectl", "delete", "--ignore-not-found", "-n", publishKafkaE2ENS, "-f",
		fmt.Sprintf("%s/%s", testdataDir(), filename))
	_, _ = utils.Run(cmd)
}

// publishKubectlGet returns stdout of a kubectl get command scoped to publishKafkaE2ENS.
func publishKubectlGet(args ...string) (string, error) {
	base := append([]string{"get", "-n", publishKafkaE2ENS}, args...)
	cmd := exec.Command("kubectl", base...)
	return utils.Run(cmd)
}

// publishApplyInline writes inline YAML to a temp file and applies it into publishKafkaE2ENS.
func publishApplyInline(yamlContent string) {
	tmpFile, err := os.CreateTemp("", "kubezap-publish-e2e-*.yaml")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	_, err = tmpFile.WriteString(yamlContent)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, tmpFile.Close()).To(Succeed())
	DeferCleanup(os.Remove, tmpFile.Name())

	cmd := exec.Command("kubectl", "apply", "-n", publishKafkaE2ENS, "-f", tmpFile.Name())
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// ---------------------------------------------------------------------------
// Inline YAML helpers
// ---------------------------------------------------------------------------

// publishFlowWithMissingIntegration — a Flow whose publish step references an
// Integration that does not exist. Used to validate that the controller surfaces
// a clear error on the FlowRun status without panicking.
const publishFlowMissingIntegYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: e2e-publish-missing-integ
spec:
  timeout: 60s
  steps:
    - name: publish-step
      action:
        type: publish
        publish:
          integrationRef:
            name: does-not-exist
          topic: e2e-publish-topic
          body: '{"test":true}'
`

// publishFlowrun for the missing-integration test — created directly (no Trigger).
const publishFlowRunMissingIntegYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: e2e-publish-missing-integ-run
spec:
  flowRef:
    name: e2e-publish-missing-integ
`

// ---------------------------------------------------------------------------
// PDescribe — entire suite is Pending; requires external Kafka broker
// ---------------------------------------------------------------------------

// NOTE: PDescribe marks the entire block as Pending in Ginkgo v2. All nested
// It() calls will be reported as pending and will not run unless this is changed
// to Describe. See package-level comment for activation instructions.
var _ = PDescribe("publish step -> Kafka Integration E2E", Ordered, func() {
	// ------------------------------------------------------------------
	// Suite setup / teardown
	// ------------------------------------------------------------------

	BeforeAll(func() {
		By("creating isolated publish-kafka e2e namespace")
		cmd := exec.Command("kubectl", "create", "ns", publishKafkaE2ENS)
		_, err := utils.Run(cmd)
		if err != nil && strings.Contains(err.Error(), "AlreadyExists") {
			By("namespace already exists, continuing")
		} else {
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", publishKafkaE2ENS)
		}

		By("labelling namespace with restricted pod security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", publishKafkaE2ENS,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("provisioning MultiNamespace RBAC so the controller can reconcile in publishKafkaE2ENS")
		Expect(utils.ProvisionMultiNamespaceRBAC(publishKafkaE2ENS, "kubezap-system", "kubezap-controller-manager")).
			To(Succeed())
	})

	AfterAll(func() {
		By("deleting publish-kafka e2e namespace")
		cmd := exec.Command("kubectl", "delete", "ns", publishKafkaE2ENS, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	// ------------------------------------------------------------------
	// Test 1: Full path — webhook fires → FlowRun → publish step → Kafka
	//
	// Requires: KAFKA_BOOTSTRAP_SERVERS env var pointing to a Kafka broker
	// reachable from inside the Kind cluster.
	// ------------------------------------------------------------------

	Describe("full path: Trigger -> FlowRun -> publish -> Kafka", Ordered, func() {
		BeforeAll(func() {
			brokers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
			if brokers == "" {
				Skip("KAFKA_BOOTSTRAP_SERVERS not set; skipping full-path publish E2E")
			}

			By("applying Kafka Integration with real broker address")
			kafkaIntegYAML := fmt.Sprintf(`
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: e2e-kafka
spec:
  type: kafka
  kafka:
    brokers:
      - %s
`, brokers)
			publishApplyInline(kafkaIntegYAML)

			By("applying publish Flow (transform + publish steps)")
			publishKubectlApply("publish-flow.yaml")

			By("applying webhook Trigger referencing publish Flow")
			publishKubectlApply("publish-trigger.yaml")
		})

		AfterAll(func() {
			publishKubectlDelete("publish-trigger.yaml")
			publishKubectlDelete("publish-flow.yaml")

			cmd := exec.Command("kubectl", "delete", "integration", "e2e-kafka",
				"-n", publishKafkaE2ENS, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			By("cleaning up any FlowRuns created in this sub-suite")
			cmd = exec.Command("kubectl", "delete", "flowruns",
				"-l", "kubezap.io/trigger=e2e-publish-webhook",
				"-n", publishKafkaE2ENS, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should mark the webhook Trigger as Accepted", func() {
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("trigger", "e2e-publish-webhook",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"), "Trigger not yet Accepted")
			}, 2*time.Minute, 3*time.Second).Should(Succeed())
		})

		It("should deploy a webhook gateway Deployment in the publish namespace", func() {
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("deployment", "kubezap-webhook-gateway",
					"-o", "jsonpath={.status.availableReplicas}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty(), "gateway deployment not yet available")
				g.Expect(out).NotTo(Equal("0"), "gateway has 0 available replicas")
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should create a FlowRun when a POST reaches the webhook path", func() {
			By("sending a POST to /hooks/e2e-publish-test via a curl pod in the cluster")
			curlArgs := fmt.Sprintf(
				"curl -s -o /dev/null -w '%%{http_code}' "+
					"-X POST http://kubezap-webhook-gateway.%s.svc.cluster.local:8080/hooks/e2e-publish-test "+
					"-H 'Content-Type: application/json' -d '{\"source\":\"e2e-publish\"}'",
				publishKafkaE2ENS)

			cmd := exec.Command("kubectl", "run", "curl-publish-e2e",
				"--restart=Never",
				"--namespace", publishKafkaE2ENS,
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
				c := exec.Command("kubectl", "delete", "pod", "curl-publish-e2e",
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(c)
			}()

			By("waiting for curl pod to complete")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("pod", "curl-publish-e2e",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "curl pod not yet Succeeded; current: %s", out)
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("asserting at least one FlowRun exists for e2e-publish-webhook")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-publish-webhook",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "no FlowRun created yet")
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should complete the FlowRun with phase Succeeded when publish step reaches Kafka", func() {
			// Allow generous timeout: FlowRun picks up, executes transform, then publish
			// to Kafka via the embedded sarama producer. Both steps must complete.
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=e2e-publish-webhook",
					"-o", "jsonpath={.items[0].status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "FlowRun phase not yet Succeeded; current: %s", out)
			}, 5*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

	// ------------------------------------------------------------------
	// Test 2: Missing Integration ref → FlowRun fails with clear error
	//
	// This test does NOT require a Kafka broker — it exercises the
	// controller's error path when the integrationRef.name points to
	// an Integration that does not exist.
	// ------------------------------------------------------------------

	Describe("publish step with missing Integration ref", Ordered, func() {
		BeforeAll(func() {
			By("applying Flow with publish step referencing non-existent Integration")
			publishApplyInline(publishFlowMissingIntegYAML)

			By("creating FlowRun that triggers the missing-integration flow")
			publishApplyInline(publishFlowRunMissingIntegYAML)
		})

		AfterAll(func() {
			cmd := exec.Command("kubectl", "delete", "flowrun",
				"e2e-publish-missing-integ-run",
				"-n", publishKafkaE2ENS, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			cmd = exec.Command("kubectl", "delete", "flow",
				"e2e-publish-missing-integ",
				"-n", publishKafkaE2ENS, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should fail the FlowRun and surface an error message about the missing Integration", func() {
			By("waiting for the FlowRun to reach a terminal Failed phase")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowrun", "e2e-publish-missing-integ-run",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Failed"),
					"FlowRun should have failed; current phase: %s", out)
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the FlowRun step status references the missing Integration")
			// The controller records the error in status.stepStatuses[*].message.
			// We check that the message contains the Integration name so operators
			// can diagnose the root cause without inspecting controller logs.
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowrun", "e2e-publish-missing-integ-run",
					"-o", "jsonpath={.status.stepStatuses[*].message}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.ToLower(out)).To(ContainSubstring("does-not-exist"),
					"step message should mention the missing Integration name; got: %s", out)
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// ------------------------------------------------------------------
	// Test 3: Plugin publish endpoint returns 4xx — body snippet captured
	//
	// This test validates the behaviour introduced in PR #93: when the
	// plugin's /publish endpoint returns a 4xx response, the FlowRun step
	// message must include a truncated snippet of the response body so
	// operators can diagnose misconfiguration without reading plugin logs.
	//
	// Infrastructure note: this test uses Mockoon deployed into the
	// publish-kafka namespace (same pod as webhook E2E, different namespace).
	// Mockoon is configured with a POST /publish route that returns 422.
	//
	// The Integration here is of type=plugin and points to the Mockoon
	// service as its plugin publisher — the controller will POST to
	// http://kubezap-plugin-e2e-mock-plugin.<ns>.svc.cluster.local:8090/publish
	// which is served by Mockoon via a headless Service aliased to that name.
	//
	// NOTE: This sub-suite is inside PDescribe and therefore Pending by
	// default. If you activate the outer Describe, this sub-test WILL run
	// without a real Kafka broker (Mockoon is sufficient). Uncomment the
	// mockoon manifests below and the Service alias to enable.
	// ------------------------------------------------------------------

	Describe("publish step returns 4xx — FlowRun message captures response body", Ordered, func() {
		// Mockoon config for this sub-suite: one route POST /publish → 422 Unprocessable Entity.
		const publishMockoon422ConfigYAML = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: mockoon-publish-config
  namespace: kubezap-e2e-publish-kafka
data:
  environment.json: |
    {
      "uuid": "e2e-publish-mockoon",
      "lastMigration": 32,
      "name": "Publish E2E Mock",
      "port": 3000,
      "hostname": "0.0.0.0",
      "endpointPrefix": "",
      "latency": 0,
      "routes": [
        {
          "uuid": "route-publish",
          "type": "http",
          "documentation": "Returns 422 to simulate plugin error",
          "method": "post",
          "endpoint": "publish",
          "responses": [
            {
              "uuid": "resp-422",
              "body": "{\"error\":\"invalid_topic\",\"detail\":\"topic e2e-bad-topic is not allowed\"}",
              "latency": 0,
              "statusCode": 422,
              "headers": [
                { "key": "Content-Type", "value": "application/json" }
              ],
              "label": "422 error",
              "default": true
            }
          ],
          "responseMode": null
        }
      ],
      "rootChildren": [
        { "type": "route", "uuid": "route-publish" }
      ],
      "proxyMode": false,
      "logging": true,
      "cors": true,
      "tlsOptions": { "enabled": false }
    }
`

		// Mockoon Deployment for the publish error test.
		const publishMockoonDeployYAML = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mockoon-publish
  namespace: kubezap-e2e-publish-kafka
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mockoon-publish
  template:
    metadata:
      labels:
        app: mockoon-publish
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 1001
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: mockoon
          image: mockoon/cli:latest
          args:
            - --data
            - /config/environment.json
            - --port
            - "3000"
            - --log-transaction
          ports:
            - containerPort: 3000
              name: mock
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: config
              mountPath: /config
              readOnly: true
          readinessProbe:
            tcpSocket:
              port: 3000
            initialDelaySeconds: 5
            periodSeconds: 5
      volumes:
        - name: config
          configMap:
            name: mockoon-publish-config
`

		// Service that exposes Mockoon on port 8090 under the name the controller
		// will construct for "kubezap-plugin-e2e-mock-plugin": the controller
		// derives the URL as:
		//   http://kubezap-plugin-<integration.Name>.<ns>.svc.cluster.local:<publisherPort>/publish
		// So we create the Service with name kubezap-plugin-e2e-mock-plugin.
		const publishMockoonServiceYAML = `
apiVersion: v1
kind: Service
metadata:
  name: kubezap-plugin-e2e-mock-plugin
  namespace: kubezap-e2e-publish-kafka
spec:
  selector:
    app: mockoon-publish
  ports:
    - name: publisher
      port: 8090
      targetPort: 3000
`

		// Plugin-type Integration that will cause the controller to call Mockoon.
		const publishPluginIntegYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: e2e-mock-plugin
spec:
  type: plugin
  plugin:
    image: mockoon/cli:latest
    publisherPort: 8090
`

		// Flow with a single publish step referencing the mock plugin Integration.
		const publishFlow4xxYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: e2e-publish-4xx-flow
spec:
  timeout: 60s
  steps:
    - name: publish-step
      action:
        type: publish
        publish:
          integrationRef:
            name: e2e-mock-plugin
          topic: e2e-bad-topic
          body: '{"test":true}'
`

		// FlowRun that triggers the 4xx scenario.
		const publishFlowRun4xxYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: e2e-publish-4xx-run
spec:
  flowRef:
    name: e2e-publish-4xx-flow
`

		BeforeAll(func() {
			By("deploying Mockoon ConfigMap for 422 publish error")
			publishApplyInline(publishMockoon422ConfigYAML)

			By("deploying Mockoon Deployment")
			publishApplyInline(publishMockoonDeployYAML)

			By("deploying Mockoon Service aliased as kubezap-plugin-e2e-mock-plugin")
			publishApplyInline(publishMockoonServiceYAML)

			By("waiting for Mockoon publish pod to be ready")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("deployment", "mockoon-publish",
					"-o", "jsonpath={.status.availableReplicas}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty(), "Mockoon publish deployment not yet available")
				g.Expect(out).NotTo(Equal("0"), "Mockoon publish has 0 available replicas")
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("applying plugin Integration pointing to Mockoon")
			publishApplyInline(publishPluginIntegYAML)

			By("applying Flow with publish step that will receive 422")
			publishApplyInline(publishFlow4xxYAML)

			By("creating FlowRun to trigger the 4xx scenario")
			publishApplyInline(publishFlowRun4xxYAML)
		})

		AfterAll(func() {
			for _, name := range []string{"e2e-publish-4xx-run"} {
				cmd := exec.Command("kubectl", "delete", "flowrun", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
			for _, name := range []string{"e2e-publish-4xx-flow"} {
				cmd := exec.Command("kubectl", "delete", "flow", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
			for _, name := range []string{"e2e-mock-plugin"} {
				cmd := exec.Command("kubectl", "delete", "integration", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
			for _, name := range []string{"mockoon-publish"} {
				cmd := exec.Command("kubectl", "delete", "deployment", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
			for _, name := range []string{"mockoon-publish-config"} {
				cmd := exec.Command("kubectl", "delete", "configmap", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
			for _, name := range []string{"kubezap-plugin-e2e-mock-plugin"} {
				cmd := exec.Command("kubectl", "delete", "service", name,
					"-n", publishKafkaE2ENS, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}
		})

		It("should fail the FlowRun and include the 4xx response body in the step message", func() {
			// The controller reads up to 1024 bytes of the response body on a 4xx
			// and surfaces it in the step status message:
			//   "publish endpoint returned status 422: <body snippet>"
			// This validates the body capture introduced in PR #93.

			By("waiting for FlowRun to reach Failed phase")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowrun", "e2e-publish-4xx-run",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Failed"),
					"FlowRun should have Failed due to 422; current phase: %s", out)
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the step message contains the 4xx response body snippet")
			Eventually(func(g Gomega) {
				out, err := publishKubectlGet("flowrun", "e2e-publish-4xx-run",
					"-o", "jsonpath={.status.stepStatuses[*].message}")
				g.Expect(err).NotTo(HaveOccurred())
				// The error message format is: "publish endpoint returned status 422: <body>"
				// Verify the HTTP status code appears in the message.
				g.Expect(out).To(ContainSubstring("422"),
					"step message should include HTTP status 422; got: %s", out)
				// Verify a fragment of the Mockoon JSON error body is present —
				// this confirms the body capture (not just the status code) is working.
				g.Expect(out).To(ContainSubstring("invalid_topic"),
					"step message should include response body snippet; got: %s", out)
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})
	})
})
