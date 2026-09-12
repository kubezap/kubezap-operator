---
name: new-epic
description: Create a new Epic from a feature area, a Backlog Candidate row, or a fresh idea, and register it in backlog.md. Use when the user wants to turn an idea/feature area into a real epic, or promote a candidate row in backlog.md into an actual epic file.
---

# New Epic

Turn a feature area into a real `EPIC-NNN-slug.md` file and register it in the backlog index.

## Steps

1. Read `planning/backlog/backlog.md` to find the next unused `EPIC-NNN` ID (reuse the row already reserved for this feature area if it's one of the Backlog Candidates; otherwise take the next sequential number — never reuse or renumber an existing ID).
2. Read `CLAUDE.md` and `docs/architecture.md` for context on this feature area — KubeZap has no separate requirements catalog (no FR/NFR numbering); pull the relevant architecture/CRD context directly from these and from `docs/design/README.md` (indexed design records) for the "Related Design Docs" section, rather than inventing new requirement language.
3. Copy `.claude/templates/epic.md` to `planning/backlog/epics/EPIC-NNN-slug.md` and fill it in collaboratively with the user (this is an owner decision — Problem/Goal/Success Metric should reflect what *they* want, not be assumed). Candidate Stories can be rough; sizing/footprinting happens later in `/groom-backlog`.
4. Update the epic's row in `planning/backlog/backlog.md`: set Status to `Backlog`, link the File column to the new file. If this epic wasn't already a candidate row, add one.
5. If the epic changes the shape of the current PI (new epic not in `planning/roadmap/pi-plan.md`'s committed order), flag that to the user — don't silently edit the PI plan without asking; that's `/plan-pi`'s job.

## Don't

- Don't invent scope the user hasn't confirmed — when in doubt about Problem/Goal, ask.
- Don't break an epic into Stories here — that's `/groom-backlog`.
