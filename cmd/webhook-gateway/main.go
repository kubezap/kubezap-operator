package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
	"github.com/yourname/kubezap/internal/gateway/webhook"
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
	var port int
	var namespace string
	var logLevel string

	flag.IntVar(&port, "port", 8080, "HTTP server port")
	flag.StringVar(&namespace, "namespace", "", "Namespace to watch; empty=all namespaces")
	flag.StringVar(&logLevel, "log-level", "info", "Log level: debug|info|warn|error")
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
	logger := zapr.NewLogger(coreLogger).WithName("webhook-gateway")
	ctrl.SetLogger(logger)
	log = logger

	if namespace == "" {
		ns := strings.TrimSpace(os.Getenv("WATCH_NAMESPACES"))
		if ns == "" {
			log.Info("watching all namespaces")
		} else {
			namespace = ns
			log.Info("WATCH_NAMESPACES applied", "namespace", namespace)
		}
	} else {
		log.Info("namespace flag set", "namespace", namespace)
	}

	cfg := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(cfg, client.Options{Scheme: schemeInstance})
	if err != nil {
		log.Error(err, "unable to create Kubernetes client")
		os.Exit(1)
	}

	registry := webhook.NewRouteRegistry(log.WithName("route-registry"))
	watcher, err := webhook.NewTriggerWatcher(k8sClient, registry, namespace, log.WithName("trigger-watcher"))
	if err != nil {
		log.Error(err, "unable to create trigger watcher")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := watcher.Start(ctx); err != nil && err != context.Canceled {
			log.Error(err, "trigger watcher stopped with error")
		}
	}()

	mux := http.NewServeMux()
	handler := webhook.NewWebhookHandler(k8sClient, registry, log.WithName("webhook-handler"))
	mux.Handle("/hooks/", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if registry.IsSynced() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	})

	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	go func() {
		log.Info("starting webhook gateway HTTP server", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(err, "server failed")
			cancel()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received, shutting down gracefully")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "failed to shutdown HTTP server gracefully")
		os.Exit(1)
	}

	cancel()
	log.Info("webhook gateway stopped")
}
