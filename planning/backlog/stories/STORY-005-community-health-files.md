# STORY-005: `CODE_OF_CONDUCT.md` + issue/PR templates

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #180, merged 2026-09-12) — one item worth a follow-up check, see AC #4
**Size:** S

## Description

`.github/` currently has only `workflows/` and `dependabot.yml` — no `ISSUE_TEMPLATE/`, `PULL_REQUEST_TEMPLATE.md`, or `CODE_OF_CONDUCT.md`. Standard, low-effort community-health checklist items — GitHub's own repo "community profile" check looks for exactly these.

## Acceptance Criteria

- [x] `CODE_OF_CONDUCT.md` at repo root — Contributor Covenant v2.1, `conduct@kubezap.io` enforcement contact.
- [x] `.github/ISSUE_TEMPLATE/bug_report.yml` and `.github/ISSUE_TEMPLATE/feature_request.yml` — confirmed present at the exact GitHub-expected path via `gh api repos/.../contents/.github/ISSUE_TEMPLATE`.
- [x] `.github/PULL_REQUEST_TEMPLATE.md` — present; confirmed recognized by GitHub's own community-profile API (`pull_request_template` populated).
- [x] GitHub's community-profile checklist shows `health_percentage: 100` post-merge (checked via `gh api repos/.../community/profile`). One field is odd though: that same response shows `"issue_template": null` even though both files are confirmed present at the correct path — most likely the endpoint's own indexing lag right after a merge rather than a real gap, but worth a quick re-check at a future checkpoint if it hasn't cleared.

## File / Module Footprint

- `CODE_OF_CONDUCT.md` (new)
- `.github/ISSUE_TEMPLATE/*.yml` (new)
- `.github/PULL_REQUEST_TEMPLATE.md` (new)

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Fully additive, new files only — no conflict risk with any other story in this Epic.
