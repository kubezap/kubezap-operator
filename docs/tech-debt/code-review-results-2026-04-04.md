# Code Review Results — 2026-04-04

Reviewer: Claude Opus (research-analyst agent)
Scope: Full codebase review per `docs/tech-debt/code-review-context-2026-04-04.md`

---

## HIGH — fix before public release

- [internal/controller/flowrun_controller.go:202–215] **Unreachable GC for terminal FlowRuns due to double-check on phase**: Lines 202–208 run GC for `Succeeded`/`Failed` FlowRuns and then return. Lines 212–215 skip terminal FlowRuns (`Succeeded`, `Failed`, `Cancelled`). The GC block catches `Succeeded`/`Failed` before the skip block, so `Cancelled` FlowRuns are correctly skipped. However, the GC block `return`s unconditionally after handling GC — meaning a terminal FlowRun that has already been GC'd and deleted will still return `ctrl.Result{}` even after `r.Delete` succeeds. This is correct behavior BUT: **`Cancelled` FlowRuns are never GC'd**. They fall through to the skip block and are permanently exempt from TTL and count-based GC. Impact: `Cancelled` FlowRuns accumulate forever in etcd, which is a resource leak under any workload that uses cancellation. Recommendation: Add `"Cancelled"` to the GC phase check at line 202, or add a separate GC path for Cancelled after the skip block.

- [internal/gateway/webhook/handler.go:177–183] **Basic auth credential comparison is not constant-time**: The Basic auth handler at line 181 uses `username != entry.BasicUsername || password != entry.BasicPassword` — a plain string comparison that short-circuits on the first differing byte. This enables timing attacks to enumerate valid usernames and passwords. Impact: Security vulnerability — timing side-channel on webhook Basic auth. The bearer, apiKey, and header-equals handlers all correctly use `subtle.ConstantTimeCompare`. Recommendation: Replace with `subtle.ConstantTimeCompare([]byte(username), []byte(entry.BasicUsername)) != 1 || subtle.ConstantTimeCompare([]byte(password), []byte(entry.BasicPassword)) != 1`.

- [internal/controller/flowrun_controller.go:518–561] **Parallel step goroutines share mutable `stepResults` map without synchronization**: The `stepResults` map (declared at line 304) is passed by reference to all parallel goroutines launched at line 522 via `r.executeStep(...)`. While the goroutines do not write to `stepResults` directly (they return their results which are merged after `wg.Wait()`), the `executeStep` call chain passes `stepResults` to `substituteVars` and `substituteVarsWithSecrets`, which read from the map. However, `stepResults` is populated from existing completed steps and is not mutated during wave execution — reads from goroutines without concurrent writes are safe. **Correction: This is actually safe as-is.** *(Kept as review note — no action needed.)*

- [internal/controller/resource_watcher.go:293–305] **cooldownTracker grows without bound; entries are never evicted**: The `cooldownTracker` map stores a timestamp per `(trigger, resource, eventType)` tuple but never removes entries for deleted resources or deregistered triggers. For resource triggers watching high-churn types (e.g., Pods in a large cluster), this map can grow to millions of entries. Impact: Unbounded memory growth proportional to the total number of unique resources seen across the lifetime of the controller. Recommendation: (1) Clear all cooldownTracker entries for a trigger key in `Deregister()`. (2) Consider periodic eviction of entries older than the cooldown duration.

- [internal/controller/flowrun_controller.go:940–953] **New `http.Client` with `http.Transport` allocated per executor RPC call when mTLS is enabled**: When `ExecutorTLSConfig` is non-nil, `callExecutor` creates a fresh `*http.Client` and `*http.Transport` on every call (line 943–946). `http.Transport` maintains a connection pool; creating a new one per call defeats connection reuse, leaks idle connections, and incurs TLS handshake overhead on every step execution. Impact: Performance degradation and TCP/TLS connection exhaustion under high step throughput with mTLS enabled. Recommendation: Create a single mTLS `*http.Client` at startup (alongside the non-TLS `HTTPClient`) and reuse it. Store it as a field on `FlowRunReconciler` initialized in `cmd/main.go` when `--executor-mtls=true`.

