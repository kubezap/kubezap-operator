You are performing a backlog grooming pass on the KubeZap Kubernetes operator project. Your job is to read the current schedule, apply prioritization rules, and leave the backlog in correct dependency order. Execute the following workflow autonomously.

## Rate-limit guardrail

This skill is read-heavy and sequential — well within safe limits. Proceed.

## 0. Setup

Create and check out a working branch:

```bash
git checkout -b groom/$(date +%Y-%m-%d) 2>/dev/null || git checkout groom/$(date +%Y-%m-%d)
```

## 1. Read inputs

Read all of the following before making any changes:

- `docs/schedule.md` — full file
- `docs/review-latest.md` — findings from the most recent `/research` run (may not exist; skip if absent)
- `docs/tech-debt/pending-input-required.md` — any open questions that affect priority
- `CLAUDE.md` — architectural constraints that imply ordering rules

Hold all observations in memory. Do NOT write anything yet.

## 2. Identify ordering violations

Apply these prioritization rules (highest precedence first):

1. **Research/audits before tests** — tests written against buggy behavior need rewriting after the bug is fixed. Any R-type research items must precede T-type test items that touch the same code paths.
2. **Bug fixes before features that depend on the same code** — a confirmed bug in component X must be scheduled before new work that builds on X.
3. **Prerequisites before dependents** — if item B requires a type, API, or behavior introduced by item A, A must come first. Common patterns: type changes before tests, integration type before examples using it, MockEndpoint replacement docs before new examples that would use mock servers.
4. **Tests before new feature development in the same area** — once an area has confirmed-correct behavior, test it before adding more features on top.
5. **Shared infrastructure before consumers** — shared gateway RBAC, shared SA/Role, shared webhook infra must be correct before example or test items that exercise those paths.

For each violation found, note:
- Which two items are out of order
- Which rule is violated
- Proposed fix (move item X above item Y in section Z)

## 3. Identify stale or missing items

Scan for:

- `[ ]` items that cross-reference code or docs that no longer exist — mark them for removal or update
- Items in `## 10. Future / Backlog` that have now become active (referenced by recent findings or decisions) — propose promoting them to an active section
- Gaps: work clearly needed that is not scheduled at all

Do NOT add speculative items. Only add items that are evidenced by:
- A finding in `docs/review-latest.md`
- A confirmed bug or gap in `docs/tech-debt/`
- An explicit architectural constraint in `CLAUDE.md`

## 4. Apply changes to `docs/schedule.md`

Make all approved changes:

- Move items to correct positions per ordering violations found in Step 2
- Remove or annotate stale items
- Add new items evidenced in Step 3 (append to the appropriate section — do NOT reorder existing items beyond what is necessary)
- Update the **Prioritization rationale** section at the top of `docs/schedule.md` if any new principle was applied that is not already documented there

Write `docs/schedule.md` once, completely. Do not make incremental edits.

## 5. Write grooming summary

Create or overwrite `docs/groom-latest.md`:

```markdown
# Backlog groom: $(date +%Y-%m-%d)

## Reordering applied
- <item X moved above item Y — rule violated: ...>

## Items added
- <new item — evidence: ...>

## Items removed or annotated
- <item — reason: ...>

## Items promoted from Future/Backlog
- <item — reason: ...>

## No-change items
<count> items reviewed, no change needed.

## Files changed
- docs/schedule.md
- docs/groom-latest.md
```

## 6. Commit and push

Stage `docs/schedule.md` and `docs/groom-latest.md`. Commit with:

```
docs: backlog groom $(date +%Y-%m-%d)
```

Then push and open a PR against `main`:

```bash
git push -u origin HEAD
```

Open a PR with `gh pr create` using this body template:

```
## Summary
Backlog grooming pass — schedule reordering, stale item cleanup, and new items from recent review findings.

## Reordering applied
<bullet list from groom-latest.md>

## Items added
<bullet list>

## Items removed or annotated
<bullet list>

## Files changed
- docs/schedule.md
- docs/groom-latest.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## 7. Report

Print a concise summary of:
- **Branch**: name of the working branch
- **PR**: URL of the opened pull request
- **Reordering**: how many items moved and which rules applied
- **New items**: count and brief descriptions
- **Removed/annotated**: count
- **Suggested next step**: run `/backlog` to work the top item
