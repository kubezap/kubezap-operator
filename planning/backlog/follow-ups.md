# Follow-ups

A running log of edge cases, deferred ideas, or "we should revisit this" notes discovered during implementation, review, or grooming — captured here the moment they're noticed so they don't get buried in a story/epic file nobody rereads. Triaged during `/checkpoint`.

Each entry: date discovered, source (which story/epic/conversation surfaced it), a short description, and status. Once triaged, update the status rather than deleting the entry — this file is also a record of what got noticed and what happened to it.

Status values: `Open` (not yet triaged) → `Routed: <EPIC-NNN/STORY-NNN>` (turned into real backlog work) or `Dropped: <reason>`.

This replaces `docs/tech-debt/pending-input-required.md`'s role — an owner-decision question gets logged here as `Open` and triaged the same way, rather than in a separate file with its own `<!-- BACKLOG-PROMPT -->` convention.

## Log

- **2026-09-12** — Source: project-management restructuring conversation. `docs/schedule.md`, `docs/groom-latest.md`, `docs/review-latest.md`, and `docs/tech-debt/` were internal working documents shipping in the same repo about to go public, with no decided mechanism for keeping them out.
  Status: `Routed: chore/pm-migration-2026-09-12` — resolved by this same migration: project-management content now lives under top-level `planning/`, a single directory that's straightforward to exclude from a public mirror or split to a private repo later, rather than needing to be carved out of `docs/`.