---

## MEDIUM — fix before GA

- [internal/controller/flowrun_controller.go:1026–1027] **Kafka publish step does not support retry — attempts always returns 1**: The `publishToKafka` call at line 1026 returns `(result, 1, err)` — the attempt count is hardcoded to 1. Unlike `executeHTTPStep` which has a full retry loop with `RetryPolicy` support, the Kafka publish path has no retry logic. If the Kafka broker is temporarily unavailable, the step fails immediately. Impact: Publish steps are less resilient than HTTP steps despite both being IO operations. Recommendation: Apply the same `RetryPolicy` loop used in `executeHTTPStep` to the Kafka publish path. Consider also supporting transport-error requeue (like executor transport errors) for broker unavailability.

- [internal/controller/flowrun_controller.go:1082–1097] **Plugin publish step does not support retry either**: Same issue as Kafka publish — the plugin HTTP call at lines 1087–1095 has no retry loop and hardcodes attempt count to 1. Impact: Plugin publish steps fail permanently on transient errors. Recommendation: Unify all IO step types under a shared retry wrapper.

- [internal/controller/flowrun_controller.go:604–617] **Post-completion failure check scans all steps but only respects step-level `onFailure`, not flow-level `failurePolicy` for individual step failures**: At line 612, the check is `step.OnFailure != "Continue"` — it does not also check `flow.Spec.FailurePolicy == "Continue"`. However, during wave execution (line 554), both are checked: `step.OnFailure != "Continue" && flow.Spec.FailurePolicy != "Continue"`. This inconsistency means a step with `failurePolicy: Continue` at the Flow level but no per-step `onFailure` will be correctly Continue'd during execution (line 554), but if it reaches the post-completion check (line 612) it will incorrectly fail the FlowRun. Impact: FlowRuns with `failurePolicy: Continue` may incorrectly transition to `Failed` after all steps complete if any step without an explicit `onFailure: Continue` had failed. Recommendation: Add `&& flow.Spec.FailurePolicy != "Continue"` to the check at line 612 to match the logic at line 554.

- [internal/gateway/webhook/handler.go:401–406] **FlowRun create uses `context.Background()` parent when request context is cancelled, breaking trace propagation**: When `r.Context().Err() != nil`, `createParent` falls back to `context.Background()`, which loses all trace context. The comment says "OTel span context propagates when possible" but the fallback path silently drops it. Impact: Trace breaks on slow API server responses where the HTTP client disconnects before FlowRun creation completes. This is by design for fire-and-forget semantics, but the trace loss is not documented. Recommendation: Extract the OTel span context from `r.Context()` before checking `Err()` and inject it into the background-derived context. This preserves traces without reintroducing cancellation.

- [internal/controller/flowrun_controller.go:2079–2083] **Kafka producer creation ignores TLS/SASL config from Integration**: `getOrCreateKafkaProducer` creates a plain `sarama.NewConfig()` (line 2079) without applying TLS or SASL settings from the Integration spec. The Kafka gateway watcher (`internal/gateway/kafka/watcher.go:220–277`) correctly applies TLS and SASL, but the publish-path producer does not. Impact: Kafka publish steps fail silently or insecurely against TLS/SASL-enabled brokers. Only plaintext, unauthenticated Kafka clusters work for publish steps. Recommendation: Thread TLS/SASL config from `integration.Spec.Kafka` into the producer config, mirroring the consumer config logic in `kafka/watcher.go:startSubscription`.

- [internal/controller/flowrun_controller.go:462–465] **Wait step `StartTime` overwritten on re-entry, masking actual start time**: When a wait step is re-admitted (line 440–441) after being in `Waiting` phase, the code at line 464 always sets `ss.StartTime = &now` (current time), overwriting the original start time. The `ResumeAfter` is preserved from the existing status (line 1252), but `StartTime` is lost. Impact: Step duration metrics and status timestamps are incorrect for wait steps — they show the last reconcile time, not when the wait actually began. Recommendation: Check for existing start time and preserve it: `if existing != nil && existing.StartTime != nil { ss.StartTime = existing.StartTime }`.

