# Backlog groom: 2026-03-27

## Reordering applied

- **§18 moved before §16** in schedule order — Rule #2 (bugs before validation) and Rule #3 (prerequisites before dependents): `StepRunStatus.Attempts` bug fix and `security-checklist.md` are prerequisites to a meaningful VALIDATION pass. Both the §18 section header and §16 VALIDATION item note now explicitly state the dependency.
- **Duplicate OperatorHub submission PR removed** — appeared twice in §1 (top-level + under "OLM Readiness" subsection). The "OLM Readiness" subsection has been collapsed into the §1 header note.

## Items added

None — no new evidence from findings or architectural constraints warranted new items. §18 items were already present from the 2026-03-27 review.

## Items removed or annotated

- **Stale prioritization rationale rules 3, 4, 5** (MockEndpoint replacement, `type: http` Integration before examples, MockEndpoint code removal last) — those sections no longer exist in the active schedule; the rationale was stale noise. Removed.
- **Prioritization rationale rule 9** (§17 gates OperatorHub) — §17 is fully complete; the rule is superseded by the §18 P1 gate. Removed and replaced with new rule 7.
- **Duplicate OperatorHub submission PR entry** under OLM Readiness subsection — removed. One entry at §1 top level is sufficient.

## Items promoted from Future/Backlog

None.

## No-change items

10 items reviewed, no change needed (§15 dashboard futures, §17 completion note, §10 future items).

## Files changed

- docs/schedule.md
- docs/groom-latest.md
