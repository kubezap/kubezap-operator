# Review: 2026-03-27

## Doc issues

- **HIGH** — `WATCH_NAMESPACES` default contradicted across docs: CLAUDE.md and impl say OwnNamespace, but `docs/architecture.md` and `docs/overview.md` said empty = AllNamespaces. **Fixed** in this PR (architecture.md table, overview.md Helm examples, architecture.md values.yaml snippet).
- **HIGH** — `docs/api/trigger.md` FlowReference section showed `namespace` field as supported with no cross-namespace restriction. **Fixed** — updated to document v1alpha1 restriction and admission webhook enforcement.
- **HIGH** — `docs/dev/http-executor.md` status banner still said "Draft — gate document, do not implement." Executor has been fully implemented (§17 P0 complete 2026-03-27). **Fixed** — updated to "Implemented."
- **MED** — `docs/api/flow.md` CEL section had no mention of cost limits or `--cel-cost-limit` flag. **Fixed** — added CEL cost limits paragraph.
- **MED** — `docs/api/trigger.md` Resource Trigger section has no alpha-stability callout within the section itself. Not fixed here — added as §18 P1 backlog item.
- **MED** — No operator-facing security checklist page. Not fixed here — added as §18 P1 backlog item.
- **LOW** — `docs/architecture.md` Helm values snippet still showed `watchNamespaces: ""  # AllNamespaces (default)`. **Fixed.**

## Code issues

- **HIGH** — `internal/controller/flowrun_controller.go` line ~442: `StepRunStatus.Attempts` hardcoded to 1 (known TODO T7). Added as §18 P1 backlog item; owner input added to `pending-input-required.md`.
- **HIGH** — `internal/controller/resource_watcher.go`: no kubebuilder RBAC markers for dynamic informer watches on arbitrary resource types. Added as §18 P2 item.
- **MED** — Executor RPC transport failures fail the step immediately instead of requeuing with backoff. Added as §18 P2 item; owner input added to `pending-input-required.md`.
- **MED** — `internal/controller/executor_reconciler.go`: executor Deployment missing `terminationGracePeriodSeconds`. Added as §18 P2 item.
- **MED** — Kafka producer pool has no lifecycle logging (cache hits/misses/evictions). Added as §18 P2 observability item.
- **LOW** — Kafka consumer group prefix field has no kubebuilder validation pattern. Low risk — runtime error is adequate. Not scheduled.
- **LOW** — mTLS bundle rotation concurrency not tested. Added as §18 P2 testing item.
- **LOW** — Kafka producer idle TTL eviction not unit tested. Added as §18 P2 testing item.

## Schedule corrections

- Added §18 Code Quality section with 8 new items (3 P1, 5 P2) from 2026-03-27 review findings.
- Purged all completed `[x]` items — schedule now shows only pending work.
- No `[ ]` → `[x]` or `[x]` → `[ ]` corrections needed.

## Decisions needed

Two owner-input questions added to `docs/tech-debt/pending-input-required.md` §2026-03-27 (tagged `<!-- BACKLOG-PROMPT -->`):
1. **StepRunStatus.Attempts wiring** — fix vs. document as known limitation
2. **Executor RPC backoff** — requeue on transport errors vs. keep current fail-immediately behavior

## Files changed

- `docs/dev/http-executor.md` — status banner updated
- `docs/api/trigger.md` — FlowReference cross-namespace restriction documented
- `docs/api/flow.md` — CEL cost limits paragraph added
- `docs/architecture.md` — WATCH_NAMESPACES table and Helm values snippet updated
- `docs/overview.md` — Helm install examples updated (OwnNamespace default)
- `docs/tech-debt/pending-input-required.md` — §2026-03-27 section appended
- `docs/schedule.md` — §18 Code Quality section added; completed items purged
- `docs/review-latest.md` — this file
