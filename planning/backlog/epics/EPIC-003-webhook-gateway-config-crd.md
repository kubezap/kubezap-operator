# EPIC-003: WebhookGatewayConfig CRD

**Status:** Done — all 5 stories merged (STORY-012, PR #202, closed the epic 2026-09-13)
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
- A namespace with no `WebhookGatewayConfig` behaves identically to today (same HPA defaults; TLS annotations are hard-cut-over per `docs/design/2026-09-12-webhookgatewayconfig-crd.md`, not kept alongside the CRD).
- A namespace that sets `minAvailable` on the PDB field actually gets a `PodDisruptionBudget` reconciled for its gateway Deployment — closing the "no real HA by default" gap.

## Related Design Docs

- `docs/design/2026-09-12-webhookgatewayconfig-crd.md` — **Approved.** This epic's own design record: CRD shape (`spec.tls`/`spec.hpa`/`spec.podDisruptionBudget`), admission-webhook singleton enforcement, hard-cutover annotation migration, plain-passthrough `minReplicas` (no HA minimum enforced).
- `docs/design/2026-09-11-webhook-gateway-trust-boundary.md` — the existing `--trusted-proxy-cidrs` flag this CRD could eventually also hold per-namespace.
- Tangentially related backlog item (see `planning/backlog/backlog.md`'s Backlog Candidates): the discovery that `docs/api/trigger.md`'s *outbound* TLS annotations (`tls-ca-secret`, `tls-client-cert-secret`, `tls-insecure-skip-verify`) are undocumented-in-code fiction. That's a separate, unrelated mechanism (outbound calls, not gateway inbound serving) and is not in scope here — noted only so the two don't get conflated during design.

## Candidate Stories

Groomed 2026-09-12 (`/groom-backlog`); re-groomed same day once STORY-008's design record was approved:

- [x] [STORY-008](../stories/STORY-008-webhookgatewayconfig-design-record.md) — Design record: CRD shape, singleton convention, defaulting behavior, annotation migration path. Done — `docs/design/2026-09-12-webhookgatewayconfig-crd.md`, Approved.
- [x] [STORY-009](../stories/STORY-009-webhookgatewayconfig-crd-hpa-controller.md) — `WebhookGatewayConfig` CRD types + controller (HPA reconciliation + singleton admission webhook). Done — PR #190, merged.
- [x] [STORY-010](../stories/STORY-010-webhookgatewayconfig-pdb.md) — `PodDisruptionBudget` reconciliation. Done — PR #196, merged.
- [x] [STORY-011](../stories/STORY-011-webhookgatewayconfig-tls-migration.md) — Hard-cutover TLS annotations onto the CRD. Done — PR #197, merged.
- [ ] [STORY-012](../stories/STORY-012-webhookgatewayconfig-docs.md) — Docs for the new CRD. Unblocked (STORY-009/010/011 all Done) — ready to groom/dispatch, last story in this epic.

## Dependencies

- Depends on: none
- Blocks: none currently identified

## Notes

- Owner-confirmed direction (2026-09-12): CRD over continuing to expand annotations/hardcoded constants, specifically because it's easier to extend later.
- Committed to PI-1 (2026-09-12, via `/plan-pi`) as a parallel track alongside `EPIC-001` — not blocked on EPIC-001 closing, and not competing with it for files or focus (see `planning/roadmap/pi-plan.md`'s Revisions).
- Groomed 2026-09-12 (`/groom-backlog`): initially only STORY-008 (the design record) could be fully groomed — the other 4 candidate stories were genuinely not footprintable until that record's decisions landed.
- **Design decisions made same day, directly with the owner** (see `docs/design/2026-09-12-webhookgatewayconfig-crd.md` for full rationale): singleton enforced via admission webhook (not a fixed name); annotation migration is a hard cutover (no deprecate-and-warn window — the project hasn't gone public yet, so there's no compatibility obligation to protect); `minReplicas` is plain passthrough with a minimum of 1, no HA-minimum enforcement (matches today's actual default, avoids rejecting a config that mirrors current behavior). STORY-009/010/011 re-groomed immediately after with real AC/footprints reflecting these decisions.
- Next step: dispatch STORY-009 first (STORY-010/011 both depend on the CRD types/controller it introduces).
