// Command aws-messaging-plugin is the KubeZap AWS messaging plugin
// (Integration{type: plugin}). This binary currently implements the SQS
// subscriber role; the SNS publisher role is added in STORY-073.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	crcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automationv1alpha1 "github.com/kubezap/kubezap-operator/api/v1alpha1"
	"github.com/kubezap/kubezap-operator/internal/plugin/awsmessaging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aws-messaging-plugin: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := awsmessaging.LoadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	zc := zap.NewProductionConfig()
	zc.EncoderConfig.TimeKey = "time"
	zc.EncoderConfig.MessageKey = "msg"
	zc.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if lvl, err := zap.ParseAtomicLevel(cfg.LogLevel); err == nil {
		zc.Level = lvl
	}
	zl, err := zc.Build()
	if err != nil {
		return fmt.Errorf("building logger: %w", err)
	}
	defer func() { _ = zl.Sync() }()
	log := zapr.NewLogger(zl).WithName("aws-messaging-plugin")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	scheme := runtime.NewScheme()
	utilruntime.Must(automationv1alpha1.AddToScheme(scheme))

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	k8s, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	// The plugin Role only grants namespaced triggers get/list/watch, so the
	// cache must be restricted to the plugin's own namespace.
	cache, err := crcache.New(restCfg, crcache.Options{
		Scheme:            scheme,
		DefaultNamespaces: map[string]crcache.Config{cfg.Namespace: {}},
	})
	if err != nil {
		return fmt.Errorf("trigger cache: %w", err)
	}

	sqsClient, err := awsmessaging.NewSQSClient(ctx, cfg)
	if err != nil {
		return err
	}

	health := awsmessaging.NewHealth()
	healthSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HealthPort),
		Handler:           health.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("starting health HTTP server", "port", cfg.HealthPort)
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "health HTTP server failed")
		}
	}()

	sub := awsmessaging.NewSubscriber(k8s, sqsClient, health, cfg.Namespace, cfg.IntegrationName,
		awsmessaging.Options{}, log.WithName("subscriber"))

	informer, err := cache.GetInformer(ctx, &automationv1alpha1.Trigger{})
	if err != nil {
		return fmt.Errorf("trigger informer: %w", err)
	}
	if _, err := informer.AddEventHandler(sub.EventHandler(ctx)); err != nil {
		return fmt.Errorf("adding trigger event handler: %w", err)
	}
	go func() {
		if err := cache.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error(err, "trigger cache stopped with error")
		}
	}()
	if !cache.WaitForCacheSync(ctx) {
		return errors.New("timed out waiting for initial Trigger cache sync")
	}
	health.SetStarted()
	log.Info("aws messaging plugin started", "namespace", cfg.Namespace,
		"integration", cfg.IntegrationName, "region", cfg.Region)

	<-ctx.Done()
	log.Info("shutting down")
	sub.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "failed to shut down health server gracefully")
	}
	log.Info("aws messaging plugin stopped")
	return nil
}
