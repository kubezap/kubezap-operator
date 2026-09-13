# STORY-007: Final release validation and OperatorHub submission

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Groomed
**Size:** M — mostly validation/process, not new code

## Description

The rollup gate before the actual "open the repo, submit to OperatorHub" moment. Carries forward the pre-existing submission gate list (previously tracked ad-hoc in the project's old task log, all individually satisfied already — see Notes) plus everything else in this Epic.

## Acceptance Criteria

- [ ] STORY-001, 002, 003, 005, 006, 016 all `Done` (STORY-004 may remain open if the owner decides the website isn't launch-blocking — confirm at the time; STORY-016 added 2026-09-12 checkpoint).
- [ ] A **fresh** full spec-drift check (`planning/process/spec-drift.md` Steps 1–4, all 4 CRD types) — this is a recurring gate by its own design, not satisfied by any past one-time run, however recent.
- [ ] `make test-e2e` green with no undiagnosed failures (depends on STORY-003's outcome).
- [ ] `make lint`/`make test`/`make vulncheck` all clean on `main` at submission time.
- [ ] OLM bundle passes `bundle validate` and `scorecard` (last confirmed 2026-03-21 — re-verify, don't assume still true).
- [ ] Git history secret-scan clean (if not already done as part of dependency/security work).
- [ ] `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue/PR templates all present and correct.

## File / Module Footprint

None expected — this is a validation pass. If the spec-drift check or e2e re-run finds something, that becomes its own follow-up story via `follow-ups.md`, not scope creep into this one.

## Dependencies

- Depends on: every other story in EPIC-001
- Blocks: the actual OperatorHub submission PR / making the repo public

## Post-launch (not gates — do these right after the repo goes public, not before)

- Set the Pages source to "GitHub Actions" in Settings → Pages (STORY-004 is code-complete and waiting on this; see `planning/backlog/follow-ups.md`).
- Enable Discussions in Settings → Features, with starter categories (STORY-006 is code-complete and waiting on this too).

Both were deliberately held back (owner decision, 2026-09-12) since enabling either on a still-private repo would be premature.

## Notes

The individual older gate items this absorbs (pre-public readiness validation, manual per-example e2e, code/doc review passes, the WATCH_NAMESPACES e2e fix) were all already satisfied as of a 2026-09-11 grooming pass — not re-litigated here. The one gate that's explicitly a *recurring* check by its own design (full spec-drift, not the lighter per-PR version in `planning/process/pre-merge-checklist.md` §6) is re-listed above because "already done once" doesn't satisfy a recurring gate.
