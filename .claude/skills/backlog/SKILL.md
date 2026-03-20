You are working on the KubeZap Kubernetes operator project. Execute the following workflow autonomously.

## 0. Check for pending inputs

Read `docs/tech-debt/pending-input-required.md`. Look for any section tagged `<!-- BACKLOG-PROMPT -->`. If any exist, **stop and present each question to the user now**, wait for their answers, record the answers inline in `pending-input-required.md` (replace the `<!-- BACKLOG-PROMPT -->` tag with `<!-- ANSWERED -->` and append the answer), then continue to Step 1.

If no pending inputs exist, continue immediately to Step 1.

## 1. Select the backlog item

If the user provided a specific item or keyword in `$ARGUMENTS`, find the matching `[ ]` item in `docs/schedule.md`.

Otherwise, read `docs/schedule.md` and select the **first unchecked `[ ]` item** that is not in the `## 10. Future / Backlog` section (those are speculative; skip them unless explicitly requested). Prefer items in lower section numbers (higher priority).

State clearly which item you selected and why.

Create and check out a working branch for this item before making any changes:

```bash
# Derive a short slug from the item text (lowercase, hyphens, max 40 chars)
git checkout -b backlog/<slug> 2>/dev/null || git checkout backlog/<slug>
```

## 2. Research

Before touching any code:

- Read all files directly relevant to the item (the types, controllers, tests, and docs it references).
- Read any design docs in `docs/api/` or `docs/guides/` that describe the feature.
- Check `CLAUDE.md` for architectural constraints.
- Identify every file that will need to change. List them explicitly.

If the item is ambiguous or has design choices to make, state your assumptions clearly before proceeding. If a question genuinely cannot be answered without user input (blocked on a policy, security, or API design decision), add it to `docs/tech-debt/pending-input-required.md` tagged `<!-- BACKLOG-PROMPT -->` so it surfaces next session, then continue with the safest fallback assumption.

## 3. Implement

Make all required code changes. Follow these rules:

- After any change to types in `api/v1alpha1/`, run: `make generate && make manifests`
- After generating CRD types, add a sample CR in `config/samples/` if one is missing.
- After adding RBAC markers, re-run `make manifests`.
- Add or update Ginkgo tests for any new logic.
- Run `gofmt -w .` after all edits.

Use parallel sub-agents if the item has clearly independent sub-tasks (e.g., implementing a type AND writing its docs simultaneously). Each sub-agent should work on non-overlapping files. If two tasks touch shared files (e.g., `cmd/main.go`), do them sequentially.

## 4. Validate

Run the following in order. Fix any failures before proceeding to the next step.

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

If `make generate` or `make manifests` was run during implementation, also run:

```bash
make generate
make manifests
go build ./...
```

Do NOT commit until all three validation steps pass with zero failures.

## 5. Mark complete and commit

- In `docs/schedule.md`, change the item's `[ ]` to `[x]`. If the item has sub-tasks, mark all completed sub-tasks `[x]`. If sub-tasks were intentionally skipped, leave them `[ ]` and note why.
- Commit with a conventional commit message (`feat:`, `fix:`, `chore:`, `docs:` as appropriate) referencing the item.

## 6. Open pull request

Push the working branch and open a PR against `main`:

```bash
git push -u origin HEAD
```

Then open a PR with `gh pr create` using this body template:

```
## Summary
- Backlog item: <copy item text from schedule.md>
- <1-2 bullets describing what was implemented>

## Changes
- <bullet list of files changed>

## Test plan
- go build ./... ✓
- go vet ./... ✓
- go test ./... ✓ (<N> tests passed)
- <any manual steps needed to verify>

## Decisions made
<list any architectural assumptions or fallback choices made during implementation>

## Pending inputs needed
<if any BACKLOG-PROMPT items were added to pending-input-required.md, list them here; otherwise write "None">

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## 7. Report

Output a concise summary:
- **Item completed**: (copy the item text)
- **Branch**: name of the working branch
- **PR**: URL of the opened pull request
- **Files changed**: (list)
- **Tests**: pass count, any skipped
- **Pending inputs**: any questions added to pending-input-required.md
- **Notes**: any design decisions or known limitations
