# Checkpoint 2026-09-12 — Goal

First `/checkpoint` run under the new `planning/` structure (adopted this same day) — "since last checkpoint" below covers the whole session, not a prior checkpoint boundary.

## Since last checkpoint

**Shipped:**
- **Process migration**: `docs/schedule.md`'s ad-hoc task log replaced with the `planning/` Epic/Story/PI/checkpoint structure (Epics, Stories, PI-1, this checkpoint ritual).
- **e2e fixes** (PR #174): all 4 pre-existing `make test-e2e` failures root-caused and fixed (an SSRF-bypass scoping bug, a metrics-test ordering dependency, a redundant controller redeploy that was the actual root cause of the SSRF tests' flakiness), plus 4 more latent bugs found along the way (two wrong JSONPaths, a dead CRD field in a test fixture, a real `dependenciesMet` controller bug affecting `onFailure: Continue` semantics). `make test-e2e` went from 4 known failures to 28 passed / 0 failed.
- **Open-source positioning cleanup** (PR #175): MockEndpoint doc references removed, README "Why KubeZap" section, SchoolCircle-naming/acquisition-framing removed from planning docs.
- **PI-1 scope** (PR #176): added `EPIC-003` (WebhookGatewayConfig CRD) and `EPIC-004` (execution latency benchmarking) as parallel tracks alongside `EPIC-001`.
- **EPIC-001 fully groomed and mostly done** (PRs #177–185): STORY-001/002/004/005/006 all merged. STORY-004/006 have one outstanding *admin* action each (Pages source, Discussions) — deliberately deferred until public launch, not blocked/forgotten.
- **EPIC-003 (WebhookGatewayConfig CRD)**: design record written directly with the owner (singleton via admission webhook, hard annotation cutover, plain-passthrough `minReplicas`) — `docs/design/2026-09-12-webhookgatewayconfig-crd.md`, Approved. STORY-009 (CRD + HPA reconciliation + singleton webhook) merged (PR #190), including a wiring-pass fix for a gap the dispatching agent itself flagged (HPA config wasn't actually reaching the live reconcile loop until a follow-up commit).
- **EPIC-004 (execution latency benchmarking)**: STORY-013 methodology doc merged (PR #189) — correctly identified and avoided a real methodological trap (Argo's native `http` template isn't actually Pod-per-step).
- **Process fix**: batching rule added to `CLAUDE.md` after several single-action planning PRs caused avoidable merge conflicts on `backlog.md`.
- **Real bug found and logged**: `+kubebuilder:webhook` markers suffer the same silent-drop failure as the known `+kubebuilder:rbac` gotcha — `config/webhook/` had never actually been generated for the Trigger or FlowRun webhooks.

**Still in flight / blocked:**
- STORY-003 (test suite value review) — partially done (its e2e-failures AC was satisfied by the PR #174 work); brittle-test review + write-up still open.
- STORY-007 (final release validation, the EPIC-001 rollup gate) — blocked on STORY-003 and STORY-016 (new).
- STORY-010/011/012 (EPIC-003's remaining stories) — 010/011 now unblocked (STORY-009 merged), 012 (docs) still waits on those two.
- STORY-014/015 (EPIC-004's remaining stories) — 014 now unblocked (STORY-013 merged), 015 waits on it.
- STORY-016/017/018 — new, groomed this checkpoint from follow-ups triage, all ready to dispatch, none started.

## This checkpoint's focus

Owner priority: **finish EPIC-003** — STORY-010 (PodDisruptionBudget reconciliation), STORY-011 (TLS annotation hard cutover), then STORY-012 (docs). Both 010/011 are unblocked and parallel-safe with each other pending a footprint check at dispatch time (STORY-010's own notes flag they might land in the same file as STORY-009's HPA work — confirm before treating them as parallel).

## Backlog changes

- New `EPIC-002` (Post-Release Hardening & Feature Backlog) populated for the first time — was a reserved-but-empty slot since the PI-1 planning session.
- 3 new stories groomed from `follow-ups.md` triage: STORY-016 (EPIC-001, broken doc links), STORY-017 (EPIC-002, `goconst` cleanup), STORY-018 (EPIC-002, webhook marker fix).
- `follow-ups.md`: 3 entries routed, 1 re-confirmed and explicitly deferred (community-profile `issue_template` gap — indexing-lag theory disproven on re-check, but not urgent).