- [internal/controller/trigger_controller.go:119–123] **Webhook gateway resources not cleaned up when Trigger is disabled or deleted**: The trigger reconciler ensures the webhook gateway Deployment exists when `type=webhook && enabled` (line 119), but there is no cleanup path — disabling or deleting the last webhook Trigger in a namespace leaves the gateway Deployment, Service, ServiceAccount, Role, RoleBinding, and HPA behind as orphaned resources. Impact: Resource leak — orphaned gateway pods continue running in namespaces with no active webhook triggers. Recommendation: Add reference counting or a periodic sweep that removes gateway resources from namespaces with zero enabled webhook Triggers.

- [api/v1alpha1/trigger_types.go:277–278] **ResourceTrigger.Events field has `MinItems=1` but is also `+optional`**: The `Events` field is marked `+kubebuilder:validation:MinItems=1` AND `+optional`. When omitted (nil), the MinItems validation does not fire (optional fields skip validation when absent). When set to an empty slice `[]`, it fails validation. This is inconsistent — the code defaults to `["create"]` when events is empty (resource_watcher.go:384–387), but a user who explicitly sets `events: []` gets a validation error instead of the default. Impact: Confusing validation behavior. Recommendation: Remove `MinItems=1` since the code handles empty/nil gracefully, OR make the field required and remove the runtime default.

- [api/v1alpha1/flow_types.go:65] **FlowStep.Name has no uniqueness validation**: Step names within a Flow are used as keys in `stepResults`, `findStepStatus`, and CEL activation maps. Duplicate step names would cause silent overwrites in `upsertStepStatus` and unpredictable CEL evaluation. Impact: User error (duplicate step names) causes undefined runtime behavior with no API-level guardrail. Recommendation: Add a CEL-based XValidation rule on `FlowSpec.Steps` to enforce name uniqueness, e.g., `self.steps.all(s, self.steps.filter(t, t.name == s.name).size() == 1)`.

---

## LOW — tech debt / nice to have

- [internal/controller/flowrun_controller.go:1997] **Kafka producer cache key uses only bootstrap server list, not TLS/SASL config**: If the same bootstrap servers are referenced by two Integrations with different auth configs, the cached producer from the first Integration will be incorrectly reused for the second. Impact: Unlikely in practice (same brokers with different auth is unusual), but architecturally unsound. Recommendation: Include a hash of the TLS/SASL config in the cache key.

- [internal/controller/resource_watcher.go:407] **`fieldsChanged` uses `fmt.Sprintf("%v")` for deep comparison**: Comparing `fmt.Sprintf("%v", oldVal)` vs `fmt.Sprintf("%v", newVal)` is a lossy comparison — it conflates distinct types that have the same string representation (e.g., `int64(1)` vs `string("1")`). Impact: False negatives are unlikely but possible for exotic field types. Recommendation: Use `reflect.DeepEqual` or `equality.Semantic.DeepEqual` from apimachinery for correct deep comparison.

- [internal/gateway/webhook/handler.go:337–339] **Double body truncation with inconsistent limits**: The body is first capped at 4MB (line 300, `maxBody`), then independently truncated to 4096 bytes for the FlowRun TriggerData (line 338). The `bodyTruncated` flag set at line 310 reflects the 4MB cap, but is then potentially overridden at line 337 for the 4096 cap. The 4096 truncation should also set the flag. Impact: FlowRun TriggerData may contain a truncated body without `bodyTruncated: true`. Recommendation: Set `bodyTruncated = true` at line 337 when truncating to 4096.

- [internal/gateway/kafka/watcher.go:119] **`Start` returns `ctx.Err()` on shutdown instead of nil**: The Kafka watcher `Start` method returns `ctx.Err()` (line 119) when the context is cancelled. controller-runtime's `Runnable` contract expects `Start` to return nil on graceful shutdown. Returning `context.Canceled` may cause the manager to log a spurious error on shutdown. Impact: Noisy shutdown logs. Recommendation: Return `nil` instead of `ctx.Err()`.

