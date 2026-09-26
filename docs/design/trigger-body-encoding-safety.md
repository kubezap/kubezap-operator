# TriggerData.Body Binary-Safety Fix

> Status: Approved
> Date: 2026-09-19
> Related: `api/v1alpha1/flowrun_types.go`, `internal/gateway/webhook/handler.go`, `internal/gateway/kafka/handler.go`, `docs/api/trigger.md`, `docs/design/kafka-message-key-capture.md`

## Problem

`TriggerData.Body` is populated via a naive `string(payload)` conversion in both `internal/gateway/webhook/handler.go` and `internal/gateway/kafka/handler.go`, with no UTF-8 safeguard. A non-UTF-8 binary payload (common for Kafka — Avro/Protobuf/schema-registry-encoded messages; possible for webhooks with a binary content type) is silently corrupted: `encoding/json`'s string marshaling replaces invalid byte sequences with `U+FFFD` when the FlowRun is written to etcd, so the payload a Flow step later reads via `$(trigger.body)` is not the payload that was actually received. `BodyTruncated` covers oversized-body truncation only; it does nothing for encoding safety. Surfaced during `/adr` for `docs/design/kafka-message-key-capture.md`, which deliberately avoided repeating this exact pattern for the new `Key` field.

## Constraints

- `v1alpha1` API stability: the fix must be additive (`omitempty`) — existing FlowRuns and any Flow/CEL expression reading `$(trigger.body)` for an already-valid-UTF8 payload (the overwhelming majority of webhook triggers) must see byte-identical behavior after the fix.
- Must compose correctly with the existing `BodyTruncated` truncation logic — truncation and encoding are independent axes (a truncated payload can still be either UTF-8 or binary) and must not be conflated into a single flag.
- Truncating a UTF-8 payload at an arbitrary byte boundary can itself produce invalid UTF-8 (a split multi-byte rune) — the fix must handle this case correctly, not just the "whole payload happens to be binary" case.
- Must reuse the same encoding convention just established for `TriggerData.Key`/`KeyEncoding` (`docs/design/kafka-message-key-capture.md`) rather than inventing a second, inconsistent scheme in the same type.

## Rejected Alternatives

- **Leave as-is** — rejected: this is an active silent data-corruption bug on an already-shipped, in-use field, not a latent edge case worth deferring.
- **Reject/error on non-UTF-8 payloads at ingestion instead of encoding them** — rejected: would turn a currently-degraded-but-delivered event into a dropped one. Binary Kafka payloads (Avro, Protobuf) are a legitimate, common triggering case, not malformed input.
- **Always base64-encode `Body` unconditionally, dropping the conditional check** — rejected: breaking change for the common case — every existing Flow/CEL expression reading `$(trigger.body)` as literal text (the standard webhook-JSON path) would silently start seeing base64 instead, for zero benefit over the conditional approach.

## Decision

Add `TriggerData.BodyEncoding string` (`json:"bodyEncoding,omitempty"`, values `"utf8"` or `"base64"`), mirroring `KeyEncoding`'s convention from `docs/design/kafka-message-key-capture.md`. In both `internal/gateway/webhook/handler.go` and `internal/gateway/kafka/handler.go`, at the same point `BodyTruncated` is currently determined: truncate the raw payload bytes first using the existing (unchanged) truncation logic, then run `utf8.Valid()` on the resulting (possibly truncated) bytes to pick the encoding — this ordering means a truncation that happens to split a multi-byte rune correctly falls back to `"base64"` rather than producing another silently-invalid string. Populate `Body` as the literal string for `"utf8"`, or `base64.StdEncoding.EncodeToString(...)` for `"base64"`. Document the new field and the corrected behavior in `docs/api/trigger.md`, noting explicitly that any Flow previously depending on `$(trigger.body)`'s mangled output for a binary payload was already relying on undefined behavior — this is a bug fix, not a compatibility break.

### Tradeoffs

A Flow/CEL expression that needs to inspect a `"base64"`-encoded body must decode it itself — same caller-responsibility tradeoff already accepted for `Key`/`KeyEncoding`, kept for consistency rather than inventing an auto-decode helper for one field and not the other.
