# FlowRun Storage Architecture

**Status**: Decision Finalised — 2026-03-18
**Deciders**: Project owner + Claude (architecture advisor)

---

## Problem Statement

The current design stores every `FlowRun` resource — both active and completed — as a Kubernetes CRD object in etcd. At the target enterprise scale (10,000+ FlowRuns/min from Kafka gateway triggers) this creates three compounding problems:

1. **etcd write pressure** — each FlowRun generates 7–12 API server writes (create → Running → per-step phase transitions → terminal phase → GC delete). At 10,000 FlowRuns/min with 5 steps each, that is ~100,000+ status patch writes/min. etcd is designed for configuration storage, not high-throughput event streams.

2. **GC inefficiency** — `enforceMaxFlowRunsByPhase` does a full label-indexed list of all FlowRuns per trigger on every terminal reconcile. Under high volume this is an unbounded O(n) list scan that grows without bound unless GC is aggressive enough to keep pace with ingest.

3. **etcd storage quota** — each FlowRun object (spec + full step status + trigger body snapshot) is 10–50 KB. 10,000 FlowRuns/min retained for 24 hours = 864M FlowRuns × ~20 KB average = far beyond the default 2 GB etcd quota. Even with TTL GC, the volume of writes itself causes etcd compaction pressure.

---

## Two Distinct Concerns Conflated in One Type

The root cause is that the `FlowRun` CRD serves two fundamentally different purposes:

| Concern | Phase | Needs |
|---------|-------|-------|
| **Work dispatch** | Pending, Running, Waiting | Watch semantics, low-latency access, crash recovery |
| **Audit / history** | Succeeded, Failed, Cancelled | Query capability, long retention, does not need Kubernetes watch |

etcd (via CRDs) is the correct store for the first concern. It is the wrong store for the second. At low volumes, conflating them is fine — this is how Kubernetes Jobs, CronJobs, and many operators work. At enterprise Kafka scale it breaks.

---

## Is This Apache Flink / Kafka Streams Territory?

**Short answer: No — these are different tool classes with different value propositions.**

| Dimension | KubeZap | Flink / Kafka Streams |
|---|---|---|
| Unit of work | Orchestrated workflow (N steps, HTTP calls, retries, waits) | Message transform / aggregate |
| Acceptable latency | Seconds to minutes per execution | Sub-millisecond |
| Execution model | Stateful, persistent, resumable | Stateless stream pipeline |
| Step side effects | External HTTP calls, Slack, DB writes, publish | Stateless map/filter/window |
| Observability | Per-instance audit trail | Aggregate metrics |

KubeZap adds value when the workflow has meaningful **coordination logic**: conditional branching, external API calls, retry policies, wait/escalation steps. A 10-step workflow that calls three external APIs, waits 1 hour for acknowledgement, and publishes to two topics on success is exactly the KubeZap use case — even at 10,000/min.

If a user wants to apply a simple stateless transform to every Kafka message at 10,000/min, they should use Kafka Streams or Flink. KubeZap is the right tool when each message triggers a **workflow instance** with meaningful coordination overhead.

One implication: at 10,000 FlowRuns/min with multi-step flows, the **controller concurrency model** also matters — 10,000 in-flight goroutines executing HTTP steps in parallel is a separate concern from storage (tracked separately under the non-blocking execution tech debt item, which has been resolved via per-FlowRun goroutines). The storage design below assumes the controller can maintain adequate execution concurrency; etcd/storage pressure should not be the bottleneck.

---

## Decision: Two-Tier Storage (Active CRD + Postgres Archive)

### Architecture

```
  Kafka Message  ──► kafka-gateway  ──► create FlowRun CRD (active tier)
  Webhook        ──► webhook-gateway ──► create FlowRun CRD (active tier)
  Cron           ──► controller      ──► create FlowRun CRD (active tier)

                      controller watches FlowRun CRDs
                      executes steps, updates CRD status in-flight
                      on terminal phase:
                        1. write complete record to Postgres (archive tier)
                        2. delete the CRD

  Query: `kubezap history` / web UI ──► Postgres (SQL, indexed, retained)
  Active: `kubectl get flowruns`     ──► etcd (bounded to in-flight set)
```

