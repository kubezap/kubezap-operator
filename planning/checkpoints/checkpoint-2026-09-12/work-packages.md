# Work Packages — 2026-09-12

Produced by `/plan-parallel`. Each package is either dispatched alone or in parallel with other packages in this same batch — never with a package it overlaps.

## Candidate stories considered

All `Groomed` stories in `planning/backlog/backlog.md` as of this session:

- STORY-001 — footprint: `docs/*.md`, `docs/api/*.md`, `docs/guides/*.md` (excludes `docs/design/`, `planning/`, and `docs/contributing.md`)
- STORY-002 — footprint: `docs/contributing.md`
- STORY-003 — **no footprint** ("no footprint until findings are in" — read-only investigation by design; its own file explicitly defers footprinting until findings exist). Excluded from this batch per `/plan-parallel`'s own rule ("a story with a vague or missing footprint cannot be parallel-planned"). Its remaining scope (AC #1 brittle-test review, AC #3 write-up) can run standalone at any time — it isn't blocked by anything here, it just isn't part of a footprint-conflict-checked parallel batch.
- STORY-004 — footprint: `.github/workflows/pages.yml` (new), a site-generator config (e.g. `mkdocs.yml`) at repo root or a new `docs-site/` directory
- STORY-005 — footprint: `CODE_OF_CONDUCT.md` (new), `.github/ISSUE_TEMPLATE/*.yml` (new), `.github/PULL_REQUEST_TEMPLATE.md` (new)
- STORY-006 — footprint: `README.md`, plus a repo-settings change (not a file, needs admin access — no merge-conflict risk either way)
- STORY-007 — footprint: none expected (validation pass), but **depends on every other EPIC-001 story being Done** — excluded from this batch by definition; it's the final sequential gate, not a parallel candidate.

## Conflict analysis

Pairwise footprint overlap check across STORY-001, 002, 004, 005, 006 (003 and 007 excluded per above).

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-001 | none (see Notes) | parallel-safe |
| STORY-002 | none | parallel-safe |
| STORY-004 | none | parallel-safe |
| STORY-005 | none | parallel-safe |
| STORY-006 | none | parallel-safe |

Notes on specific checks:
- **STORY-001 vs STORY-002**: STORY-001's footprint nominally includes `docs/*.md`, which would have literally included `docs/contributing.md` (STORY-002's file). Resolved this session by explicitly excluding `docs/contributing.md` from STORY-001's footprint (updated in both stories' files) — no longer overlapping.
- **STORY-004 vs STORY-005**: both touch paths under `.github/` (`.github/workflows/pages.yml` vs. `.github/ISSUE_TEMPLATE/*.yml` + `.github/PULL_REQUEST_TEMPLATE.md`) — a shared parent directory, but distinct, non-overlapping files within it. Not a real conflict (new files, no shared filename), flagged here per the skill's "ask if unsure" guidance rather than silently assumed safe.
- **STORY-004 vs STORY-006**: STORY-004 doesn't touch `README.md`; no overlap.
- **Hot-files check** (`CLAUDE.md`'s Parallel Agent Guidelines list: `cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, `planning/backlog/backlog.md`): none of these 5 stories touch any hot file. **No wiring pass needed** after this batch merges.
- **Soft ordering note (not a conflict)**: STORY-004's own Dependencies section prefers STORY-001 land first ("so the published site doesn't need a second pass immediately after launch"), but explicitly calls this a preference, not a hard block. Not enforced as a serialization here — both are dispatched in the same round below; if the published site needs a touch-up after STORY-001 lands, that's a fast follow, not a redo.

## Rate-limit guardrail

5 parallel-safe stories, but this project's cap is 3 parallel workers per batch (`CLAUDE.md` + this skill). Split into two sequential batches rather than dispatching all 5 at once.

## Work packages (dispatch plan)

### Batch 1 (dispatch together, worktree-isolated)

- **WP-1** — STORY-001 (docs cleanup pass)
- **WP-2** — STORY-002 (`docs/contributing.md` cleanup — remaining ACs only, see story file for what PR #175 already covered)
- **WP-3** — STORY-005 (community health files)

### Batch 2 (dispatch after Batch 1 merges — not because of a conflict, purely the 3-worker cap)

- **WP-4** — STORY-004 (GitHub Pages website, rendered `docs/` only)
- **WP-5** — STORY-006 (support channel: enable GitHub Discussions + link from README)

### Not included in either batch

- **STORY-003** — footprint not yet real (see Candidate stories above); run standalone whenever, not part of this conflict-checked plan.
- **STORY-007** — sequential rollup gate; dispatch only after every other EPIC-001 story (including STORY-003) is `Done`.
