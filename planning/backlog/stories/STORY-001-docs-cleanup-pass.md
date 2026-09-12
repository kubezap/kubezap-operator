# STORY-001: Final public-facing docs cleanup pass

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Planned (Batch 1, WP-1 — see `planning/checkpoints/checkpoint-2026-09-12/work-packages.md`)
**Size:** M

## Description

A pass over `docs/` for public-facing polish — tone, consistency, dead links, screenshots/examples that reference internal-only infra (e.g. the k3s/Strimzi dev cluster used during this project's own validation work) — distinct from the spec-drift process, which checks correctness-vs-implementation, not readability for an external audience.

## Acceptance Criteria

- [ ] No broken internal links across `docs/*.md` and `docs/{api,guides}/*.md` (a repo-relative link check).
- [ ] No example or screenshot references infra specific to this project's own dev environment (cluster names, internal hostnames) without being clearly marked as illustrative.
- [ ] Tone consistency pass — no leftover "TODO"/draft-sounding language in pages meant to read as finished product docs.

## File / Module Footprint

- `docs/*.md` (root-level docs)
- `docs/api/*.md`
- `docs/guides/*.md`
- Excludes: `docs/design/` (technical decision records, not public-facing polish targets), anything under `planning/` (internal PM content, out of scope for a public-docs pass by definition), and `docs/contributing.md` (owned by STORY-002 — resolved 2026-09-12 via `/plan-parallel` to make these two stories parallel-safe rather than serialized)

## Dependencies

- Depends on: none
- Blocks: none — independent of STORY-002/003/005/006

## Notes

Broad footprint by nature (a sweep, not a targeted fix). Resolved 2026-09-12 (`/plan-parallel`): `docs/contributing.md` is explicitly excluded from this story's footprint (see above) so it doesn't overlap STORY-002, which owns that file — the two run in parallel.
