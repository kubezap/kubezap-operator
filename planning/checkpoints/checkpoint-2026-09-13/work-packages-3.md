# Work Packages — 2026-09-13 (round 3)

Produced by `/plan-parallel`. Single-story batch for EPIC-005's implementation.

## Candidate stories considered

- STORY-026 — [Implement HTTP-step outbound TLS/CA support](../../backlog/stories/STORY-026-http-outbound-tls-impl.md) — footprint: `api/v1alpha1/integration_types.go`, `internal/executor/http/types.go`, `internal/executor/http/handler.go`, `internal/controller/flowrun_controller.go`, `internal/executor/http/handler_test.go`, `internal/controller/flowrun_controller_test.go`, `config/rbac/role.yaml` (regenerated), `config/crd/bases/automation.kubezap.io_integrations.yaml` + `charts/kubezap-operator/crds/automation.kubezap.io_integrations.yaml` (regenerated)

## Conflict analysis

Only one candidate story ready this round — no pairwise overlap to check. STORY-026 touches `config/rbac/role.yaml`, one of `CLAUDE.md`'s Parallel Agent Guidelines hot files — normally requires a wiring pass when dispatched alongside another RBAC-touching story, but nothing else is in this batch, so no wiring pass needed here.

**Dependency note:** STORY-026 depends on STORY-025 (design record) per its own Dependencies section — but that dependency is "the design must be Approved," which it already is (`docs/design/2026-09-13-http-step-outbound-tls.md`, Status: Approved), not "STORY-025's PR (#213) must be merged first." STORY-025's PR is docs-only (no code), so there is no code-level blocker to starting STORY-026. The worktree for STORY-026 is branched from `backlog/http-outbound-tls-design-record` (STORY-025's still-open branch, not `origin/main`) so the design record is present for the implementing agent to read directly; this branch will need a rebase onto `main` once PR #213 merges, before STORY-026's own PR can land — same reconciliation pattern already used earlier this session for PR #209.

## Work packages (dispatch plan)

### WP-1
- STORY-026 — HTTP-step outbound TLS/CA support implementation
