# STORY-024: Fix OLM scorecard descriptor/resource gaps in the CSV

**Epic:** EPIC-001 — Open Source Release Readiness
**Status:** Done (PR #209)
**Size:** S

## Description

`operator-sdk scorecard` (re-run during STORY-007's validation pass, 2026-09-13) fails 2 of 6 tests against `config/manifests/bases/kubezap.clusterserviceversion.yaml` — the hand-maintained CSV base that `make bundle` builds `bundle/manifests/kubezap.clusterserviceversion.yaml` from:

- **`olm-spec-descriptors`**: 6 fields have no OLM UI spec descriptor: `http`, `flowRef`, `type`, `webhook`, `hpa`, `tls`.
- **`olm-crds-have-resources`**: owned CRDs are missing a `resources:` list (tells the OperatorHub UI which built-in Kubernetes objects, e.g. `Deployment`, each CRD's controller causes to be created).

Reading the current base file confirms the root cause: `Trigger`'s owned-CRD entry has **zero** `specDescriptors` and **zero** `resources` at all (only `description`/`displayName`/`kind`/`name`/`version`) — unlike `Flow`/`FlowRun`/`Integration`, which are at least partially filled in. `Integration`'s `specDescriptors` cover only `type` and `kafka`, missing `amqp`/`nats`/`http`/`plugin`. `WebhookGatewayConfig` isn't in the base file's owned-CRD list at all — `operator-sdk generate bundle` auto-adds a bare stub (`kind`/`name`/`version` only, no `description`/`displayName`/`specDescriptors`/`resources`) for any CRD not already described there, which is why it silently has none of this metadata despite existing since STORY-009 (PR #190).

STORY-007 (final release validation) is blocked on this per owner decision (2026-09-13) — this story unblocks it.

## Acceptance Criteria

- [ ] `Trigger`'s owned-CRD entry in `config/manifests/bases/kubezap.clusterserviceversion.yaml`: add `specDescriptors` for at least `type` (webhook/cron/kafka/amqp/nats/resource) and `webhook` (the webhook auth/route config), plus `flowRef`/`action` (whichever the type actually uses — verify against `api/v1alpha1/trigger_types.go`, don't assume). Add a `resources:` list — Trigger causes webhook/Kafka/AMQP/NATS gateway `Deployment`s to exist (shared per-namespace, but still a caused resource) and `FlowRun` objects to be created; verify against `internal/controller/trigger_controller.go` what it actually creates/manages before listing.
- [ ] `Integration`'s owned-CRD entry: add `specDescriptors` for `amqp`, `nats`, `http`, and `plugin` (matching the existing `type`/`kafka` entries' style and the actual field descriptions from `api/v1alpha1/integration_types.go`).
- [ ] `WebhookGatewayConfig`'s owned-CRD entry: currently just an auto-added stub — add a real `description`, `displayName`, `specDescriptors` for `hpa`, `tls`, `podDisruptionBudget` (matching `docs/api/webhookgatewayconfig.md`'s field descriptions, written in STORY-012 — reuse that language rather than re-deriving it), and a `resources:` list (`HorizontalPodAutoscaler`, `PodDisruptionBudget` — verify against `internal/controller/gateway_deployment.go` what it actually reconciles).
- [ ] `make bundle` regenerates cleanly (no manual edits to the generated `bundle/` output — only the `config/manifests/bases/` source).
- [ ] `operator-sdk scorecard ./bundle -n <any-namespace> -w 180s` (needs a reachable cluster — `make setup-test-e2e` provides one) shows `olm-spec-descriptors` and `olm-crds-have-resources` both `pass`. Cleanup afterward (`kind delete cluster` if one was created for this).

## File / Module Footprint

- `config/manifests/bases/kubezap.clusterserviceversion.yaml`
- `bundle/manifests/kubezap.clusterserviceversion.yaml` (regenerated via `make bundle`, not hand-edited)
- `api/v1alpha1/{trigger,flow,flowrun,integration,webhookgatewayconfig}_types.go` (`+operator-sdk:csv:customresourcedefinitions` markers on all 5 CRDs — see Notes; expanded well past the original footprint estimate once the root cause was found and the same fix was applied everywhere rather than just to `Trigger`)
- `PROJECT` (registered all 5 CRDs, not just `Trigger`)
- `config/crd/bases/automation.kubezap.io_{flows,flowruns,integrations}.yaml`, `charts/kubezap-operator/crds/` (regenerated — description-only additions, no structural schema change)

## Dependencies

- Depends on: none
- Blocks: STORY-007 (owner decision 2026-09-13: scorecard must be clean before the release-validation rollup closes)

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-13, from STORY-007's OLM scorecard re-run). No design record needed — this is CSV/OLM-metadata authoring, not a CRD schema or behavior change (exempt per `design-process.md`'s trigger list).

**Real gotcha found while implementing, root cause since confirmed:** `operator-sdk generate kustomize manifests` (a `make bundle` prerequisite, runs every time) fully **regenerates** an owned-CRD's CSV entry from its Go type — `description` from the type's doc comment, `specDescriptors`/`resources` only from `+operator-sdk:csv:customresourcedefinitions` markers — but **only for kinds listed in the `PROJECT` file's `resources:`**. `Trigger` is the *only* CRD registered in `PROJECT` (Flow/FlowRun/Integration/WebhookGatewayConfig were hand-written and never added); kinds not registered there are passed through untouched, which is the entire reason hand-authored `specDescriptors`/`resources` on the CSV base file survived for those four but not for Trigger. Decisive test that confirmed this (and ruled out the initially-suspected causes — description-text matching, Go doc comment presence/content, array ordering, the `XValidation` marker unique to `TriggerSpec`): adding `Flow` to `PROJECT` caused *its* entry to be stripped too, despite Flow sharing none of those suspected differentiators.

An initial fix landed as a kustomize JSON6902 patch (`config/manifests/patches/trigger-csv-descriptors.yaml`, applied at `kustomize build` time to survive the base-file stripping) — this was a working bypass, not an understood fix, and was **replaced** once the root cause above was confirmed: `+operator-sdk:csv:customresourcedefinitions` markers were added directly to `Trigger`'s Go type instead (`api/v1alpha1/trigger_types.go`) — the tool's actual intended mechanism, verified idempotent across repeated regenerations and inert for `controller-gen` (byte-identical `config/crd/` output, `TriggerSpec`'s `XValidation` rule untouched). The patch file and its `kustomization.yaml` wiring were deleted.

**This same landmine was latent for Flow/FlowRun/Integration/WebhookGatewayConfig** — initially filed as a follow-up to fix later, but landed directly in this same PR instead once the fix mechanism was understood: all 5 CRDs are now registered in `PROJECT` with matching `+operator-sdk:csv:customresourcedefinitions` markers, so none of them depend on the accident of non-registration anymore. Incidental improvement along the way: `Flow` and `Integration`'s Go types gained real doc comments (previously undocumented at the type level) that now flow through to their CRD schemas' top-level `description` — `Flow`'s schema previously had none at all. Verified: `make generate`/`make manifests` diffs are description-only (no structural schema change) across all 4 newly-registered CRDs; stable across 3 consecutive regenerations; live `operator-sdk scorecard`: 6/6 pass.
