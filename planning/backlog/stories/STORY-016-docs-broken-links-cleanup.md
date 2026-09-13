# STORY-016: Fix broken doc links/anchors surfaced by the MkDocs build

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #193)
**Size:** XS

## Description

STORY-004's MkDocs site build surfaced pre-existing broken links in `docs/` content that STORY-004 wasn't permitted to fix (its footprint was the site generator/workflow only). The site build itself succeeds (non-strict) regardless, so this isn't launch-blocking — a quick, standalone cleanup pass.

## Acceptance Criteria

- [ ] `docs/architecture/flowrun-state-model.md`'s link to `../../planning/process/code-review-strategy.md` fixed — it points outside the published site's `docs_dir` and 404s. Since `planning/` is deliberately excluded from the public site (internal PM content), either remove the link, replace it with prose that doesn't assume a clickable cross-reference, or point at a public-facing equivalent if one exists.
- [ ] `docs/guides/getting-started.md`'s link to `../../examples/order-router/README.md` fixed — same problem (outside `docs_dir`, will 404). `examples/` isn't part of the published docs site at all; either link to the GitHub repo path directly (works fine on GitHub itself, and works as an absolute URL on the published site too) or restructure the reference.
- [ ] `docs/architecture.md`'s link to `docs/design/scale-limitations.md` fixed — 404s because `docs/design/` is deliberately excluded from the site. Same fix pattern as above (GitHub-absolute link, or reword to not assume clickability on the published site).
- [ ] Spot-check the handful of in-page anchor links MkDocs/Python-Markdown flagged as mismatched (e.g. `#trigger--flow-invocation` in `architecture.md`) — cosmetic, page-internal only, fix if trivial.

## File / Module Footprint

- `docs/architecture/flowrun-state-model.md`
- `docs/guides/getting-started.md`
- `docs/architecture.md`

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md`'s STORY-004 entry (2026-09-12). Triaged at the 2026-09-12 checkpoint as its own standalone story rather than folded into STORY-001 (already closed).
