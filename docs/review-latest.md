# Review: 2026-03-20

## Doc issues

- **`docs/api/trigger.md` — PubSubTrigger type enum stale**: Listed only `kafka`; AMQP and NATS are implemented. Added `amqp` and `nats` to the enum, and added `routingKey` and `subject` field rows. _(fixed)_
- **`docs/api/trigger.md` — "Pub/Sub — Kafka _(in development)_" header marker**: Kafka pub/sub is fully implemented; `_(in development)_` label removed. _(fixed)_
- **`docs/api/trigger.md` — CronTrigger spec missing `timezone` field**: `spec.cron.timezone` is implemented in the CRD and scheduler but was not documented. Added field row. _(fixed)_
- **`docs/api/trigger.md` — Limitations: cron timezone stale**: Said "Timezone support is planned for a future release." Timezone is now supported. Updated to describe actual behavior. _(fixed)_
- **`docs/architecture.md` — Container Images table incomplete**: Only listed 3 images; AMQP and NATS gateway images (`kubezap/amqp-gateway`, `kubezap/nats-gateway`) were missing. Added both rows and Dockerfile entries. _(fixed)_
- **`docs/overview.md` — Architecture section omitted AMQP/NATS gateways**: Mentioned only webhook and Kafka gateways. Added AMQP and NATS descriptions. _(fixed)_
- **`docs/overview.md` — Core Concepts: trigger types description**: Updated to list AMQP and NATS as built-in rather than plugin-only. _(fixed)_
- **`docs/overview.md` — Roadmap v0.3: "Additional message brokers (AMQP, NATS)" marked `[ ]`**: Both are implemented and in schedule as `[x]`. Corrected to `[x]` and reordered above OLM item. _(fixed)_
- **`docs/design/scale-limitations.md` — CLI History section said "Planned"**: CLI is fully implemented. Removed "planned" language, updated to reference actual CLI docs. _(fixed)_

## Code issues

- **`go.mod` — `github.com/spf13/cobra` placement**: cobra is in the second `require` block (indirect group) without `// indirect` marker. It is a direct CLI dependency and should be in the first block. Minor cosmetic issue; functionally harmless. Not fixed here — decision deferred to owner (see Decisions needed). Low severity.
- No new bugs, nil dereferences, or controller-runtime antipatterns found in the reconcilers read.
- Build passes cleanly: `go build ./...` — 0 errors.

## Schedule corrections

- No changes to `docs/schedule.md` — all items are correctly marked.
- `docs/overview.md` roadmap corrected: AMQP/NATS moved to `[x]`.

## Decisions needed

- **AMQP/NATS integration.md status label**: Should the "beta" label be promoted to "Available" in the CRD overview table? (See `docs/tech-debt/pending-input-required.md`)
- **`go.mod` cobra placement**: Move cobra to first direct-deps block, or leave for `go mod tidy` to normalize? (See `docs/tech-debt/pending-input-required.md`)

## Files changed

- `docs/api/trigger.md` — CronTrigger `timezone` field, PubSubTrigger enum + AMQP/NATS fields, header marker, limitations
- `docs/architecture.md` — Container Images table + Dockerfile list
- `docs/overview.md` — Architecture section, Core Concepts, Roadmap v0.3
- `docs/design/scale-limitations.md` — CLI History section
- `docs/tech-debt/pending-input-required.md` — 2026-03-20 review decisions appended
- `docs/review-latest.md` — this file
