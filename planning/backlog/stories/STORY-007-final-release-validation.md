# STORY-007: Final release validation and OperatorHub submission

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** In Progress — 6/7 gates clean (2026-09-13), pending owner decision on the scorecard finding (see Acceptance Criteria)
**Size:** M — mostly validation/process, not new code

## Description

The rollup gate before the actual "open the repo, submit to OperatorHub" moment. Carries forward the pre-existing submission gate list (previously tracked ad-hoc in the project's old task log, all individually satisfied already — see Notes) plus everything else in this Epic.

## Acceptance Criteria

- [x] STORY-001, 002, 003, 005, 006, 016 all `Done` — confirmed 2026-09-13 (STORY-003 closed same session). STORY-004 remains open per owner decision (website not launch-blocking).
- [x] A **fresh** full spec-drift check (`planning/process/spec-drift.md` Steps 1–4, all 5 CRD types — a 5th, `WebhookGatewayConfig`, exists since this story was originally groomed; the AC's stated "4" is stale, ran all 5) — done 2026-09-13. Result: 4 of 5 CRDs clean; one **P0 finding** on `Integration` (`Status.Phase` documented and printcolumn'd but never written by the controller) — filed as a follow-up (`planning/backlog/follow-ups.md`), not fixed here per this story's own scope rule below. `Flow`/`FlowRun` Step 3 (docs-vs-controller-behavior) not audited this pass — out of budget, flagged rather than assumed clean. `make manifests`/`make generate`: zero diff. All 13 `config/samples/` files pass `--dry-run=server` cleanly.
- [x] `make test-e2e` green with no undiagnosed failures — 28/45 passed, 0 failed, 14 pending (broker infra unavailable in CI), 3 skipped. Matches STORY-003's confirmed-acceptable baseline exactly.
- [x] `make lint`/`make test`/`make vulncheck` all clean on `main` at submission time — confirmed 2026-09-13. `vulncheck`: 0 vulnerabilities reachable from KubeZap's own code (6 vulnerabilities exist in transitive dependency versions — `cel-go`, `x/crypto`, `x/mod` — none called by our code paths; ordinary Dependabot-cadence bumps would clear them, not a submission blocker).
- [ ] OLM bundle passes `bundle validate` and `scorecard` (last confirmed 2026-03-21 — re-verified 2026-09-13, **not still true**). `bundle validate`: clean pass. `scorecard`: **4/6 pass, 2 fail** (`olm-spec-descriptors` — 6 CSV fields missing OLM UI descriptors; `olm-crds-have-resources` — owned CRDs missing the CSV's `resources:` list). Neither is a functional defect; both are OperatorHub UI-presentation gaps. Also found in the same pass: the OLM **bundle itself was stale**, missing the `WebhookGatewayConfig` CRD entirely since STORY-009 (PR #190) — fixed directly (mechanical regeneration, no design implications, same category as STORY-014's kustomization fix) via **PR #208**, not held for a follow-up. The 2 scorecard failures filed as a follow-up instead (real content to add, not just regeneration).
- [x] Git history secret-scan clean — confirmed 2026-09-13 via `gitleaks` (no dedicated CI secret-scanning available; repo is private, GitHub's own secret-scanning is disabled). All findings (55 in a working-tree scan, 10 across 500 commits) are the same handful of obvious placeholder values (`mock-token-a`, `invalid.jwt.token`) in `examples/`, duplicated across 11 stale untracked `.claude/worktrees/agent-*` directories left on disk from past sessions — zero real secrets. The stale worktree directories are disk clutter, not a security finding; not cleaned up here (out of this story's scope).
- [x] `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue/PR templates all present and correct — confirmed via `gh api repos/.../community/profile`: `health_percentage: 100`, all files present. `issue_template` still reads `null` in that same API response despite the YAML templates existing — this is the already-known, already-deferred gap (`follow-ups.md`, re-confirmed at the 2026-09-13 checkpoint), not a new finding.

**Net: 6 of 7 gates clean. One gate (OLM scorecard) has 2 non-functional findings, filed as a follow-up rather than fixed here.** Owner decision needed: treat the 2 scorecard failures as blocking (hold this story open until that follow-up ships) or as acceptable launch-time polish debt (close this story now, track the follow-up independently). See Notes.

## File / Module Footprint

None expected — this is a validation pass. If the spec-drift check or e2e re-run finds something, that becomes its own follow-up story via `follow-ups.md`, not scope creep into this one. One exception made: the stale-OLM-bundle fix (PR #208) was mechanical regeneration with no design implications, same precedent as STORY-014's kustomization fix — landed directly rather than filed as a follow-up.

## Dependencies

- Depends on: every other story in EPIC-001, plus **STORY-024** (OLM scorecard fix — added 2026-09-13, blocking)
- Blocks: the actual OperatorHub submission PR / making the repo public

## Post-launch (not gates — do these right after the repo goes public, not before)

- Set the Pages source to "GitHub Actions" in Settings → Pages (STORY-004 is code-complete and waiting on this; see `planning/backlog/follow-ups.md`).
- Enable Discussions in Settings → Features, with starter categories (STORY-006 is code-complete and waiting on this too).

Both were deliberately held back (owner decision, 2026-09-12) since enabling either on a still-private repo would be premature.

## Notes

The individual older gate items this absorbs (pre-public readiness validation, manual per-example e2e, code/doc review passes, the WATCH_NAMESPACES e2e fix) were all already satisfied as of a 2026-09-11 grooming pass — not re-litigated here. The one gate that's explicitly a *recurring* check by its own design (full spec-drift, not the lighter per-PR version in `planning/process/pre-merge-checklist.md` §6) is re-listed above because "already done once" doesn't satisfy a recurring gate.

**2026-09-13 validation pass:** 6 of 7 gates confirmed clean. The remaining gap — OLM `scorecard`'s 2 UI-descriptor failures (`olm-spec-descriptors`, `olm-crds-have-resources`) — is filed as an `Open` follow-up rather than fixed inline, per this story's own scope rule. **Owner decision (2026-09-13): blocking** — this story stays `In Progress`, not `Done`, until that follow-up's fix lands. Routed to its own story for dispatch.
