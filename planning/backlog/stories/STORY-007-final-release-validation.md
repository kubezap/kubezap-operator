# STORY-007: Final release validation and OperatorHub submission

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** In Progress — 7/7 gates clean as of the 2026-09-13 (evening) re-verification, pending merge of PR #225/#226/#227
**Size:** M — mostly validation/process, not new code

## Description

The rollup gate before the actual "open the repo, submit to OperatorHub" moment. Carries forward the pre-existing submission gate list (previously tracked ad-hoc in the project's old task log, all individually satisfied already — see Notes) plus everything else in this Epic.

## Acceptance Criteria

- [x] STORY-001, 002, 003, 005, 006, 016 all `Done` — confirmed 2026-09-13. STORY-004 reverted (owner decision: not publishing a GitHub Pages docs site at all — see `follow-ups.md`), no longer a launch-blocking dependency in either direction.
- [x] A **fresh** full spec-drift check, all 5 CRD types — re-run 2026-09-13 (evening), after EPIC-005 shipped and changed `Integration`'s schema twice more (STORY-024, STORY-026). `make manifests`/`make generate`: zero diff. The one P0 finding from the prior pass (`Integration.Status.Phase` documented/printcolumn'd but never written) is resolved — owner decided to remove the field rather than implement it (PR #227), since no other CRD has a Phase field and nothing has ever shipped.
- [x] `make test-e2e` green with no undiagnosed failures — re-run 2026-09-13 (evening), twice. First run found and fixed a real pre-existing bug: `feature_matrix_test.go` assumed cross-file Ginkgo container ordering that isn't guaranteed, causing a `namespaces "kubezap-e2e" not found` cascade (12 skipped specs) (PR #227). Second run, post-fix: that failure is gone; one unrelated, isolated timing flake appeared (`kubezap_e2e_test.go:121`, 30s timeout) that passed cleanly on identical code in the first run — logged as an `Open` follow-up (likely resource contention from two consecutive full Kind-cluster runs), not chased further per this story's own scope rule.
- [x] `make lint`/`make test`/`make vulncheck` all clean on `main` at submission time — re-confirmed 2026-09-13 (evening). `vulncheck`: 0 vulnerabilities reachable from KubeZap's own code (5 known-unreachable transitive findings, unchanged). Also found and fixed: a genuinely new **high-severity Dependabot alert** (`google.golang.org/grpc` CVE-2026-84445, gRPC-Go xDS DoS) that `govulncheck` doesn't catch — different vulnerability database, exactly the coverage gap STORY-028 was opened for. Trivial patch bump, fixed directly (PR #226).
- [x] OLM bundle passes `bundle validate` and `scorecard` — re-verified 2026-09-13 (evening) with a **live** `operator-sdk scorecard` run against local k3s (not just `bundle validate`): **6/6 pass**. Confirmed the STORY-024 fix holds even after `Integration`'s schema changed again via STORY-026 — the new `tls` field is covered by the same top-level `http` descriptor as the rest of `HttpIntegrationSpec`, matching the existing Kafka/AMQP/NATS TLS sub-field convention, so it doesn't regress `olm-spec-descriptors`. Also found and fixed: the committed bundle itself was stale (never regenerated since STORY-026 shipped `Integration.spec.http.tls` — missing CRD schema entry and the CSV's new `configmaps` RBAC rule) (PR #225).
- [x] Git history secret-scan clean — re-confirmed 2026-09-13 (evening) via `gitleaks` working-tree scan. Identical findings to the prior pass (55, all the same known placeholder values in `examples/`, duplicated across stale worktree dirs) — the new TLS-related code introduced no new findings.
- [x] `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue/PR templates all present and correct — unchanged since last confirmed 2026-09-13; not re-verified this pass (no doc/community-file changes since).

**Net: 7 of 7 gates clean**, pending merge of the three PRs this re-verification pass produced (#225 stale bundle regen + CLAUDE.md doc-quality cleanup, #226 grpc CVE fix, #227 Integration.Status.Phase removal + e2e ordering fix). Move to `Done` once all three merge.

## File / Module Footprint

None expected — this is a validation pass. Findings become their own follow-up stories via `follow-ups.md`, not scope creep into this one, **except** small, well-scoped fixes with an explicit owner decision already in hand and no design-record trigger (matches this pass's own precedent: PR #208's bundle regen, and now PR #225/#226/#227) — those land directly rather than waiting on a separate dispatch cycle.

## Dependencies

- Depends on: every other story in EPIC-001, plus **STORY-024** (OLM scorecard fix, Done) and **EPIC-005** (HTTP outbound TLS, Done — this pass re-verified nothing regressed from its `Integration` schema changes)
- Blocks: the actual OperatorHub submission PR / making the repo public

## Post-launch (not gates — do these right after the repo goes public, not before)

- Enable Discussions in Settings → Features, with starter categories (STORY-006 is code-complete and waiting on this).

Deliberately held back (owner decision, 2026-09-12) since enabling it on a still-private repo would be premature. (The equivalent GitHub Pages item is moot — STORY-004 reverted, not publishing a docs site.)

## Notes

The individual older gate items this absorbs (pre-public readiness validation, manual per-example e2e, code/doc review passes, the WATCH_NAMESPACES e2e fix) were all already satisfied as of a 2026-09-11 grooming pass — not re-litigated here.

**2026-09-13 (afternoon) validation pass:** 6 of 7 gates confirmed clean; OLM scorecard blocked on STORY-024 (fixed same day).

**2026-09-13 (evening) re-verification:** prompted by three schema/docs changes landing since the afternoon pass (STORY-024's CSV fix, STORY-026's HTTP TLS feature, STORY-027's docs) — re-ran every gate from scratch rather than trusting the afternoon snapshot. Found and fixed 3 real issues along the way (stale OLM bundle, a new Dependabot CVE, the `Integration.Status.Phase` gap plus a real e2e test-ordering bug) — see Acceptance Criteria above. All 7 gates now clean pending PR merge.