- [internal/controller/flowrun_controller.go:1785–1791] **Malformed secret placeholder `$(secrets.name)` (no dot separator) causes infinite loop**: If a secret placeholder is malformed (e.g., `$(secrets.mysecret)` with no second dot), the code at line 1785 does `strings.Replace(actualStr, placeholder, placeholder, 1)` — a no-op replacement — and then `break`s. The `break` prevents an infinite loop, but only for the first malformed placeholder. If there are two or more malformed placeholders, only the first is handled before breaking. Impact: Extremely unlikely in practice; users would need multiple malformed placeholders. Recommendation: Use `continue` with an offset-tracking mechanism instead of `break` to handle multiple malformed placeholders.

- [internal/controller/executor_reconciler.go:151] **Executor container `--port=8091` is hardcoded in args, not derived from `r.executorPort()`**: The container args always include `"--port=8091"` (line 151), but the `executorPort()` method (line 92) respects `r.ExecutorPort`. If someone sets a non-default port, the container still starts on 8091 while the Service targets the custom port. Impact: Broken executor connectivity if `ExecutorPort` is changed from default. Recommendation: Use `fmt.Sprintf("--port=%d", r.executorPort())` at line 151.

- [internal/controller/executor_mtls.go:164–172] **`ClientTLSConfig()` does not set `ServerName`**: The TLS config returned by `ClientTLSConfig` does not set `ServerName`, so Go's TLS library will use the hostname from the URL. Since the executor URL includes the namespace (`kubezap-http-executor.<ns>.svc.cluster.local`), the server cert's CN (`kubezap-http-executor`) won't match the SNI. The cert has DNS SANs that should cover this, but if SANs are misconfigured, the error message will be confusing. Impact: Low — SANs should cover it, but fragile. Recommendation: Set `ServerName` explicitly in the TLS config to match the cert CN.

- [api/v1alpha1/flow_types.go:205] **RetryPolicy.MaxRetries has no validation marker for minimum value**: A negative `MaxRetries` would cause the retry loop to use `maxAttempts = negative + 1 = 0` or negative, which means the step executes zero times. Impact: User error causes silent step skip. Recommendation: Add `+kubebuilder:validation:Minimum=0`.

- [api/v1alpha1/flow_types.go:65–66] **FlowStep.Name has no validation marker for format or length**: Step names are used in Kubernetes resource names (via FlowRun status) and CEL identifiers (with hyphen-to-underscore conversion). Names with special characters, excessive length, or empty strings could cause subtle failures. Impact: Poor user experience on edge cases. Recommendation: Add `+kubebuilder:validation:Pattern=...` and `+kubebuilder:validation:MinLength=1`.

---

## Items reviewed with no findings

- **executor_mtls.go**: Certificate generation logic (ECDSA P-256, 24h lifetime, proper CA constraints, correct ExtKeyUsage) is sound. `NeedsRotation()` threshold of 1h before expiry is reasonable given 24h cert lifetime.
- **executor_reconciler.go**: CreateOrUpdate idempotency is correct. Security context (non-root, read-only root FS, drop all capabilities, seccomp) meets OpenShift restricted SCC. NetworkPolicy correctly restricts ingress to controller pods only. `terminationGracePeriodSeconds: 30` is set.
- **SSRF protection (ssrf.go)**: Blocklist is comprehensive (RFC1918, loopback, link-local, CGNAT, IPv6 equivalents). DNS resolution failure correctly fails closed. In-cluster `.svc.cluster.local` is explicitly blocked.
- **Webhook route registry**: Thread-safe via `sync.RWMutex`. Path normalization is consistent. Lookup, Register, Deregister are all correctly synchronized.
- **Webhook HMAC verification**: Uses `hmac.Equal` (constant-time) for signature comparison. Correct SHA-256 construction.
- **OIDC/JWT validation**: Uses `lestrrat-go/jwx` with proper issuer/audience validation. Key rotation retry on verification failure is a good pattern. JWKS cache with refresh interval prevents excessive outbound calls.
- **Kafka consumer lifecycle**: Proper goroutine management with context cancellation. Tombstone handling in delete handler. Consumer group rebalance strategy set.
- **Kafka SCRAM implementation**: Correct RFC 5802 implementation with proper HMAC and XOR operations.
- **Trigger reconciler finalizer management**: Correct pattern — add finalizer before registration, remove on deletion. Re-fetch before status patch prevents conflicts.
- **FlowRun GC**: TTL and count-based GC work correctly for Succeeded/Failed. Retain annotation exemption works. Sort-by-completion-time for oldest-first deletion is correct.
- **CEL evaluation**: Cache is appropriately implemented with `sync.Map`. Cost limit support prevents DoS. Hyphen-to-underscore normalization for step names is documented and consistent.

