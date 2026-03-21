# Backlog groom: 2026-03-20

## Reordering applied

None. All sections are in correct dependency order:
- R2 (§1, complete) → R1 (§2) → Tests T1-T8 (§3): satisfies Rule 1 (research before tests)
- Tests (§3) before features (§4-6): satisfies Rule 4
- §4 MockEndpoint docs + §5 `type: http` Integration before §6 Examples: satisfies Rules 3 and 5
- §6 Examples before §7 MockEndpoint removal: satisfies Rule 3

## Items added

- `[x] docs/guides/using-the-cli.md` — evidence: `docs/review-latest.md` (created in 2026-03-20 doc review pass)
- `[x] docs/guides/cron-triggers.md` — evidence: `docs/review-latest.md` (created in 2026-03-20 doc review pass)
- `[x] docs/guides/troubleshooting.md` — evidence: `docs/review-latest.md` (created in 2026-03-20 doc review pass)

## Items removed or annotated

None.

## Items promoted from Future/Backlog

None. AMQP/NATS guide items remain in §9 pending owner decision (see `docs/review-latest.md` → Decisions needed).

## No-change items

51 items reviewed, no reordering or removal needed.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