### Active Tier (etcd/CRD) — unchanged semantics

FlowRun CRDs continue to be created by gateways exactly as today. The controller watches them, executes steps, and updates status during execution. **No changes to gateway code, FlowRun spec, or the watch/reconcile loop.**

Phases that remain in etcd: `Pending`, `Running`, `Waiting`.

Key property: at any point in time, the number of active FlowRuns in etcd is bounded by `(ingest rate) × (average execution duration)`. At 10,000/min with an average execution time of 5 seconds, the steady-state active set is ~833 objects — trivial for etcd.

### Archive Tier (PostgreSQL) — new

When the controller transitions a FlowRun to a terminal phase (`Succeeded`, `Failed`, `Cancelled`), it:

1. Writes the complete FlowRun record (spec + final status + all step statuses) to Postgres.
2. Deletes the CRD.

This is a single transaction from the controller's perspective. If the Postgres write fails, the CRD is **not** deleted and the controller retries on the next reconcile (idempotent by `UNIQUE(namespace, name)` on the Postgres table). If the CRD delete fails after a successful Postgres write, the next reconcile detects the terminal phase, finds the existing Postgres record (via `ON CONFLICT DO NOTHING`), and retries the CRD delete.

### Retention Policy in Postgres

Both existing GC policies translate cleanly and become more efficient:

**TTL-based GC:**

```sql
-- Run periodically (e.g., every 5 minutes via background goroutine)
DELETE FROM flow_runs
WHERE retain = false
  AND completion_time < NOW() - (ttl_after_finished_seconds || ' seconds')::interval;
```

Priority order is preserved: per-FlowRun TTL column overrides trigger-level TTL, which overrides operator default.

**Count-based GC:**

```sql
-- Prune to maxSucceeded for a trigger
DELETE FROM flow_runs
WHERE namespace = $1 AND trigger_name = $2 AND phase = 'Succeeded' AND retain = false
  AND id NOT IN (
    SELECT id FROM flow_runs
    WHERE namespace = $1 AND trigger_name = $2 AND phase = 'Succeeded'
    ORDER BY completion_time DESC
    LIMIT $3  -- maxSucceeded
  );
```

This is a single indexed query versus the current O(n) full list scan + N individual delete calls. At high volume this is dramatically more efficient.

**`kubezap.io/retain=true` annotation** → `retain = true` column. Excluded from all GC queries.

### Postgres Schema

```sql
CREATE TABLE flow_runs (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL,
    namespace            TEXT NOT NULL,
    trigger_name         TEXT,
    trigger_type         TEXT,
    flow_name            TEXT NOT NULL,
    phase                TEXT NOT NULL,   -- Succeeded | Failed | Cancelled
    params               JSONB,
    trigger_data         JSONB,
    steps                JSONB,           -- []StepRunStatus serialised
    conditions           JSONB,
    start_time           TIMESTAMPTZ,
    completion_time      TIMESTAMPTZ NOT NULL,
    ttl_after_finished_seconds BIGINT,   -- NULL = use operator default
    retain               BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (namespace, name)
);

CREATE INDEX ON flow_runs (namespace, trigger_name, completion_time DESC);
CREATE INDEX ON flow_runs (namespace, flow_name, completion_time DESC);
CREATE INDEX ON flow_runs (namespace, phase, completion_time DESC);
CREATE INDEX ON flow_runs (completion_time DESC) WHERE retain = false;
```

### Kafka Deduplication — Unaffected

The Kafka dedup guarantee (`<trigger>-p<partition>-offset-<offset>` name collision → 409 Conflict) continues to work. The CRD exists for the full duration of execution. On redelivery after completion: the CRD is gone but the Postgres `UNIQUE(namespace, name)` constraint provides a second line of defence.

### Wait Step — Unaffected

`Waiting` phase FlowRuns stay in etcd until the `resumeAfter` timer fires. `Waiting` is an active state, not terminal. The controller requeues and resumes from CRD state as today.

### Crash Recovery — Unaffected

