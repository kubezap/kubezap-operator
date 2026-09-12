# PI-1

**Status:** Committed — 2026-09-12 (the first PI under this planning structure; see note below). Scope revised same-day — see Revisions.

## Objective

Get KubeZap ready to go open-source and submit to OperatorHub: close the release-readiness gaps identified in `EPIC-001`, then complete the existing `docs/schedule.md`-era submission gates (spec-drift check, e2e green, code/doc review clean — see `EPIC-001`'s Related Design Docs for where those live now). `EPIC-001` remains the priority and the thing that actually blocks the public launch; `EPIC-003`/`EPIC-004` are committed as parallel tracks that don't compete with it for files or focus, not as work that must wait for it to close.

## Committed epic order

1. **EPIC-001** — Open Source Release Readiness (vulnerability/dependency management, community-health files, docs cleanup, test-suite value review, website, support channel) — blocks the public launch itself; stays the priority.
2. **EPIC-003** — WebhookGatewayConfig CRD (typed per-namespace gateway config: HPA min/max/target-CPU, PodDisruptionBudget, migrating the `webhook-tls-secret`/`webhook-mtls-ca-secret` Namespace annotations) — runs in parallel with EPIC-001, not blocked on it.
3. **EPIC-004** — Execution Latency Benchmarking (RPC executor vs. Pod-per-step research spike) — runs in parallel with EPIC-001, not blocked on it.

## Timeframe

No fixed deadline — flow-based, reviewed at each `/checkpoint`, not against a calendar target.

## Out of scope for PI-1

- New Trigger/Flow/Integration feature work beyond EPIC-003/004 above (`Step` CRD, multi-namespace flows, additional brokers, plugin marketplace, etc. — see `planning/backlog/backlog.md`'s Future/Backlog candidates) until the release-readiness epic is done.
- Image signing/provenance (cosign/SLSA) — real, separate scope with its own design questions; a backlog candidate, not committed to this PI.

## Revisions

- **2026-09-12**: Added `EPIC-003` (WebhookGatewayConfig CRD) and `EPIC-004` (Execution Latency Benchmarking) to the committed epic order, both as parallel tracks alongside `EPIC-001` rather than sequenced after it — owner decision, made explicitly (not a unilateral scope change). Narrowed the "Out of scope" feature-work exclusion accordingly, since `EPIC-003` is itself new CRD/feature work that the original wording would otherwise have excluded.

## Note on history before this PI

This is the first PI planned under the new planning structure (`planning/`, adopted 2026-09-12, replacing `docs/schedule.md`'s ad-hoc numbered sections). All work before this point — spec-drift fixes, the interpolation security audit, webhook attack-surface hardening, the typed Phase enum refactor, the e2e validation pass, etc. — was completed under the old process and is not retroactively modeled as a prior PI; its still-relevant technical rationale lives in `docs/design/` (indexed at `docs/design/README.md`), and the code/tests are the record of everything else. Nothing here should be read as "PI-1 is actually the Nth phase of work" — it's PI-1 because it's the first one under this structure.
