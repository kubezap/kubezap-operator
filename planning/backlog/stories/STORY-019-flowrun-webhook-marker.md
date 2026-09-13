# STORY-019: Add the missing `+kubebuilder:webhook` marker for FlowRun

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Groomed
**Size:** XS

## Description

`internal/webhook/flowrun_webhook.go`'s `FlowRunValidator` (rejects legacy cross-namespace `FlowRef`) is registered directly in `cmd/main.go` at the Go level and works correctly, but has **no** `+kubebuilder:webhook` marker at all — unlike Trigger's marker (fixed in STORY-018), there's nothing to reposition here; one needs to be written from scratch. `config/webhook/manifests.yaml` has never had a `vflowrun.kb.io` entry as a result, so no `ValidatingWebhookConfiguration` is generated for it.

**Owner decision (2026-09-13 checkpoint): `failurePolicy: Ignore`**, matching Trigger's policy — FlowRun objects are created on every single Trigger fire (a hot path), unlike Trigger (created rarely, by a human) or WebhookGatewayConfig (a rare per-namespace singleton). `Fail` here would mean any brief webhook-server unavailability blocks flow execution cluster-wide, not just one object's creation.

## Acceptance Criteria

- [ ] Add a free-floating `+kubebuilder:webhook` marker (own comment block, blank line before the next declaration — per `CLAUDE.md`'s marker-placement gotcha) above `SetupFlowRunWebhook` in `internal/webhook/flowrun_webhook.go`, matching the existing markers' style: `path=/validate-automation-kubezap-io-v1alpha1-flowrun` (already the path used in `SetupFlowRunWebhook`'s `Register` call — keep them consistent), `mutating=false`, `failurePolicy=ignore`, `sideEffects=None`, `groups=automation.kubezap.io`, `resources=flowruns`, `verbs=create;update` (matches what `FlowRunValidator.Handle` actually gates), `versions=v1alpha1`, `name=vflowrun.kb.io`, `admissionReviewVersions=v1`.
- [ ] Run `make manifests` and confirm `config/webhook/manifests.yaml` now contains a `vflowrun.kb.io` entry alongside the existing `vtrigger.kb.io`/`vwebhookgatewayconfig.kb.io` entries — do not assume it landed, grep for it.
- [ ] `go build ./...`, `go vet ./...`, `make test`, `make lint` all still pass — no behavior change, this only adds a previously-missing generated manifest.

## File / Module Footprint

- `internal/webhook/flowrun_webhook.go`
- `config/webhook/manifests.yaml` (regenerated via `make manifests`)

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-12/13, from STORY-018's implementation). No design record needed — the only open design question (`failurePolicy`) was decided directly with the owner at the 2026-09-13 checkpoint, recorded above; this is now a mechanical marker-addition story, same shape as STORY-018.
