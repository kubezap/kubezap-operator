# Technical Debt — Pending User Input

> Items from the HIGH priority debt list that require design decisions before implementation can proceed.
> Researched on 2026-03-16. See `docs/schedule.md` section 8 for the full debt list.

---

## A. Kafka Watcher: Polling Loop → Informer-Based Watch

**Schedule entry:** `internal/gateway/kafka/watcher.go` uses a 30s ticker instead of an informer.

### Research Findings

The current `internal/gateway/kafka/watcher.go` polls every 30 seconds via a `time.NewTicker`:
- `Start()` calls `reconcileTriggers()` immediately then every 30s (lines 62, 66–67)
- `reconcileTriggers()` lists ALL Kafka Triggers in the namespace each poll
- New/deleted/modified Triggers are not reflected until the next tick (up to 30s delay)

The webhook watcher (`internal/gateway/webhook/watcher.go`) already shows the correct pattern:
- Uses a controller-runtime informer cache
- Registers `AddFunc`/`UpdateFunc`/`DeleteFunc` event handlers
- Reacts to Trigger changes in <100ms

The same 30s polling pattern exists in `internal/gateway/nats/watcher.go` and `internal/gateway/amqp/watcher.go` (both not yet implemented, have TODO comments about this).

### Migration Scope

- Refactor `NewWatcher()` to initialize a controller-runtime cache (like webhook watcher lines 40–62)
- Refactor `Start()` to register Add/Update/Delete event handlers
- Extract event handlers: `onTriggerAdd`, `onTriggerUpdate`, `onTriggerDelete`
- `startSubscription()`, `stopSubscription()`, `readSecretKey()` remain unchanged
- Remove ticker and polling loop (~lines 55–151)
- Apply same pattern to NATS and AMQP watchers when those are implemented

**Estimated effort:** 4–6 hours for Kafka; 2–3 hours each for NATS/AMQP when implemented.

### Decision Needed

1. **Prioritize now or defer?** The 30s delay is a correctness issue (messages consumed with no flowRef) but not a crash. Defer until after current debt sprint, or fix now?
2. **Scope**: Fix Kafka only, or plan NATS/AMQP at the same time (since they share the same polling pattern)?

---

## B. `basic` Auth Full Implementation

**Schedule entry:** `WebhookAuth.Type` includes `basic` but it is not implemented in `authenticateRequest`. (Note: the silent security bypass — `default` case allowing requests — has been fixed separately to fail-closed.)

### Research Findings

`api/v1alpha1/trigger_types.go`:
- `WebhookAuth.Type` enum includes `basic` (line 186)
- There is a comment reference at line 201–202 for basic auth but **no `WebhookAuth.Basic` struct field exists** — the type has no spec for how credentials are stored

`internal/gateway/webhook/handler.go`:
- `authenticateRequest` has no `case "basic":`
- After the security fix, `default` now returns 401 (fail-closed)

`internal/gateway/webhook/watcher.go`:
- `buildRouteEntry` has no `case "basic":` for populating the `RouteEntry`

### Decision Needed

Need to agree on the credential storage shape before implementing:

**Option 1: Single secret with `username:password` format**
```yaml
spec:
  webhook:
    auth:
      type: basic
      basic:
        secretRef:
          name: my-webhook-credentials
          # Secret must have key "credentials" = "username:password"
```

**Option 2: Separate secret keys**
```yaml
spec:
  webhook:
    auth:
      type: basic
      basic:
        secretRef:
          name: my-webhook-credentials
          usernameKey: username   # default: "username"
          passwordKey: password   # default: "password"
```

**Option 3: bcrypt/htpasswd format** (more secure, matches nginx/traefik conventions)
```yaml
spec:
  webhook:
    auth:
      type: basic
      basic:
        secretRef:
          name: my-webhook-credentials
          # Secret key "auth" = htpasswd format (bcrypt hashed)
```

**Recommendation:** Option 2 is the most Kubernetes-idiomatic (matches how most operators handle username/password secrets). Option 3 is more secure but adds a bcrypt dependency and is less user-friendly for quick setups.

---

## C. `mtls` Auth Full Implementation

**Schedule entry:** `WebhookAuth.Type` includes `mtls` but it is not implemented. (Note: the silent security bypass — `default` case allowing requests — has been fixed separately to fail-closed.)

### Research Findings

mTLS is fundamentally different from other auth types — it cannot be implemented in the HTTP handler. It requires:
1. Configuring the TLS listener with `tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: caPool}`
2. The CA cert pool populated per-route (or shared)
3. The handler reads `r.TLS.VerifiedChains` or `r.TLS.PeerCertificates` to confirm which cert was presented

The current webhook gateway starts an HTTP server (not HTTPS — check `cmd/webhook-gateway/main.go` to confirm). mTLS requires the server to be TLS-enabled.

### Decision Needed

This is a significant design decision touching the server bootstrap:

1. **Is the webhook gateway currently HTTP or HTTPS?** If HTTP-only, mTLS requires adding TLS termination (cert + key for the server itself, plus CA cert for client verification). How is the server certificate managed — self-signed, cert-manager, user-provided?

2. **Per-route vs server-wide mTLS?** Go's TLS stack applies `ClientAuth` server-wide, not per-route. Per-route CA validation requires reading the peer certificate in the handler and checking against a per-route CA allowlist. The server must be configured with `ClientAuth: tls.RequestClientCert` globally, and enforcement is per-handler.

3. **Certificate storage**: Where does the CA cert live? Options:
   - `WebhookAuth.MTLS.CASecretRef` pointing to a Secret with `ca.crt` key
   - A ClusterIssuer/Issuer (cert-manager integration)
   - A ConfigMap (CAs are public so don't need Secret)

4. **Scope**: Is mTLS a priority for v0.2, or defer to v0.3 (post-Helm chart)?

---

## D. Stale FlowRef in Cron Closure — Confirmed Non-Issue

**Schedule entry:** `cron_scheduler.go` cron job closure captures `flowRef` by value at registration time.

### Research Findings

**This item can be closed.** The closure captures `flowRef` as a Go string value (line 72: `flowRef = trigger.Spec.FlowRef.Name`). Go strings are immutable value types — the closure holds a copy. When the Trigger's FlowRef is updated, the TriggerReconciler calls `Register()` which removes and re-adds the cron entry with the new value (trigger_controller.go line 95). The window between the last tick with the old value and the new registration is one cron interval at most — not a correctness bug, just a bounded staleness window inherent to any reconcile-based system.

**Recommendation:** Mark this item as `[x]` in schedule.md with note "confirmed safe — string value capture, reconciler re-registers on update."
