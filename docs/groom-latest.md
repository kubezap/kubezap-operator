# Backlog groom: 2026-03-22 (post-review pass)

## Reordering applied (post-review decisions)

- `§16 P2 BUG` resource_watcher.go:95 context detachment → **promoted to P0** (owner decision: goroutine leak blocks clean shutdown and E2E stability)
- `§16 P2 INFRA` Gateway Deployments missing health probes → **promoted to P1** (owner decision: required before OperatorHub submission)
- `§16 P2 SECURITY` Restrict secrets RBAC to OwnNamespace → **promoted to P1** (owner concern: cluster-wide secret read is a security blocker)

## Items marked done

- `§16 P2 DOCS` `docs/architecture.md` KEDA stale statement → **[x]** (fixed in review/2026-03-22)

## Items removed

- Duplicate `resource_watcher.go:95` entry in P2 (now in P0)
- `INFRA` gateway probes duplicate in P2 (now in P1)
- `SECURITY` secrets RBAC duplicate in P2 (now in P1)

## Ordering violations from prior groom still valid

- `§16 P1 SECURITY` (constant-time comparison) remains before `§16 P1 DOCS/API` (WebhookAuth restructure) — same handler, security fix must land first

## No-change items

40 items reviewed across §1, §10, §15, §16. All other items unchanged.

## Files changed

- `docs/schedule.md`
- `docs/tech-debt/pending-input-required.md` — 2026-03-22 questions marked ANSWERED
- `docs/groom-latest.md`

---

# Backlog groom: 2026-03-22

## Reordering applied

- `§16 P1 SECURITY` (constant-time comparison in `handler.go`) moved above `§16 P1 DOCS/API` (WebhookAuth struct restructure) — **Rule 7**: security fix before architectural refactor in the same code path (`handler.go`). Both touch the webhook handler; the restructure should apply on top of the security fix to avoid re-patching.

## Items added

- `§1 Release Process — CI workflow` (`.github/workflows/ci.yml`): lint, test, build, docker-build on every push/PR — evidenced by explicit owner request.
- `§16 P1 BUG — resource_watcher.go naive pluralization` (line ~113): `Ingress` → `ingresss` silent failure — evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- `§16 P1 BUG — resource_watcher.go FlowRun name collision` (line ~207): second-precision timestamp with no random suffix — evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- `§16 P2 BUG — resource_watcher.go no retry on sync failure` — evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- `§16 P2 BUG — resource_watcher.go no cooldown mechanism` — evidenced by `docs/tech-debt/pending-input-required.md` Q2.
- `§16 P2 TECH DEBT — Kafka producer pool no TTL/health check` — evidenced by `docs/review-latest.md`.
- `§16 P2 TECH DEBT — CEL environment init failure cached via sync.Once` — evidenced by `docs/review-latest.md`.

## Items updated

- `§16 P1 DOCS/API — WebhookAuth`: changed from "Decide: restructure or update docs" to "Decision made (2026-03-22): restructure Go types to nested structs." Now an implementation task with specific struct names and file list. Evidenced by `docs/tech-debt/pending-input-required.md`.
- `§1` section note updated to confirm GitHub org migration to `kubezap/kubezap-operator` complete (2026-03-22).
- Prioritization rationale Rule 6 updated to reference §16 P1/P2 resource watcher bugs instead of stale "Future/Backlog" reference.

## Items removed (completed, no forward context)

- `§1 INFRA — Create GitHub org kubezap` — done; git remote confirms it.
- `§1 OLM Readiness — 4 completed checklist items` — replaced with a single summary note (bundle passes validate + scorecard).
- `§15 Phases 1 & 2 — ~15 completed implementation items` — replaced with single "Complete (2026-03-21)" header note.
- `§16 P0 — 6 completed items` (UI flag redesign, module rename, flow.md fix, pubsub fix, header-equals, gitignore verify) — no forward context.
- `§16 P1 INFRA — GHCR publishing` — duplicate of `§1 Release Process` items.
- `§16 P1 BRANDING — CHANGELOG` — duplicate of `§1 Release Process — Add CHANGELOG.md`.
- `§10 — 6 completed guide items` (using-the-cli.md, cron-triggers.md, troubleshooting.md, amqp-setup.md, nats-setup.md, web UI) — no forward context.

## Items with forward context retained

- `§10 — Kubernetes resource-event trigger type [x]`: kept with note that Example 6 is blocked on its pluralization fix (active dependency reference).

## No-change items

37 pending items reviewed with no change needed.

## Files changed

- `docs/schedule.md`
- `docs/groom-latest.md`
