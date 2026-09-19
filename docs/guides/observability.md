# Observability

KubeZap exposes three complementary observability signals:

- **Prometheus metrics** — aggregates and counters for alerting and dashboards
- **Structured access logs** — per-request detail including source IP, payload size, auth results
- **OpenTelemetry traces** — distributed traces spanning trigger receipt through flow step execution

---

## Contents

- [Prometheus Metrics](#prometheus-metrics)
  - [Trigger Metrics](#trigger-metrics)
  - [Webhook Gateway Metrics](#webhook-gateway-metrics)
  - [Flow Execution Metrics](#flow-execution-metrics)
  - [Labels Reference](#labels-reference)
- [Structured Access Logs](#structured-access-logs)
  - [Access Log Fields](#access-log-fields)
  - [Source IP Tracking](#source-ip-tracking)
  - [Log Configuration](#log-configuration)
- [OpenTelemetry Traces](#opentelemetry-traces)
  - [Status](#status)
  - [Sampling Strategy](#sampling-strategy)
  - [Configuration](#configuration)
  - [Trace Structure](#trace-structure)
  - [Trace Context Propagation](#trace-context-propagation)
  - [Collection for Trace Exporters](#collection-for-trace-exporters)
- [Example Alerts](#example-alerts)
- [Example Grafana Panels](#example-grafana-panels)
- [Cardinality Guidance](#cardinality-guidance)

---

## Prometheus Metrics

All metrics use the `kubezap_` prefix. Each component exposes a `/metrics` endpoint on a dedicated metrics port:

| Component                 | Default metrics port | Protocol                                                                     |
| ------------------------- | -------------------- | ---------------------------------------------------------------------------- |
| `kubezap-controller`      | `:9090`              | HTTP (default); HTTPS via `--metrics-secure` + `--metrics-cert-path`         |
| `kubezap-webhook-gateway` | `:9090`              | HTTP (default); HTTPS via `--metrics-tls-cert-file`/`--metrics-tls-key-file` |
| `kubezap-kafka-gateway`   | `:9090`              | HTTP (default); HTTPS via `--metrics-tls-cert-file`/`--metrics-tls-key-file` |

Each component runs a dedicated metrics server on `:9090`, separate from its main serving port. The webhook gateway's hook server remains on `:8080` (`/hooks/*`, `/healthz`, `/readyz`).

> **Note — ServiceMonitor not auto-created**: KubeZap does **not** automatically create
> `ServiceMonitor` resources. This is intentional — `ServiceMonitor` is a Prometheus Operator
> CRD that may not be present in every cluster. After installing KubeZap, create
> `ServiceMonitor` resources manually. See the [Prometheus ServiceMonitor](#prometheus-servicemonitor)
> section at the end of this guide for ready-to-use templates.

> **Verified 2026-09-18 against a live cluster**: `kubezap_flowrun_duration_seconds`,
> `kubezap_flowrun_queue_duration_seconds`, and `kubezap_step_duration_seconds` are
> emitted correctly by the controller and confirmed queryable in Prometheus.
>
> **Fixed and re-verified live 2026-09-18 (STORY-040)**: the webhook/kafka gateway
> registry mismatch and missing metrics ports described below (see
> [Webhook Gateway Metrics](#webhook-gateway-metrics) and the
> [ServiceMonitor](#prometheus-servicemonitor) section) are now fixed. Confirmed
> live: after 13 real webhook requests, `kubezap_webhook_request_duration_seconds`
> appeared on the webhook gateway's own `/metrics` endpoint with a non-zero count.
>
> **New gap found during that same live pass, not yet fixed**:
> `kubezap_trigger_firings_total` is only ever incremented for `cron` triggers
> (`internal/controller/cron_scheduler.go`) — nothing in the webhook gateway
> (`internal/gateway/webhook/handler.go`) or the kafka gateway
> (`internal/gateway/kafka/`) increments it for webhook or kafka firings. The
> kafka gateway binary doesn't even import `internal/metrics`, so none of the
> `kubezap_*` counters are registered in its process at all — confirmed live: a
> real Kafka message correctly produced a FlowRun, but the kafka gateway's
> `/metrics` endpoint showed zero `kubezap_*` series before and after. Tracked as
> a new follow-up (not STORY-040's scope, which was the registry/port bugs only).

### Trigger Metrics

#### `kubezap_trigger_firings_total`
**Type**: Counter

Total number of trigger firings.

| Label       | Values                                    | Description                         |
| ----------- | ----------------------------------------- | ----------------------------------- |
| `namespace` | string                                    | Kubernetes namespace of the Trigger |
| `trigger`   | string                                    | Name of the Trigger                 |
| `type`      | string                                    | Trigger type (e.g. `webhook`, `cron`, `kafka`) |
| `result`    | `success`, `rate_limited`, `error`        | Outcome of the firing               |

```promql
# Firing rate per trigger
sum by (trigger, namespace) (rate(kubezap_trigger_firings_total[5m]))

# Error rate per trigger
sum by (trigger) (rate(kubezap_trigger_firings_total{result="error"}[5m]))
```

---

### Webhook Gateway Metrics

> **Fixed and verified live (2026-09-18, STORY-040)**: the three metrics in this
> section are registered into controller-runtime's internal `ctrlmetrics.Registry`
> (see `internal/metrics/metrics.go`'s `init()`). `cmd/webhook-gateway/main.go`
> previously served its `/metrics` endpoint via `promhttp.Handler()`, which
> gathers from the process's default Prometheus registry — a different registry
> instance — so none of these three metrics ever appeared on the webhook
> gateway's own `/metrics` endpoint. It now uses
> `promhttp.HandlerFor(ctrlmetrics.Registry, promhttp.HandlerOpts{})`. Confirmed
> live: after 13 real requests to a port-forwarded webhook gateway pod,
> `kubezap_webhook_request_duration_seconds_count{result="accepted",...}` read
> `13`.

#### `kubezap_webhook_request_duration_seconds`
**Type**: Histogram

Duration of webhook HTTP requests in seconds. Buckets: Prometheus default (`.005`, `.01`, `.025`, `.05`, `.1`, `.25`, `.5`, `1`, `2.5`, `5`, `10`).

| Label      | Values                                  | Description                       |
| ---------- | --------------------------------------- | --------------------------------- |
| `trigger`  | string                                  | Name of the Trigger               |
| `result`   | `accepted`, `rejected`, `rate_limited`  | Outcome of the request            |

```promql
# 99th percentile latency per trigger
histogram_quantile(0.99, sum by (trigger, le) (
  rate(kubezap_webhook_request_duration_seconds_bucket[5m])
))
```

---

#### `kubezap_webhook_ip_blocked_total`
**Type**: Counter

Total requests blocked by the webhook IP allowlist, labelled by `/24` (IPv4) or `/48` (IPv6) source CIDR bucket.

The `source_range` label uses CIDR truncation as a compromise: enough specificity to identify attack sources without per-IP cardinality explosion.

| Label          | Description                                                                                               |
| -------------- | --------------------------------------------------------------------------------------------------------- |
| `trigger`      | Name of the Trigger                                                                                       |
| `source_range` | Source IP truncated to `/24` (IPv4) or `/48` (IPv6), e.g. `203.0.113.0/24`. See [Cardinality Guidance](#cardinality-guidance). |

```promql
# IP block rate by source range
sort_desc(sum by (source_range) (
  rate(kubezap_webhook_ip_blocked_total[5m])
))
```

---

#### `kubezap_webhook_rate_limited_total`
**Type**: Counter

Total requests suppressed by webhook cooldown window policy.

| Label       | Description                         |
| ----------- | ----------------------------------- |
| `trigger`   | Name of the Trigger                 |
| `namespace` | Kubernetes namespace of the Trigger |

---

### Flow Execution Metrics

These are emitted by the controller as it processes FlowRun resources.

#### `kubezap_flowrun_duration_seconds`
**Type**: Histogram

Duration of FlowRun execution from start to terminal phase. Buckets: Prometheus default.

| Label       | Values                   | Description      |
| ----------- | ------------------------ | ---------------- |
| `namespace` | string                   | Namespace        |
| `flow`      | string                   | Name of the Flow |
| `phase`     | `Succeeded`, `Failed`    | Terminal phase   |

```promql
# P95 FlowRun duration per flow
histogram_quantile(0.95, sum by (flow, le) (
  rate(kubezap_flowrun_duration_seconds_bucket[5m])
))
```

---

#### `kubezap_flowrun_queue_duration_seconds`
**Type**: Histogram

Time from FlowRun creation to first transition to Running phase. Indicates scheduling latency and controller backpressure. Buckets: Prometheus default.

| Label       | Description      |
| ----------- | ---------------- |
| `namespace` | Namespace        |
| `flow`      | Name of the Flow |

```promql
# P99 queue wait time
histogram_quantile(0.99, sum by (flow, le) (
  rate(kubezap_flowrun_queue_duration_seconds_bucket[5m])
))
```

---

#### `kubezap_step_duration_seconds`
**Type**: Histogram

Duration of individual step execution. Buckets: Prometheus default.

| Label       | Values                    | Description               |
| ----------- | ------------------------- | ------------------------- |
| `namespace` | string                    | Namespace                 |
| `flow`      | string                    | Name of the Flow          |
| `step_type` | string                    | Type of the step action   |
| `outcome`   | `Succeeded`, `Failed`     | Outcome of the step       |

```promql
# Step failure rate by step type
sum by (step_type) (rate(kubezap_step_duration_seconds_count{outcome="Failed"}[5m]))
```

---

#### `kubezap_flowruns_active`
**Type**: Gauge

Number of FlowRuns currently in Running or Pending phase, by namespace and phase. This is a custom collector — values are computed from the live controller-runtime cache at each scrape, so they remain accurate across controller restarts (no stale gauge values).

| Label       | Values                 | Description               |
| ----------- | ---------------------- | ------------------------- |
| `namespace` | string                 | Namespace                 |
| `phase`     | `Running`, `Pending`   | Current FlowRun phase     |

```promql
# Total active FlowRuns
sum(kubezap_flowruns_active)

# Active FlowRuns by namespace
sum by (namespace) (kubezap_flowruns_active)
```

---

### Labels Reference

KubeZap metrics use these labels where applicable:

| Label       | Description                                              |
| ----------- | -------------------------------------------------------- |
| `namespace` | Kubernetes namespace of the resource                     |
| `trigger`   | Name of the Trigger CRD                                  |
| `flow`      | Name of the Flow CRD                                     |
| `type`      | Trigger type: `webhook`, `cron`, `kafka`, etc.           |
| `result`    | Outcome of an operation (metric-specific values)         |
| `phase`     | FlowRun phase: `Running`, `Pending`, `Succeeded`, `Failed` |
| `step_type` | Step action type                                         |
| `outcome`   | Step execution outcome: `Succeeded`, `Failed`            |
| `source_range` | Source IP CIDR bucket (`/24` IPv4 or `/48` IPv6) — used on `kubezap_webhook_ip_blocked_total` only |

---

## Structured Access Logs

Every request to the webhook gateway produces a structured JSON access log entry. Access logs are written to stdout and collected by your log aggregation stack (Loki, Splunk, CloudWatch, Datadog, etc.).

**This is where source IP tracking lives.** Raw IP addresses are not Prometheus label values due to cardinality — they are in the access log.

Access logging is implemented via `internal/gateway/webhook/accesslog.go`. The `AccessLogMiddleware` wraps every request and emits one log line per request containing the core fields listed below. Auth-specific detail (reason, FlowRun name) is additionally logged inline by the webhook handler at `warn` or `error` level.

> **Corrected 2026-09-18 after live validation.** Only the "Core access log
> line" example immediately below matches real output byte-for-byte (confirmed
> against `kubectl logs` on a live webhook gateway pod after 13 real requests).
> The "Full structured entry" and "On auth failure" examples further down are
> the **intended target schema**, not current output — real code does not
> currently emit a `trace_id`, nested `request`/`auth`/`response`/`flowrun`
> objects, `request_id`, `source_port`, `forwarded_for`, `user_agent`,
> `body_bytes`, or any structured `auth.reason` field anywhere. What the
> webhook handler (`internal/gateway/webhook/handler.go:348`) actually emits
> as its one supplementary line per request, verified live, is a **flat** set
> of fields — `method`, `path`, `status`, `trigger`, `namespace`, `flowRun`,
> `duration_ms`, `content_type`, `source_ip` — logged through a different
> logger (controller-runtime's zap-based logr, console-encoded) than the
> middleware's line (Go `slog`, JSON-encoded), so unlike the middleware line it
> is **not** parseable as a single JSON value — it looks like:
> `2026-09-18T22:40:35.884Z\tINFO\twebhook-gateway.webhook-handler\twebhook/handler.go:348\twebhook access\t{"method": "POST", ...}`.
> This is a real product gap (the richer structured schema described below was
> designed but not implemented), not a doc typo — logged as a follow-up, not
> fixed here. The [Source IP Tracking](#source-ip-tracking) LogQL examples
> further down have been corrected to only rely on fields that exist today.

### Access Log Fields

The `AccessLogMiddleware` emits one compact JSON line per request (core fields). The webhook handler emits supplementary log lines for auth detail and FlowRun outcomes. Together they form the full picture shown in the example below — see the note above for which parts of this are implemented today versus intended.

**Core access log line (emitted by middleware):**
```json
{
  "time": "2026-03-14T10:32:11Z",
  "level": "INFO",
  "msg": "access",
  "timestamp": "2026-03-14T10:32:11Z",
  "method": "POST",
  "path": "/hooks/orders",
  "status": 202,
  "duration_ms": 12,
  "source_ip": "203.0.113.42",
  "trigger": "orders"
}
```

**Full structured entry (conceptual — combining middleware + handler log fields):**
```json
{
  "ts": "2026-03-14T10:32:11.423Z",
  "level": "info",
  "msg": "webhook_request",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",

  "request": {
    "method": "POST",
    "path": "/hooks/orders",
    "trigger": "order-received",
    "namespace": "automation",
    "source_ip": "203.0.113.42",
    "source_port": 54321,
    "forwarded_for": "203.0.113.42, 10.0.0.1",
    "user_agent": "GitHub-Hookshot/abc123",
    "request_id": "req-9f8e7d6c",
    "content_type": "application/json",
    "body_bytes": 1247
  },

  "auth": {
    "type": "hmac",
    "result": "success"
  },

  "response": {
    "status_code": 202,
    "duration_ms": 12.4
  },

  "flowrun": {
    "created": true,
    "name": "order-received-1741954332-xk92p",
    "flow": "process-order"
  }
}
```

**On auth failure:**
```json
{
  "ts": "2026-03-14T10:35:44.001Z",
  "level": "warn",
  "msg": "webhook_request",
  "trace_id": "...",

  "request": {
    "method": "POST",
    "path": "/hooks/orders",
    "trigger": "order-received",
    "namespace": "automation",
    "source_ip": "198.51.100.7",
    "user_agent": "curl/7.88.1",
    "body_bytes": 0
  },

  "auth": {
    "type": "hmac",
    "result": "failure",
    "reason": "invalid_signature"
  },

  "response": {
    "status_code": 401,
    "duration_ms": 0.8
  },

  "flowrun": {
    "created": false
  }
}
```

### Access Log Fields Reference

| Field                   | Type    | Description                                                                    |
| ----------------------- | ------- | ------------------------------------------------------------------------------ |
| `ts`                    | RFC3339 | Request timestamp                                                              |
| `level`                 | string  | `info` (success), `warn` (auth failure, rate limited), `error` (gateway error) |
| `trace_id`              | string  | OpenTelemetry trace ID for correlation with traces                             |
| `request.source_ip`     | string  | Client IP (respects `trustedProxies` for X-Forwarded-For)                      |
| `request.forwarded_for` | string  | Raw X-Forwarded-For header if present                                          |
| `request.user_agent`    | string  | HTTP User-Agent header                                                         |
| `request.body_bytes`    | integer | Request body size in bytes                                                     |
| `request.content_type`  | string  | Normalized content type: `json`, `xml`, `form`, `text`, `binary`               |
| `auth.type`             | string  | Auth method configured on the trigger                                          |
| `auth.result`           | string  | `success`, `failure`, `skipped`                                                |
| `auth.reason`           | string  | Failure reason (only present when `result: failure`)                           |
| `response.status_code`  | integer | HTTP status code returned                                                      |
| `response.duration_ms`  | float   | Total request handling time in milliseconds                                    |
| `flowrun.created`       | boolean | Whether a FlowRun was created                                                  |
| `flowrun.name`          | string  | Name of the created FlowRun (only when `created: true`)                        |

### Source IP Tracking

To analyze traffic by source, query your log aggregation stack.

> **Corrected 2026-09-18 after live validation.** The examples below previously
> filtered on `msg="webhook_request"` — the real `msg` value the middleware
> emits is `"access"` (verified against live pod logs); `"webhook_request"`
> does not appear anywhere in the codebase. The "top source IPs" query below
> now works as written against the real `access` line's flat fields. The
> auth-failure examples further down could not be corrected the same way: no
> current code path logs a structured `auth.result`/`auth.reason` (or flat
> `auth_result`/`auth_reason`) field anywhere — see the note under [Access Log
> Fields](#access-log-fields). Rewritten below to use `status="401"` on the
> real `access` line as today's best available proxy for an auth rejection;
> per-request auth failure *reason* is not queryable from logs until that gap
> is closed (real gap, logged as a follow-up, not fixed here).

**Loki — top source IPs for a trigger:**
```logql
topk(10,
  sum by (source_ip) (
    count_over_time(
      {namespace="automation"} |= "access" | json | trigger="order-received"
      [1h]
    )
  )
)
```

**Loki — requests rejected (HTTP 401) in the last 24h:**
```logql
{namespace="automation"}
  | json
  | msg="access"
  | status="401"
  | line_format "{{.timestamp}} {{.source_ip}} {{.trigger}}"
```

**Identifying scanning/probing activity (by 401 rate, not auth reason):**
```logql
sum by (source_ip) (
  count_over_time(
    {namespace="automation"} | json | msg="access" | status="401" [10m]
  )
) > 20
```

### Log Configuration

Access logging is enabled by default. Configure via operator environment variables:

| Variable                        | Default                   | Description                                                             |
| ------------------------------- | ------------------------- | ----------------------------------------------------------------------- |
| `ACCESS_LOG_ENABLED`            | `true`                    | Enable/disable access logging                                           |
| `ACCESS_LOG_LEVEL`              | `info`                    | Minimum log level: `debug`, `info`, `warn`, `error`                     |
| `ACCESS_LOG_REDACT_HEADERS`     | `Authorization,X-Api-Key` | Comma-separated list of headers to redact in logs                       |
| `ACCESS_LOG_MAX_BODY_LOG_BYTES` | `0`                       | Log request body bytes (0 = disabled; set carefully for PII compliance) |
| `TRUSTED_PROXIES`               | `""`                      | Comma-separated CIDR list for X-Forwarded-For processing                |

> **PII and compliance**: Request bodies may contain personal data. `ACCESS_LOG_MAX_BODY_LOG_BYTES` is off by default. If enabled, ensure your log retention and access controls meet applicable regulations (GDPR, HIPAA, PCI DSS).

---

## OpenTelemetry Traces

### Status

> **Added 2026-09-18** — this heading was linked from the Contents list above
> but did not exist in this file; the anchor was dead. Filling it in with what
> live validation actually found, since it is directly relevant to the two
> sections below.

Verified against `internal/telemetry/tracing.go` and every `tracer.Start(...)`
call site in the repo:

- **Sampling is not currently configurable.** There is no `--otel-sample-rate`
  or `--otel-exporter-endpoint` CLI flag anywhere in `cmd/main.go`, and nothing
  in the codebase reads `OTEL_TRACES_SAMPLER_ARG`. `InitTracerProvider` builds
  the `TracerProvider` with no `sdktrace.WithSampler(...)` option at all, so it
  falls back to the OpenTelemetry Go SDK's own default — `ParentBased(AlwaysSample())`
  — not the `ParentBased(TraceIDRatioBased(0.1))` described below. In practice
  tracing today is all-or-nothing: a no-op (0% — zero overhead) when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, or effectively 100% sampled when it's
  set. The **Sampling Strategy** and **Configuration** sections below describe
  the intended design, not current behavior — real gap, logged as a follow-up,
  not fixed here.
- **Span coverage is much smaller than the diagram in [Trace Structure](#trace-structure)
  suggests.** The only spans that exist anywhere in the codebase today are
  `flowrun.reconcile` (root, per FlowRun, in the controller) and `flowrun.step`
  (one per step). None of `webhook_request`, `auth_verify`, `cooldown_check`,
  `payload_parse`, `flowrun_create`, `cron_fire`, `kafka_message_received`,
  `http_call`, or `publish_call` are implemented — there are no gateway-side
  spans at all. The diagram is the intended target shape, not what a trace
  backend will show today.
- **What *is* implemented and verified working**: W3C trace context
  propagation via the `kubezap.io/traceparent` FlowRun annotation, for both the
  webhook gateway (`internal/gateway/webhook/handler.go`) and the Kafka gateway
  (`internal/gateway/kafka/handler.go`), and extracted correctly by the
  controller (`internal/controller/flowrun_controller.go`) to parent
  `flowrun.reconcile`. See [Trace Context Propagation](#trace-context-propagation).

### Sampling Strategy

KubeZap uses **head sampling** via OpenTelemetry's `ParentBased(TraceIDRatioBased)` sampler.

- The sampling decision is made once, at the trace root — either at the webhook request (gateway) or at FlowRun start (cron trigger fired by the controller).
- Child spans — step executions, publish calls to plugin endpoints — inherit the parent's decision. There are no partial traces where the root is sampled but a child is dropped, or vice versa.
- Default sample rate: **10%** (`0.1`).

This approach gives predictable, low overhead in production while still capturing a statistically representative sample for latency analysis and error rate monitoring. For debugging a specific workflow, raise the rate to `1.0` temporarily (see Configuration below).

> **This section describes the intended design — see [Status](#status).** As of
> 2026-09-18 there is no sampler wired up at all, so the actual behavior is 0%
> (tracing off) or ~100% (tracing on), never a configurable ratio in between.

---

### Configuration

> **Corrected 2026-09-18**: the `--otel-sample-rate` / `--otel-exporter-endpoint`
> CLI flags in the original table do not exist in `cmd/main.go` — removed
> below. `OTEL_EXPORTER_OTLP_ENDPOINT` is real and verified (confirmed in
> `internal/telemetry/tracing.go`). `OTEL_TRACES_SAMPLER_ARG` is listed for
> forward-compatibility but currently has **no effect** — see [Status](#status).

| Env var                       | Default         | Description                                                                                                               |
| ------------------------------ | --------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `OTEL_TRACES_SAMPLER_ARG`      | n/a             | **Currently has no effect** — nothing reads this variable. Reserved for when configurable sampling is implemented.        |
| `OTEL_EXPORTER_OTLP_ENDPOINT`  | `""` (disabled) | OTLP gRPC endpoint for the trace exporter, e.g. `otel-collector:4317`. When empty, tracing is a no-op with zero overhead. Verified in code and live. |

When `OTEL_EXPORTER_OTLP_ENDPOINT` is empty (the default), tracing is completely disabled — the `TracerProvider` is a no-op implementation and no goroutines or connections are created. This is the recommended configuration for development clusters.

**To enable tracing for troubleshooting** (note: this currently traces every request — see Status above), patch the operator or gateway Deployment:

```yaml
env:
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "otel-collector.monitoring:4317"
```

A rolling restart picks up the new configuration. Revert the patch to disable tracing again once troubleshooting is complete.

---

### Trace Structure

The intended span hierarchy for a webhook-triggered flow (**intended** — see
[Status](#status) for what is actually implemented today: only
`flowrun.reconcile` and `flowrun.step` exist):

```
webhook_request (root span — gateway process)
├── auth_verify (span)
├── cooldown_check (span)
├── payload_parse (span)
└── flowrun_create (span)

flowrun_reconcile (child of webhook_request — controller process)
├── step: fetch-order (span)
│   └── http_call (span)
├── step: transform (span)
└── step: notify-slack (span)
    └── publish_call (span)   # POST to plugin /publish endpoint
```

For cron-triggered flows, the root span is `cron_fire` emitted by the controller's cron scheduler. For Kafka-triggered flows, the root span is `kafka_message_received` emitted by the Kafka gateway.

Key span attributes:

| Attribute              | Set on              | Description                                             |
| ---------------------- | ------------------- | ------------------------------------------------------- |
| `kubezap.trigger.name` | Root span           | Name of the Trigger CRD                                 |
| `kubezap.trigger.type` | Root span           | `webhook`, `cron`, `kafka`, `amqp`, `nats`, `resource`  |
| `kubezap.flowrun.name` | `flowrun_reconcile` | Name of the FlowRun resource                            |
| `kubezap.flow.name`    | `flowrun_reconcile` | Name of the referenced Flow                             |
| `kubezap.step.name`    | Each step span      | Step name within the Flow                               |
| `kubezap.step.type`    | Each step span      | Step action type: `http`, `transform`, `publish`, etc.  |
| `kubezap.step.outcome` | Each step span      | `Succeeded`, `Failed`, `Skipped`                        |
| `http.url`             | `http_call`         | Target URL (auth tokens redacted)                       |
| `http.status_code`     | `http_call`         | Response status code                                    |
| `net.peer.ip`          | Root span (webhook) | Source IP — as a span attribute, not a Prometheus label |

The `trace_id` in structured access logs matches the OTel trace ID, enabling log-to-trace correlation in Grafana, Jaeger, Honeycomb, or any OTLP-compatible backend.

---

### Trace Context Propagation

The webhook gateway and the controller are separate processes. To continue the same trace across the process boundary without a direct gRPC call between them, KubeZap uses the FlowRun resource as the carrier for W3C trace context:

1. **Gateway** — when creating a FlowRun, the gateway injects the current W3C `traceparent` value into the FlowRun annotation `kubezap.io/traceparent`.
2. **Controller** — when picking up a FlowRun for execution, the controller reads the `kubezap.io/traceparent` annotation and uses it as the parent context for the `flowrun_reconcile` span. This continues the same trace ID started by the gateway.

```yaml
# FlowRun created by the webhook gateway
metadata:
  annotations:
    kubezap.io/traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
```

If the annotation is absent (e.g., the FlowRun was created manually or by a gateway without tracing configured), the controller starts a new root span.

This design is intentional: it avoids tight coupling between the gateway and controller processes, works across pod restarts, and requires no additional sidecar or messaging infrastructure.

---

### Collection for Trace Exporters

KubeZap does **not** auto-create any resources for trace collection. Deploying and configuring an OpenTelemetry Collector (or a compatible backend such as Jaeger, Tempo, or a SaaS vendor) is the user's responsibility.

The OTLP gRPC endpoint set via `OTEL_EXPORTER_OTLP_ENDPOINT` must be reachable from all KubeZap pods (controller and gateways). In a typical in-cluster setup, this is the Service address of an OpenTelemetry Collector Deployment, e.g. `otel-collector.monitoring.svc.cluster.local:4317`.

---

## Metrics Server Configuration

Each component accepts flags to control its dedicated metrics server (port `:9090` by default).

| Flag                      | Component                      | Default | Description                                                                                         |
| ------------------------- | ------------------------------ | ------- | --------------------------------------------------------------------------------------------------- |
| `--metrics-bind-address`  | controller                     | `:9090` | Address the metrics endpoint binds to. Use `:9090` for HTTP (default) or `:8443` for HTTPS.         |
| `--metrics-secure`        | controller                     | `false` | When `true`, serves metrics over HTTPS. Requires `--metrics-cert-path`.                             |
| `--metrics-cert-path`     | controller                     | `""`    | Directory containing `tls.crt` and `tls.key` for the metrics server (cert-manager compatible).      |
| `--metrics-port`          | webhook-gateway, kafka-gateway | `9090`  | Port for the dedicated Prometheus metrics server.                                                   |
| `--metrics-tls-cert-file` | webhook-gateway, kafka-gateway | `""`    | Path to TLS certificate PEM. When set with `--metrics-tls-key-file`, the metrics server uses HTTPS. |
| `--metrics-tls-key-file`  | webhook-gateway, kafka-gateway | `""`    | Path to TLS private key PEM. Required when `--metrics-tls-cert-file` is set.                        |

The webhook gateway hook server (port `:8080`) TLS flags (`--tls-cert-file`, `--tls-key-file`, `--mtls-ca-file`) are independent of the metrics server TLS flags.

---

## Prometheus ServiceMonitor

KubeZap does **not** automatically create `ServiceMonitor` resources. This is intentional — `ServiceMonitor` is a Prometheus Operator CRD that may not be installed in every cluster, and auto-creating it would cause the operator to fail in clusters without Prometheus Operator. It is also common for platform teams to control what is scraped centrally.

Create the `ServiceMonitor` manually after installing KubeZap.

> **Fixed 2026-09-18 after live validation**: the selectors below previously
> read `app.kubernetes.io/name: kubezap` + `app.kubernetes.io/component: <role>`
> for every component. That never matched any real Service. The controller's
> metrics Service (kubebuilder-scaffolded, `config/manager`) actually carries
> `app.kubernetes.io/name: kubezap` + `control-plane: controller-manager` (no
> `component` label at all) — fixed below. The webhook-gateway and kafka-gateway
> Services (created by the controller's own gateway reconcilers) use a
> completely different scheme, `kubezap.io/component: <role>`, with no
> `app.kubernetes.io/name` label — also fixed below. `honorLabels: true` was
> added to every endpoint: without it, kube-prometheus-stack's default
> `namespace`-relabeling collides with the metric's own `namespace` label (and
> `flow`/`trigger` where present) and Prometheus silently renames the real one
> to `exported_namespace`, silently breaking every `sum by (namespace, ...)`
> query in this guide. Confirmed live: `namespace="kubezap-system"` (the
> Service's own namespace) shadowed the real `namespace="kubezap-obs"` label
> until `honorLabels: true` was added.

### Controller ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kubezap-controller
  namespace: kubezap-system
  labels:
    # Match the label selector your Prometheus uses to discover ServiceMonitors.
    # For kube-prometheus-stack, the default is: release: <helm-release-name>
    release: prometheus
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: kubezap
      control-plane: controller-manager
  namespaceSelector:
    matchNames:
      - kubezap-system
  endpoints:
    - port: metrics
      path: /metrics
      scheme: http
      interval: 30s
      scrapeTimeout: 10s
      honorLabels: true
      # To scrape over HTTPS, add --metrics-secure=true and --metrics-cert-path to the
      # controller Deployment, then change scheme to https and add a tlsConfig block.
```

### Webhook Gateway ServiceMonitor

One `ServiceMonitor` can match all webhook gateway Services across namespaces using `namespaceSelector: any: true`. Adjust the namespace selector to match your deployment topology.

> **Fixed and verified live (2026-09-18, STORY-040)**: the webhook gateway
> Deployment/Service (`internal/controller/gateway_deployment.go`) previously
> never opened a container port or Service port for the metrics server — only
> the hook-server port (`8080`) was exposed, despite the gateway binary itself
> listening on `:9090` by default. Both the Deployment's container port and the
> Service's `metrics` port (`9090`) are now added. Confirmed live: `kubectl get
> svc` on the webhook gateway Service now shows both `http` (`8080`) and
> `metrics` (`9090`) ports, and the port-forwarded `metrics` port serves real
> `kubezap_*` data (see the callout above).

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kubezap-webhook-gateways
  namespace: kubezap-system
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      kubezap.io/component: webhook-gateway
  namespaceSelector:
    any: true   # match webhook-gateway Services in all namespaces
  endpoints:
    - port: metrics
      path: /metrics
      scheme: http    # adjust to https if TLS is enabled on the metrics endpoint
      interval: 15s   # more frequent — gateway is on the hot path
      scrapeTimeout: 10s
      honorLabels: true
```

### Kafka Gateway ServiceMonitor

> **Fixed and verified live (2026-09-18, STORY-040)**: `internal/controller/integration_controller.go`
> previously created no Service at all for the kafka gateway (only a
> Deployment) — there was nothing for any ServiceMonitor to scrape, a deeper
> gap than the webhook gateway's missing port. It now also creates a Service
> exposing the `metrics` port (`9090`), matching the container port added to
> the Deployment, with the `kubezap.io/component: kafka-gateway` selector this
> ServiceMonitor expects. Confirmed live: after a real Kafka message produced a
> FlowRun, the kafka gateway's `/metrics` endpoint was reachable via this
> Service and served `ctrlmetrics.Registry` content (confirmed by the presence
> of `certwatcher_*`/`controller_runtime_*` series, which only that registry
> carries). See the note above the Prometheus Metrics table: `kubezap_*`
> counters still don't appear on this endpoint — that's a separate,
> not-yet-fixed instrumentation gap, not a ServiceMonitor/scrape-target
> problem.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kubezap-kafka-gateways
  namespace: kubezap-system
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      kubezap.io/component: kafka-gateway
  namespaceSelector:
    any: true
  endpoints:
    - port: metrics
      path: /metrics
      scheme: http
      honorLabels: true
      interval: 30s
```

### Verifying scrape targets

After creating the `ServiceMonitor`, confirm Prometheus is picking up the targets:

```bash
# Port-forward to Prometheus and check targets
kubectl port-forward -n monitoring svc/prometheus-operated 9090

# Then open: http://localhost:9090/targets
# Look for: kubezap-system/kubezap-controller, kubezap-webhook-gateways, etc.
```

Or query via the API:
```bash
curl http://localhost:9090/api/v1/targets | jq '.data.activeTargets[] | select(.labels.job | startswith("kubezap"))'
```

---

## Example Alerts

PrometheusRule examples for common alerting scenarios.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kubezap-alerts
  namespace: monitoring
spec:
  groups:
    - name: kubezap.webhook.security
      interval: 30s
      rules:

        - alert: WebhookIPBlockSpike
          expr: |
            sum by (trigger, source_range) (
              rate(kubezap_webhook_ip_blocked_total[5m])
            ) > 2
          for: 1m
          labels:
            severity: warning
          annotations:
            summary: "IP blocks on {{ $labels.trigger }} from {{ $labels.source_range }}"
            description: >
              {{ $value | humanize }} requests/sec from {{ $labels.source_range }}
              are being blocked by the IP allowlist on trigger {{ $labels.trigger }}.

        - alert: WebhookHighRateLimiting
          expr: |
            sum by (namespace, trigger) (
              rate(kubezap_webhook_rate_limited_total[5m])
            ) > 5
          for: 2m
          labels:
            severity: warning
          annotations:
            summary: "High rate-limiting on webhook trigger {{ $labels.trigger }}"
            description: >
              Trigger {{ $labels.namespace }}/{{ $labels.trigger }} is suppressing
              {{ $value | humanize }} requests/sec due to the cooldown window policy.

    - name: kubezap.webhook.health
      rules:

        - alert: WebhookHighRejectionRate
          expr: |
            sum by (trigger) (
              rate(kubezap_webhook_request_duration_seconds_count{result="rejected"}[5m])
            )
            /
            sum by (trigger) (
              rate(kubezap_webhook_request_duration_seconds_count[5m])
            ) > 0.05
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "High rejection rate on webhook trigger {{ $labels.trigger }}"

        - alert: WebhookHighLatency
          expr: |
            histogram_quantile(0.99,
              sum by (trigger, le) (
                rate(kubezap_webhook_request_duration_seconds_bucket[5m])
              )
            ) > 2
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "P99 webhook latency > 2s on {{ $labels.trigger }}"

    - name: kubezap.flowrun.health
      rules:

        - alert: FlowRunHighFailureRate
          expr: |
            sum by (namespace, flow) (
              rate(kubezap_flowrun_duration_seconds_count{phase="Failed"}[5m])
            )
            /
            sum by (namespace, flow) (
              rate(kubezap_flowrun_duration_seconds_count[5m])
            ) > 0.1
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "High FlowRun failure rate for flow {{ $labels.flow }}"

        - alert: FlowRunQueueLatencyHigh
          expr: |
            histogram_quantile(0.99,
              sum by (flow, le) (
                rate(kubezap_flowrun_queue_duration_seconds_bucket[5m])
              )
            ) > 30
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "FlowRun queue wait time > 30s for flow {{ $labels.flow }}"
            description: >
              The controller is taking more than 30s to pick up FlowRuns for
              {{ $labels.namespace }}/{{ $labels.flow }}. Check controller health
              and FlowRun backlog.

        - alert: FlowRunsActiveBacklog
          expr: |
            sum by (namespace) (kubezap_flowruns_active) > 500
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: "Large FlowRun backlog in namespace {{ $labels.namespace }}"
```

---

## Example Grafana Panels

Key panels for a KubeZap dashboard:

**Trigger firing rate by result:**
```promql
sum by (trigger, result) (rate(kubezap_trigger_firings_total[5m]))
```

**Webhook request rate by outcome:**
```promql
sum by (trigger, result) (rate(kubezap_webhook_request_duration_seconds_count[5m]))
```

**P50 / P95 / P99 webhook request latency:**
```promql
histogram_quantile(0.50, sum by (le) (rate(kubezap_webhook_request_duration_seconds_bucket[5m])))
histogram_quantile(0.95, sum by (le) (rate(kubezap_webhook_request_duration_seconds_bucket[5m])))
histogram_quantile(0.99, sum by (le) (rate(kubezap_webhook_request_duration_seconds_bucket[5m])))
```

**IP blocks by source range (bar chart):**
```promql
sort_desc(sum by (source_range) (
  increase(kubezap_webhook_ip_blocked_total[1h])
))
```

**FlowRun success vs failure rate:**
```promql
sum by (flow, phase) (rate(kubezap_flowrun_duration_seconds_count[5m]))
```

**P95 FlowRun end-to-end duration:**
```promql
histogram_quantile(0.95, sum by (flow, le) (
  rate(kubezap_flowrun_duration_seconds_bucket[5m])
))
```

**FlowRun queue latency (P99):**
```promql
histogram_quantile(0.99, sum by (flow, le) (
  rate(kubezap_flowrun_queue_duration_seconds_bucket[5m])
))
```

**Active FlowRun backlog (stat panel):**
```promql
sum by (namespace, phase) (kubezap_flowruns_active)
```

**Step failure rate by step type:**
```promql
sum by (step_type) (rate(kubezap_step_duration_seconds_count{outcome="Failed"}[5m]))
```

---

## Cardinality Guidance

### Why source IPs are not Prometheus label values

Prometheus stores one time series per unique combination of label values. A busy gateway receiving traffic from thousands of IP addresses would create thousands of time series per metric — this is a **cardinality explosion** that degrades Prometheus query performance and increases memory usage.

**The rule of thumb**: a label should have bounded, low cardinality (ideally < 100 unique values). Source IPs are unbounded.

**The solution**: use the structured access log for per-IP analysis, and use `/24`-bucketed `source_range` labels in Prometheus for coarse-grained IP source metrics on the `kubezap_webhook_ip_blocked_total` metric only.

| Signal             | Source IP handling                                             |
| ------------------ | -------------------------------------------------------------- |
| Prometheus metrics | `/24` bucket on `ip_blocked` only — everything else is IP-free |
| Access logs        | Full source IP in every log entry                              |
| OTel traces        | Source IP as a span attribute (not a metric label)             |

### Label cardinality table

| Label          | Cardinality                              | Safe?                                      |
| -------------- | ---------------------------------------- | ------------------------------------------ |
| `namespace`    | Low (tens)                               | Yes                                        |
| `trigger`      | Medium (hundreds)                        | Yes                                        |
| `flow`         | Medium (hundreds)                        | Yes                                        |
| `type`         | Very low (~5)                            | Yes                                        |
| `result`       | Very low (~3–5)                          | Yes                                        |
| `phase`        | Very low (~2)                            | Yes                                        |
| `step_type`    | Low (tens)                               | Yes                                        |
| `outcome`      | Very low (2)                             | Yes                                        |
| `source_range` | Medium (/24 buckets, thousands possible) | **Limited — `ip_blocked` metric only**     |
| `source_ip`    | Unbounded                                | **No — use structured access log**         |
| `user_agent`   | Unbounded                                | **No — use structured access log**         |
