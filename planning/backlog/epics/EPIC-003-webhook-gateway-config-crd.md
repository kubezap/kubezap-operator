# EPIC-003: WebhookGatewayConfig CRD

**Status:** Backlog
**PI:** PI-1

## Problem

Per-namespace webhook gateway configuration is split across two mechanisms today, neither of which is discoverable or validated:

- **Namespace annotations** (`kubezap.io/webhook-tls-secret`, `kubezap.io/webhook-mtls-ca-secret`) control the gateway's inbound TLS/mTLS serving. Untyped, no `kubectl explain`, no schema validation — a typo in the annotation key silently no-ops (see `internal/controller/trigger_controller.go`).
- **Hardcoded Go constants** control HPA behavior: `internal/controller/gateway_deployment.go`'s `desiredWebhookGatewayHPA` always sets CPU target 70%, min replicas 1, max replicas 10, for every namespace, with no per-namespace override.

Separately, there's a real HA gap independent of this epic's CRD-vs-annotation question: the gateway Deployment's replica floor is 1 and there is no `PodDisruptionBudget`, so a namespace running at low traffic has zero redundancy during a rollout or node drain despite the HPA existing.

This came up while discussing (see `docs/overview.md`'s TLS annotation docs) that annotation-based config for shared per-namespace infrastructure (the webhook gateway is one shared Deployment per namespace, not per-Trigger — see `CLAUDE.md`'s Runtime Architecture) is a workable but informal pattern; a small typed CRD is more maintainable and easier to extend as more gateway-level settings accumulate (e.g. `--trusted-proxy-cidrs`, currently a manager-wide flag — see `docs/design/2026-09-11-webhook-gateway-trust-boundary.md` — with no per-namespace override either).

## Goal

A namespace-scoped `WebhookGatewayConfig` CRD (one per namespace, singleton-style) that replaces the two TLS annotations and exposes HPA min/max/target-CPU and a PodDisruptionBudget `minAvailable`, with backward-compatible defaults (today's hardcoded values) when the CRD is absent from a namespace.

## Success Metric

- `kubectl explain webhookgatewayconfig` documents every field; invalid values are rejected at admission time (CEL/kubebuilder validation), not silently ignored.
- A namespace with no `WebhookGatewayConfig` behaves identically to today (same HPA defaults, same annotation-driven TLS behavior during the migration window, or documented equivalent defaults after annotations are removed).
- A namespace that sets `minAvailable` on the PDB field actually gets a `PodDisruptionBudget` reconciled for its gateway Deployment — closing the "no real HA by default" gap.

## Related Design Docs

- `docs/design/2026-09-11-webhook-gateway-trust-boundary.md` — the existing `--trusted-proxy-cidrs` flag this CRD could eventually also hold per-namespace.
- None yet for this epic's own decisions (CRD shape, annotation migration/deprecation path, PDB defaults) — needs a new `docs/design/*.md` record via `/adr` before implementation, since this is a new CRD + controller + a migration touching security-relevant TLS config.
- Tangentially related backlog item (see `planning/backlog/backlog.md`'s Backlog Candidates): the discovery that `docs/api/trigger.md`'s *outbound* TLS annotations (`tls-ca-secret`, `tls-client-cert-secret`, `tls-insecure-skip-verify`) are undocumented-in-code fiction. That's a separate, unrelated mechanism (outbound calls, not gateway inbound serving) and is not in scope here — noted only so the two don't get conflated during design.

## Candidate Stories

Rough, not yet sized — footprinting happens in `/groom-backlog`:

- [ ] Design record: CRD shape (fields, singleton naming convention, defaulting behavior), and the annotation deprecation/migration path (deprecate-and-warn vs. hard cutover)
- [ ] `WebhookGatewayConfig` CRD types + controller: reconciles HPA min/max/target-CPU from spec instead of hardcoded constants
- [ ] Add `PodDisruptionBudget` reconciliation (new, doesn't exist today) — gated on the CRD's `minAvailable` field being set
- [ ] Migrate `webhook-tls-secret` / `webhook-mtls-ca-secret` Namespace annotations onto the CRD, per the design record's chosen migration path
- [ ] Docs: `docs/api/` page for the new CRD; update `docs/guides/webhook-security.md` and `docs/overview.md`'s TLS sections

## Dependencies

- Depends on: none
- Blocks: none currently identified

## Notes

- Owner-confirmed direction (2026-09-12): CRD over continuing to expand annotations/hardcoded constants, specifically because it's easier to extend later.
- Open question raised by owner: should this CRD also enable HA more directly (e.g. a `minReplicas >= 2` recommendation/validation, not just a raw passthrough number)? Not decided — flag for the design record.
- Committed to PI-1 (2026-09-12, via `/plan-pi`) as a parallel track alongside `EPIC-001` — not blocked on EPIC-001 closing, and not competing with it for files or focus (see `planning/roadmap/pi-plan.md`'s Revisions).
