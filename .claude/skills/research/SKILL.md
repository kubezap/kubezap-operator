---
name: research
description: Periodic health review of docs and code — find gaps, staleness, inconsistencies, and bugs, and feed findings into the backlog as follow-ups or new Stories. Use for a doc/code health scan, not for implementing fixes.
---

You are performing a periodic health review of the KubeZap Kubernetes operator project. Execute the following workflow autonomously.

## Rate-limit guardrail

Before starting, estimate scope. This review uses **two parallel read-only agents** for phases 1–2, then sequential writes. That is within safe limits. Proceed.

## 0. Setup

Create and check out a working branch for all review changes:

```bash
git checkout -b review/$(date +%Y-%m-%d) 2>/dev/null || git checkout review/$(date +%Y-%m-%d)
```

## 1 & 2. Parallel read-only review

Launch **two Explore agents simultaneously** (no worktree isolation needed — both are read-only):

### Agent A — Doc review

Read every file in `docs/` (all subdirectories) and `CLAUDE.md`. For each file note:

- **Gaps**: topics referenced in CLAUDE.md or other docs that have no corresponding doc page
- **Staleness**: docs that describe things as "planned" or "future" that are now implemented (or vice versa)
- **Inconsistencies**: fields, CRD names, API shapes, or behaviors described differently across docs
- **Broken cross-references**: links to files or sections that don't exist

Also compare `CLAUDE.md` against `planning/backlog/backlog.md` and the current epics in `planning/backlog/epics/` — flag any divergence.

Return findings as a structured list (do not write any files).

### Agent B — Code review

Read the following files only (do not read every file in the repo):

- `api/v1alpha1/*_types.go` (all type files — skim for markers, field naming, status conditions)
- `cmd/main.go`
- `internal/controller/` (file list only — read each reconciler at high level, ~50 lines each)
- `go.mod` (dependency versions)
- `.golangci.yml` if present
- `Makefile` targets at a glance

For each area note:

- **Bugs**: obvious logic errors, missing error checks on critical paths, nil dereferences
- **Tech debt**: TODOs, HACKs, workarounds, deprecated API usage
- **Design issues**: patterns that violate controller-runtime best practices, RBAC markers missing for resources being touched, missing status conditions, reconcilers that don't requeue on transient errors
- **Gaps**: CRDs referenced in CLAUDE.md as "designed" but with no reconciler; reconcilers that exist but have no Ginkgo tests

Return findings as a structured list (do not write any files).

---

Wait for both agents to complete, then consolidate their findings in memory before proceeding.

## 3. Cross-check backlog vs. reality

Read `planning/backlog/backlog.md` and the Epic/Story files it links to. For each `Done` item, briefly verify it looks actually implemented (not just marked done). For each open item, check whether it's already partially or fully done in code.

Flag:
- Items marked `Done` that appear unimplemented
- Open items that appear already implemented
- Real gaps missing from the backlog entirely

## 4. Write findings

Use the file ownership rules below to avoid conflicts — write each file once, completely.

### 4a. `planning/backlog/follow-ups.md`

**Append** (do not overwrite) one dated entry per finding, following the file's existing Log format (date, source, description, `Open` status). Do not create a separate pending-input file — this is the one place open questions live now.

### 4b. Doc fixes

Fix any doc inconsistencies or broken cross-references found in Phase 1 — edit files in-place. For large gaps (missing doc pages), add a `<!-- TODO: write this doc -->` stub rather than creating a full page, unless the content is straightforward.

Do NOT touch `CLAUDE.md` — that file is managed by the user.

### 4c. `planning/backlog/backlog.md` and Epic/Story files

- Correct any Story/Epic status that's verified wrong (`Done` when not implemented, or vice versa) — edit the specific file plus its row in `backlog.md`.
- For real gaps found that aren't already tracked, add a row to `backlog.md`'s Backlog Candidates section (not a full Epic/Story yet — that's `/new-epic`/`/groom-backlog`) rather than writing entries in the Epics/Stories tables directly.

## 5. Validate (docs only — no code was changed)

Run a quick sanity check:

```bash
go build ./...
```

If this fails, note it in the PR description but do NOT attempt to fix code — that belongs in a `/groom-backlog` + `/dispatch-work` cycle.

## 6. Commit and push

Stage only the files you changed in Phase 4. Commit with:

```
docs: periodic review findings $(date +%Y-%m-%d)
```

Then push the branch and open a PR against `main`. Put the full findings (doc issues, code issues, backlog corrections, new follow-ups) in the **PR description** — do not create a standalone `review-latest.md`-style file that just gets overwritten next time; the PR itself and the `follow-ups.md` entries are the durable record.

```bash
git push -u origin HEAD
```

Open a PR with `gh pr create` using this body template:

```
## Summary
Periodic project health review — doc fixes, backlog corrections, and follow-ups filed.

## Doc issues
<bullet list>

## Code issues
<bullet list — high level only>

## Backlog corrections
<what was corrected in backlog.md/Epic/Story files>

## Follow-ups filed
<copy the entries added to planning/backlog/follow-ups.md>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## 7. Report

Print a concise summary of:
- **Branch**: name of the working branch
- **PR**: URL of the opened pull request
- **Doc issues found**: count and severity
- **Code issues found**: count and severity
- **Backlog corrections**: what changed
- **Follow-ups filed**: how many, brief topic list — these are in `planning/backlog/follow-ups.md`, tagged `Open`, and get triaged at the next `/checkpoint`
- **Suggested next step**: run `/checkpoint` to triage the new follow-ups and reprioritize