In-flight FlowRuns (Pending, Running, Waiting) remain in etcd throughout execution. Controller restart re-lists and reconciles them as today. There is no recovery scenario that requires reading completed FlowRun state from Postgres — the controller only ever needs to resume from active state.

---

## Deployment Tiers

### Tier 1: Standalone (no Postgres) — default

No `--flowrun-archive-dsn` flag set. Behaviour is identical to current design. TTL + count-based CRD GC continues to apply. Suitable for development, small-scale production.

### Tier 2: Archive enabled (Postgres configured)

`--flowrun-archive-dsn` set to a PostgreSQL connection string. On terminal phase transition, controller archives to Postgres then deletes the CRD. CRD GC is bypassed for completed FlowRuns (Postgres GC takes over). `kubectl get flowruns` shows active/recent runs only.

The operator does **not** provision the Postgres instance. Supported options:
- User-provided external Postgres (RDS, Azure Database, Cloud SQL, etc.)
- [CloudNativePG](https://cloudnative-pg.io/) operator (recommended on-cluster)
- [CrunchyData PGO](https://access.crunchydata.com/documentation/postgres-operator/)

Connection string is provided via a referenced Kubernetes Secret to avoid plaintext DSN in operator flags.

---

## FlowRun CRD: What Changes, What Doesn't

| Aspect | Change |
|--------|--------|
| FlowRun spec fields | **None** — spec is identical |
| FlowRun status fields | **None** — status is identical during execution |
| Gateway FlowRun creation | **None** |
| Controller reconcile loop | **New**: archive + delete on terminal phase (when Postgres configured) |
| GC logic (`enforceFlowRunGCPolicy`) | **Unchanged** in standalone tier; **bypassed** in archive tier |
| `kubectl get flowruns` | Shows active runs only in archive tier; full history in standalone tier |
| `kubezap.io/retain=true` annotation | **Preserved** — mapped to `retain=true` in Postgres |
| `spec.ttlAfterFinished` | **Preserved** — mapped to `ttl_after_finished_seconds` in Postgres |
| `spec.flowRunGC` on Trigger | **Preserved** — used by Postgres GC queries |

---

## What Is Not Changing

- The `FlowRun` CRD and its Go types remain. The CRD is the correct work-dispatch primitive.
- All gateway code is unchanged. Gateways write CRDs as today.
- The controller's step execution engine is unchanged.
- The CEL evaluation, retry, wait, and step result passing all operate on CRD state during execution — unchanged.
- The observability layer (Prometheus metrics, OTel traces) is unchanged.

---

## Tradeoff Summary

| Concern | CRD-only (current) | Two-tier archive |
|---|---|---|
| etcd writes at 10k/min | ❌ ~100k/min, approaches limits | ✅ bounded to active set (~800 objects) |
| etcd storage quota | ❌ exhausted within hours at scale | ✅ only active FlowRuns in etcd |
| GC efficiency | ❌ O(n) full list scan + N deletes | ✅ O(log n) indexed SQL |
| SQL queryability / reporting | ❌ not available | ✅ full SQL on JSONB fields |
| kubectl history | ✅ full history | Active only (per design decision) |
| Crash recovery | ✅ | ✅ unaffected |
| Kafka dedup | ✅ | ✅ unaffected (CRD exists during execution) |
| External dependency | None | Postgres (optional per tier) |
| Operator complexity | Low | Medium (+archive writer, +Postgres GC goroutine) |

---

## Open Questions (deferred)

1. **CLI/UI access to history** — `kubezap history` CLI command or web UI that queries Postgres. Not in immediate scope; documented as a follow-on.
2. **Cross-namespace history** — Postgres enables multi-namespace queries (e.g., "all failed FlowRuns for trigger X across all namespaces"). CRDs cannot do this without ClusterRole access. Postgres makes this naturally possible.
3. **Long-term analytics** — JSONB step results in Postgres enable aggregate queries (e.g., average duration per flow, failure rate by trigger). Future web UI concern.
4. **Postgres schema migrations** — operator manages schema via embedded migration on startup (e.g., `golang-migrate/migrate`). Schema versioned alongside operator. Out of scope for initial implementation.
