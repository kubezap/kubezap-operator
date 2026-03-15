package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/yourname/kubezap/api/v1alpha1"
	"github.com/yourname/kubezap/internal/gateway/kafka"
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
	var kubeconfig string

	flag.StringVar(&namespace, "namespace", "", "Namespace to watch; empty=all namespaces")
	flag.StringVar(&logLevel, "log-level", "info", "Log level: debug|info|warn|error")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig; empty=in-cluster")
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

	cfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		log.Error(err, "unable to build Kubernetes REST config")
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: schemeInstance})
	if err != nil {
		log.Error(err, "unable to create Kubernetes client")
		os.Exit(1)
	}

	watcher := kafka.NewWatcher(k8sClient, namespace, log.WithName("watcher"))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info("starting kafka gateway", "namespace", namespace, "logLevel", logLevel)

	go func() {
		if err := watcher.Start(ctx); err != nil && err != context.Canceled {
			log.Error(err, "kafka watcher stopped with error")
		}
	}()

	<-ctx.Done()
	log.Info("kafka gateway stopped")
}

// buildRestConfig returns a *rest.Config from a kubeconfig path or in-cluster defaults.
func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	cfg := ctrl.GetConfigOrDie()
	return cfg, nil
}
