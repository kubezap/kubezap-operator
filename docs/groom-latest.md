# Backlog groom: 2026-03-22

## Reordering applied

- **Duplicate `---` separator removed** between §11 and §12 (two consecutive `---` separators). No items moved; cosmetic fix.

## Items added

None. No speculative items added. All §16 items were already committed from backlog/schedule-review-items (cherry-picked into this branch).

## Items removed or annotated

None.

## Items promoted from Future/Backlog

None. §10 Future items are correctly scoped; no new evidence warrants promotion.

## Gate conditions updated

- **§8 OperatorHub submission gate**: Added `§16 P0 items` as a blocking condition. Section header and checklist item now read: "gates on §12 completion, §16 P0 items, and OLM readiness tasks below". Added "**Paused 2026-03-22 pending §16 completion**" to the section note.
- **Prioritization rationale rule 8 added**: "Public readiness (§16) gates OperatorHub submission (§8) — all P0 items in §16 must be complete before the OperatorHub submission PR is opened."

## Pending inputs added

Two `<!-- BACKLOG-PROMPT -->` entries added to `docs/tech-debt/pending-input-required.md`:

1. **Go module rename public org decision** — `github.com/kubezap/kubezap-operator` must be renamed before public release. Requires owner to decide on the public org/repo name (borfswitch personal, new org, or custom domain). Largest-blast-radius change in the project; must be decided before §16 P0 rename work begins.

2. **WebhookAuth struct design** — YAML docs show nested `hmac:`/`bearer:`/`oidc:` sub-keys but Go types are flat fields. A user following the docs gets a CRD validation error. Decision required: restructure Go types to match docs (option 1, better UX) or update all docs to match flat types (option 2). Unblocks §16 P1 WebhookAuth consistency fix.

## No-change items

491 items reviewed across §1–§16 and §10 Future/Backlog. No further reordering needed.

## Files changed

- docs/schedule.md
- docs/tech-debt/pending-input-required.md
- docs/groom-latest.md
