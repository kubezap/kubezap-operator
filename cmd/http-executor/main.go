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

// Package main is the entry point for the kubezap/http-executor binary.
//
// The http-executor makes outbound HTTP calls on behalf of the KubeZap
// controller. It accepts fully-resolved requests (secrets already substituted)
// via POST /execute, performs an independent SSRF check, executes the call,
// and returns the result. It holds no RBAC, no Kubernetes credentials, and
// makes no Kubernetes API calls.
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

	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	executorhttp "github.com/kubezap/kubezap-operator/internal/executor/http"
)

func main() {
	var port int
	var blockedCIDRs string
	var bodyLimitBytes int
	var allowTLSSkipVerify bool
	var mtls bool
	var logLevel string

	flag.IntVar(&port, "port", 8091, "Port to listen on")
	flag.StringVar(&blockedCIDRs, "blocked-cidrs", "", "Comma-separated extra CIDRs to block in addition to defaults (e.g. '203.0.113.0/24')")
	flag.IntVar(&bodyLimitBytes, "body-limit-bytes", 4096, "Maximum upstream response body size in bytes; larger bodies are truncated")
	flag.BoolVar(&allowTLSSkipVerify, "allow-tls-skip-verify", false, "Allow callers to request TLS verification skip via tlsSkipVerify:true in the request; off by default")
	flag.BoolVar(&mtls, "mtls", false, "Enable mTLS (stub — implemented in chunk [5/6]); requires --tls-cert-file, --tls-key-file, --tls-ca-file")
	flag.StringVar(&logLevel, "log-level", "info", "Log level: debug|info|warn|error")
	flag.Parse()

	// Build logger.
	opts := zap.NewProductionConfig()
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
	defer func() { _ = coreLogger.Sync() }()
	log := zapr.NewLogger(coreLogger).WithName("http-executor")

	if mtls {
		log.Info("WARNING: --mtls=true is set but mTLS support is not yet implemented (stub for chunk [5/6]); the flag is accepted but ignored")
	}

	// Parse SSRF blocklist.
	cidrs, err := executorhttp.ParseCIDRList(blockedCIDRs)
	if err != nil {
		log.Error(err, "invalid --blocked-cidrs flag value")
		os.Exit(1)
	}
	log.Info("SSRF blocklist configured", "totalCIDRs", len(cidrs))

	// Build handler.
	h := &executorhttp.Handler{
		BlockedCIDRs:       cidrs,
		BodyLimitBytes:     int64(bodyLimitBytes),
		AllowTLSSkipVerify: allowTLSSkipVerify,
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		Handler:      executorhttp.New(h),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 360 * time.Second, // must be > maxTimeoutSeconds (300s) + overhead
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background.
	go func() {
		log.Info("starting http-executor server", "port", port, "bodyLimitBytes", bodyLimitBytes, "allowTLSSkipVerify", allowTLSSkipVerify)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(err, "http-executor server failed")
			os.Exit(1)
		}
	}()

	// Wait for termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown with 15-second deadline.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "graceful shutdown failed; forcing exit")
		os.Exit(1)
	}
	log.Info("http-executor stopped")
}
