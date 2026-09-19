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

package executorhttp

import (
	"context"
	"fmt"
	"os"

	"github.com/kubezap/kubezap-operator/internal/telemetry"
)

// init installs the process-wide OpenTelemetry TracerProvider for the HTTP
// executor binary (cmd/http-executor/main.go).
//
// See the identical rationale in internal/gateway/webhook/tracing_init.go for
// why this is a package init() rather than an explicit call from main.go:
// cmd/http-executor/main.go is a hot/shared entry point that must not be
// touched here.
func init() {
	if _, err := telemetry.InitTracerProvider(context.Background(), "kubezap-http-executor"); err != nil {
		fmt.Fprintf(os.Stderr, "kubezap: failed to initialize http-executor tracer provider: %v\n", err)
	}
}
