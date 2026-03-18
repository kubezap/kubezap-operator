# KubeZap Scale Limitations

**Status**: Decision documented — 2026-03-18

---

## Design Intent

KubeZap is an **orchestration** tool, not a stream processing engine. Its value is in coordinating
multi-step workflows — branching logic, external HTTP calls, retries, wait/escalation steps,
and conditional data transforms — where each event instance requires meaningful coordination
overhead.

It is **not** the right tool for:
- Stateless message transforms at very high throughput (use Kafka Streams or Flink)
- Sub-second per-event latency requirements
- Aggregation, windowing, or stream joins (use Flink)

---

## FlowRun Storage Limits

All FlowRun state is stored in Kubernetes CRDs (etcd). This is intentional — it provides
`kubectl` observability, crash recovery, and Kubernetes-native deduplication with no external
dependencies.

The practical limits:

| Dimension | Practical limit | Notes |
|---|---|---|
| Ingest rate | ~hundreds/min sustained | etcd write throughput and storage quota bound this |
| etcd object size | ~10–50 KB per FlowRun | Spec + full step status + 4 KB body snapshot |
| etcd storage quota | 2 GB default | ~40,000–200,000 FlowRuns before GC must keep pace |
| GC list scan | O(n) per trigger | `enforceMaxFlowRunsByPhase` lists all FlowRuns per trigger on each terminal reconcile (backlog item) |

**At enterprise Kafka scale (10,000+/min)**, KubeZap CRD storage will exhaust etcd unless
GC policies are extremely aggressive (TTL of minutes, maxSucceeded of 1–5). At that volume,
KubeZap is the wrong tool — a dedicated stream processor should handle per-message logic,
and KubeZap should be invoked only for the subset of events requiring true workflow orchestration.

---

## Recommended GC Tuning for High-Volume Triggers

For busy Kafka or webhook triggers, configure aggressive GC on the Trigger:

```yaml
spec:
  flowRunGC:
    maxSucceeded: 10       # keep only last 10 succeeded runs
    maxFailed: 25          # keep last 25 failed for debugging
    ttlAfterSucceeded: 1h  # short TTL for succeeded
    ttlAfterFailed: 24h    # longer window for failed debugging
```

Set operator-level defaults via flags for all triggers:

```
--flowrun-ttl-succeeded=1h
--flowrun-ttl-failed=24h
```

---

## Decision: No External Database

A two-tier storage model (active FlowRuns in etcd, completed records archived to Postgres) was
considered and rejected (2026-03-18). The added operational complexity and mandatory external
dependency are not justified given KubeZap's intended use case. If workflow orchestration at
genuinely high ingest rates is needed, the correct design is to front KubeZap with a stream
processor that filters and aggregates messages before invoking workflows.

---

## CLI History (Planned)

A `kubezap history` CLI command is planned (see schedule section 10) to provide richer querying
of FlowRun history than `kubectl get flowruns` alone — filtering by trigger, phase, time range,
and step outcome. This will query the Kubernetes API using label/field selectors and format the
output ergonomically. It operates within the same CRD-based storage model.
