# PI-1

**Status:** Committed — 2026-09-12 (the first PI under this planning structure; see note below).

## Objective

Get KubeZap ready to go open-source and submit to OperatorHub: close the release-readiness gaps identified in `EPIC-001`, then complete the existing `docs/schedule.md`-era submission gates (spec-drift check, e2e green, code/doc review clean — see `EPIC-001`'s Related Design Docs for where those live now).

## Committed epic order

1. **EPIC-001** — Open Source Release Readiness (vulnerability/dependency management, community-health files, docs cleanup, test-suite value review, website, support channel) — blocks the public launch itself.

## Timeframe

No fixed deadline — flow-based, reviewed at each `/checkpoint`, not against a calendar target.

## Out of scope for PI-1

- Any new Trigger/Flow/Integration feature work (`Step` CRD, multi-namespace flows, additional brokers, plugin marketplace, etc. — see `planning/backlog/backlog.md`'s Future/Backlog candidates) until the release-readiness epic is done.
- Image signing/provenance (cosign/SLSA) — real, separate scope with its own design questions; a backlog candidate, not committed to this PI.

## Note on history before this PI

This is the first PI planned under the SchoolCircle-derived planning structure (`planning/`, adopted 2026-09-12, replacing `docs/schedule.md`'s ad-hoc numbered sections). All work before this point — spec-drift fixes, the interpolation security audit, webhook attack-surface hardening, the typed Phase enum refactor, the e2e validation pass, etc. — was completed under the old process and is not retroactively modeled as a prior PI; its still-relevant technical rationale lives in `docs/design/` (indexed at `docs/design/README.md`), and the code/tests are the record of everything else. Nothing here should be read as "PI-1 is actually the Nth phase of work" — it's PI-1 because it's the first one under this structure.
