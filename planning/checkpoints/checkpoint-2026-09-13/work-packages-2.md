# Work Packages — 2026-09-13 (round 2)

Produced by `/plan-parallel`. Each package is either dispatched alone or in parallel with other packages in this same batch — never with a package it overlaps.

## Candidate stories considered

- STORY-007 — [Final release validation and OperatorHub submission](../../backlog/stories/STORY-007-final-release-validation.md) — footprint: none expected (validation pass — spec-drift check, `make test-e2e`/`lint`/`vulncheck`, OLM bundle validate/scorecard, git secret-scan)
- STORY-022 — [Functional test coverage for bearer/apiKey/basic/headerEquals webhook auth](../../backlog/stories/STORY-022-webhook-auth-test-coverage.md) — footprint: `internal/gateway/webhook/handler_test.go`
- STORY-023 — [Fix fictional outbound-TLS-annotation docs](../../backlog/stories/STORY-023-outbound-tls-annotation-docs-fix.md) — footprint: `docs/api/trigger.md`, `docs/overview.md`, `docs/guides/troubleshooting.md`

## Conflict analysis

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-007 | none | parallel-safe |
| STORY-022 | none | parallel-safe |
| STORY-023 | none | parallel-safe |

No shared paths, no parent/child directory relationships. STORY-007 has no footprint of its own — it reads/validates across the repo (spec-drift check, `make test-e2e`, lint, vulncheck, bundle validate) but writes nothing itself; any issue it finds gets filed as its own follow-up/story rather than fixed inline, per the story's own scope note, so there's no real write-conflict risk with STORY-022/023's file edits. STORY-022 and STORY-023 touch entirely disjoint files (one Go test file vs. three doc files).

**Hot-files cross-check** (`CLAUDE.md` Parallel Agent Guidelines): none of the three touch `cmd/main.go`, `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, `cmd/http-executor/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`, or `planning/backlog/backlog.md` directly. No wiring pass needed.

## Work packages (dispatch plan)

### WP-1 (parallel-safe with WP-2, WP-3)
- STORY-007 — final release validation

### WP-2 (parallel-safe with WP-1, WP-3)
- STORY-022 — webhook auth test coverage

### WP-3 (parallel-safe with WP-1, WP-2)
- STORY-023 — outbound-TLS-annotation docs fix

All three fit within the 3-parallel-worker cap. Note: STORY-007's AC includes "STORY-001/002/003/005/006/016 all Done" — all six are now confirmed Done (STORY-003 closed 2026-09-13, no PR). STORY-007 also depends on `make test-e2e` being green per STORY-003's outcome — STORY-003 found no test-suite defects requiring a fix (see its findings write-up), so this gate is satisfied as-is.
