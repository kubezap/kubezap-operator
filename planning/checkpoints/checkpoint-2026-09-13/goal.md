# Checkpoint 2026-09-13 — Goal

## Since last checkpoint

Everything dispatched at the 2026-09-12 checkpoint shipped:

- **EPIC-003 (WebhookGatewayConfig CRD)**: STORY-010 (PDB reconciliation, PR #196), STORY-011 (hard-cutover TLS annotation migration, PR #197) both merged. Only STORY-012 (docs for the shipped CRD) remains — unblocked, ready to groom/dispatch.
- **EPIC-004 (Execution Latency Benchmarking)**: STORY-014 (harness, PR #195) and STORY-015 (results write-up, PR #199) both merged — epic fully closed. Go/no-go: **pursue "faster/lighter than Pod-per-step engines"** as an engineering focus and stated differentiator, backed by real numbers (KubeZap ~5-10ms/step vs. Argo ~3-4s pod-lifecycle/~23s total per 3-step run).
- **EPIC-002 (Post-Release Hardening)**: STORY-017 (goconst cleanup, PR #198) and STORY-018 (webhook marker fix, PR #194) both merged.
- **EPIC-001**: STORY-016 (broken doc links, PR #193) merged.
- Plus 2 small bugs found and fixed directly during dispatch (not their own stories): `config/crd/kustomization.yaml` was silently missing the WebhookGatewayConfig CRD from `make install`; `docs/api/trigger.md`'s inbound-mTLS section was left stale by STORY-011's cutover.

## This checkpoint's focus

Per owner decision (2026-09-13): **STORY-012** (close out EPIC-003), **STORY-003** (finish the brittle-test write-up — the one thing still blocking STORY-007's final release rollup), and **groom something from Backlog Candidates** (specific item TBD — the outbound-TLS-annotation doc/code mismatch and the observability-guide real-world validation gap are both flagged as real, undecided candidates).

Two new follow-ups triaged into concrete stories this checkpoint (both in EPIC-002):
- STORY-019 — add the missing FlowRun webhook marker, `failurePolicy: Ignore` (owner-decided).
- STORY-020/021 — design record + implementation for sub-second FlowRun step-timing visibility (currently only visible via a Prometheus scrape, not `kubectl get flowrun`), motivated directly by STORY-015's benchmark findings.

## Backlog changes

- STORY-010/011/014/015/016/017/018 all flipped to `Done` with merged PR numbers.
- STORY-012 unblocked (all 3 dependencies now Done).
- STORY-019/020/021 added to EPIC-002.
- EPIC-002/003/004 Status headers updated to reflect current state (EPIC-004 fully closed; EPIC-003 4/5 done; EPIC-002 back to In Progress with 3 new stories).
