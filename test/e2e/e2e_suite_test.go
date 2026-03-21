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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/borfswitch/kubezap/test/utils"
)

// kindKubeconfigFile holds the path to the temp file written with the kind cluster kubeconfig.
// It is set in BeforeSuite and cleaned up in AfterSuite.
var kindKubeconfigFile string

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// These variables are useful if CertManager is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "kubezap/controller:latest"
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purposed to be used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kubezap integration test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

	By("building the webhook gateway image")
	cmd = exec.Command("docker", "build", "-t", "kubezap/webhook-gateway:latest", "-f", "cmd/webhook-gateway/Dockerfile", ".")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the webhook gateway image")

	// TODO(user): If you want to change the e2e test vendor from Kind, ensure the image is
	// built and available before running and also remove the following block.
	By("setting the kind cluster name for image loading")
	Expect(os.Setenv("KIND_CLUSTER", "kubezap-test-e2e")).To(Succeed())

	By("writing kind kubeconfig and setting KUBECONFIG so kubectl targets the kind cluster")
	kindCfgBytes, err := exec.Command("kind", "get", "kubeconfig", "--name", "kubezap-test-e2e").Output()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to get kind kubeconfig")
	tmpKubeconfig, err := os.CreateTemp("", "kubezap-kind-kubeconfig-*.yaml")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	_, err = tmpKubeconfig.Write(kindCfgBytes)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, tmpKubeconfig.Close()).To(Succeed())
	kindKubeconfigFile = tmpKubeconfig.Name()
	Expect(os.Setenv("KUBECONFIG", kindKubeconfigFile)).To(Succeed())

	By("loading the manager(Operator) image on Kind")
	err = utils.LoadImageToKindClusterWithName(projectImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")

	By("loading the webhook gateway image on Kind")
	err = utils.LoadImageToKindClusterWithName("kubezap/webhook-gateway:latest")
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the webhook gateway image into Kind")

	// The tests-e2e are intended to run on a temporary cluster that is created and destroyed for testing.
	// To prevent errors when tests run in environments with CertManager already installed,
	// we check for its presence before execution.
	// Setup CertManager before the suite if not skipped and if not already installed
	if !skipCertManagerInstall {
		By("checking if cert manager is installed already")
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
		}
	}

	By("installing KubeZap CRDs")
	cmd = exec.Command("make", "install")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")

	By("waiting for KubeZap CRDs to be established")
	for _, crd := range []string{
		"flows.automation.kubezap.io",
		"flowruns.automation.kubezap.io",
		"integrations.automation.kubezap.io",
		"mockendpoints.automation.kubezap.io",
		"triggers.automation.kubezap.io",
	} {
		cmd = exec.Command("kubectl", "wait", "crd/"+crd,
			"--for=condition=Established", "--timeout=2m")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "CRD %s not established", crd)
	}

	By("deploying controller manager")
	cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy controller manager")

	By("waiting for controller manager to be running")
	Eventually(func(g Gomega) {
		podPhase, err := utils.Run(exec.Command("kubectl", "get", "pods", "-n", "kubezap-system",
			"-l", "control-plane=controller-manager", "-o", "jsonpath={.items[0].status.phase}"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(podPhase).To(Equal("Running"))
	}, 3*time.Minute, 5*time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	// Teardown controller manager and CRDs
	By("undeploying controller manager")
	cmd := exec.Command("make", "undeploy")
	_, _ = utils.Run(cmd)

	By("uninstalling KubeZap CRDs")
	cmd = exec.Command("make", "uninstall")
	_, _ = utils.Run(cmd)

	// Teardown CertManager after the suite if not skipped and if it was not already installed
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}

	// Restore KUBECONFIG and clean up temp file.
	_ = os.Unsetenv("KUBECONFIG")
	if kindKubeconfigFile != "" {
		_ = os.Remove(kindKubeconfigFile)
	}
})
