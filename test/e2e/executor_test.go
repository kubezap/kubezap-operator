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

// Tests for the http-executor RPC path.
// These tests require a running cluster with the operator deployed.
// They are excluded from `make test` (unit) and only run via `make test-e2e`.

package e2e

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

// executorE2ENS is the isolated namespace for the http-executor RPC path scenarios.
const executorE2ENS = "kubezap-e2e-executor"

// executorKubectlApply applies inline YAML into executorE2ENS.
func executorKubectlApply(yamlContent string) {
	tmpFile, err := os.CreateTemp("", "kubezap-executor-e2e-*.yaml")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	_, err = tmpFile.WriteString(yamlContent)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, tmpFile.Close()).To(Succeed())
	DeferCleanup(os.Remove, tmpFile.Name())

	cmd := exec.Command("kubectl", "apply", "-f", tmpFile.Name())
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

// executorKubectlGet runs kubectl get in executorE2ENS and returns stdout.
func executorKubectlGet(args ...string) (string, error) {
	base := append([]string{"get", "-n", executorE2ENS}, args...)
	cmd := exec.Command("kubectl", base...)
	return utils.Run(cmd)
}

// Mockoon echo server: POST /echo -> 200 {"echo": true}.
const executorMockoonConfigMapYAML = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: executor-mockoon-config
  namespace: kubezap-e2e-executor
data:
  environment.json: |
    {
      "uuid": "e2e-executor-mockoon",
      "lastMigration": 32,
      "name": "Executor E2E Mock",
      "port": 3000,
      "hostname": "0.0.0.0",
      "endpointPrefix": "",
      "latency": 0,
      "routes": [
        {
          "uuid": "route-echo",
          "type": "http",
          "documentation": "Echo endpoint for executor E2E",
          "method": "post",
          "endpoint": "echo",
          "responses": [
            {
              "uuid": "resp-echo",
              "body": "{\"echo\":true}",
              "latency": 0,
              "statusCode": 200,
              "headers": [
                { "key": "Content-Type", "value": "application/json" }
              ],
              "label": "success",
              "default": true
            }
          ],
          "responseMode": null
        }
      ],
      "rootChildren": [
        { "type": "route", "uuid": "route-echo" }
      ],
      "proxyMode": false,
      "logging": true,
      "cors": true,
      "tlsOptions": { "enabled": false }
    }
`

const executorMockoonDeploymentYAML = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: executor-mockoon
  namespace: kubezap-e2e-executor
spec:
  replicas: 1
  selector:
    matchLabels:
      app: executor-mockoon
  template:
    metadata:
      labels:
        app: executor-mockoon
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
            name: executor-mockoon-config
`

const executorMockoonServiceYAML = `
apiVersion: v1
kind: Service
metadata:
  name: executor-mockoon
  namespace: kubezap-e2e-executor
spec:
  selector:
    app: executor-mockoon
  ports:
    - name: mock
      port: 3000
      targetPort: 3000
`

// Flow that issues an HTTP step to the in-cluster Mockoon echo server.
// The executor receives the fully-resolved request and returns the response.
const executorHTTPFlowYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: executor-http-flow
  namespace: kubezap-e2e-executor
spec:
  timeout: 120s
  steps:
    - name: call-echo
      action:
        type: http
        http:
          url: "http://executor-mockoon.kubezap-e2e-executor.svc.cluster.local:3000/echo"
          method: POST
          body: '{"source":"executor-e2e"}'
          timeoutSeconds: 30
      results:
        - name: body
`

const executorHTTPTriggerYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: executor-http-trigger
  namespace: kubezap-e2e-executor
spec:
  type: webhook
  enabled: true
  webhook:
    path: /hooks/executor-e2e-http
    method: POST
  flowRef:
    name: executor-http-flow
`

// Flow that issues an HTTP step targeting the IMDS link-local address —
// this should be caught by the executor's SSRF blocklist.
const executorSSRFFlowYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: executor-ssrf-flow
  namespace: kubezap-e2e-executor
spec:
  timeout: 60s
  steps:
    - name: ssrf-attempt
      action:
        type: http
        http:
          url: "http://169.254.169.254/latest/meta-data/"
          method: GET
          timeoutSeconds: 10
`

const executorSSRFTriggerYAML = `
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: executor-ssrf-trigger
  namespace: kubezap-e2e-executor
spec:
  type: webhook
  enabled: true
  webhook:
    path: /hooks/executor-e2e-ssrf
    method: POST
  flowRef:
    name: executor-ssrf-flow
