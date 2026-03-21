# Backlog groom: 2026-03-21

## Reordering applied

None. All existing items are in correct dependency order. The resource watcher bug items
in §11 (pluralization → name collision → retry → cooldown → docs) are correctly sequenced;
item 8 (README update) depends on item 4 (pluralization fix) and already trails it.

## Items marked complete (stale `[ ]` → `[x]`)

- **§11 Item 3** — DOCS `docs/api/mock-endpoint.md`: already deleted (Q1 resolved 2026-03-21). Marked `[x]`.
- **§11 Item 9** — DOCS contributing link to `docs/overview.md`: confirmed done in review-latest.md. Marked `[x]`.

## Items added

- **§11** — TECH DEBT (Medium): Kafka producer pool no TTL/health check — evidence: `docs/review-latest.md`
- **§11** — TECH DEBT (Low): HTTP Integration re-fetched on every step execution — evidence: `docs/review-latest.md`
- **§11** — TECH DEBT (Low): CEL env init failure cached forever via `sync.Once` — evidence: `docs/review-latest.md`

## Items removed or annotated

None removed. One annotation updated:

- **§6 Example 6** blocked note updated from "blocked on `type: resource` trigger (see Future/Backlog)"
  to "partially blocked — trigger implemented (alpha), blocked on §11 resource watcher bug fixes".

## Items promoted from Future/Backlog

None. `[x] Kubernetes resource-event trigger type` in §10 was already correctly marked done.

## No-change items

~55 items reviewed, no other changes needed.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
