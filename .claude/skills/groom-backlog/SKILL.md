---
name: groom-backlog
description: Break an approved Epic into sized Stories with acceptance criteria and an explicit file/module footprint, and reprioritize backlog.md. Use when the user wants to groom/refine an epic, size stories, write acceptance criteria, or reorder backlog priority.
---

# Groom Backlog

Turn an Epic's rough candidate-story list into real, independently implementable Stories — each one footprinted so `/plan-parallel` can later reason about conflicts.

## Steps

1. Read the target Epic file in `planning/backlog/epics/`. If it doesn't exist yet, stop and suggest `/new-epic` first.
2. For each candidate story (or a new one identified during grooming), work with the user to define:
   - **Acceptance criteria** — concrete, checkable.
   - **Size** — XS/S/M/L, a rough Claude-session-length estimate, not velocity points.
   - **File/module footprint** — the actual paths this story will create or modify. This is the most important field in the whole framework: it's what makes parallel dispatch safe later. Be concrete (`internal/gateway/webhook/handler.go`, not "the gateway"). If you can't yet say which paths a story touches, it isn't groomed — say so rather than guessing.
   - **Dependencies** on other stories.
3. Copy `.claude/templates/story.md` to `planning/backlog/stories/STORY-NNN-slug.md` per story (next sequential ID across the whole project, not per-epic).
4. Update `planning/backlog/backlog.md`'s Stories table with each new story (ID, Title, Epic, Status=`Groomed`, Size).
5. If grooming surfaces a scope question that changes the Epic's Problem/Goal, update the Epic file too and flag the change to the user.
6. If grooming surfaces a non-obvious technical decision (touches security posture, reconciliation logic, a new CRD field/controller/binary, or an external dependency — see `planning/process/design-process.md`'s trigger list), run `/adr` before marking the story `Groomed`, and link the record from the story's Notes.

## Footprint discipline

Two stories with overlapping footprints are not parallel-safe, full stop — `/plan-parallel` will serialize them. Also check `CLAUDE.md`'s Parallel Agent Guidelines hot-files list (`cmd/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/*.yaml`, `planning/backlog/backlog.md`) — a story that must touch one of these needs a wiring pass at dispatch time, not parallel treatment. If you notice two candidate stories about to claim the same files, either merge them into one story or split responsibility for that shared path so only one story owns it.

## Don't

- Don't mark a story `Groomed` without a real footprint — a placeholder footprint defeats the entire point of this framework.
- Don't plan work packages or dispatch agents here — that's `/plan-parallel` and `/dispatch-work`.
- Don't commit, push, or open a PR for grooming edits on your own initiative — stage them (`git add`) and leave them staged; per `CLAUDE.md`'s planning-PR guidance, they wait for an explicit ask or fold into `/dispatch-work`'s final PR once a later batch's story PRs are merged.
