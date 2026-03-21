# Review: 2026-03-20 (R1 — Multi-Type Interference Audit)

## Audit scope

Read-only audit of interference, shared-resource, and naming-collision risks across multiple trigger types and integration types in the same namespace. Files audited:

- `internal/controller/integration_controller.go` — reconcileKafkaGateway, reconcileAmqpGateway, reconcileNatsGateway
- `internal/controller/trigger_controller.go` — ensureWebhookGateway, cron register/deregister
- `internal/controller/cron_scheduler.go` — entry keying, FlowRun naming
- `internal/gateway/webhook/handler.go` — FlowRun naming, collision handling
- `docs/tech-debt/rbac-ownership-gaps.md` — prior RBAC fixes

## Code issues

### CLEAN — Shared RBAC convergence

All three broker reconcilers (`reconcileKafkaGateway`, `reconcileAmqpGateway`, `reconcileNatsGateway`) produce byte-for-byte identical `kubezap-gateway` Role rules:

```
triggers:get/list/watch, integrations:get, flowruns:create
```

All three use `controllerutil.CreateOrUpdate` on the same `kubezap-gateway` Role object. Because the rules are identical, any ordering of concurrent reconciles reaches the same desired state. No bug.

### CLEAN — Gateway name isolation

Webhook gateway resources: all use prefix `kubezap-webhook-gateway` (SA, Role, RoleBinding, Service, Deployment, HPA).

Broker gateway resources: shared `kubezap-gateway` (SA, Role, RoleBinding) + per-Integration Deployments named `kubezap-{kafka|amqp|nats}-gateway-{integration.Name}`.

These two sets are completely disjoint — no name collision, no lifecycle interference possible when both a webhook Trigger and a broker Integration exist in the same namespace. No bug.

### CLEAN — Cron scheduler double-fire

`CronScheduler.entries` keyed by `"<namespace>/<name>"` — unique per Trigger regardless of schedule string. `Register()` removes the previous entry for a key before adding the new one → idempotent. Two Triggers sharing the same schedule string in the same or different namespaces produce two independent `cron.EntryID` entries that fire independently. No double-fire risk. No bug.

### BUG — Webhook FlowRun random suffix too short

**Location:** `internal/gateway/webhook/handler.go` (the `randomHex(4)` call in FlowRun name construction)

**Description:** FlowRun names are generated as `<trigger>-<unix-timestamp>-<randomHex(4)>`. `randomHex(4)` produces 4 hex characters = 2 bytes = 65 536 possible values per (trigger, second) pair. Under the birthday problem, collision probability at 100 concurrent requests/second on the same Trigger is approximately 7.5% per second.

On collision `k8sClient.Create` returns `AlreadyExists`. The handler treats this as success — returns HTTP 202 with the existing FlowRun name. But the existing FlowRun holds the **first** request's body, headers, and metadata. The colliding request's data is silently discarded. The caller receives no error indication.

**Severity:** Medium. Requires ~100 req/sec on the same Trigger to be likely, but the failure is silent and causes data loss in a production webhook pipeline.

**Fix:** Change `randomHex(4)` to `randomHex(8)` (4 bytes = 4 294 967 296 values → collision probability at 100 req/s is ~1 in 42 949 673 per second — effectively zero).

**Scheduled:** Added as `[ ]` bug fix item in §2 R1 Findings and `T9` test in §3.

### NOTE — BodyTruncated bug (R2, still pending)

Confirmed the `BodyTruncated` bug from R2 is still present in `handler.go`: bodies between 4 097 bytes and 4 MB are accepted with HTTP 202 but stored truncated to 4 096 chars in `TriggerData.Body` with `BodyTruncated: false`. This is tracked separately in §1 R2 Findings.

## Schedule changes

- §2 R1 audit items: all 5 marked `[x]`
- §2 R1 Findings — Confirmed Clean: 3 items added (all `[x]`)
- §2 R1 Findings — Bug Fixes: 1 item added (`[ ]` randomHex fix)
- §3 Testing: T9 added (`[ ]` concurrent FlowRun name uniqueness test)

## Files changed

- `docs/schedule.md` — R1 audit results recorded
- `docs/review-latest.md` — this file
