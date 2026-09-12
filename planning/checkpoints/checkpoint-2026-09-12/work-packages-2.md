# Work Packages — 2026-09-12 (batch 2)

Produced by `/plan-parallel`. Second planning pass this same day — see `work-packages.md` for the earlier EPIC-001 batch (STORY-001/002/004/005/006, all since merged). This batch covers what's `Groomed` and actually dependency-unblocked as of now: EPIC-003 and EPIC-004 stories.

## Candidate stories considered

Every `Groomed`-status story in `planning/backlog/backlog.md` as of this session:

- STORY-003 — footprint: **none yet** ("Starts as a read-only investigation — no footprint until findings are in"). Excluded per this skill's own rule ("a story with a vague or missing footprint cannot be parallel-planned") — same reasoning as the previous `/plan-parallel` pass.
- STORY-007 — Groomed, but depends on every other EPIC-001 story being `Done`, including STORY-003 which isn't. Not dependency-unblocked yet — excluded.
- STORY-009 — footprint: `api/v1alpha1/webhookgatewayconfig_types.go` (new), `api/v1alpha1/groupversion_info.go` (hot file), `internal/webhook/webhookgatewayconfig_webhook.go` (new), `internal/controller/webhookgatewayconfig_controller.go` (new) or `gateway_deployment.go`, `config/crd/bases/*.yaml`, `config/webhook/*.yaml`, `config/rbac/role.yaml` (hot file, regenerated), `cmd/main.go` (hot file), `config/samples/`. No unmet dependencies (STORY-008 is `Done`) — dependency-unblocked.
- STORY-010 — Groomed, but depends on STORY-009 (same reconcile loop/types file) which isn't `Done` yet. Not dependency-unblocked — excluded, sequential-after STORY-009, not a candidate for this batch.
- STORY-011 — Groomed, but depends on STORY-009 (the CRD must exist before annotations can be cut over to it) which isn't `Done` yet. Same as STORY-010 — excluded.
- STORY-013 — footprint: `benchmarks/execution-latency/METHODOLOGY.md` (new — also creates the `benchmarks/` top-level directory). No unmet dependencies — dependency-unblocked.

Dependency-unblocked, footprint-real candidates for this batch: **STORY-009, STORY-013**.

## Conflict analysis

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-009 | none | parallel-safe |
| STORY-013 | none | parallel-safe |

- **STORY-009 vs STORY-013**: completely disjoint — one touches `api/v1alpha1/`, `internal/controller/`, `internal/webhook/`, `config/`, `cmd/main.go`; the other creates a brand-new top-level `benchmarks/` directory untouched by anything else in the repo. No shared paths, no shared parent-directory concern.
- **Hot-files check**: STORY-009 touches three hot files (`api/v1alpha1/groupversion_info.go`, `cmd/main.go`, `config/rbac/role.yaml`) — but it's the *only* story in this batch touching any of them, so there's no cross-story wiring-pass conflict to resolve. Its own agent does those edits directly as part of its single-story implementation; this isn't the multi-worker-collision scenario the wiring-pass rule exists for. STORY-013 touches no hot files.

## Rate-limit guardrail

2 parallel-safe stories — well under the 3-worker-per-batch cap. Single batch, no splitting needed.

## Work packages (dispatch plan)

### Batch (dispatch together, worktree-isolated)

- **WP-1** — STORY-009 (`WebhookGatewayConfig` CRD types + controller + singleton admission webhook)
- **WP-2** — STORY-013 (benchmark methodology)

### Not included

- **STORY-003** — no real footprint yet; run standalone whenever, not part of this conflict-checked plan.
- **STORY-007** — blocked on STORY-003 (and the rest of EPIC-001).
- **STORY-010, STORY-011** — blocked on STORY-009 landing first; re-plan once it merges (likely then parallel-safe with each other — confirm footprints don't collide at that time, since STORY-010's note already flags they might end up in the same file).
