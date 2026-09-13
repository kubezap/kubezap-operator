# Work Packages — 2026-09-12 (batch 3)

Produced by `/plan-parallel`. Not committed as its own PR per the owner's explicit instruction this round ("no PR in between") — folded into whichever PR the resulting work lands in.

## Candidate stories considered

Every `Groomed`/unblocked story in `planning/backlog/backlog.md` as of this session:

- STORY-010 — footprint: `internal/controller/gateway_deployment.go` (or wherever STORY-009's HPA reconciliation landed — confirmed merged there) + `config/rbac/role.yaml` (hot file).
- STORY-011 — footprint: `internal/controller/trigger_controller.go`, `docs/guides/webhook-security.md`, `docs/overview.md`.
- STORY-014 — footprint: `benchmarks/execution-latency/` (new directory).
- STORY-016 — footprint: `docs/architecture/flowrun-state-model.md`, `docs/guides/getting-started.md`, `docs/architecture.md`.
- STORY-017 — footprint: `internal/controller/integration_controller.go`, `internal/controller/gateway_deployment.go`, plus (per the story's own Description, not just its Footprint bullet) smaller counts across `internal/controller/{cron_scheduler,executor_mtls,executor_reconciler,flow_controller,flowrun_controller,resource_watcher,trigger_controller}.go`, `internal/cli/*.go`, `internal/gateway/{nats,webhook}/*.go`, `.golangci.yml`.
- STORY-018 — footprint: `internal/webhook/trigger_webhook.go`, `internal/webhook/flowrun_webhook.go`, `config/webhook/manifests.yaml`.
- STORY-003, STORY-007, STORY-012, STORY-015 — excluded (STORY-003 still has no real footprint; the other three are dependency-blocked).

## Conflict analysis

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-010 | STORY-017 (`internal/controller/gateway_deployment.go`) | serialize |
| STORY-011 | STORY-017 (`internal/controller/trigger_controller.go`, per STORY-017's own Description listing it among the smaller-count goconst files) | serialize |
| STORY-014 | none | parallel-safe |
| STORY-016 | none | parallel-safe |
| STORY-017 | STORY-010, STORY-011 (see above) | serialize, and run last (sweeps up any new string literals STORY-010/011 introduce, rather than fixing consts that then get re-duplicated) |
| STORY-018 | none — `internal/webhook/*` is a distinct package from `internal/gateway/webhook/*` (STORY-017's target), no literal overlap despite the similar path segment | parallel-safe |

- **STORY-010 vs STORY-011**: no literal path overlap in their stated footprints, but both touch the same `ensureWebhookGateway`-area reconcile logic in the same package that STORY-009 just modified (STORY-010 almost certainly needs a call site there to actually create/update a `PodDisruptionBudget`, mirroring how STORY-009's HPA wiring pass added its own call site). Treating these as sequential too, out of caution, rather than assuming they land in disjoint enough regions of the same file.
- **Hot-files check**: STORY-010 touches `config/rbac/role.yaml` (hot) — the only story in its round touching it, so no cross-story wiring-pass conflict. STORY-018 regenerates `config/webhook/manifests.yaml`, not on the official hot-files list but treated with the same care since it's also `make manifests` output.

## Rate-limit guardrail

3 truly parallel-safe stories (STORY-014, 016, 018) — one batch, under the cap. STORY-010/011/017 run sequentially, one at a time, not batched with anything.

## Work packages (dispatch plan)

### Batch 1 (dispatch together, worktree-isolated, parallel)

- **WP-1** — STORY-014 (benchmark harness)
- **WP-2** — STORY-016 (docs broken-links cleanup)
- **WP-3** — STORY-018 (webhook marker fix)

### Sequential (one at a time, after Batch 1 — no footprint reason to wait for Batch 1 specifically, just dispatched in this order for this round)

1. STORY-010 (PDB reconciliation)
2. STORY-011 (TLS annotation hard cutover) — after STORY-010 merges
3. STORY-017 (goconst cleanup) — after STORY-011 merges, so it sweeps up any new literals from both

### Not included

- STORY-003, STORY-007, STORY-012, STORY-015 — excluded per Candidate stories above.
