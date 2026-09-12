# STORY-002: Clean up `docs/contributing.md`

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #179, merged 2026-09-12)
**Size:** S

## Description

Make sure `docs/contributing.md` reads well and works correctly for an external contributor before release. Partially informed by findings from the 2026-09-12 e2e validation pass (PR #155): the local-dev k3s workflow section had real gaps (missing webhook-cert step, a silently-broken `IMG=` override) — both already patched with inline workaround notes, but the doc could use a full readability/accuracy pass beyond just those two fixes.

**Progress (PR #175, 2026-09-12):** removed stale references to the already-removed `MockEndpoint` CRD, and reformatted the binary/package/reconciler/action-type reference tables for alignment.

**Completed (PR #179, 2026-09-12):** the 3 remaining acceptance criteria below, plus a real gap found along the way — `cmd/http-executor` was entirely absent from every build/save/import command list, despite being a required binary.

## Acceptance Criteria

- [x] A new contributor can follow the doc top-to-bottom without hitting an undocumented failure — re-verified the k3s workflow end-to-end; found and fixed the missing http-executor build steps and an env-var explanation that only named 2 of the 4 vars the controller actually reads.
- [x] The two existing inline workaround notes (webhook-cert generation, `IMG=` no-op) are folded into clean prose rather than reading as bolted-on bullets — done; both now appear inline where contextually relevant instead of a separate "Known issues" section.
- [x] Architecture/binary-layout tables cross-checked against `CLAUDE.md`'s Runtime Architecture table for drift — found `http-executor`/`ExecutorReconciler` missing from the binary, key-packages, and reconciler tables, and 4 of 5 reconciler file:line references stale. All fixed and verified against current source.

## File / Module Footprint

- `docs/contributing.md`

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Owns `docs/contributing.md` specifically — if STORY-001 (broader docs pass) runs in the same batch, exclude this file from STORY-001's scope rather than running both on it.
