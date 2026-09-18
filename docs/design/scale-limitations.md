# No External Database for FlowRun History

> Status: Approved
> Date: 2026-03-18
> Related: `docs/architecture.md#scale-limitations`, `docs/api/flowrun.md#garbage-collection`

## Problem

At high sustained ingest rates, FlowRun CRDs stored in etcd risk exhausting etcd's 2 GB default storage quota (~40,000–200,000 FlowRuns) faster than GC can reclaim space.

## Constraints

- No new hard external dependency for a core code path — KubeZap installs today with zero external dependencies.
- Must preserve `kubectl` observability, crash recovery, and Kubernetes-native deduplication for FlowRun state.

## Rejected Alternatives

- **Two-tier storage (active FlowRuns in etcd, completed records archived to Postgres)** — added operational complexity and a mandatory external dependency not justified by KubeZap's orchestration use case; it isn't a high-throughput stream processor (see [architecture.md#scale-limitations](../architecture.md#scale-limitations)).

## Decision

FlowRun state stays entirely in etcd via CRDs, with TTL- and count-based GC (`docs/api/flowrun.md#garbage-collection`) as the only mitigation for storage growth. If genuinely high-throughput ingest is needed, the correct design is fronting KubeZap with a stream processor that filters/aggregates before invoking workflows — not adding a second storage tier to KubeZap itself.
