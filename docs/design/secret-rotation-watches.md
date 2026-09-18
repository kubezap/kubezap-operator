# Secret rotation detection for gateway watchers

> Status: Approved
> Date: 2026-09-11
> Related: `internal/gateway/{webhook,kafka,amqp,nats}/watcher.go`, `internal/gateway/secretindex`

## Problem

Webhook Trigger auth secrets and Kafka/AMQP/NATS broker credentials are read once, via an uncached `client.Get`, when the gateway processes a Trigger add/update — then held in memory indefinitely (webhook's `RouteRegistry` entry, or the broker gateways' `subscription` entry). No watcher anywhere sets up a `Secret` watch. If an external rotation mechanism (e.g. ESO) updates a Secret without touching the referencing Trigger/Integration, the gateway keeps using the stale credential until something else triggers reconcile or the pod restarts — webhook requests signed with the new key are rejected, and broker connections eventually fail auth and silently stop receiving events. A more severe, related bug found while investigating this: the kafka/amqp/nats gateways' shared `kubezap-gateway` Role grants no `secrets` permission at all, so `readSecretKey`'s `client.Get` for SASL/TLS secrets must already be failing `Forbidden` in any real cluster with those fields set — unnoticed because every live E2E run used an unauthenticated Strimzi cluster. This design fixes both together, since the RBAC gap is a strict prerequisite for the informer.

## Constraints

- RBAC minimum: `get`/`list`/`watch` on `secrets`, scoped to the gateway's own namespace via the existing per-namespace `Role`/`RoleBinding` pattern — no cluster-wide access.
- No new external dependency — reuses the same `sigs.k8s.io/controller-runtime/pkg/cache` each watcher already constructs for its `Trigger` informer, adding a second informer for `corev1.Secret` on the same cache.
- Must not change existing dedup/idempotency behavior for real Trigger/Integration spec changes — the kafka/amqp/nats "no change, skip restart" fast path in `reconcileTrigger` must keep working for changes unrelated to credentials.
- Must not require watching `Integration` objects — kafka/amqp/nats credentials resolve via `Trigger.Spec.Kafka.IntegrationRef` → `Integration.Spec.Kafka.{SASL,TLS}.*SecretRef` on every Trigger event; Integration spec drift is a different, rarer gap, out of scope here.
- Must be safe for concurrent access — the Secret-change and Trigger-change callbacks can fire on different goroutines; the reverse index must be safe for concurrent read/write.
- A Secret referenced by at least one `Enabled` Trigger (directly for webhook auth, or indirectly via an Integration for kafka/amqp/nats) is watched, and any `.data` change reprocesses every referencing Trigger within one informer resync.
- Stale reverse-index entries (Triggers deleted, disabled, or no longer referencing a Secret) don't accumulate unboundedly.
- Reprocessing on a Secret change goes through the exact same code path as a real Trigger spec change (`handleTrigger` for webhook, `reconcileTrigger` for kafka/amqp/nats) — no parallel "credential refresh" path.
- A credential-only Secret change must still tear down and restart the consumer with new credentials — the existing no-op fast path must not swallow a rotation.
- No Secret value is ever logged, matching the existing `RouteEntry.redactedForLog()` convention.

## Rejected Alternatives

- **Poll Secrets periodically instead of watching** — strictly worse on every axis: adds rotation-detection latency up to the poll interval and constant API load even when nothing changes, when a working watch pattern (the `Trigger` informer) already exists to extend.
- **Also watch `Integration` objects** to detect a changed `secretRef` pointer (not just Secret contents) — a real but different gap with its own reverse-indexing question; bundling it doubles this design's scope for a problem not asked about here, left as a named follow-up.
- **Extract a shared `internal/gateway/secretwatch` package** used by all four watchers rather than native duplication — the four watchers' credential shapes aren't actually identical at the point the index needs building (webhook has 6 inline auth-type branches; kafka/amqp/nats resolve through an Integration's SASL/TLS config), so a shared abstraction would need a callback-heavy interface costing more than the ~30 lines of boilerplate it'd save; fits the existing "duplicate watcher-lifecycle code, share only pure helpers" pattern (`internal/gateway/redact`) rather than breaking it.

## Decision

Add a second informer (`corev1.Secret`, same `crcache.Cache` each watcher already owns) to all four gateway watchers. Each watcher maintains its own in-process reverse index (`map[secret NamespacedName][]trigger NamespacedName`, mutex-protected), rebuilt for a Trigger every time it's reconciled. On a Secret add/update event, look up affected Trigger keys and re-invoke the same per-Trigger reconcile function used for real spec changes, using the Trigger informer's already-synced local store (no extra API call). For kafka/amqp/nats, extend the `subscription` struct with a `credentialFingerprint` (SHA-256 of resolved SASL/TLS material) alongside `topic`/`consumerGroup`/`integrationName`, so the existing fast path detects credential-only rotation instead of ignoring it. Fix the prerequisite RBAC gap in the same change: add `secrets: get/list/watch` to `desiredKafkaGatewayRole`/`desiredAmqpGatewayRole`/`desiredNatsGatewayRole` (currently absent) and upgrade the webhook gateway's `secrets: get` to `get/list/watch` (`desiredWebhookGatewayRole`). Integration-object watching is explicitly out of scope, filed as a follow-up.

- Additional informer cache for `Secret` plus a small reverse-index map — bounded by the number of distinct Secrets referenced by enabled Triggers/Integrations, same class of bound already accepted for the `Trigger` cache.
- kafka/amqp/nats gateways gain real `secrets: get/list/watch` RBAC where they previously had none — fixes an existing bug (the code already assumed `get` worked), not a new grant for a new purpose, but materially larger than today's zero access.
- Credential-fingerprint hashing adds negligible CPU per Trigger reconcile next to the network I/O already happening in the same path.
