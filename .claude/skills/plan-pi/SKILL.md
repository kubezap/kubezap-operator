---
name: plan-pi
description: Roadmap-level planning — set or revise the current PI's objective and committed epic order in planning/roadmap/pi-plan.md. Use when the user wants to plan the roadmap, confirm/change scope, or reorder which epics come next.
---

# Plan PI (Program Increment)

Set or revise the roadmap horizon: one objective, one ordered list of committed epics.

## Steps

1. Read `planning/roadmap/pi-plan.md` (current state) and `planning/backlog/backlog.md` (full epic list with status).
2. Work with the user (this is squarely an owner decision) to set:
   - **PI objective** — one or two sentences, outcome-focused.
   - **Committed epic order** — which epics are in this PI, in what sequence, and why.
   - **Explicitly out of scope** — epics deliberately deferred, so it's clear they weren't forgotten.
3. Append the new PI section to `planning/roadmap/pi-plan.md` (keep prior PIs — don't delete history within this file; it's a small, append-only log of committed plans, unlike the retired `docs/schedule.md`'s sprawl).
4. Update the `PI` column for affected epics in `planning/backlog/backlog.md`.

## Don't

- Don't unilaterally decide scope — surface tradeoffs (e.g. "the website story is high-visibility but its content scope isn't decided yet") and let the user choose.
- Don't groom stories here — that's `/groom-backlog`, and normally happens epic-by-epic after the PI order is set.
