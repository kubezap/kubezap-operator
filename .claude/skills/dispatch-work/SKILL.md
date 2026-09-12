---
name: dispatch-work
description: Execute an already-approved work-packages.md plan by spawning one isolated worktree agent per parallel-safe work package. Use only after /plan-parallel has produced and the user has reviewed a work packages plan for the current checkpoint.
---

# Dispatch Work

Execute a Work Package plan that has already been through conflict analysis. This skill does not do its own conflict checking — if a `work-packages.md` doesn't exist yet or looks unreviewed, run `/plan-parallel` first (or send the user there) instead of guessing.

## Steps

1. Read the current checkpoint's `planning/checkpoints/checkpoint-YYYY-MM-DD/work-packages.md`.
2. For each work package that is ready to start now (its dependency-ordered predecessors, if any, are already merged):
   - Create an isolated worktree: `git worktree add ../kubezap-<slug> -b backlog/<slug>`.
   - Compose a self-contained brief for the agent: the story/stories' acceptance criteria, footprint (as the boundary it must stay inside), and any relevant `docs/design/*.md` records or `CLAUDE.md`/`docs/architecture.md` context it should follow. Include this hard stop: **"Do NOT write to any hot file listed in `CLAUDE.md`'s Parallel Agent Guidelines (`cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`, `go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, `planning/backlog/backlog.md`). Do NOT run `make generate`/`make manifests`. Do NOT open a PR. Commit your changes on the worktree branch and stop."**
   - Spawn it via the `Agent` tool with `isolation: "worktree"`, running in the background so multiple work packages truly run in parallel. Give each a clear `description` and a prompt that stands alone (the subagent has no memory of this conversation).
3. Update each dispatched story's Status to `In Progress` in `backlog.md` and its story file.
4. Do not dispatch a work package whose predecessor (per the plan's dependency ordering) hasn't merged yet — wait and dispatch it in a follow-up call once that predecessor is done.
5. When an agent completes, verify its work meets the story's acceptance criteria and passes `go build ./...`, `go vet ./...`, `make test`, `make lint` (and `make generate && make manifests` if it touched `api/v1alpha1/`, run once after merging, not per-agent). If it does, merge the branch into `main` yourself (or the current integration branch), clean up the worktree and branch, and update Status to `Done`. Report the merge (branch name, commit) back to the user. If something's missing or broken, report that instead of merging, and either resume the agent or dispatch a follow-up to finish it.
6. If any story in the batch touched a hot file (per `/plan-parallel`'s notes), do that wiring pass yourself, sequentially, after all worktree branches have merged — never inside a parallel worker.
7. If an agent's report documents a deliberate rough edge, deferred concern, or "future work" note (not a defect to fix now, but something worth revisiting), add a one-line entry to `planning/backlog/follow-ups.md` before moving on — don't let it live only inside that story's Notes section where it won't resurface on its own.
8. Remove worktrees after successful merge: `git worktree remove ../kubezap-<slug>`.

## Don't

- Don't run two work packages the plan marked as sequential at the same time, even if it would be faster — the ordering exists because their footprints overlap.
- Don't merge a work package that doesn't meet its acceptance criteria or fails its checks, even if it's otherwise close — fix it first (resume the agent or dispatch a follow-up).
