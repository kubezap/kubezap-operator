# STORY-022: Functional test coverage for bearer/apiKey/basic/headerEquals webhook auth

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Done (PR #210)

**Size:** S

## Description

`internal/gateway/webhook/handler.go`'s `authenticateRequest` implements 7 webhook auth types (`hmac`, `bearer`, `apiKey`, `oidc`, `basic`, `ipAllowlist`, `headerEquals`). Only 3 (`hmac`, `oidc`, `ipAllowlist`) have real accept/reject behavioral tests today. The other 4 — `bearer`, `apiKey`, `basic`, `headerEquals` — have zero functional coverage: `handler_test.go`'s `TestRegister_RedactsSecretsFromLogOutput` only checks their secrets are redacted from logs, never that a valid credential is accepted or an invalid/missing one is rejected.

Found during STORY-003's test-suite spot-check (2026-09-13). Three of the four are literal credential comparisons (`bearer`, `apiKey`, `basic` all use `subtle.ConstantTimeCompare` — read and confirmed correct as of this grooming pass, `handler.go:194-271`); a regression here (a swapped comparison argument, a broken empty-header check, an accidentally non-constant-time `==` swap) would pass every test in the suite today.

This story adds the missing tests only — no production-code change is expected, since `authenticateRequest`'s logic for all four types already reads correctly on inspection. If the new tests surface an actual bug, fix it inline (small, same-file fix) rather than spinning out a separate story, per this repo's normal allowance for fixes found while writing the test that exposes them.

## Acceptance Criteria

- [ ] `TestBearerAuth` (table-driven, matching `TestHMACAuth`'s existing shape at `handler_test.go:307`): valid token → 200/allowed; wrong token → 401 `"invalid bearer token"`; missing `Authorization` header → 401 `"missing Authorization header"`.
- [ ] `TestAPIKeyAuth`: valid key → allowed; wrong key → 401 `"invalid API key"`; missing header → 401 `"missing API key header"`; custom header name via `entry.APIKeyHeader` respected (not just the `X-Api-Key` default).
- [ ] `TestBasicAuth`: valid username+password → allowed; wrong username → 401; wrong password → 401; missing/malformed `Authorization: Basic ...` → 401 `"missing or malformed Basic auth credentials"`.
- [ ] `TestHeaderEqualsAuth`: valid header value → allowed; wrong value → 401 `"invalid header value"`; missing header → 401 `"missing required header"`; misconfigured (empty `entry.HeaderEqualsHeader`) → 401 `"header-equals auth misconfigured: no header name"`.
- [ ] `go test ./internal/gateway/webhook/...` and `make lint` both pass.

## File / Module Footprint

- `internal/gateway/webhook/handler_test.go` only. No change expected to `handler.go` itself (see Description) — if the new tests do surface a real bug there, note it in this story's Notes when done rather than silently expanding footprint.

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-13, from STORY-003's `internal/*/*_test.go` spot-check). No design record needed — this only adds coverage for existing, unchanged auth logic; not a behavior or security-posture change in itself (per `planning/process/design-process.md`'s trigger list, test-only additions are explicitly exempt).
