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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

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
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

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
