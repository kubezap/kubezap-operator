# AMQP Exchange-Based Routing Support

> Status: Approved
> Related: `api/v1alpha1/trigger_types.go`, `internal/gateway/amqp/watcher.go`, `docs/api/trigger.md`, `docs/guides/amqp-setup.md`, `planning/backlog/follow-ups.md` (2026-09-20 entry)

## 1. Problem Statement

`docs/api/trigger.md`/`docs/guides/amqp-setup.md` previously (incorrectly) documented `spec.amqp.routingKey` as binding the consumed queue to an exchange. The actual implementation (`internal/gateway/amqp/watcher.go`'s `connect091`) only calls `QueueDeclare` + `Consume` directly on the named queue (`Topic`) — it never declares an exchange or calls `QueueBind`, so `routingKey` is accepted but silently unused for routing (it's read only as an internal per-subscription dedup/log field). The only supported topology today is "one Trigger = one pre-existing/directly-declared named queue," which forces a user who wants exchange-based fan-out or pattern routing to provision exchange bindings entirely outside KubeZap, undocumented and unsupported. The docs were corrected to describe actual behavior in a prior session; this record covers building the real feature the field name and old docs already promised.

## 2. Constraints

- AMQP 1.0 (`connect10` — Azure Service Bus, ActiveMQ Artemis) has no exchange/routing-key concept in the 0-9-1 sense; it uses link-based addressing instead. This feature must be explicitly scoped to `version: "0-9-1"` only, and must reject (with a clear error/condition) rather than silently ignore an exchange config set alongside `version: "1.0"` — silent-ignore is exactly the bug being fixed here, so the new field must not repeat it.
- A Trigger with no exchange configured must behave byte-for-byte identically to today: direct named-queue consumption, `routingKey` still just an internal dedup key.
- No field removal — v1alpha1 API stability; `RoutingKey` is reused as the binding pattern, not replaced.
- Exchange declaration must be idempotent (safe to redeclare on every reconnect, matching `QueueDeclare`'s existing idempotent pattern). A mismatched redeclare (different type against an already-existing, differently-typed exchange) is a real AMQP protocol error the broker surfaces by closing the channel — this must surface as a clear reconnect error, not a silent failure or panic.
- No new external dependency — `ExchangeDeclare`/`QueueBind` are already exposed by the imported `amqp091-go` client.
- RBAC unaffected — purely broker-side protocol behavior, no new Kubernetes permissions.

## 3. Invariants

- A Trigger with no `spec.amqp.exchange` configured is unaffected: same `QueueDeclare` + `Consume` call sequence, same behavior, as before this change.
- A Trigger with `spec.amqp.exchange` set only takes effect on `version: "0-9-1"`; the same field with `version: "1.0"` is rejected at reconcile time with a clear condition/error, never silently ignored.
- When exchange routing is enabled: the exchange is declared idempotently, the queue is declared (named per `Topic`, durable — unchanged), and bound via `QueueBind` using `RoutingKey` as the binding pattern, before `Consume` starts.
- `RoutingKey`'s existing in-memory subscription-dedup-key semantics are preserved in the no-exchange case.

## 4. Rejected Alternatives

- **Leave `routingKey` as a dedup-key-only field and stop at the docs fix.** That was STORY-052-era's stopgap. Rejected now because the follow-up this record closes explicitly asks for the real feature, and shipping a user-facing field that silently does nothing is a worse API than implementing or removing it.
- **Extend exchange/routing-key support to AMQP 1.0 too**, via its link-filter/node-properties mechanisms. Rejected: AMQP 1.0's addressing model is fundamentally different (broker-specific extensions, not a portable exchange/binding concept), and both 1.0 brokers this project documents (Azure Service Bus, ActiveMQ Artemis) already expose topic-like fan-out via their own topic/subscription primitives, addressable directly through the existing `Topic` field — no new field needed there. Scoping this feature to 0-9-1 only avoids inventing a broker-specific abstraction with no real portability benefit.
- **Auto-generate an anonymous/exclusive server-named queue per Trigger when an exchange is configured** (the common RabbitMQ topic-fan-out idiom). Rejected as the default: KubeZap's `Topic` field means "the durable queue name" everywhere else in this project, and FlowRun observability/dedup benefits from a stable queue name across gateway pod restarts — an anonymous queue's name changes every reconnect and RabbitMQ auto-deletes it when the last consumer disconnects, losing messages queued during a gateway restart. `Topic` stays the durable, named queue in all cases; the exchange/`routingKey` combination only changes *how* that named queue receives messages (bound to an exchange vs. consumed directly), never whether it's named.

## 5. Tradeoffs

- Operators wanting RabbitMQ's ephemeral per-consumer fan-out queue idiom still don't get it from this feature — they get a durable named queue bound to an exchange, a deliberately different (more restart-safe, less flexible) shape.
- Adds one more idempotent broker round-trip (`ExchangeDeclare`, then `QueueBind`) to every 0-9-1 reconnect when configured — small, bounded, same cost class as the existing `QueueDeclare` call.
- AMQP 1.0 users get no equivalent from this story — documented as explicitly out of scope, not silently deferred.

## 6. Final Decision

Add an optional `Exchange *AmqpExchangeSpec` field to `AmqpTrigger` (`api/v1alpha1/trigger_types.go`), where `AmqpExchangeSpec` has `Name string` and `Type string` (`direct` | `topic` | `fanout` | `headers`, matching RabbitMQ's native exchange types). When set — valid only for `version: "0-9-1"`, rejected with a clear error for `version: "1.0"` — `connect091` declares the exchange idempotently (`ExchangeDeclare`, durable, matching `QueueDeclare`'s existing durability choice), declares the queue exactly as today (named per `Topic`, durable), and binds it via `QueueBind(topic, routingKey, exchangeName, ...)` before starting `Consume` — reusing the existing `RoutingKey` field as the binding pattern, delivering exactly what the (until now, incorrect) docs already promised. When `Exchange` is unset, behavior is byte-for-byte unchanged from today.
