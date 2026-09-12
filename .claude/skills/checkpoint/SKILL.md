---
name: checkpoint
description: Run the checkpoint ritual - roll up what shipped/is blocked since the last checkpoint, prompt backlog re-prioritization, and record a review note. Use when the user wants a status rollup, a periodic review, or to close out the current checkpoint and re-plan.
---

# Checkpoint

The periodic review ritual. Not a sprint boundary — work keeps flowing between checkpoints via `/plan-parallel` and `/dispatch-work`. This is where the owner steps back and looks at the whole board.

## Steps

1. Find the most recent `planning/checkpoints/checkpoint-YYYY-MM-DD/` directory (if any) and read its `work-packages.md`/`review.md`. Cross-reference `planning/backlog/backlog.md` for anything that changed status since then (`In Progress` → `Done`, new stories groomed, etc.).
2. Create today's checkpoint directory if it doesn't exist, copy `.claude/templates/checkpoint-goal.md` in as `goal.md`, and summarize:
   - What shipped since last checkpoint (merged stories).
   - What's still in flight or blocked, and why.
3. Read `planning/backlog/follow-ups.md` and triage every `Open` entry with the user: route it into an existing epic (`/groom-backlog`), spin it into a new epic (`/new-epic`, then flag for `/plan-pi` if it reshapes the roadmap), or explicitly drop it (`Dropped: <reason>`). Update each entry's status in place rather than deleting it — this log is a record of what got noticed, not just a todo list.
4. Ask the user what they want prioritized for the coming stretch — this drives what gets groomed/planned next. Don't assume; backlog order is an owner call.
5. Write `review.md` from `.claude/templates/checkpoint-review.md`: shipped items, open items, what worked / what to change, backlog re-groom notes.
6. If the user says something during this review that's a durable process preference (not specific to this stretch), consider whether it's worth saving to Claude's memory so future sessions inherit it — ask if unsure.

## Don't

- Don't treat this as a gate that blocks work from moving between checkpoints — that's the opposite of the flow-based cadence this project uses (see `planning/roadmap/pi-plan.md`'s Timeframe sections).