`

var _ = Describe("HTTP executor", Label("executor"), Ordered, func() {
	BeforeAll(func() {
		if os.Getenv("SKIP_EXECUTOR_E2E") == envTrue {
			Skip("SKIP_EXECUTOR_E2E=true; skipping executor E2E scenario")
		}

		By("creating isolated executor e2e namespace")
		cmd := exec.Command("kubectl", "create", "ns", executorE2ENS)
		_, err := utils.Run(cmd)
		if err != nil && strings.Contains(err.Error(), "AlreadyExists") {
			By("namespace already exists, continuing")
		} else {
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", executorE2ENS)
		}

		By("labelling namespace with restricted pod security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", executorE2ENS,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("deploying Mockoon ConfigMap for executor E2E")
		executorKubectlApply(executorMockoonConfigMapYAML)

		By("deploying Mockoon Deployment for executor E2E")
		executorKubectlApply(executorMockoonDeploymentYAML)

		By("deploying Mockoon Service for executor E2E")
		executorKubectlApply(executorMockoonServiceYAML)

		By("waiting for Mockoon to be ready")
		Eventually(func(g Gomega) {
			out, err := executorKubectlGet("deployment", "executor-mockoon",
				"-o", "jsonpath={.status.availableReplicas}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).NotTo(BeEmpty(), "Mockoon deployment not yet available")
			g.Expect(out).NotTo(Equal("0"), "Mockoon has 0 available replicas")
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("applying Flow and Trigger CRs for the HTTP step scenario")
		executorKubectlApply(executorHTTPFlowYAML)
		executorKubectlApply(executorHTTPTriggerYAML)

		By("applying Flow and Trigger CRs for the SSRF scenario")
		executorKubectlApply(executorSSRFFlowYAML)
		executorKubectlApply(executorSSRFTriggerYAML)
	})

	AfterAll(func() {
		for _, pod := range []string{"curl-executor-http", "curl-executor-ssrf"} {
			cmd := exec.Command("kubectl", "delete", "pod", pod,
				"-n", executorE2ENS, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		}

		By("deleting executor e2e namespace")
		cmd := exec.Command("kubectl", "delete", "ns", executorE2ENS, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	Context("HTTP step via executor", func() {
		It("routes an HTTP step through the executor and returns 200", func() {
			By("waiting for the executor-http-trigger to be Accepted")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("trigger", "executor-http-trigger",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"), "Trigger not yet Accepted")
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("waiting for the webhook gateway Deployment to be available")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("deployment", "kubezap-webhook-gateway",
					"-o", "jsonpath={.status.availableReplicas}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty())
				g.Expect(out).NotTo(Equal("0"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the http-executor Deployment to be available")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("deployment", "kubezap-http-executor",
					"-o", "jsonpath={.status.availableReplicas}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty(), "http-executor deployment not yet available")
				g.Expect(out).NotTo(Equal("0"), "http-executor has 0 available replicas")
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("firing the webhook to create a FlowRun")
			curlArgs := fmt.Sprintf(
				"curl -s -o /dev/null -w '%%{http_code}' "+
					"-X POST http://kubezap-webhook-gateway.%s.svc.cluster.local:8080/hooks/executor-e2e-http "+
					"-H 'Content-Type: application/json' -d '{\"source\":\"executor-e2e\"}'",
				executorE2ENS)

			cmd := exec.Command("kubectl", "run", "curl-executor-http",
				"--restart=Never",
				"--namespace", executorE2ENS,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create curl-executor-http pod")

			By("waiting for the curl pod to complete")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("pod", "curl-executor-http",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "curl pod not yet Succeeded; phase: %s", out)
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("asserting a FlowRun was created for executor-http-trigger")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-http-trigger",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "no FlowRun created yet")
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("waiting for the FlowRun to reach phase Succeeded")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-http-trigger",
					"-o", "jsonpath={.items[0].status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "FlowRun phase not Succeeded; current: %s", out)
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the call-echo step has a non-empty result body")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-http-trigger",
					"-o", `jsonpath={.items[0].status.steps[?(@.name=="call-echo")].results[?(@.name=="body")].value}`)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "step result body is empty")
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})

		It("fails an HTTP step that targets an SSRF-blocked IP", func() {
			By("waiting for the executor-ssrf-trigger to be Accepted")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("trigger", "executor-ssrf-trigger",
					"-o", "jsonpath={.status.conditions[?(@.type=='Accepted')].status}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("True"), "SSRF Trigger not yet Accepted")
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("firing the webhook to trigger the SSRF flow")
			curlArgs := fmt.Sprintf(
				"curl -s -o /dev/null -w '%%{http_code}' "+
					"-X POST http://kubezap-webhook-gateway.%s.svc.cluster.local:8080/hooks/executor-e2e-ssrf "+
					"-H 'Content-Type: application/json' -d '{\"source\":\"ssrf-test\"}'",
				executorE2ENS)

			cmd := exec.Command("kubectl", "run", "curl-executor-ssrf",
				"--restart=Never",
				"--namespace", executorE2ENS,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create curl-executor-ssrf pod")

			By("waiting for the curl pod to complete")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("pod", "curl-executor-ssrf",
					"-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Succeeded"), "SSRF curl pod not yet Succeeded; phase: %s", out)
			}, 2*time.Minute, 3*time.Second).Should(Succeed())

			By("asserting a FlowRun was created for executor-ssrf-trigger")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-ssrf-trigger",
					"-o", "jsonpath={.items[*].metadata.name}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty(), "no SSRF FlowRun created yet")
			}, 30*time.Second, 2*time.Second).Should(Succeed())

			By("waiting for the SSRF FlowRun to reach phase Failed")
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-ssrf-trigger",
					"-o", "jsonpath={.items[0].status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Failed"), "SSRF FlowRun phase not Failed; current: %s", out)
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("asserting the step result message reports the SSRF block")
			// The controller's own defence-in-depth pre-check (checkSSRF in
			// internal/controller/flowrun_controller.go) rejects this target before ever
			// calling the executor, so the message comes from that path ("step %q blocked
			// by SSRF protection: %w"), not the executor's "ssrf_blocked:"-prefixed one —
			// that prefix only appears when the *executor* independently catches something
			// the controller's pre-check didn't (e.g. a DNS-rebound address).
			Eventually(func(g Gomega) {
				out, err := executorKubectlGet("flowruns",
					"-l", "kubezap.io/trigger=executor-ssrf-trigger",
					"-o", `jsonpath={.items[0].status.steps[?(@.name=="ssrf-attempt")].message}`)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(ContainSubstring("blocked by SSRF protection"))
				g.Expect(out).To(ContainSubstring("169.254.169.254"))
			}, 30*time.Second, 2*time.Second).Should(Succeed())
		})
	})
})
