# STORY-004: Product/docs website via GitHub Pages

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #184, merged 2026-09-12) — site not live yet; checked `gh api repos/.../pages` post-merge and it 404s, confirming the Pages source hasn't been set to "GitHub Actions" yet. Needs an admin to do that one-time settings step before the workflow's deploy actually publishes anything.
**Size:** S

## Description

A public-facing website for the project via GitHub Pages, no custom domain for now (default `<org>.github.io/<repo>` URL). **Content scope decided (2026-09-12, via `/groom-backlog`): rendered `docs/` only** — no separate landing/product page for v1. Smallest footprint, no new content to write/maintain beyond what already exists, matches where the project actually is pre-launch.

**Implemented (PR #184, 2026-09-12):** MkDocs + mkdocs-material, `docs_dir: docs`, `docs/design/` excluded from the build. A handful of pre-existing broken links/anchors in `docs/` content were found during verification (out of scope for this story's footprint) — logged to `planning/backlog/follow-ups.md`.

## Acceptance Criteria

- [x] `docs/` renders as a browsable static site — MkDocs + mkdocs-material chosen (least new config, points directly at existing `docs/` tree). Verified with an actual local `mkdocs build`, not just reviewed.
- [x] Site builds and deploys automatically on push to `main` via GitHub Actions, publishing to GitHub Pages — workflow merged in PR #184. **Still blocked on one manual step**: `gh api repos/kubezap/kubezap-operator/pages` returns 404 post-merge, meaning the repo's Pages source hasn't been set to "GitHub Actions" yet (Settings → Pages) — an admin needs to do this before the workflow's deploy step actually publishes anything.
- [x] `planning/` is NOT included in the published site — confirmed not reachable (outside `docs_dir` entirely). `docs/design/` is also excluded per this story's own description (internal technical decision records) — confirmed via a real local build (`find $SITE -iname '*design*'` returns nothing).
- [ ] Site is reachable at `<org>.github.io/<repo>` — **not yet true**, blocked on the same admin step above; will resolve itself once Pages is enabled and the workflow runs. Internal doc links resolve correctly once rendered — confirmed via local build spot-checks (several representative internal links converted to correct relative HTML hrefs).

## File / Module Footprint

- `.github/workflows/pages.yml` (new — deploy workflow)
- A site-generator config file (e.g. `mkdocs.yml`), added at repo root or under a new `docs-site/` directory depending on which generator is chosen
- Does not modify `docs/` content itself — this story publishes what STORY-001/002 leave in place, it doesn't rewrite it

## Dependencies

- Depends on: none (STORY-001's docs polish pass is not a hard blocker, but ordering it first means the published site doesn't need a second pass immediately after launch)
- Blocks: none

## Notes

Landing/product page content (beyond rendered docs) is explicitly deferred — if wanted later, that's a new story, not scope creep into this one.
