---
name: plan-parallel
description: Select ready (Groomed) Stories and group them into Work Packages by checking their file/module footprints for overlap, writing the plan to a checkpoint's work-packages.md. This is the conflict-avoidance gate — use before dispatching any parallel agent work, whenever there's more than one Groomed story ready to build.
---

# Plan Parallel Work

This is the safety gate for the whole parallel-agent workflow: **no story gets dispatched alongside another without its footprint having been checked against every other story in the batch.**

## Rate-limit guardrail

Check the resulting work-package count against `CLAUDE.md`'s Rate Limit Guardrails section before finalizing the plan (cap at 3 parallel workers per batch, same as the file-ownership rules elsewhere in this project). If more than 3 stories would be genuinely parallel-safe, split them across sequential batches rather than dispatching more than 3 workers at once.

## Steps

1. Read `planning/backlog/backlog.md` and pull every Story with Status `Groomed` (ready, not yet planned) — read each one's File/Module Footprint from its `planning/backlog/stories/` file.
2. Do a full pairwise overlap check across the candidate set:
   - Two stories overlap if their footprints share a path, one's footprint is a parent directory of the other's, or they're both likely to touch a shared file neither explicitly listed (a shared router/scheme/config file) — flag this last case explicitly even if it's not a literal path match, and ask the user if unsure.
   - Also check each story's footprint against `CLAUDE.md`'s Parallel Agent Guidelines hot-files list (`cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, `planning/backlog/backlog.md`) — a story touching one of these needs a wiring pass after all worktree branches merge, not parallel dispatch of the hot file itself.
   - A story with a vague or missing footprint cannot be parallel-planned — send it back to `/groom-backlog` instead of guessing.
3. Group non-overlapping stories into parallel Work Packages (one or more stories each, but a single package's own stories must also not overlap with each other). Overlapping stories become sequential: package B is ordered after package A rather than run alongside it.
4. Determine today's checkpoint directory: `planning/checkpoints/checkpoint-YYYY-MM-DD/` (create it if it doesn't exist yet — use today's actual date). Copy `.claude/templates/work-packages.md` there and fill in the candidate list, the conflict-analysis table, and the resulting work package groupings.
5. If any story touches a hot file, note in the plan that a sequential wiring pass (owned by whoever runs `/dispatch-work`, not a parallel worker) is needed after the other packages merge.
6. Update each planned story's Status to `Planned` in `backlog.md` and its own file. Stage these edits (`git add`) along with the new `work-packages.md`, but do not commit, push, or open a PR for them — per `CLAUDE.md`'s planning-PR guidance, planning-only edits wait for an explicit ask or fold into `/dispatch-work`'s final PR once that batch's story PRs are merged.
7. Present the plan to the user before anyone runs `/dispatch-work` — this is a good moment for them to catch a footprint they disagree with, since dispatch is harder to interrupt cleanly once running.

## Don't

- Don't dispatch agents from this skill — that's `/dispatch-work`, and it should only ever execute a work-packages.md this skill already produced.
- Don't commit, push, or open a PR for this plan on your own initiative — leave the edits staged (see step 6).
- Don't parallelize stories whose footprints you're not confident about. When uncertain, serialize — a false "safe" call here is exactly the merge-conflict risk this whole framework exists to prevent.
