# STORY-005: `CODE_OF_CONDUCT.md` + issue/PR templates

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Groomed
**Size:** S

## Description

`.github/` currently has only `workflows/` and `dependabot.yml` — no `ISSUE_TEMPLATE/`, `PULL_REQUEST_TEMPLATE.md`, or `CODE_OF_CONDUCT.md`. Standard, low-effort community-health checklist items — GitHub's own repo "community profile" check looks for exactly these.

## Acceptance Criteria

- [ ] `CODE_OF_CONDUCT.md` at repo root (Contributor Covenant or similar standard text).
- [ ] `.github/ISSUE_TEMPLATE/bug_report.yml` and `.github/ISSUE_TEMPLATE/feature_request.yml` (or equivalent).
- [ ] `.github/PULL_REQUEST_TEMPLATE.md`.
- [ ] GitHub's community-profile checklist (Insights → Community Standards) shows all items satisfied once merged.

## File / Module Footprint

- `CODE_OF_CONDUCT.md` (new)
- `.github/ISSUE_TEMPLATE/*.yml` (new)
- `.github/PULL_REQUEST_TEMPLATE.md` (new)

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Fully additive, new files only — no conflict risk with any other story in this Epic.
