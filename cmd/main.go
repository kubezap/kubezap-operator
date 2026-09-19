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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/controller"
	"github.com/kubezap/kubezap-operator/internal/telemetry"
	kubezapwebhook "github.com/kubezap/kubezap-operator/internal/webhook"
	"github.com/kubezap/kubezap-operator/internal/webhookcerts"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// defaultNamespace is the fallback used when POD_NAMESPACE is unset.
const defaultNamespace = "default"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(automationv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var developmentLogging bool
	var tlsOpts []func(*tls.Config)
	var flowRunTTLSucceeded time.Duration
	var flowRunTTLFailed time.Duration
	var maxConcurrentFlowRuns int
	var flowRunExecutionTimeout time.Duration
	var disableCELCache bool
	var celCostLimit int
	var httpStepBlockedCIDRs string
	var ssrfAllowClusterInternal bool
	var executorImage string
	var executorMTLS bool
	var webhookServiceName string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":9090", "The address the metrics endpoint binds to. "+
		"Use :9090 for HTTP (default) or :8443 for HTTPS.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "/tmp/k8s-webhook-server/serving-certs",
		"The directory the operator's self-managed webhook serving certificate is written to and watched from.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&webhookServiceName, "webhook-service-name", "kubezap-webhook-service",
		"Name of the Service fronting the webhook server, used as the serving cert's DNS SAN.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.DurationVar(&flowRunTTLSucceeded, "flowrun-ttl-succeeded", 24*time.Hour, "TTL for succeeded FlowRuns before GC")
	flag.DurationVar(&flowRunTTLFailed, "flowrun-ttl-failed", 72*time.Hour, "TTL for failed FlowRuns before GC")
	flag.IntVar(&maxConcurrentFlowRuns, "max-concurrent-flowruns", 25,
		"Maximum number of FlowRun reconciliations to run concurrently. With one-step-per-reconcile, "+
			"the goroutine is held only for the duration of a single step (one HTTP call), not the entire flow.")
	flag.DurationVar(&flowRunExecutionTimeout, "flowrun-execution-timeout", time.Hour,
		"Maximum time a FlowRun may remain in Running phase before being failed as orphaned (0 = disabled).")
	flag.BoolVar(&disableCELCache, "disable-cel-cache", false,
		"Disable the CEL expression program cache. The cache is unbounded but converges once Flows "+
			"stabilise; disable only when continuously deploying throwaway expressions or for debugging.")
	flag.IntVar(&celCostLimit, "cel-cost-limit", 10000,
		"Maximum CEL evaluation cost budget per 'when' expression. 0 disables the limit. Prevents DoS "+
			"via combinatorially-expensive expressions (e.g. nested comprehensions).")
	flag.StringVar(&httpStepBlockedCIDRs, "http-step-blocked-cidrs", "",
		"Comma-separated list of additional CIDR ranges to block for HTTP step outbound requests "+
			"(added to the default RFC1918/loopback/link-local blocklist).")
	flag.BoolVar(&ssrfAllowClusterInternal, "ssrf-allow-in-cluster", false,
		"Disable SSRF protection for in-cluster service endpoints (.svc.cluster.local) only; other "+
			"targets (including RFC1918 IP literals) remain blocked. For dev/test only — NOT safe in "+
			"production without NetworkPolicy enforcement.")
	flag.StringVar(&executorImage, "executor-image", "ghcr.io/kubezap/http-executor:latest",
		"Container image for the http-executor Deployment managed in each namespace.")
	var executorPort int
	flag.IntVar(&executorPort, "executor-port", int(controller.DefaultExecutorPort),
		"TCP port the http-executor server listens on, and the executor Deployment/Service are configured with. "+
			"If --executor-rpc-base-url is left at its default, its embedded port is derived from this flag "+
			"instead of needing to be updated separately.")
	var executorRPCBaseURL string
	defaultExecutorRPCBaseURL := fmt.Sprintf(
		"http://kubezap-http-executor.%%s.svc.cluster.local:%d", controller.DefaultExecutorPort)
	flag.StringVar(&executorRPCBaseURL, "executor-rpc-base-url", defaultExecutorRPCBaseURL,
		"Base URL format string for the http-executor Service RPC calls; %s is replaced with the target namespace. "+
			"Leave unset when overriding --executor-port — the port is derived automatically in that case.")
	flag.BoolVar(&executorMTLS, "executor-mtls", false,
		"Enable mTLS between controller and http-executor. When true, the controller generates a "+
			"self-signed CA at startup, injects certs into the executor Deployment, and rotates them every 23h.")
	flag.BoolVar(&developmentLogging, "development", false,
		"Enable development logging mode (human-readable, with caller info). Defaults to false for production JSON logging.")
	var opts zap.Options
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	// Apply the --development flag after parsing so it takes effect regardless of --zap-devel.
	if developmentLogging {
		opts.Development = true
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctx := ctrl.SetupSignalHandler()

	operatorNamespace := os.Getenv("POD_NAMESPACE")
	if operatorNamespace == "" {
		operatorNamespace = defaultNamespace
		setupLog.Info("POD_NAMESPACE not set; defaulting to namespace 'default'")
	}

	restCfg := ctrl.GetConfigOrDie()

	// Generate initial mTLS bundle when --executor-mtls=true.
	// The bundle is in-memory (never written to etcd as a Secret with the key material).
	// The controller reconciler writes only the server cert + CA cert to a Secret in each
	// managed namespace; the CA private key and client cert never leave the controller process.
	var initialMTLSBundle *controller.MTLSBundle
	if executorMTLS {
		dnsSANs := []string{
			fmt.Sprintf("kubezap-http-executor.%s.svc.cluster.local", operatorNamespace),
			fmt.Sprintf("kubezap-http-executor.%s.svc", operatorNamespace),
		}
		bundle, err := controller.GenerateMTLSBundle(dnsSANs)
		if err != nil {
			setupLog.Error(err, "failed to generate initial executor mTLS bundle")
			os.Exit(1)
		}
		initialMTLSBundle = bundle
		setupLog.Info("executor mTLS enabled", "expiresAt", bundle.ExpiresAt)
	}

	shutdownTracing, err := telemetry.InitTracerProvider(ctx, "kubezap-controller")
	if err != nil {
		setupLog.Error(err, "failed to initialize tracing")
		os.Exit(1)
	}
	defer shutdownTracing()

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	{
		// Self-provision the webhook server's TLS cert: generate (or rotate) a
		// self-signed CA + serving cert, store it in a Secret in this namespace,
		// write it to webhookCertPath, and patch the CA into the
		// ValidatingWebhookConfiguration's caBundle. Uses an uncached client since
		// the manager's cache isn't running yet. See
		// docs/design/self-managed-webhook-certs.md.
		bootstrapClient, err := client.New(restCfg, client.Options{Scheme: scheme})
		if err != nil {
			setupLog.Error(err, "unable to create bootstrap client for webhook cert provisioning")
			os.Exit(1)
		}
		webhookCertOpts := webhookCertOptions(
			operatorNamespace, webhookServiceName, webhookCertPath, webhookCertName, webhookCertKey,
		)
		if err := webhookcerts.Ensure(ctx, bootstrapClient, webhookCertOpts); err != nil {
			setupLog.Error(err, "unable to ensure webhook serving certificate")
			os.Exit(1)
		}

		setupLog.Info("Initializing webhook certificate watcher",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// To use cert-manager for the metrics server, uncomment the following in config/:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	// WATCH_NAMESPACES controls operator scope (least-privilege by default):
	//   ""           → OwnNamespace: only the operator's own namespace (default)
	//   "*"          → AllNamespaces: cluster-wide watch; secret reads are gated at
	//                  the application layer (not via RBAC, which cannot express a
	//                  per-namespace-label restriction) to namespaces labeled
	//                  kubezap.io/managed=true — see
	//                  docs/design/allnamespaces-secrets-label-restriction.md
	//   comma-list   → MultiNamespace
	cacheOpts := cache.Options{}
	watchNS := os.Getenv("WATCH_NAMESPACES")
	allNamespacesMode := watchNS == "*"
	switch watchNS {
	case "*":
		setupLog.Info("AllNamespaces mode: watching all namespaces")
	case "":
		// Default: OwnNamespace — watch only the operator's own namespace.
		cacheOpts.DefaultNamespaces = map[string]cache.Config{operatorNamespace: {}}
		setupLog.Info("OwnNamespace mode: restricting watch to operator namespace", "namespace", operatorNamespace)
	default:
		ns := map[string]cache.Config{}
		for _, n := range strings.Split(watchNS, ",") {
			if n = strings.TrimSpace(n); n != "" {
				ns[n] = cache.Config{}
			}
		}
		cacheOpts.DefaultNamespaces = ns
		setupLog.Info("MultiNamespace mode: restricting watch to namespaces", "namespaces", watchNS)
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "f94ed2f5.kubezap.io",
		Cache:                  cacheOpts,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	cronScheduler := controller.NewCronScheduler(mgr.GetClient(), ctrl.Log.WithName("cron-scheduler"))
	defer cronScheduler.Stop()

	dynClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create discovery client")
		os.Exit(1)
	}
	resourceWatcher := controller.NewResourceWatcher(
		mgr.GetClient(), dynClient, discoveryClient, ctrl.Log.WithName("resource-watcher"))
	if err := mgr.Add(resourceWatcher); err != nil {
		setupLog.Error(err, "unable to add ResourceWatcher to manager")
		os.Exit(1)
	}

	if err = (&controller.TriggerReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		CronScheduler:     cronScheduler,
		ResourceWatcher:   resourceWatcher,
		AllNamespacesMode: allNamespacesMode,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Trigger")
		os.Exit(1)
	}
	ssrfBlockedCIDRs, err := controller.ParseCIDRList(httpStepBlockedCIDRs)
	if err != nil {
		setupLog.Error(err, "invalid --http-step-blocked-cidrs flag")
		os.Exit(1)
	}
	// If --executor-rpc-base-url was left at its default and --executor-port was
	// changed, derive the RPC URL's port from --executor-port instead of requiring
	// both flags to be kept in sync by hand. An explicit --executor-rpc-base-url
	// override always wins as-is, port included.
	rpcBaseURL := executorRPCBaseURL
	if rpcBaseURL == defaultExecutorRPCBaseURL && executorPort != int(controller.DefaultExecutorPort) {
		rpcBaseURL = fmt.Sprintf("http://kubezap-http-executor.%%s.svc.cluster.local:%d", executorPort)
	}
	// When mTLS is enabled, switch the executor RPC base URL to https.
	var executorTLSConfig *tls.Config
	if executorMTLS && initialMTLSBundle != nil {
		rpcBaseURL = strings.ReplaceAll(rpcBaseURL, "http://", "https://")
		executorTLSConfig = initialMTLSBundle.ClientTLSConfig()
	}

	flowRunReconciler := &controller.FlowRunReconciler{
		Client:                   mgr.GetClient(),
		Scheme:                   mgr.GetScheme(),
		TTLSucceeded:             flowRunTTLSucceeded,
		TTLFailed:                flowRunTTLFailed,
		MaxConcurrentReconciles:  maxConcurrentFlowRuns,
		ExecutionTimeout:         flowRunExecutionTimeout,
		DisableCELCache:          disableCELCache,
		CELCostLimit:             celCostLimit,
		SSRFBlockedCIDRs:         ssrfBlockedCIDRs,
		SSRFAllowClusterInternal: ssrfAllowClusterInternal,
		ExecutorBaseURL:          rpcBaseURL,
		ExecutorTLSConfig:        executorTLSConfig,
		AllNamespacesMode:        allNamespacesMode,
	}
	if err = flowRunReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FlowRun")
		os.Exit(1)
	}
	if err = (&controller.FlowReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Flow")
		os.Exit(1)
	}
	if err = (&controller.IntegrationReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Integration")
		os.Exit(1)
	}
	executorReconciler := &controller.ExecutorReconciler{
		Client:                   mgr.GetClient(),
		Scheme:                   mgr.GetScheme(),
		ExecutorImage:            executorImage,
		ExecutorPort:             int32(executorPort),
		MTLSEnabled:              executorMTLS,
		MTLSBundle:               initialMTLSBundle,
		SSRFAllowClusterInternal: ssrfAllowClusterInternal,
		OperatorNamespace:        operatorNamespace,
	}
	if err = executorReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Executor")
		os.Exit(1)
	}

	// Start mTLS rotation goroutine (23h rotation, 1h before expiry).
	if executorMTLS && initialMTLSBundle != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			dnsSANs := []string{
				fmt.Sprintf("kubezap-http-executor.%s.svc.cluster.local", operatorNamespace),
				fmt.Sprintf("kubezap-http-executor.%s.svc", operatorNamespace),
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !executorReconciler.MTLSBundle.NeedsRotation() {
						continue
					}
					newBundle, err := controller.GenerateMTLSBundle(dnsSANs)
					if err != nil {
						setupLog.Error(err, "failed to rotate executor mTLS bundle")
						continue
					}
					executorReconciler.MTLSBundle = newBundle
					flowRunReconciler.ExecutorTLSConfig = newBundle.ClientTLSConfig()
					setupLog.Info("executor mTLS bundle rotated", "expiresAt", newBundle.ExpiresAt)
				}
			}
		}()
	}
	// +kubebuilder:scaffold:builder

	// Register admission webhooks.
	if err := kubezapwebhook.SetupFlowRunWebhook(mgr); err != nil {
		setupLog.Error(err, "unable to set up FlowRun validating webhook")
		os.Exit(1)
	}
	if err := kubezapwebhook.SetupTriggerWebhook(mgr); err != nil {
		setupLog.Error(err, "unable to set up Trigger validating webhook")
		os.Exit(1)
	}

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	setupLog.Info("Adding webhook certificate watcher to manager")
	if err := mgr.Add(webhookCertWatcher); err != nil {
		setupLog.Error(err, "unable to add webhook certificate watcher to manager")
		os.Exit(1)
	}

	if err := mgr.Add(&webhookcerts.Rotator{
		Client:  mgr.GetClient(),
		Options: webhookCertOptions(operatorNamespace, webhookServiceName, webhookCertPath, webhookCertName, webhookCertKey),
	}); err != nil {
		setupLog.Error(err, "unable to add webhook certificate rotator to manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// webhookConfigurationName is the ValidatingWebhookConfiguration name generated
// by config/webhook/manifests.yaml (kustomize applies namePrefix: kubezap- only
// to namespaced resources, not this cluster-scoped one, so the name is unprefixed).
const webhookConfigurationName = "validating-webhook-configuration"

// webhookCertOptions builds the webhookcerts.Options shared by the bootstrap
// Ensure call and the manager-registered Rotator, so the two never drift apart.
func webhookCertOptions(namespace, serviceName, certDir, certName, certKey string) webhookcerts.Options {
	return webhookcerts.Options{
		Namespace:                namespace,
		SecretName:               "kubezap-webhook-server-cert",
		ServiceName:              serviceName,
		WebhookConfigurationName: webhookConfigurationName,
		CertDir:                  certDir,
		CertFileName:             certName,
		KeyFileName:              certKey,
	}
}
