# Backlog groom: 2026-04-05

## Reordering applied

- **§23 (Code Review Findings) moved above §19 (Manual E2E)** — Rule 2 + Rule 10 violated. §23 contains two P0 bugs (Cancelled FlowRun GC exemption; Basic auth timing side-channel) that would produce misleading E2E results if not fixed first. Physical section order changed from `…§21 → §19 → §23 → §24…` to `…§21 → §23 → §24 → §19…`.
- **§24 (Documentation Review Findings) moved above §19 (Manual E2E)** — Rule 10 violated. §24 contains three P0 doc-reality mismatches (webhook-security.md auth fields, integration.md status fields, phantom KafkaIntegrationSpec/PluginIntegrationSpec fields) that would invalidate manual validation of those features.

## Items added

None. All findings from §20/§21 are already captured in §23/§24.

## Items removed or annotated

None.

## Items promoted from Future/Backlog

None. No §10 items are evidenced by recent findings.

## Prioritization rationale updated

Added rule 10: **§23/§24 P0 fixes before §19 manual E2E** — P0 findings from code/doc review must be resolved before manual validation. E2E over broken or incorrectly documented behavior produces misleading results and may need to be re-run.

## §19 gate condition updated

Header updated from "Run after §22 VERIFY, §20, and §21 complete" to "Run after §22 VERIFY, §20, §21, §23 P0, and §24 P0 complete".

## §23 header updated

Added "Must precede §19 manual E2E" note consistent with §24.

## No-change items

54 items reviewed, no content changes beyond reordering and gate condition updates.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
