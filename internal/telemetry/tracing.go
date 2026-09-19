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

package telemetry

import (
	"context"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// defaultTraceSampleRatio is the fallback sampling ratio used when
// OTEL_TRACES_SAMPLER_ARG is unset, empty, unparseable, or out of the valid
// [0,1] range. Matches the "Default sample rate: 10%" documented in
// docs/guides/observability.md's Sampling Strategy section.
const defaultTraceSampleRatio = 0.1

// traceSampleRatio reads the standard OpenTelemetry OTEL_TRACES_SAMPLER_ARG
// environment variable (a float string, e.g. "0.1") and returns it as a
// head-sampling ratio in [0,1]. Falls back to defaultTraceSampleRatio when the
// variable is unset, unparseable, or outside [0,1].
//
// This intentionally reads the env var directly here rather than via a
// cmd/main.go CLI flag: threading a new flag through would require editing
// cmd/main.go (and every other binary that calls InitTracerProvider), which
// is treated as a hot/shared entry point in this repo. OTEL_TRACES_SAMPLER_ARG
// is also the standard OTel env var for this purpose, so this keeps
// configuration consistent with the wider OTel ecosystem's own conventions
// (e.g. an OTel Operator or Helm chart that sets these env vars generically).
func traceSampleRatio() float64 {
	val := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if val == "" {
		return defaultTraceSampleRatio
	}
	ratio, err := strconv.ParseFloat(val, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return defaultTraceSampleRatio
	}
	return ratio
}

// InitTracerProvider initialises the global OpenTelemetry TracerProvider.
//
// If the OTEL_EXPORTER_OTLP_ENDPOINT environment variable is empty, a no-op
// provider is installed and a no-op shutdown function is returned so that
// callers need no conditional logic.
//
// Otherwise an OTLP gRPC exporter is created, a BatchSpanProcessor is
// attached, and the provider is registered as the global provider together
// with a W3C TraceContext + Baggage composite propagator.
//
// The returned shutdown func must be called (typically via defer) to flush
// and close the exporter cleanly.
func InitTracerProvider(ctx context.Context, serviceName string) (func(), error) {
	// Always install the W3C TraceContext + Baggage propagator, regardless of
	// whether an exporter is configured below. Propagation (extracting an
	// inbound traceparent, injecting an outbound one — e.g. into a FlowRun's
	// kubezap.io/traceparent annotation) is a distinct concern from whether
	// spans are actually recorded/exported: callers like the webhook handler
	// forward trace context unconditionally, and the OTel API's own default
	// global propagator (before anyone calls SetTextMapPropagator) is a no-op
	// that silently drops every Extract/Inject call. Setting this only in the
	// exporter-configured branch below previously meant context propagation
	// was silently broken whenever OTEL_EXPORTER_OTLP_ENDPOINT was unset — the
	// common case, including in every existing unit test.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		// No endpoint configured — install a no-op provider so that all
		// otel.Tracer() calls are safe and produce zero overhead.
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func() {}, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion("v0.1.0"),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(traceSampleRatio()))),
	)

	otel.SetTracerProvider(tp)

	shutdown := func() {
		_ = tp.Shutdown(ctx)
	}
	return shutdown, nil
}

// Tracer returns a named tracer from the global provider.
// Convenience wrapper so callers don't need to import the otel package directly.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
