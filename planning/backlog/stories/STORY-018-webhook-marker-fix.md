# STORY-018: Fix silently-dropped `+kubebuilder:webhook` markers (Trigger, FlowRun)

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Groomed
**Size:** XS

## Description

`internal/webhook/trigger_webhook.go`'s `+kubebuilder:webhook` marker (on `SetupTriggerWebhook`) is attached directly to that function's doc comment rather than in its own free-floating comment block — the same "silently dropped by controller-gen" failure mode `CLAUDE.md` already documents for `+kubebuilder:rbac` markers, just never previously noticed for `+kubebuilder:webhook`. Found while building `EPIC-003`'s `WebhookGatewayConfig` webhook: `config/webhook/` had never been generated for *any* webhook in this repo before that story, despite both the Trigger and FlowRun webhook *handlers* being correctly registered and working at the Go level in `cmd/main.go`.

`internal/webhook/flowrun_webhook.go`'s marker should be checked for the same issue — not yet confirmed either way, do that first as part of this story.

## Acceptance Criteria

- [ ] Check `internal/webhook/flowrun_webhook.go`'s `+kubebuilder:webhook` marker placement — confirm whether it has the same bug or was already correctly free-floating.
- [ ] Fix `trigger_webhook.go`'s marker (and `flowrun_webhook.go`'s, if it needs it too) to be free-floating, matching the corrected pattern used for `WebhookGatewayConfig`'s own marker in `internal/webhook/webhookgatewayconfig_webhook.go`.
- [ ] `make manifests` run once after the fix; confirm `config/webhook/manifests.yaml` then contains all three `ValidatingWebhookConfiguration` entries (Trigger, FlowRun, WebhookGatewayConfig) — grep for each, don't assume.
- [ ] No behavior change expected (the handlers already work correctly at the Go level) — `make test` should pass unchanged.

## File / Module Footprint

- `internal/webhook/trigger_webhook.go`
- `internal/webhook/flowrun_webhook.go` (if it turns out to need the same fix)
- `config/webhook/manifests.yaml` (regenerated via `make manifests`)

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-12, found during `EPIC-003`/STORY-009). Note this doesn't make the webhooks *deployed* — `config/webhook/` still has no `kustomization.yaml` wiring it into `config/default`'s kustomize build, which is a separate, larger, already-tracked gap (see `backlog.md`'s Backlog Candidates: "Properly scaffold `config/webhook` + `config/certmanager`..."). This story only fixes manifest *generation*, not deployment wiring.
