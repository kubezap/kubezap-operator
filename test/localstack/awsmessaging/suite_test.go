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

// Package awsmessaging_test holds the LocalStack-backed integration suite for
// the AWS messaging plugin (internal/plugin/awsmessaging). It runs the REAL
// SQS subscriber and SNS publisher against LocalStack (AWS API emulation) and
// an envtest API server loaded with the real CRDs.
//
// The files carry the `localstack` build tag, so `make test` / `go test ./...`
// never compile them. Run with `make test-localstack` or see ../README.md.
package awsmessaging_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/plugin/awsmessaging"
)

const (
	// envEndpoint is the standard AWS SDK endpoint override; the suite is
	// skipped when it is unset so an accidental tagged run never talks to AWS.
	envEndpoint = "AWS_ENDPOINT_URL"

	// LocalStack accepts any credentials; these are its documented defaults.
	defaultRegion    = "us-east-1"
	defaultAccessKey = "test"
	defaultSecretKey = "test"

	// testIntegration is the KUBEZAP_INTEGRATION_NAME the in-process plugin
	// runs as; Triggers select it via spec.plugin.integrationRef.
	testIntegration = "aws-messaging"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	restCfg   *rest.Config
	k8sClient client.Client
	scheme    *runtime.Scheme

	endpointURL string
	region      string
	sqsClient   *sqs.Client
	snsClient   *sns.Client
)

func TestAWSMessagingLocalStack(t *testing.T) {
	if os.Getenv(envEndpoint) == "" {
		t.Skipf("%s is unset; start LocalStack and export %s=http://localhost:4566 "+
			"(see test/localstack/README.md) to run the LocalStack suite", envEndpoint, envEndpoint)
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "AWS Messaging Plugin LocalStack Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.Background())

	endpointURL = strings.TrimRight(os.Getenv(envEndpoint), "/")
	region = envOrDefault("AWS_REGION", defaultRegion)

	By("checking LocalStack is reachable at " + endpointURL)
	Eventually(func() error {
		resp, err := http.Get(endpointURL + "/_localstack/health")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("LocalStack health returned %d", resp.StatusCode)
		}
		return nil
	}).WithTimeout(60*time.Second).WithPolling(time.Second).Should(Succeed(),
		"LocalStack is not reachable at %s; see test/localstack/README.md", endpointURL)

	By("building AWS clients through the plugin's own config path")
	cfg := pluginConfig()
	var err error
	sqsClient, err = awsmessaging.NewSQSClient(ctx, cfg)
	Expect(err).NotTo(HaveOccurred())
	snsClient, err = awsmessaging.NewSNSClient(ctx, cfg)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping envtest with the real CRDs")
	scheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(automationv1alpha1.AddToScheme(scheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := firstEnvtestBinaryDir(); dir != "" && os.Getenv("KUBEBUILDER_ASSETS") == "" {
		testEnv.BinaryAssetsDirectory = dir
	}
	restCfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// pluginConfig builds the plugin Config exactly as the plugin binary does —
// via awsmessaging.LoadConfig over an environment — so AWS_ENDPOINT_URL is
// honoured through the production code path. Namespace is a placeholder; the
// subscriber/publisher take theirs explicitly.
func pluginConfig() awsmessaging.Config {
	env := map[string]string{
		awsmessaging.EnvNamespace:          "localstack",
		awsmessaging.EnvIntegrationName:    testIntegration,
		awsmessaging.EnvAWSRegion:          region,
		awsmessaging.EnvAWSEndpointURL:     endpointURL,
		awsmessaging.EnvAWSAccessKeyID:     envOrDefault("AWS_ACCESS_KEY_ID", defaultAccessKey),
		awsmessaging.EnvAWSSecretAccessKey: envOrDefault("AWS_SECRET_ACCESS_KEY", defaultSecretKey),
	}
	cfg, err := awsmessaging.LoadConfig(func(k string) string { return env[k] })
	Expect(err).NotTo(HaveOccurred())
	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// firstEnvtestBinaryDir finds envtest binaries under <repo>/bin/k8s (installed
// by `make setup-envtest`) for runs that do not set KUBEBUILDER_ASSETS.
func firstEnvtestBinaryDir() string {
	base := filepath.Join("..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(base, e.Name())
		}
	}
	return ""
}

// uniqueName returns a DNS-1123 / SQS / SNS safe name unique to this run.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000_000_000)
}
