You are performing a periodic health review of the KubeZap Kubernetes operator project. Execute the following workflow autonomously.

## Rate-limit guardrail

Before starting, estimate scope. This review uses **sequential** phases (not fan-out agents), so it is within safe limits. Proceed.

## 0. Setup

Create and check out a working branch for all review changes:

```bash
git checkout -b review/$(date +%Y-%m-%d) 2>/dev/null || git checkout review/$(date +%Y-%m-%d)
```

## 1. Doc review (read-only, no agents needed)

Read every file in `docs/` (all subdirectories). For each file note:

- **Gaps**: topics referenced in CLAUDE.md or other docs that have no corresponding doc page
- **Staleness**: docs that describe things as "planned" or "future" that are now implemented (or vice versa)
- **Inconsistencies**: fields, CRD names, API shapes, or behaviors described differently across docs
- **Broken cross-references**: links to files or sections that don't exist

Also read `CLAUDE.md` and compare its "Current Status" checklist against `docs/schedule.md` — flag any divergence.

Keep your findings as an in-memory list. Do NOT write anything yet.

## 2. High-level code review (read-only, capacity-conscious)

Read the following files only — do not read every file in the repo:

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

Keep findings in memory. Do NOT write code changes.

## 3. Cross-check schedule vs. reality

Read `docs/schedule.md`. For each `[x]` item, briefly verify it looks actually implemented (not just marked done). For each `[ ]` item, check whether it's already partially or fully done in code.

Flag:
- Items marked `[x]` that appear unimplemented
- Items marked `[ ]` that appear already implemented
- Items that are missing from the schedule entirely

## 4. Write findings

Now write all findings to the appropriate docs. Use the file ownership rules below to avoid conflicts — write each file once, completely.

### 4a. `docs/tech-debt/pending-input-required.md`

**Append** (do not overwrite) a new dated section:

```markdown
## Review $(date +%Y-%m-%d)

### Decisions needed from owner

<!-- BACKLOG-PROMPT -->
**Q: <question>**
Why it matters: <reason>
Options: <option A> / <option B> / ...
<!-- BACKLOG-PROMPT -->

(Repeat the BACKLOG-PROMPT block for each question. The `/backlog` skill reads these tags and prompts the user at the start of the next session.)
```

### 4b. New or updated tech debt files

For each significant tech debt cluster found, either:
- Create a new file in `docs/tech-debt/<slug>.md` (if it's a new topic), OR
- Update an existing file in `docs/tech-debt/` (if it clearly belongs there)

Do NOT create more than 3 new tech debt files — consolidate minor items.

### 4c. `docs/schedule.md`

- Correct any `[ ]` → `[x]` for items verified as implemented
- Correct any `[x]` → `[ ]` for items verified as NOT implemented (add a note inline)
- Add new backlog items for gaps found (append to the appropriate section — do NOT reorder existing items)

### 4d. Doc fixes

Fix any doc inconsistencies or broken cross-references you found in Phase 1 — edit files in-place. For large gaps (missing doc pages), add a `<!-- TODO: write this doc -->` stub rather than creating a full page, unless the content is straightforward.

Do NOT touch `CLAUDE.md` — that file is managed by the user.

### 4e. Review summary

Create or overwrite `docs/review-latest.md` with a concise summary:

```markdown
# Review: $(date +%Y-%m-%d)

## Doc issues
- (bullet list)

## Code issues
- (bullet list — high-level only, no code snippets)

## Schedule corrections
- (what was corrected)

## Decisions needed
- (copy from pending-input-required additions)

## Files changed
- (list)
```

## 5. Schedule prioritization

After all findings are written, re-read `docs/schedule.md` holistically and apply these prioritization rules. Reorder sections or move items **only when a clear dependency would cause rework** if violated — do not reorder for style:

**Rules (highest precedence first):**

1. **Research/audits before tests** — tests written against buggy behavior need rewriting after the bug is fixed. Any new R-type research items found in this review must precede T-type test items that touch the same code paths.
2. **Bug fixes before features that depend on the same code** — a confirmed bug in component X must be scheduled before new work that builds on X. If a bug fix item was added in step 4c, check whether any existing feature items downstream of that code should move after it.
3. **Prerequisites before dependents** — if item B requires a type, API, or behavior introduced by item A, A must come first. Common patterns: type changes before tests, integration type before examples using it, MockEndpoint replacement docs before new examples that would use mock servers.
4. **Tests before new feature development in the same area** — once an area has confirmed-correct behavior, test it before adding more features on top.
5. **Shared infrastructure before consumers** — shared gateway RBAC, shared SA/Role, shared webhook infra must be correct before example or test items that exercise those paths.

**What to update:**
- Move newly-added items to the correct position relative to existing items.
- Update the **Prioritization rationale** section at the top of `docs/schedule.md` if any new principle was applied that is not already documented there.
- Do NOT renumber sections — insert items within the appropriate existing section or add a new section with the next available number.
- Record any reordering in the review summary under a new **Schedule reordering** heading.

## 6. Validate (docs only — no code was changed)

Run a quick sanity check:

```bash
go build ./...
```

If this fails, note it in the review summary but do NOT attempt to fix code — that belongs in a `/backlog` session.

## 7. Commit and push

Stage only the files you changed in Phase 4 and 5. Commit with:

```
docs: periodic review findings $(date +%Y-%m-%d)
```

Then push the branch and open a PR against `main`:

```bash
git push -u origin HEAD
```

Open a PR with `gh pr create` using this body template:

```
## Summary
Periodic project health review — doc fixes, schedule corrections, and tech debt findings.

## Doc issues
<bullet list from review-latest.md>

## Code issues
<bullet list from review-latest.md — high level only>

## Schedule corrections
<what was corrected>

## Schedule reordering
<any items moved for dependency/rework-avoidance reasons>

## Decisions needed from owner
<copy the questions added to pending-input-required.md, each tagged clearly>

## Files changed
<list>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## 8. Report

Print a concise summary of:
- **Branch**: name of the working branch
- **PR**: URL of the opened pull request
- **Doc issues found**: count and severity
- **Code issues found**: count and severity
- **Schedule corrections**: what changed
- **Schedule reordering**: any items moved and why
- **Decisions needed**: how many, brief topic list — these are in the PR description and in `docs/tech-debt/pending-input-required.md` tagged `<!-- BACKLOG-PROMPT -->` so `/backlog` will surface them next session
- **Next suggested `/backlog` item**: based on what you found
