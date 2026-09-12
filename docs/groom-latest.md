# Backlog groom: 2026-09-12

## Reordering applied

- §39's "Final validation and cleanup pass before going public and submitting to OperatorHub" moved from the last item in P1 to its own new "Final Gate" subsection at the end of §39 — rule violated: prerequisites before dependents. The item's own description says it depends on "the items below," but at its old position those "items below" were the P2 list, which is physically below it in the document. A reader working top-to-bottom would reach the gate before the prerequisites it names. No content change beyond a one-line placement note.
- §1's "OperatorHub submission PR" item updated to also gate on §39 P1 items — rule violated: prerequisites before dependents (§39 exists specifically to track pre-open-source/OperatorHub readiness, but §1's own gate list, last updated before §39 existed, didn't reference it). Added a new Prioritization rationale rule 11 documenting this dependency explicitly, matching the existing pattern (rule 6 does the same for §16).

## Items added

None — no new evidence from `docs/review-latest.md` or `docs/tech-debt/` surfaced anything not already captured by the owner's §39 items or the existing backlog. (§39 itself was populated across the prior two conversation turns, before this grooming pass; not re-added here.)

## Items removed or annotated

- Annotated (not removed): §39's "Review build/release/registry-push automation" and "Vulnerability/dependency management" P1 items both touch `.github/workflows/` — added a note that these are hot files per `CLAUDE.md`'s Parallel Agent Guidelines if picked up in parallel (serialize or assign one wiring pass), to prevent two independent agents editing `ci.yml` at the same time later.
- No stale items found: spot-checked every guide/doc path referenced from an open `[ ]` item (`docs/guides/spec-drift.md`, `pre-merge-checklist.md`, `code-review-strategy.md`, `design-process.md`, `security-checklist.md`, `docs/architecture/flowrun-state-model.md`) — all exist.

## Items promoted from Future/Backlog

None. Reviewed all 9 Future/Backlog items against recent findings (`docs/schedule.md` §38, this session's PR #155) — none have new evidence promoting them to active work. Two of them (`config/webhook`/`config/certmanager` scaffolding, the `kustomization.yaml` image-transformer bug) were *added* to Future/Backlog by the prior session's work (§38), not promoted by this pass.

## No-change items

`docs/tech-debt/pending-input-required.md` — checked for open `<!-- BACKLOG-PROMPT -->` blocks per Step 0; none found, all entries are `<!-- ANSWERED -->`. No `/backlog` blocker.

`docs/review-latest.md` — still the 2026-03-27 review; all its findings are long since closed out (`[x]` in §18). Not stale enough to delete (kept per this file's normal practice of leaving completed-work records as history, per `docs/schedule.md`'s own §31/§33 precedent), but contributed nothing new to this pass.

~530 lines across §1–§39 and Future/Backlog reviewed; no other reordering violations, stale references, or promotion candidates found.

## Files changed

- docs/schedule.md
- docs/groom-latest.md

## Suggested next step

Run `/backlog` to work the top items — likely candidates from §39 P1 are `SECURITY.md` (small, self-contained, no code dependencies) or the git history secret-scan (read-only, informational, no code change risk) as easy, low-conflict first picks; the two `.github/workflows/`-touching items (vulnerability automation, build/release review) should probably go together in one pass given the note added above.
