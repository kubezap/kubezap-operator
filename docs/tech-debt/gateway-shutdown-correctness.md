# Tech Debt: Gateway Shutdown Correctness (AMQP & NATS)

> Identified: 2026-03-18 (codebase review)
> Severity: HIGH
> Affects: `internal/gateway/amqp/watcher.go`, `internal/gateway/nats/watcher.go`

---

## Summary

The AMQP and NATS gateways have two related shutdown-correctness issues: incorrect context propagation that prevents clean pod shutdown, and insecure temporary credential file handling. Both issues were fixed in the Kafka gateway but not ported forward when AMQP and NATS were implemented.

---

## Issue 1: Subscription Context Not Derived from Caller

### Location

| File | Line |
|------|------|
| `internal/gateway/amqp/watcher.go` | ~168 in `startSubscription` |
| `internal/gateway/nats/watcher.go` | ~233 in `startSubscription` |

### Problem

Both `startSubscription` functions create a detached context:

```go
// WRONG — current code in both amqp and nats watcher
subCtx, cancel := context.WithCancel(context.Background())
```

The caller's `ctx` (passed down from `watcher.Start(ctx)`) is derived from the pod's signal context. When Kubernetes sends `SIGTERM`, the signal context is cancelled, which propagates down to `watcher.Start(ctx)` — but NOT into the subscription goroutines, because they hold a `context.Background()`-derived context that is never cancelled by the shutdown signal.

**Consequences:**
- AMQP and NATS subscriptions continue running after pod receives `SIGTERM`
- Kubernetes waits for the pod's `terminationGracePeriodSeconds` before force-killing
- Subscriptions may receive and begin processing messages during this window without the ability to commit/ack cleanly
- Graceful drain of in-flight messages does not work as designed

**Comparison:** The Kafka gateway had the same bug and was fixed (schedule.md line 265):
```go
// CORRECT — kafka/watcher.go
subCtx, cancel := context.WithCancel(ctx)
```

### Fix

Change both occurrences from `context.WithCancel(context.Background())` to `context.WithCancel(ctx)`. This is a one-line change in each file.

---

## Issue 2: Polling Loop Instead of Informer-Based Watch

### Location

| File | Lines |
|------|-------|
| `internal/gateway/amqp/watcher.go` | ~54–75 in `Start` |
| `internal/gateway/nats/watcher.go` | ~53–74 in `Start` |

### Problem

Both watchers use a 30-second ticker to poll for Trigger changes:

```go
ticker := time.NewTicker(30 * time.Second)
for {
    select {
    case <-ticker.C:
        // re-list triggers and reconcile subscriptions
    case <-ctx.Done():
        return
    }
}
```

This means:
- When a `Trigger` is created or deleted, the AMQP/NATS gateway takes up to 30 seconds to react
- During this window, events on new topics are missed; deleted topics are still consumed
- This is the same issue that was fixed in the Kafka gateway (schedule.md line 236)

**Comparison:** The Kafka gateway was refactored to informer/cache pattern post-debt-sprint. The AMQP/NATS implementations were authored after that fix but did not adopt the pattern.

### Fix

Refactor both watchers to use `controller-runtime` informer/cache, mirroring `internal/gateway/kafka/watcher.go`. Key changes:
1. Replace the ticker loop with a `cache.Cache` and `AddEventHandler`
2. Use `WaitForCacheSync` before starting subscriptions
3. Register `OnAdd`, `OnUpdate`, `OnDelete` handlers to manage subscriptions reactively

---

## Issue 3: Temporary Credential Files in `/tmp`

### Location

| File | Lines |
|------|-------|
| `internal/gateway/nats/watcher.go` | ~207–210 in `startSubscription` |

### Problem

NATS NKey/JWT credentials are written to predictable paths in `/tmp`:

```go
credsFile := fmt.Sprintf("/tmp/nats-creds-%s.creds", trigger.Name)
os.WriteFile(credsFile, credContent, 0600)
```

Issues:
1. **Predictable path** — `/tmp/nats-creds-<trigger-name>.creds` is guessable; in a shared-PID-namespace scenario another process could read it
2. **Name collision** — two triggers with the same name (different namespaces) write to the same file path, causing a race condition and credential cross-contamination
3. **No cleanup** — the file is never deleted when the subscription is stopped; credentials persist on disk until pod restarts

Note: AMQP credentials are passed via connection URL/SASL parameters and do not have this issue.

### Fix

Replace `os.WriteFile` with `os.CreateTemp("", "nats-creds-*.creds")` to get a randomly-suffixed path. Track the temp file path in the subscription state and `os.Remove` it in the cleanup/cancellation path. Alternatively, if the NATS Go client supports in-memory NKey loading (via `nats.NkeyOptionFromSeed` or similar), avoid the file entirely.

---

## Resolution Order

1. **Context propagation** (Issue 1) — one-line fix per file; do immediately
2. **Temp file handling** (Issue 3) — small fix, good security hygiene
3. **Polling → informer refactor** (Issue 2) — larger change; mirrors kafka/watcher.go; do as part of AMQP/NATS stabilization sprint

---

## Related Schedule Items

- `docs/schedule.md` section 8 backlog: polling loop item (line 260)
- Context propagation and temp file issues are **new** (not previously tracked)
