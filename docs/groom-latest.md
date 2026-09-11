# Backlog groom: 2026-09-11

## Reordering applied

- **§28's ARCHITECTURE NOTE (line ~303) now cross-references §25's TESTING item (line ~250)** — rule violated: Rule 3 (Prerequisites before dependents), applied to a same-area open-decision-blocks-test-completion case. The architecture question ("should synchronous steps persist an observable `Running` phase?") is a genuine prerequisite for fully satisfying the state-model test matrix, but the two items lived in unrelated sections (§25 "Workflow Improvements," §28 "State Transition Correctness Review") with no link between them, so a reader hitting §25's item wouldn't know §28 blocks it. Rather than physically reordering whole sections (§25 predates §28 chronologically and both are thematically anchored to their own review dates), added an explicit forward/back cross-reference in §28's item pointing at §25's, and confirmed §25's item (already resolved with this exact caveat in PR #137, merged today) says the same thing from its side. No other physical moves were needed — the rest of the file's dependency order (research → tests → validation, §20/§21 before §19, §23/§24 P0 before §19, etc.) was already correct on inspection.

## Items added

None. No finding in this pass was evidenced strongly enough to justify a brand-new schedule item that didn't already exist in some form — the two substantive findings below are status corrections to existing items, not new work.

## Items removed or annotated

- **§16 P1 VALIDATION (line ~86)** — annotated `[x]`. Its own text already said the real work is "broken down into per-example tasks in §19 below," and every §19 task is now `[x]` except one (`github-autolabel`), which `docs/tech-debt/pending-input-required.md` records as explicitly and permanently deferred by the owner ("still deferred, proceed with other backlog items," confirmed 2026-04-05). A deliberately-deferred owner decision was already treated as non-blocking everywhere else in this schedule; this rollup item's checkbox was simply stale.
- **§1's header blockquote (line ~38-39)** — added a note recording the above and its consequence: the OperatorHub submission PR's gate list (line 43) is now satisfied except the spec-drift-clean requirement, which by its own "recurring gate" wording must be re-run fresh at actual submission time rather than relying on the one-time §31 baseline. Deliberately did **not** touch the "Paused" language on line 38 itself or the gate-list line 43 — whether to actually start the submission PR is a product decision for the owner, not something a grooming pass should silently resolve by flipping a pause banner.

## Items promoted from Future/Backlog

None. Reviewed every `## 10. Future / Backlog` item against the last several sections' findings (§26-§31) for anything that might have become newly relevant — nothing in recent work references `Step` CRD, multi-namespace flows, additional brokers, the plugin catalog, OpenLineage, multi-region HA, or the S3/Git trigger source. All seven remain genuinely speculative.

## Overlap with in-flight/just-merged work

This session had an open PR (#137) touching `docs/schedule.md` when grooming began; it merged into `main` partway through this pass (rebased cleanly, no conflicts). Four items the groom process would otherwise have flagged as stale (§18 P2 OIDC JWKS doc note, §25 TESTING transition matrix, §25 RECURRING spec-drift gate, §26 P2 RBAC drift) were already resolved by that PR's content — not re-touched here to avoid duplicate/conflicting edits. Two stale "in flight, open PR #137" cross-references written before the merge landed were corrected to reflect that #137 is now merged, once its merge was confirmed mid-pass.

## Other observations (not acted on)

- `docs/review-latest.md` is dated 2026-03-27 and every finding in it is already reflected as `[x]` in the current schedule (§18's items). It's stale but this is expected/by-design for that file (each `/research` run is meant to overwrite it, not accumulate) — not a schedule.md issue, just a note for whoever runs `/research` next.
- No `[ ]` item was found referencing code, docs, or example directories that no longer exist — spot-checked `IntegrationList.vue` (correctly a *planned* file, not claimed to exist), all `docs/tech-debt/*.md` and `docs/guides/*.md` references, `docs/architecture/flowrun-state-model.md`, and all 10 `examples/*` directories referenced from §19. All present.

## No-change items

~55 non-Future `[ ]`/`[x]` line items reviewed across §1, §15-§31; only the 2 described above needed a change. No dependency-order violations found beyond the one described above.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
