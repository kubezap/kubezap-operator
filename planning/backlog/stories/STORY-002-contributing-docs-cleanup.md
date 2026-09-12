# STORY-002: Clean up `docs/contributing.md`

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Groomed — partially done
**Size:** S

## Description

Make sure `docs/contributing.md` reads well and works correctly for an external contributor before release. Partially informed by findings from the 2026-09-12 e2e validation pass (PR #155): the local-dev k3s workflow section had real gaps (missing webhook-cert step, a silently-broken `IMG=` override) — both already patched with inline workaround notes, but the doc could use a full readability/accuracy pass beyond just those two fixes.

**Progress (PR #175, 2026-09-12):** removed stale references to the already-removed `MockEndpoint` CRD, and reformatted the binary/package/reconciler/action-type reference tables for alignment. Remaining acceptance criteria below are unaffected by that PR — still open.

## Acceptance Criteria

- [ ] A new contributor can follow the doc top-to-bottom without hitting an undocumented failure (re-verify the local k3s workflow section end-to-end, not just re-read it).
- [ ] The two existing inline workaround notes (webhook-cert generation, `IMG=` no-op — still present as a "Known issues with this workflow" bullet list) are folded into clean prose rather than reading as bolted-on bullets.
- [ ] Architecture/binary-layout tables cross-checked against `CLAUDE.md`'s Runtime Architecture table for drift.

## File / Module Footprint

- `docs/contributing.md`

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Owns `docs/contributing.md` specifically — if STORY-001 (broader docs pass) runs in the same batch, exclude this file from STORY-001's scope rather than running both on it.
