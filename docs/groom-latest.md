# Backlog groom: 2026-04-04

## Reordering applied

- **§20 (Code Review) and §21 (Doc Review) moved above §19 (Manual E2E)** — Rule 1 (research before tests): code review may surface bugs that invalidate manual validation results; doc review may expose example incorrectness. Both must complete before the full manual E2E pass.
- **Prioritization rationale: rules 8 and 9 added** — Rule 8: automated e2e (§22) gates manual e2e (§19). Rule 9: research tasks §20/§21 gate §19 manual validation. Previously undocumented ordering constraints.

## Items added

None — all new items were already added during the 2026-04-04 session (§19, §20, §21, §22) that preceded this groom. No additional evidence warranted new items.

## Items removed or annotated

- **§1 OperatorHub submission gate language updated** — Previous text: "gates on §16 P1 VALIDATION + §18 P1 items complete". Updated to reflect: §18 P1 items are done (✓), and new gates added: §22 VERIFY (e2e tests green), §20 code review clean, §21 doc review clean.
- **§16 VALIDATION note updated** — Added "after §22 VERIFY and §20/§21 research complete" to the item description to make the dependency explicit.
- **§19 section header updated** — Added "Run after §22 VERIFY, §20, and §21 complete" note per new rules 8–9.
- **§20 header updated** — Added "Must precede §19 manual E2E" note per rule 9.
- **§21 header updated** — Added "Must precede §19 manual E2E" note per rule 9.

## Items promoted from Future/Backlog

None.

## No-change items

7 items reviewed with no change needed (§15 dashboard futures, §17 completion note, §10 future items, §18 P2 items).

## Files changed

- docs/schedule.md
- docs/groom-latest.md
