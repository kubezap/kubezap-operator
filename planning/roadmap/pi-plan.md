# PI-1

**Status:** Committed — 2026-09-12 (the first PI under this planning structure; see note below). Scope revised 2026-09-12 and 2026-09-13 — see Revisions.

## Objective

Get KubeZap ready to go open-source and submit to OperatorHub: close the release-readiness gaps identified in `EPIC-001`, then complete the existing `docs/schedule.md`-era submission gates (spec-drift check, e2e green, code/doc review clean — see `EPIC-001`'s Related Design Docs for where those live now). `EPIC-001` and, as of 2026-09-13, `EPIC-005` (HTTP Step Outbound TLS/CA Support) are both launch-blocking — the public launch waits on both closing, not just `EPIC-001`. `EPIC-003`/`EPIC-004` were committed as parallel, non-blocking tracks and are now both fully closed.

## Committed epic order

1. **EPIC-001** — Open Source Release Readiness (vulnerability/dependency management, community-health files, docs cleanup, test-suite value review, website, support channel) — blocks the public launch; stays the priority. STORY-007 (final release validation) is the one remaining open item.
2. **EPIC-005** — HTTP Step Outbound TLS/CA Support (Integration-scoped CA-bundle/client-cert config for HTTP steps, extending the controller→executor RPC protocol) — **added 2026-09-13, also blocks the public launch**, alongside `EPIC-001` rather than after it. Not yet groomed past rough candidate stories; its first story is a design record (CRD/type change + RPC credential-handling change, per `design-process.md`'s trigger list) before any implementation can start.
3. ~~**EPIC-003**~~ — WebhookGatewayConfig CRD — **Done**, fully closed 2026-09-13 (all 5 stories merged).
4. ~~**EPIC-004**~~ — Execution Latency Benchmarking — **Done**, fully closed 2026-09-12 (go/no-go: pursue).

## Timeframe

No fixed deadline — flow-based, reviewed at each `/checkpoint`, not against a calendar target.

## Out of scope for PI-1

- New Trigger/Flow/Integration feature work beyond `EPIC-005` above (`Step` CRD, multi-namespace flows, additional brokers, plugin marketplace, etc. — see `planning/backlog/backlog.md`'s Future/Backlog candidates) until the release-readiness epics are done.
- Image signing/provenance (cosign/SLSA) — real, separate scope with its own design questions; a backlog candidate, not committed to this PI.

## Revisions

- **2026-09-12**: Added `EPIC-003` (WebhookGatewayConfig CRD) and `EPIC-004` (Execution Latency Benchmarking) to the committed epic order, both as parallel tracks alongside `EPIC-001` rather than sequenced after it — owner decision, made explicitly (not a unilateral scope change). Narrowed the "Out of scope" feature-work exclusion accordingly, since `EPIC-003` is itself new CRD/feature work that the original wording would otherwise have excluded.
- **2026-09-13**: `EPIC-003`/`EPIC-004` both closed. Added `EPIC-005` (HTTP Step Outbound TLS/CA Support — sourced from a follow-up filed during `STORY-023`'s grooming) to the committed epic order, explicitly as a **launch-blocking** epic alongside `EPIC-001` — owner decision, stated directly ("Prioritize epic 5 before release"). This is a real scope escalation, not a parallel-track addition like `EPIC-003`/`EPIC-004` were: the public launch now waits on `EPIC-005` closing too, not just `EPIC-001`. Objective and "Out of scope" bullet updated to match.

## Note on history before this PI

This is the first PI planned under the new planning structure (`planning/`, adopted 2026-09-12, replacing `docs/schedule.md`'s ad-hoc numbered sections). All work before this point — spec-drift fixes, the interpolation security audit, webhook attack-surface hardening, the typed Phase enum refactor, the e2e validation pass, etc. — was completed under the old process and is not retroactively modeled as a prior PI; its still-relevant technical rationale lives in `docs/design/` (indexed at `docs/design/README.md`), and the code/tests are the record of everything else. Nothing here should be read as "PI-1 is actually the Nth phase of work" — it's PI-1 because it's the first one under this structure.
