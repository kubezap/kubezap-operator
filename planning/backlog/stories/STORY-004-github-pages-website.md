# STORY-004: Product/docs website via GitHub Pages

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Planned (Batch 2, WP-4 — see `planning/checkpoints/checkpoint-2026-09-12/work-packages.md`)
**Size:** S

## Description

A public-facing website for the project via GitHub Pages, no custom domain for now (default `<org>.github.io/<repo>` URL). **Content scope decided (2026-09-12, via `/groom-backlog`): rendered `docs/` only** — no separate landing/product page for v1. Smallest footprint, no new content to write/maintain beyond what already exists, matches where the project actually is pre-launch.

## Acceptance Criteria

- [ ] `docs/` renders as a browsable static site (via a static-site generator — e.g. mkdocs, Jekyll, or a plain markdown-to-HTML pipeline; pick whichever needs the least new config given `docs/`'s existing structure).
- [ ] Site builds and deploys automatically on push to `main` via GitHub Actions, publishing to GitHub Pages.
- [ ] `planning/` is NOT included in the published site (internal PM content, out of scope by definition — same exclusion STORY-001 applies to its own docs pass).
- [ ] Site is reachable at `<org>.github.io/<repo>` and internal doc links resolve correctly once rendered (not just as raw markdown).

## File / Module Footprint

- `.github/workflows/pages.yml` (new — deploy workflow)
- A site-generator config file (e.g. `mkdocs.yml`), added at repo root or under a new `docs-site/` directory depending on which generator is chosen
- Does not modify `docs/` content itself — this story publishes what STORY-001/002 leave in place, it doesn't rewrite it

## Dependencies

- Depends on: none (STORY-001's docs polish pass is not a hard blocker, but ordering it first means the published site doesn't need a second pass immediately after launch)
- Blocks: none

## Notes

Landing/product page content (beyond rendered docs) is explicitly deferred — if wanted later, that's a new story, not scope creep into this one.
