# Checkpoint 2026-09-12 — Review

## Shipped this checkpoint

- STORY-001 — merged (PR #181): docs cleanup pass, 3 broken links fixed.
- STORY-002 — merged (PR #179): `docs/contributing.md` cleanup, found a missing http-executor build step and stale reconciler table.
- STORY-004 — merged (PR #184): GitHub Pages site via MkDocs; site not live yet (Pages source not enabled — deliberately deferred to launch).
- STORY-005 — merged (PR #180): community-health files (`CODE_OF_CONDUCT.md`, issue/PR templates).
- STORY-006 — merged (PR #183): README Discussions link; Discussions not enabled yet (same deliberate deferral as STORY-004).
- STORY-008 — merged: `WebhookGatewayConfig` design record, Approved.
- STORY-009 — merged (PR #190): `WebhookGatewayConfig` CRD + HPA reconciliation + singleton webhook, including a wiring-pass fix for a gap the dispatching agent's own report flagged.
- STORY-013 — merged (PR #189): execution-latency benchmark methodology.
- Also: 4 pre-existing e2e failures fixed + 4 more found and fixed (PR #174); open-source positioning cleanup (PR #175); PI-1 scope revision adding EPIC-003/004 (PR #176); multiple grooming/planning passes (PRs #177, #178, #186, #187, #188).

## Still open

- STORY-003 — partially done (e2e-failures AC satisfied); brittle-test review + write-up remain.
- STORY-007 — blocked on STORY-003 and STORY-016.
- STORY-010, STORY-011 — groomed, unblocked, not yet dispatched. **Owner priority for next stretch.**
- STORY-012 — blocked on STORY-010/011.
- STORY-014 — groomed, unblocked, not yet dispatched.
- STORY-015 — blocked on STORY-014.
- STORY-016, STORY-017, STORY-018 — new this checkpoint, groomed, not yet dispatched.
- STORY-004/006's admin-only follow-ups (Pages source, Discussions) — intentionally waiting for public launch, not a gap.

## What worked / what to change

- **Dispatched-agent work needs the same scrutiny as any other work, not just a pass/fail on its own report.** STORY-009's agent correctly flagged in its own report that it hadn't wired its new HPA-config-reading function into the real reconcile call site (that file was off-limits per the dispatch brief, for an unrelated reason) — catching that and doing the wiring pass myself before merging is exactly what "verify its work meets the acceptance criteria" is for, not just "did it build and pass tests."
- **Verify claims against live systems rather than trusting them at face value** — checking `gh api .../pages`, `.../community/profile`, and `has_discussions` directly caught that "should be enabled" wasn't actually true yet, and that an "indexing lag" theory didn't hold up on re-check. Worth doing this reflexively whenever a story's completion depends on external (non-repo) state.
- **Process change, saved to memory**: batch planning-only PRs (grooming, PI planning, status updates) from one session into a single PR instead of one per action — several single-action PRs this session caused avoidable merge conflicts on `backlog.md`. Now in `CLAUDE.md` and a feedback memory.
- **A design decision made collaboratively, fast, beats deferring it.** The WebhookGatewayConfig design record's three open questions (singleton mechanism, migration path, HA validation) got resolved in one short round of targeted questions rather than blocking STORY-009 on a separate design-review cycle.

## Backlog re-groom notes

- `EPIC-002` populated for the first time (previously a reserved-but-empty slot) — now holds STORY-017 (`goconst` cleanup) and STORY-018 (webhook marker fix), both triaged from `follow-ups.md` this checkpoint.
- STORY-016 added to `EPIC-001` (broken doc links from the MkDocs build) — also added to STORY-007's own rollup-gate AC so it isn't missed.
- `follow-ups.md`: 3 entries moved from `Open` to `Routed`; 1 re-confirmed still `Open` (community-profile `issue_template`) with its working theory corrected (not indexing lag) but left deliberately unresolved for now.
