# STORY-017: `goconst` cleanup in production code

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Done (merged, PR #198)
**Size:** M

## Description

The `golangci-lint` v2.1.0 → v2.13.2 bump (forced by a separate dependabot `go.mod` bump to `go 1.26.0`) surfaced 176 new `goconst` findings the old linter version never reported — a linter-sensitivity change, not a code regression. 117 of 176 were in test files, already excluded via a `.golangci.yml` path rule. The remaining **59 are real production code**, concentrated in `internal/controller/integration_controller.go` (22), `internal/controller/gateway_deployment.go` (14), and smaller counts across `internal/cli/*` and several other `internal/controller/*.go`/`internal/gateway/{nats,webhook}/*.go` files — mostly Kubernetes API/label conventions (`app.kubernetes.io/name`, `automation.kubezap.io`, `ServiceAccount`, `Role`, `create`, `/healthz`, `Ready`, `cron`, `resource`) that would genuinely benefit from named constants.

## Acceptance Criteria

- [ ] Reproduce the current exact finding list: `./bin/golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` (with the existing test-file exclusions still in place) — confirm the count and file distribution still roughly matches the 59/2-file-concentration described above (may have shifted slightly with other changes since).
- [ ] Introduce named constants for the repeated string literals, scoped sensibly (e.g. package-level consts in each file, or a small shared `internal/controller/labels.go`-style file if the same literal repeats identically across multiple controller files — check for that before assuming per-file constants are the right shape).
- [ ] `make lint` shows 0 `goconst` findings outside the already-excluded test files.
- [ ] `.golangci.yml`'s temporary `internal/*` goconst exclusion (added alongside the permanent test-file one during the original dependabot validation pass) is removed once the real findings are fixed — don't leave a now-unnecessary exclusion in place.
- [ ] `go build ./...`, `go vet ./...`, `make test` all still pass — purely mechanical const-extraction, no behavior change.

## File / Module Footprint

- `internal/controller/integration_controller.go`, `internal/controller/gateway_deployment.go`, and the other files the full finding list identifies (exact list only known once the reproduce step in AC #1 runs — do not guess the full file list up front)
- `internal/cli/*.go` (per the original finding's "smaller counts across" note)
- `internal/gateway/nats/*.go`, `internal/gateway/webhook/*.go`
- `.golangci.yml` (remove the temporary exclusion once fixed)

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-12, from the post-dependabot validation pass). Purely mechanical cleanup — low risk, no behavior change, good candidate for a single focused pass rather than splitting further.
