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

---

## Review 2026-03-20

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Should AMQP and NATS gateways be promoted to beta/stable in `docs/api/integration.md` and the overview, or remain undocumented-in-overview?**
Why it matters: The gateways are implemented and marked `[x]` complete in schedule.md, but `docs/overview.md` architecture section previously omitted them, and `docs/api/trigger.md` PubSubTrigger only listed `kafka` — both have been fixed in this review. The remaining question is whether the integration.md "beta" label on AMQP/NATS is still appropriate or should be bumped to "available".
**Answer (2026-03-20): Keep "beta" label — conservative, no reported production use.**

<!-- ANSWERED -->
**Q: `go.mod` has `github.com/spf13/cobra` listed in the second `require` block (where `go mod tidy` puts transitive deps) without the `// indirect` marker. Should it be moved to the first direct-deps block?**
Why it matters: cobra is a direct CLI dependency; its placement in the second block is cosmetically odd and could confuse future maintainers running `go mod tidy`, which may reorder it unexpectedly.
**Answer (2026-03-20): Run `go mod tidy` to let tooling normalize it.**

## Review 2026-03-20 (doc focus)

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Should user guides be created for AMQP and NATS gateway setup (mirroring the Kafka enrichment guide)?**
Why it matters: AMQP and NATS gateways are fully implemented and marked `[x]` in schedule.md, but there are no user guides for setting them up beyond the API reference. Users who want to use these brokers have to piece together the steps themselves. The Kafka enrichment guide is the model.
Options: Create `docs/guides/amqp-setup.md` and `docs/guides/nats-setup.md` now (full guides, similar to kafka-enrichment.md) / Add TODO stubs and defer until the beta label is promoted / Leave undocumented (API docs in integration.md are sufficient for experienced users)
**Answer (2026-03-20): Create full guides now — `docs/guides/amqp-setup.md` and `docs/guides/nats-setup.md`, mirroring kafka-enrichment.md.**

## Review 2026-03-20 (MockEndpoint removal)

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Which third-party tool should replace MockEndpoint in demos, tests, and docs?**
Why it matters: MockEndpoint removal (schedule section 11) is blocked on this decision. All demo updates, guide rewrites, and test changes depend on which tool is chosen. The tool will be deployed in-cluster (Kubernetes Deployment + Service) and must handle basic HTTP request capture/inspection.
Options: WireMock (Helm chart available, industry standard, Java-based, full standalone mode) / Mockoon (Docker image, simpler to configure, Node.js-based) / MockServer (Helm chart, Java, rich expectation API) / other (specify)
**Answer (2026-03-20): Use Mockoon — Docker image, simpler to configure, Node.js-based.**
