# Work Packages — 2026-09-13

Produced by `/plan-parallel`. Each package is either dispatched alone or in parallel with other packages in this same batch — never with a package it overlaps.

## Candidate stories considered

- STORY-007 — [Final release validation and OperatorHub submission](../../backlog/stories/STORY-007-final-release-validation.md) — footprint: none expected (validation pass); **excluded from this batch** (see below)
- STORY-012 — [Docs for `WebhookGatewayConfig`](../../backlog/stories/STORY-012-webhookgatewayconfig-docs.md) — footprint: `docs/api/webhookgatewayconfig.md` (new), `docs/guides/webhook-security.md`, `docs/overview.md`
- STORY-019 — [Add missing `+kubebuilder:webhook` marker for FlowRun](../../backlog/stories/STORY-019-flowrun-webhook-marker.md) — footprint: `internal/webhook/flowrun_webhook.go`, `config/webhook/manifests.yaml` (regenerated)
- STORY-020 — [Design record: sub-second FlowRun step-timing visibility](../../backlog/stories/STORY-020-flowrun-step-timing-precision-design.md) — footprint: `docs/design/YYYY-MM-DD-flowrun-step-timing-precision.md` (new), `docs/design/README.md` (index entry)
- STORY-003 — [Test suite value review](../../backlog/stories/STORY-003-test-suite-value-review.md) — footprint: none yet (read-only investigation); **excluded from this batch** (see below)

## Excluded from this batch

- **STORY-003**: explicitly footprint-less today ("no footprint until findings are in") — it's a research-shaped story, not an implementation task with a fixed file list. Its likely output (new follow-up stories filed into `planning/backlog/follow-ups.md` / `backlog.md`) touches the same hot file (`backlog.md`) that dispatch-work's own status updates touch, and per the plan-parallel rule a story with a vague/missing footprint gets sent back rather than guessed at. Recommend running it standalone (or via `/research`), not inside this parallel batch.
- **STORY-007**: its own Acceptance Criteria requires STORY-001/002/003/005/006/016 all `Done` — and STORY-003 is currently only partially done (AC #1 and #3 still open). STORY-007 is structurally blocked on STORY-003's completion even though its backlog status reads `Groomed`, not `Blocked`. Recommend sequencing it after STORY-003 rather than dispatching now.

## Conflict analysis

Pairwise footprint overlap check across the three parallel-safe candidates. No shared paths, no parent/child directory relationships, and no likely shared-file collision (docs, webhook Go file, and generated webhook manifest are all disjoint).

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-012 | none | parallel-safe |
| STORY-019 | none | parallel-safe |
| STORY-020 | none | parallel-safe |

**Hot-files cross-check** (`CLAUDE.md` Parallel Agent Guidelines): none of the three touch `cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, or `planning/backlog/backlog.md` directly. STORY-019 touches `config/webhook/manifests.yaml`, a generated file — not on the explicit hot-files list, but codegen-adjacent. It's the only story in this batch touching any generated manifest, so no parallel conflict; still, per Step 5 (Post-merge codegen), re-run `make generate && make manifests` once after all three branches merge as a final confirmation pass, in addition to STORY-019's own in-worktree verification.

No hot-file wiring pass is needed beyond that codegen re-check — none of these three add a new controller, scheme, flag, or dependency.

## Work packages (dispatch plan)

### WP-1 (parallel-safe with WP-2, WP-3)
- STORY-012 — WebhookGatewayConfig docs — [PR #202](https://github.com/kubezap/kubezap-operator/pull/202)

### WP-2 (parallel-safe with WP-1, WP-3)
- STORY-019 — FlowRun webhook marker fix — [PR #203](https://github.com/kubezap/kubezap-operator/pull/203)

### WP-3 (parallel-safe with WP-1, WP-2)
- STORY-020 — FlowRun step-timing design record — [PR #204](https://github.com/kubezap/kubezap-operator/pull/204)

All three fit within the 3-parallel-worker cap (Rate Limit Guardrails / this skill's own guardrail). Each was built in its own isolated worktree, verified independently (`go build ./...`, `make generate && make manifests` showed no drift, `make test`, `make lint` all clean against a local integration of all three), then split back into three independent single-commit branches off `origin/main` and opened as separate PRs per this repo's normal convention — not merged directly to `main`.
