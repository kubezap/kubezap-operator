# Backlog groom: 2026-09-11

## Reordering applied

None. §34–§37 (all added since the last groom, PR #138) are retrospective completed-work logs like §26–§33 before them — self-contained find-and-fix passes with no cross-section dependency on each other, and no test-before-fix or research-before-test ordering issue among them. Re-checked the numbered Prioritization Rationale rules against this session's new sections (rule 5, "P0 security fixes before architectural refactors in the same area," is the only one arguably in play — §36's deferred goconst/gocyclo items are exactly the kind of "architectural refactor in the same area" rule 5 says should come after security fixes, and they correctly already do: §37's security fixes are merged, §36's deferred item remains open behind them). No new prioritization rule was needed in the rationale section — the "weigh infra-layer mitigation before a code fix" and "grep exhaustively rather than trust a subagent's sample" principles applied this session are review *methodology*, already spelled out inline in §34/§37's own blockquotes, not schedule-ordering rules in the shape of the existing 10.

## Items added

Four items added to `## 10. Future / Backlog`, each an explicit, evidenced follow-up named in a design record from this session's work but never promoted to a tracked item — found by grep'ing `docs/design/2026-09-11-*.md` for "follow-up"/"not implemented"/"left as"/"revisit" and cross-checking each hit against `schedule.md` for an existing tracking item (none existed for any of the four):

- Integration-object `secretRef`-change detection (`docs/design/2026-09-11-secret-rotation-watches.md`'s named out-of-scope follow-up).
- Multi-hop trusted-proxy chain for the webhook gateway (`docs/design/2026-09-11-webhook-gateway-trust-boundary.md`'s Tradeoffs section).
- Transport-level SSRF DNS-rebinding fix, independent of NetworkPolicy/CNI enforcement (`docs/design/2026-09-11-executor-egress-networkpolicy.md`'s Rejected Alternative A, explicitly "not rejected permanently").
- Single source of truth for the SSRF blocked-CIDR list, currently duplicated across two Go locations and one YAML file (`docs/design/2026-09-11-executor-egress-networkpolicy.md`'s Tradeoffs section).

All four are genuinely speculative/low-urgency (consistent with the rest of `## 10`), not urgent gaps — filed there, not as a new numbered section.

## Items removed or annotated

None. Re-checked every completed-work section for staleness given this session's major changes (RetryOn removal, CEL `bodyFields`, single-pass interpolation, lint cleanup, webhook hardening, and the earlier web-dashboard removal from before this groom's window): no `[x]` item was found describing something the codebase has since removed or contradicted. The dashboard/`--enable-ui` mentions still present in §16, §21, §23, and §19 (all pre-dating the §33 removal) accurately describe those historical validation tasks *as they were scoped and completed at the time* — matching the explicit precedent already established when §33 was groomed (historical completed-work entries describing now-superseded state are left alone; only §33 itself was special-cased for full historical-record removal, per an explicit owner instruction unique to that feature).

## Items promoted from Future/Backlog

None. Reviewed all 8 pre-existing Future/Backlog items against every finding from §34–§37 — nothing in this session's work (spec-drift fixes, interpolation security, lint cleanup, webhook hardening) touches `Step` CRD, multi-namespace flows, additional brokers, the plugin catalog/reference plugin, OpenLineage, multi-region HA, or the S3/Git trigger source. All eight remain genuinely speculative.

## No-change items

~40 `[x]` line items across §34–§37 reviewed (all newly added since the last groom pass) plus the 3 previously-open `[ ]` items (§1 OperatorHub gate, §19 `github-autolabel`, §36 DEFERRED lint) re-verified for continued accuracy:
- `examples/github-autolabel/` confirmed still present (§19 item's reference is not stale).
- §36's five `gocyclo` complexity numbers (97/31/33/32/37) re-measured via a fresh `make lint` run and found byte-for-byte unchanged despite this session's subsequent edits to two of the five functions (`authenticateRequest` gained a new parameter in §37; complexity score unaffected) — the deferred item's specifics are still accurate, not stale.
- §36's `goconst` count (7) re-confirmed unchanged after §37's changes.
- §1's OperatorHub gate blockquote (added by the prior groom, PR #138) still correctly states the recurring-gate caveat; no update needed — the P0 spec-drift baseline it references is by design a one-time historical check, re-run fresh at actual submission time regardless of how much intervening work has happened.

## Files changed

- docs/schedule.md
- docs/groom-latest.md
