# STORY-001: Final public-facing docs cleanup pass

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #181, merged 2026-09-12)
**Size:** M

## Description

A pass over `docs/` for public-facing polish — tone, consistency, dead links, screenshots/examples that reference internal-only infra (e.g. the k3s/Strimzi dev cluster used during this project's own validation work) — distinct from the spec-drift process, which checks correctness-vs-implementation, not readability for an external audience.

## Acceptance Criteria

- [x] No broken internal links across `docs/*.md` and `docs/{api,guides}/*.md` — found and fixed 3 (two `../api/integration.md` path escapes in `docs/architecture.md`, one dead link to a nonexistent `kafka-setup.md` in `docs/guides/nats-setup.md`); all other repo-relative links checked, none broken.
- [x] No example or screenshot references infra specific to this project's own dev environment — checked every in-cluster hostname/CIDR example across the guides; all use generic Kubernetes DNS conventions or RFC 2606 reserved example domains. Nothing to fix.
- [x] Tone consistency pass — removed one leftover doc-history meta-comment in `docs/api/flow.md`. A stray `TODO` in `docs/guides/plugin-security.md` and `docs/architecture.md`'s "TBD" roadmap markers were checked and correctly left alone (legitimate template placeholder / explicitly-labeled backlog section, not draft prose).

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
