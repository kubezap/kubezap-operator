package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/gateway/kafka"
)

var (
	schemeInstance = runtime.NewScheme()
	log            logr.Logger
)

func init() {
	utilruntime.Must(scheme.AddToScheme(schemeInstance))
	utilruntime.Must(automationv1alpha1.AddToScheme(schemeInstance))
}

func main() {
	var namespace string
	var logLevel string
	var metricsPort int
	var metricsTLSCertFile string
	var metricsTLSKeyFile string
	var healthPort int

	flag.StringVar(&namespace, "namespace", "", "Namespace to watch; empty=all namespaces")
	flag.StringVar(&logLevel, "log-level", "info", "Log level: debug|info|warn|error")
	flag.IntVar(&metricsPort, "metrics-port", 9090, "Port for the dedicated Prometheus metrics server")
	flag.StringVar(&metricsTLSCertFile, "metrics-tls-cert-file", "",
		"Path to TLS certificate PEM for the metrics server. When set with --metrics-tls-key-file "+
			"the metrics server uses HTTPS.")
	flag.StringVar(&metricsTLSKeyFile, "metrics-tls-key-file", "",
		"Path to TLS private key PEM for the metrics server. Required when --metrics-tls-cert-file is set.")
	flag.IntVar(&healthPort, "health-port", 8090,
		"Port for the liveness/readiness /healthz endpoint. Must match the Deployment's probe port "+
			"(internal/controller/integration_controller.go).")
	flag.Parse()

	opts := zap.NewDevelopmentConfig()
	if level, err := zap.ParseAtomicLevel(logLevel); err == nil {
		opts.Level = level
	} else {
		opts.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	coreLogger, err := opts.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to build zap logger: %v\n", err)
		os.Exit(1)
	}
	logger := zapr.NewLogger(coreLogger).WithName("kafka-gateway")
	ctrl.SetLogger(logger)
	log = logger

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "unable to build Kubernetes REST config")
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: schemeInstance})
	if err != nil {
		log.Error(err, "unable to create Kubernetes client")
		os.Exit(1)
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(ctrlmetrics.Registry, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{Addr: fmt.Sprintf(":%d", metricsPort), Handler: metricsMux}
	go func() {
		if metricsTLSCertFile != "" && metricsTLSKeyFile != "" {
			log.Info("starting metrics HTTPS server", "port", metricsPort)
			if err := metricsSrv.ListenAndServeTLS(metricsTLSCertFile, metricsTLSKeyFile); err != nil &&
				err != http.ErrServerClosed {
				log.Error(err, "metrics HTTPS server failed")
			}
		} else {
			log.Info("starting metrics HTTP server", "port", metricsPort)
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error(err, "metrics HTTP server failed")
			}
		}
	}()

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	healthSrv := &http.Server{Addr: fmt.Sprintf(":%d", healthPort), Handler: healthMux}
	go func() {
		log.Info("starting health HTTP server", "port", healthPort)
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(err, "health HTTP server failed")
		}
	}()

	// WATCH_NAMESPACES=* (AllNamespaces mode) is not supported — see
	// docs/design/namespace-scoped-watch-modes-only.md.
	if os.Getenv("WATCH_NAMESPACES") == "*" {
		log.Error(fmt.Errorf("WATCH_NAMESPACES=* is not supported"),
			"AllNamespaces mode has been removed; set WATCH_NAMESPACES to an explicit "+
				"comma-separated namespace list, or leave it unset for OwnNamespace mode")
		os.Exit(1)
	}

	watcher, err := kafka.NewWatcher(k8sClient, cfg, namespace, log.WithName("watcher"))
	if err != nil {
		log.Error(err, "unable to create kafka watcher")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info("starting kafka gateway", "namespace", namespace, "logLevel", logLevel)

	go func() {
		if err := watcher.Start(ctx); err != nil && err != context.Canceled {
			log.Error(err, "kafka watcher stopped with error")
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "failed to shutdown metrics server gracefully")
	}
	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "failed to shutdown health server gracefully")
	}

	log.Info("kafka gateway stopped")
}
