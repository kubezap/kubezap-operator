# Detect an Integration's `secretRef` Being Repointed to a Different Secret

> Status: Draft
> Related: `internal/gateway/{kafka,amqp,nats}/watcher.go`, `internal/gateway/secretindex`, `internal/controller/integration_controller.go`, `docs/design/secret-rotation-watches.md`

## 1. Problem Statement

Confirmed by tracing the actual code (this record's investigation, not assumed): the kafka/amqp/nats gateway watchers resolve an `Integration`'s `secretRef`/`caSecretRef`/`clientCertSecretRef` fields via an uncached `client.Get` inside `reconcileTrigger` (`internal/gateway/kafka/watcher.go:212-213` and its amqp/nats equivalents), which runs whenever the *Trigger* informer fires or a *Secret* the Trigger currently depends on changes (per `docs/design/secret-rotation-watches.md`). No informer watches `Integration` objects at all — that record explicitly scoped it out as a named follow-up, which is this story.

Consequence: if an operator edits `Integration.spec.kafka.sasl.secretRef` (or the amqp/nats/TLS equivalents) to point at a different Secret, without touching the referencing Trigger, nothing ever notices. `reconcileTrigger` never re-runs, so (a) the stale credential from the *old* Secret keeps being used indefinitely — the same class of staleness bug `secret-rotation-watches.md` fixed for direct content rotation, one level removed — and (b) the gateway's Secret reverse-index still only watches the *old* Secret, so even a subsequent rotation of the *new* Secret's contents also goes undetected, since nothing is watching it either. Webhook is unaffected: its auth config lives directly on the Trigger, with no Integration indirection.

A related, prerequisite gap: the kafka/amqp/nats gateway Roles (`internal/controller/integration_controller.go`, three near-identical rule sets around lines 540/914/1108) currently grant only `get` on `integrations` — no `list`/`watch` — so an `Integration` informer cannot be added without also fixing RBAC, the same shape of prerequisite the Secret-watch design already worked through for `secrets`.

## 2. Constraints

- No CRD schema change.
- RBAC addition scoped to the existing per-namespace gateway Role pattern (`get`/`list`/`watch` on `integrations`, namespace-scoped) — no cluster-wide access, mirroring the `secrets` grant added by `secret-rotation-watches.md`.
- Must reuse the existing `internal/gateway/secretindex` reverse-index type rather than introduce a new package — its implementation is already generic over `types.NamespacedName → types.NamespacedName`, not actually Secret-specific despite the package name/doc comment.
- Must not introduce a parallel "handle Integration change" code path — an Integration-change event must re-invoke the exact same `reconcileTrigger` function real Trigger/Secret events already use, per the existing design's "no parallel path" precedent.
- Webhook gateway is out of scope — it has no Integration dependency.
- How a missing/invalid Secret after a repoint is surfaced (log-only today, confirmed by tracing `reconcileTrigger`'s error handling — no Trigger-status Condition is set from the gateway process) is explicitly **not** changed by this story; that's pre-existing behavior for *any* credential-resolution failure, not something introduced or fixed here.

## 3. Invariants

- Editing `Integration.spec.*.secretRef` (or `caSecretRef`/`clientCertSecretRef`) to point at a different Secret causes every Trigger referencing that Integration to be reprocessed within one informer resync, without requiring the Trigger itself to be touched.
- After such a reprocess, the gateway's Secret reverse-index reflects only the Secrets the Trigger *currently* depends on — the old Secret's watch dependency is dropped automatically as a consequence of `secretindex.Index.Update`'s existing full-replace semantics, not via new cleanup code.
- A Trigger with no `IntegrationRef` (webhook) is never added to the new Integration index — zero behavior change for webhook.
- Reprocessing an Integration-change event never makes a live API call beyond what `reconcileTrigger` already made for a real Trigger/Secret event (uses the already-synced Trigger informer's local store to look up affected Triggers, same as the Secret-event path).

## 4. Rejected Alternatives

**Build a new, Integration-specific reverse-index type instead of reusing `secretindex.Index`.** Rejected: `secretindex.Index`'s implementation has no Secret-specific types anywhere — it's `map[NamespacedName]map[NamespacedName]struct{}` in both directions. A second instance of the same type, keyed by Integration instead of Secret, is a direct fit; a new type would duplicate ~90 lines of already-tested concurrency-safe code for no behavioral difference. (The package's doc comment and name are Secret-specific; renaming the package to something more generic like `revindex` was considered and rejected as unnecessary churn to every existing caller for a cosmetic improvement — a one-line doc-comment update noting the second use is enough.)

**Also fix the "missing/invalid Secret after repoint is only logged, not surfaced as a Condition" gap in this same story.** Rejected as scope creep: that's a pre-existing limitation of *all* credential-resolution failures in these watchers (confirmed via investigation above), not specific to the repoint scenario this story targets, and fixing it means deciding how a gateway process (which today has no RBAC to patch Trigger/status) would surface a Condition at all — a separate design question. Logged as a candidate follow-up in the project's backlog instead.

**Poll `Integration` objects periodically instead of adding a third informer.** Rejected on the same grounds `secret-rotation-watches.md` already rejected polling for Secrets: strictly worse on every axis given a working informer-based pattern already exists on the exact same cache to extend.

## 5. Tradeoffs

- A third informer (`Integration`) on each of the three broker watchers' existing `crcache.Cache`, plus a second small reverse-index map — bounded by the number of distinct Integrations referenced by enabled Triggers, the same class of bound already accepted for the Trigger and Secret caches.
- kafka/amqp/nats gateways gain `integrations: list/watch` RBAC where they previously had only `get` — fixes a real prerequisite gap, not a new grant for a new purpose, but is a materially larger permission footprint than today's `get`-only.
- The pre-existing "credential-resolution failures are log-only, no Trigger Condition" limitation remains after this story ships — a real UX gap (an operator repointing to a nonexistent Secret gets no CR-visible signal, only gateway logs), explicitly deferred rather than silently left unnoticed.

## 6. Final Decision

Confirmed via code investigation that a real gap exists (not a no-op): add a third informer for `automationv1alpha1.Integration` to each of the kafka/amqp/nats watchers' existing cache, alongside a second `secretindex.Index` instance (reused as-is, keyed by Integration `NamespacedName` instead of Secret) that's updated every time `reconcileTrigger` runs — mirroring exactly the Trigger→Secret reverse-index pattern `secret-rotation-watches.md` already established. On an Integration add/update event, look up affected Trigger keys via this new index and re-invoke `reconcileTrigger` for each, using the already-synced Trigger informer's local store. This single change closes both symptoms at once: the stale-credential bug (re-resolution now actually happens) and the stale-Secret-watch bug (the existing Secret-index `Update()` call inside `reconcileTrigger` naturally drops the old Secret and picks up the new one as a side effect of running again, with no new cleanup logic needed). Fix the prerequisite RBAC gap in the same change: bump `integrations` from `get`-only to `get`/`list`/`watch` in all three gateway Role rule sets in `internal/controller/integration_controller.go`. Webhook gateway is untouched. The separate, pre-existing "failures are log-only" limitation is explicitly deferred, not bundled in.
