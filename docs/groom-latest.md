# Backlog groom: 2026-03-21 (session 2)

## Reordering applied

- **§12c moved before §12b** — P0 security fix (secret redaction in `substituteVars`/`executeHTTPStep`/`executePublishStep`) should not be blocked behind the large FlowRun execution model refactor. Both sections touch `internal/controller/flowrun_controller.go`. Rule violated: new Rule 7 (P0 security before architectural refactors in same code area). New order in §12: 12a → 12c → 12b → 12d.
- **§12 preamble updated** to reflect new ordering rationale.

## Items added

- **§15 Phase 1 — CLI `watch` command** (3 tasks): `kubezap watch` subcommand streaming live FlowRun terminal timeline with box-drawing chars + per-step status, tests in `cmd/kubezap/watch_test.go`, docs addition to `docs/guides/using-the-cli.md`. Evidence: Q5 decision (2026-03-21) — build both CLI and web; CLI first.
- **§15 Phase 2 — Read-only web dashboard** (4 tasks): `--ui-port` flag on controller (default `8082`), `internal/ui/` handlers using Go templates + htmx, handler tests in `internal/ui/handler_test.go`, `docs/guides/dashboard.md`. Evidence: Q5 decision (2026-03-21).
- **Prioritization rule 7** added to the Prioritization rationale section: P0 security fixes before architectural refactors in the same code area.

## Items removed or annotated

- **§15 placeholder** `_(pending owner input on CLI-vs-Web approach)_` replaced with concrete Phase 1 + Phase 2 tasks — decision answered (Both, 2026-03-21).
- **§12 preamble note** updated from "§12a → §12b → §12c → §12d" to "§12a → §12c → §12b → §12d" to match new priority order.
- **§12d note** added: can be folded into §12a (same file `trigger_types.go`) or done standalone after §12b.

## Items promoted from Future/Backlog

- **§10 "Web UI for flow monitoring"** — marked `[x]` (superseded and promoted to active §15 Phase 2).

## No-change items

~95 items reviewed across §1–§14 and §10, no change needed.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
