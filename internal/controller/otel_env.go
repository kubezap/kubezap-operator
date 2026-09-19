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

package controller

import (
	"os"

	corev1 "k8s.io/api/core/v1"
)

// otelPassthroughEnv returns OTEL_EXPORTER_OTLP_ENDPOINT and
// OTEL_TRACES_SAMPLER_ARG as container EnvVars, read from the controller
// manager's own process environment, when set. It is used by the webhook
// gateway, Kafka gateway, and http-executor Deployment reconcilers so that
// configuring tracing once on the controller (see docs/guides/observability.md)
// automatically and durably propagates to every gateway/executor Deployment
// the controller manages.
//
// This exists because those Deployments' desired PodSpec is fully rebuilt on
// every reconcile (via controllerutil.CreateOrUpdate's mutate function) and
// none of them previously declared any Env at all — so an operator (or, while
// testing, `kubectl set env`) patching OTEL_* env vars directly onto one of
// these Deployments was silently reverted on the very next reconcile, which
// for the http-executor Deployment specifically is triggered by every FlowRun
// event and so happens continuously. Reading from the controller's own env
// makes the desired state stable across reconciles instead of transient.
func otelPassthroughEnv() []corev1.EnvVar {
	var env []corev1.EnvVar
	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		env = append(env, corev1.EnvVar{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: v})
	}
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		env = append(env, corev1.EnvVar{Name: "OTEL_TRACES_SAMPLER_ARG", Value: v})
	}
	return env
}
