# STORY-006: Support/community channel decision

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #183, merged 2026-09-12) — Discussions intentionally not enabled yet (owner decision: wait until the repo is actually public, see `planning/backlog/follow-ups.md`); checked `gh api repos/.../` and `has_discussions` is `false`, as expected. Enable in Settings → Features as part of the public-launch sequence, not before.
**Size:** XS

## Description

**Decided (2026-09-12, via `/groom-backlog`): GitHub Discussions.** Lowest friction — built into the repo, no separate account/tool for users to adopt, enables directly in repo settings.

## Acceptance Criteria

- [x] Owner decision recorded (GitHub Discussions).
- [ ] Discussions enabled in repo settings with starter categories (e.g. Q&A, Ideas, Show and tell) — **needs an admin to do this manually**, not achievable via a code change.
- [x] `README.md` links to Discussions — added a "Getting Help" section (PR #183).

## File / Module Footprint

- `README.md`
- (repo settings — not a file change, needs whoever has admin access to enable Discussions)

## Dependencies

- Depends on: none
- Blocks: none — STORY-004 (website) can optionally link here too, but doesn't hard-depend on it.

## Notes

Decision made during the 2026-09-12 `/groom-backlog` session — no longer an open owner question.
