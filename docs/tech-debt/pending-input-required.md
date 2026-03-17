# Technical Debt — Pending User Input

> Items from the HIGH priority debt list that require design decisions before implementation can proceed.
> Researched on 2026-03-16. See `docs/schedule.md` section 8 for the full debt list.

---

## A. Kafka Watcher: Polling Loop → Informer-Based Watch

**Decision (2026-03-16): Fix now. Scope = Kafka only.**

NATS and AMQP watchers will get the same fix when those gateways are implemented.

---

## B. `basic` Auth Full Implementation

**Decision (2026-03-16): Implement. Use Option 2 (separate `usernameKey`/`passwordKey` in secret).**

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

---

## C. `mtls` Auth / TLS Termination

**Decision (2026-03-16):**
- Remove `mtls` from `WebhookAuth.Type` enum — mTLS does not apply to HTTP webhooks.
- mTLS belongs to broker gateways (Kafka, AMQP, NATS) where mutual authentication is meaningful.
- Add **TLS termination** to the webhook gateway server (server cert + key) as a separate concern, independent of auth. Use case: end-to-end TLS, OpenShift re-encrypt routes.
- Server TLS cert managed via user-provided Secret (cert-manager compatible); not a `WebhookAuth` type.

---

## D. Stale FlowRef in Cron Closure — Confirmed Non-Issue

**Decision (2026-03-16): Close. Mark `[x]` in schedule.md.**

Go string value capture is immutable. Reconciler re-registers on update. Bounded staleness window is inherent to any reconcile-based system — not a bug.
