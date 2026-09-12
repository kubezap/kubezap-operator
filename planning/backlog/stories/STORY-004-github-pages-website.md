# STORY-004: Product/docs website via GitHub Pages

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Backlog — not groomed (see below)
**Size:** unknown — cannot size until content scope is decided

## Description

A public-facing website for the project. **Decided (2026-09-12):** GitHub Pages, no custom domain for now (default `<org>.github.io/<repo>` URL). **Not yet decided:** what content actually goes on it (rendered `docs/`, a landing/product page, or both) — owner said this will be locked down later.

## Acceptance Criteria

Cannot be written yet — depends on the content-scope decision.

## File / Module Footprint

Cannot be footprinted yet — per `/groom-backlog`'s own rule, a story without a real footprint isn't groomed, so this stays `Backlog`, not `Groomed`, until that follow-up conversation happens. Likely candidates once scoped: a `.github/workflows/pages.yml` deploy workflow, and either a `docs-site/` source directory (if using Jekyll/mkdocs/Hugo) or direct publication from `docs/`.

## Dependencies

- Depends on: a decision on content scope (source generator, whether it publishes from `docs/` directly)
- Blocks: none

## Notes

Do not start implementation until the content-scope conversation happens. Also needs to account for `planning/`'s existence (this migration) — whatever publishes the site must not accidentally pull internal PM content in, the same concern originally raised when this item was first scoped.
