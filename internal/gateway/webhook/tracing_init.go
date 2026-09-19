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

package webhook

import (
	"context"
	"fmt"
	"os"

	"github.com/kubezap/kubezap-operator/internal/telemetry"
)

// init installs the process-wide OpenTelemetry TracerProvider for the webhook
// gateway binary (cmd/webhook-gateway/main.go).
//
// This lives here — as a package init(), rather than an explicit call from
// cmd/webhook-gateway/main.go — because that file is treated as a hot/shared
// entry point in this repo (see CLAUDE.md's Parallel Agent Guidelines) and was
// concurrently being edited by another in-flight story at the time this was
// written (STORY-040). A package init() runs exactly once per process, before
// main(), which gives the same effective timing as calling this from the top
// of main() would.
//
// Without OTEL_EXPORTER_OTLP_ENDPOINT set, InitTracerProvider installs a
// no-op provider (zero overhead) — safe to call unconditionally, including in
// this package's own unit tests.
//
// The returned shutdown func is intentionally not retained: there is no hook
// into the binary's graceful-shutdown path available from here (again because
// main.go is off-limits). The SDK's BatchSpanProcessor still flushes on its
// own default periodic interval, so only spans emitted in the last moments
// before process exit are at risk of being dropped — an accepted trade-off,
// not fixed here. See docs/guides/observability.md.
func init() {
	if _, err := telemetry.InitTracerProvider(context.Background(), "kubezap-webhook-gateway"); err != nil {
		// No structured logger exists yet at init() time (main() builds one
		// after flag parsing) — fall back to stderr, matching main()'s own
		// pre-logger error handling.
		fmt.Fprintf(os.Stderr, "kubezap: failed to initialize webhook-gateway tracer provider: %v\n", err)
	}
}