---

## Schedule additions

Items to add to `docs/schedule.md` under a new section `## 23. Code Review Findings — 2026-04-04`:

- `[ ]` **P0 BUG** — `flowrun_controller.go:202`: Cancelled FlowRuns are exempt from GC (TTL and count-based). Add `"Cancelled"` to the GC phase check.
- `[ ]` **P0 SECURITY** — `webhook/handler.go:181`: Basic auth uses non-constant-time comparison. Replace with `subtle.ConstantTimeCompare`.
- `[ ]` **P1 PERFORMANCE** — `flowrun_controller.go:943`: mTLS-enabled executor calls allocate new `http.Client`/`http.Transport` per call, defeating connection reuse. Create once at startup.
- `[ ]` **P1 BUG** — `flowrun_controller.go:612`: Post-completion failure check ignores `flow.Spec.FailurePolicy`. FlowRuns with flow-level `failurePolicy: Continue` may incorrectly fail.
- `[ ]` **P1 BUG** — `flowrun_controller.go:2079`: Kafka publish producer ignores TLS/SASL config from Integration. Only plaintext brokers work for publish steps.
- `[ ]` **P1 BUG** — `flowrun_controller.go:464`: Wait step `StartTime` overwritten on re-entry. Step duration metrics are incorrect for wait steps.
- `[ ]` **P1 TECH DEBT** — `resource_watcher.go:293`: cooldownTracker grows without bound. Clear entries on Deregister; add periodic eviction.
- `[ ]` **P1 TECH DEBT** — `trigger_controller.go:119`: Webhook gateway resources not cleaned up when last Trigger is disabled/deleted. Orphaned pods persist.
- `[ ]` **P2 BUG** — `flowrun_controller.go:1026`: Kafka publish step has no retry support. Hardcoded attempts=1.
- `[ ]` **P2 BUG** — `flowrun_controller.go:1087`: Plugin publish step has no retry support.
- `[ ]` **P2 BUG** — `webhook/handler.go:337`: Body truncated to 4096 bytes without setting `bodyTruncated` flag.
- `[ ]` **P2 BUG** — `executor_reconciler.go:151`: Executor container `--port` arg hardcoded to 8091, not derived from `ExecutorPort` field.
- `[ ]` **P2 VALIDATION** — `trigger_types.go:277`: `Events` field has conflicting `MinItems=1` and `+optional` markers.
- `[ ]` **P2 VALIDATION** — `flow_types.go:65`: FlowStep.Name lacks uniqueness validation. Duplicate names cause undefined behavior.
- `[ ]` **P2 VALIDATION** — `flow_types.go:205`: RetryPolicy.MaxRetries lacks `Minimum=0` validation.
- `[ ]` **P2 OBSERVABILITY** — `webhook/handler.go:401`: Trace context lost on `context.Background()` fallback for FlowRun creation.
- `[ ]` **P2 TECH DEBT** — `kafka/watcher.go:119`: `Start` returns `ctx.Err()` instead of nil on graceful shutdown. Noisy logs.
