---
name: dispatch-work
description: Execute an already-approved work-packages.md plan by spawning one isolated worktree agent per parallel-safe work package. Use only after /plan-parallel has produced and the user has reviewed a work packages plan for the current checkpoint.
---

# Dispatch Work

Execute a Work Package plan that has already been through conflict analysis. This skill does not do its own conflict checking — if a `work-packages.md` doesn't exist yet or looks unreviewed, run `/plan-parallel` first (or send the user there) instead of guessing.

## Steps

1. Read the current checkpoint's `planning/checkpoints/checkpoint-YYYY-MM-DD/work-packages.md`.
2. For each work package that is ready to start now (its dependency-ordered predecessors, if any, are already merged):
   - Create an isolated worktree from `origin/main` (or the current integration branch's remote tip, not a local branch with uncommitted staged changes on it): `git worktree add ../kubezap-<slug> -b backlog/<slug> origin/main`.
   - Compose a self-contained brief for the agent: the story/stories' acceptance criteria, footprint (as the boundary it must stay inside), and any relevant `docs/design/*.md` records or `CLAUDE.md`/`docs/architecture.md` context it should follow. Include this hard stop: **"Do NOT write to any hot file listed in `CLAUDE.md`'s Parallel Agent Guidelines (`cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`, `go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, `planning/backlog/backlog.md`). Do NOT run `make generate`/`make manifests`. Do NOT open a PR. Commit your changes on the worktree branch and stop."** (Opening the PR is this skill's own job, step 5 below — not the sub-agent's.)
   - Spawn it via the `Agent` tool with `isolation: "worktree"`, running in the background so multiple work packages truly run in parallel. Give each a clear `description` and a prompt that stands alone (the subagent has no memory of this conversation).
3. Update each dispatched story's Status to `In Progress` in `backlog.md` and its story file. Stage these edits (`git add`) but do not commit/push/PR them yet — they fold into the final planning PR in step 6.
4. Do not dispatch a work package whose predecessor (per the plan's dependency ordering) hasn't merged yet — wait and dispatch it in a follow-up call once that predecessor is done.
5. When an agent completes, verify its work meets the story's acceptance criteria and passes `go build ./...`, `go vet ./...`, `make test`, `make lint` (and `make generate && make manifests` if it touched `api/v1alpha1/`, run once locally across all this batch's branches together before opening any PR, not per-agent — but do NOT commit the result; each PR's own branch stays exactly what the agent produced). If it does, **push the branch and open a PR for it** (`gh pr create`, one PR per story/work package — never a direct merge to `main`, local or remote). Update that story's Status to `In Progress — PR #NNN open` (staged, not committed — see step 6). Report the PR link back to the user. If something's missing or broken, report that instead of opening a PR, and either resume the agent or dispatch a follow-up to finish it.
6. Once the user confirms a work package's PR is merged on GitHub, update that story's Status to `Done (PR #NNN)` in the staged `backlog.md`/story-file edits. Once **every** PR in this batch is confirmed merged, commit the accumulated staged planning edits (this batch's `Planned` → `In Progress` → `Done` transitions, plus the checkpoint's `work-packages.md`) as **one** final planning-only PR per `CLAUDE.md`'s planning-PR guidance. Don't open this final PR early — wait for all of this batch's story PRs to merge first.
7. If any story in the batch touched a hot file (per `/plan-parallel`'s notes), do that wiring pass yourself, sequentially, after all of this batch's story PRs have merged — never inside a parallel worker, and never before merge.
8. If an agent's report documents a deliberate rough edge, deferred concern, or "future work" note (not a defect to fix now, but something worth revisiting), add a one-line entry to `planning/backlog/follow-ups.md` before moving on — don't let it live only inside that story's Notes section where it won't resurface on its own. (This, too, stays staged until the final planning PR.)
9. Remove each worktree once its branch is pushed and its PR is open — no need to wait for merge: `git worktree remove ../kubezap-<slug>`.

## Don't

- Don't run two work packages the plan marked as sequential at the same time, even if it would be faster — the ordering exists because their footprints overlap.
- Don't merge a work package that doesn't meet its acceptance criteria or fails its checks, even if it's otherwise close — fix it first (resume the agent or dispatch a follow-up).
- Don't merge a story branch directly into `main` (local or remote), and don't skip opening a PR for it, regardless of how the triggering request phrases "no intermediate PR" — that phrase is about skipping a separate PR for the planning step, not about the story branches themselves. Only merge directly if the user says so in those exact terms.
- Don't mark a story `Done` before its PR is actually merged on GitHub.
