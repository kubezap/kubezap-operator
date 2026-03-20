# Review: 2026-03-20 (doc focus — user friendliness)

## Doc issues

- **`docs/api/flow.md` + `docs/overview.md` — `$(trigger.payload.*)` vs `$(trigger.body.*)`**: The API docs documented `$(trigger.payload.<field>)` as the expression syntax, but the implementation uses `$(trigger.body.<field>)`. The getting-started guide (validated against actual behavior) correctly uses `trigger.body`. Fixed both files to use the correct `trigger.body.*` syntax and noted the nested field limitation. _(fixed)_
- **`docs/guides/getting-started.md` — `enrich-mockendpoint.yaml` reference**: The guide referenced `config/samples/demo/enrich-mockendpoint.yaml` which does not exist as a standalone file (the `enrich-order` MockEndpoint is bundled in `mockendpoints.yaml` applied in Step 1). Fixed to remove the nonexistent file reference. _(fixed)_
- **`docs/guides/kafka-enrichment.md` — dead link to `gitops-deploy-gate.md`**: Line 321 linked to `[GitOps deployment gate demo](gitops-deploy-gate.md) ← coming soon` which does not exist. Replaced with a note pointing to the Future / Backlog schedule section. _(fixed)_
- **Missing CLI user guide**: The `kubezap` CLI is fully implemented but only had a design doc (`docs/design/cli.md`). No user-facing guide existed showing practical usage. Created `docs/guides/using-the-cli.md` covering `history`, `triggers`, `flows`, `integrations`, and debugging tips. _(created)_
- **Missing cron trigger guide**: No how-to guide existed for the cron trigger type despite it being a primary use case. Created `docs/guides/cron-triggers.md` covering schedule syntax, timezones, FlowRun naming, GC policy, monitoring, and Flow design patterns. _(created)_
- **Missing troubleshooting guide**: Troubleshooting tips were scattered across getting-started, incident-escalation, and other guides with no central reference. Created `docs/guides/troubleshooting.md` consolidating all common issues: controller startup, trigger acceptance, webhook routing, FlowRun lifecycle, CEL errors, MockEndpoint, Kafka, RBAC, and CLI usage. _(created)_

## Code issues

- No code files were read or changed in this review pass.

## Schedule corrections

- Added `[x]` entries for the three new guide files to `docs/schedule.md`.
- Added `[ ]` items for AMQP/NATS guides (awaiting owner decision per pending-input-required.md).

## Decisions needed

- **AMQP/NATS setup guides**: Should `docs/guides/amqp-setup.md` and `docs/guides/nats-setup.md` be created? Options: create now (full guides), add TODO stubs, or leave for when beta label is promoted. (See `docs/tech-debt/pending-input-required.md` — tagged `BACKLOG-PROMPT`.)

## Files changed

- `docs/guides/using-the-cli.md` — created (new CLI user guide)
- `docs/guides/cron-triggers.md` — created (new cron trigger how-to)
- `docs/guides/troubleshooting.md` — created (new consolidated troubleshooting guide)
- `docs/guides/getting-started.md` — fixed `enrich-mockendpoint.yaml` reference
- `docs/guides/kafka-enrichment.md` — fixed dead link to `gitops-deploy-gate.md`
- `docs/api/flow.md` — corrected `trigger.payload.*` → `trigger.body.*` throughout
- `docs/overview.md` — corrected Payload Formats section to use `trigger.body.*`
- `docs/tech-debt/pending-input-required.md` — appended new review section with AMQP/NATS guide question
- `docs/schedule.md` — added new guide items
- `docs/review-latest.md` — this file
