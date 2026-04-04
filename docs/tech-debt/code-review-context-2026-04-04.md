# Code Review / Bug Hunt Context — 2026-04-04

This document provides context and instructions for agents executing the code
review / bug hunt task (§20 in schedule).

---

## Scope

A comprehensive review of the KubeZap codebase to find:
- Logic bugs (incorrect behavior, off-by-one errors, nil panics, race conditions)
- Design flaws (API surface issues, reconciler correctness, RBAC over-privilege)
- Missing edge case handling
- Security vulnerabilities (beyond what was covered in §17)
- Performance problems (unbounded loops, missing rate limits, goroutine leaks)
- Observability gaps (missing metrics, missing log events, untraceable code paths)

---

## Key Packages to Review

In priority order:

| Package | Why it matters |
|---------|----------------|
| `internal/controller/flowrun_controller.go` | Core execution path — most complex logic |
| `internal/controller/resource_watcher.go` | Known alpha-quality — multiple known bugs |
| `internal/controller/executor_reconciler.go` | HTTP executor lifecycle management |
| `internal/controller/trigger_reconciler.go` | Trigger lifecycle and gateway management |
| `internal/controller/executor_mtls.go` | TLS cert rotation — concurrency sensitive |
| `internal/gateway/webhook/` | Webhook auth, route management, HMAC verification |
| `internal/gateway/kafka/` | Kafka consumer lifecycle |
| `api/v1alpha1/` | CRD type definitions and validation markers |

---

## Known Issues — Do NOT Re-Report

These are already in the schedule (§18) and do not need to be re-discovered:

- `StepRunStatus.Attempts` hardcoded to 1 — **FIXED** (2026-03-27)
- Resource watcher RBAC markers missing (§18 P2)
- Executor Deployment missing `terminationGracePeriodSeconds` (§18 P2)
- Executor RPC transport failures fail immediately (§18 P2)
- Kafka producer pool missing lifecycle logging (§18 P2)
- mTLS bundle concurrent rotation test missing (§18 P2)
- Kafka producer idle TTL eviction not unit tested (§18 P2)
- Resource watcher naive pluralization (§18 P2 — partially addressed)
- CEL cache unbounded (resolved — documented as intentional)

---

## Review Methodology

1. Read each package's controller/reconciler logic end-to-end
2. For each reconciler, verify:
   - Idempotency (safe to re-run at any time)
   - Error handling completeness (every error path is handled)
   - Status update correctness (conditions updated on all paths)
   - Requeue behavior (appropriate backoff, no infinite tight loops)
   - Race conditions (shared state, goroutine safety)
3. For each CRD type, verify:
   - Validation markers are complete and correct
   - Required vs optional fields match the intended API
   - Defaulting is correct
4. For gateway code, verify:
   - Auth handlers are correct (HMAC timing-safe comparison, JWT validation)
   - Route registration/deregistration is race-free
   - Memory/goroutine lifecycle (no leaks on Trigger deletion)

---

## Output Format

Create (or update) `docs/tech-debt/code-review-results-YYYY-MM-DD.md` with:

```markdown
# Code Review Results — YYYY-MM-DD

## HIGH priority — fix before public release
- [FILE:LINE] Description of issue / impact / fix recommendation

## MEDIUM priority — fix before GA
- ...

## LOW priority / tech debt
- ...

## No-change items reviewed
- ...

## Schedule additions
- [List of items to add to schedule.md with priority and section]
```

Then update `docs/schedule.md` with new schedule items, and update
`docs/tech-debt/pending-input-required.md` if any items need owner decision.

---

## Notes for Reviewing Agent

- Use the Explore or general-purpose agent type for the code review
- **Opus model recommended** — the review benefits from deeper reasoning
- The codebase is ~15k lines of Go; focus on the controller package first
- Cross-reference behavior with API docs in `docs/api/` to find doc-reality mismatches
- Pay special attention to concurrent code (goroutine-per-resource patterns)
- The §17 security review (docs/security-review-2026-03-24.md) covered security
  thoroughly — focus on logic correctness and design flaws in this review
