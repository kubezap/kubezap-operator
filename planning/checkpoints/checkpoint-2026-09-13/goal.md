# Checkpoint 2026-09-13 — Goal

## Since last checkpoint

Everything dispatched at the 2026-09-12 checkpoint shipped, plus a full round dispatched and shipped today:

- **EPIC-003 (WebhookGatewayConfig CRD)**: STORY-010 (PDB reconciliation, PR #196), STORY-011 (hard-cutover TLS annotation migration, PR #197), and now **STORY-012** (docs, PR #202) all merged. **Epic fully closed.**
- **EPIC-004 (Execution Latency Benchmarking)**: fully closed as of last checkpoint (STORY-014/015).
- **EPIC-002 (Post-Release Hardening)**: STORY-017/018 merged last checkpoint; **STORY-019** (FlowRun webhook marker, PR #203) and **STORY-020** (step-timing design record, PR #204) merged today. **STORY-022** (webhook auth test coverage, found during STORY-003) newly groomed, ready to dispatch.
- **EPIC-001**: **STORY-003** (test suite value review) closed today — all 3 ACs done, no brittle/tautological tests found in `internal/controller/`, one real security-relevant gap found in webhook auth test coverage (routed to STORY-022). This unblocks **STORY-007** (final release validation), now genuinely ready.
- **Process fix (PR #206)**: `/dispatch-work` was instructing a direct merge to `main` instead of opening a PR per story — caught live after dispatching today's first batch that way, corrected (reset + redone as PRs #202/#203/#204), and the skill files + `CLAUDE.md` rewritten so this doesn't recur. Also codified: `/groom-backlog`/`/plan-parallel` edits stay staged (not committed/PR'd) until an explicit ask or until a later `/dispatch-work` batch's story PRs all merge — final planning-only PR #207 closes that loop for today's batch.

## This checkpoint's focus

Per owner decision (2026-09-13 afternoon checkpoint), next stretch prioritizes:
- **STORY-007** — final release validation and OperatorHub submission rollup gate (now unblocked).
- **STORY-022** — webhook auth test coverage (`bearer`/`apiKey`/`basic`/`headerEquals` have zero functional coverage today).
- **Groom a Backlog Candidate**: the outbound-TLS-annotation doc/code mismatch — `docs/api/trigger.md` documents 3 annotations (`kubezap.io/tls-ca-secret`, `tls-client-cert-secret`, `tls-insecure-skip-verify`) that don't exist anywhere in the Go code. Needs a decision (implement for real, security-facing, design record required — vs. delete the fictional docs and point at the real `Integration`-field mechanism) before it can be scoped into a story.

All three `Open` `follow-ups.md` entries triaged this checkpoint:
- STORY-005's `issue_template: null` gap — re-confirmed deferred, no change.
- STORY-004/006's admin-only actions (Pages source, Discussions) — re-confirmed deferred to public launch, no change.
- Webhook auth test coverage gap (new, from STORY-003) — routed to **STORY-022**.

## Backlog changes

- STORY-003/012/019/020 flipped to `Done` with merged PR numbers (STORY-003 has no PR — pure investigation/write-up).
- STORY-007 unblocked (STORY-003 Done) — status updated to reflect it's ready to dispatch.
- STORY-021 unblocked (STORY-020 Done) but still needs `/groom-backlog` before it's dispatchable — not yet real-footprinted.
- STORY-022 added to EPIC-002, Groomed, ready to dispatch — no design record needed (test-only addition to already-correct auth logic).
- EPIC-002/003 Status headers updated (EPIC-003 now fully closed; EPIC-002 at 4 done + 2 open).
