# Kafka Message Key Capture

> Status: Approved
> Date: 2026-09-19
> Related: `api/v1alpha1/flowrun_types.go`, `internal/gateway/kafka/handler.go`, `docs/api/trigger.md`

## Problem

`internal/gateway/kafka/handler.go` reads `sarama.ConsumerMessage.Key` nowhere — there's no `TriggerData` field for it and no `$(trigger.key)` interpolation token, confirmed accurate against `docs/api/trigger.md`. A Flow triggered from Kafka cannot key off the message's own partitioning key (e.g. to derive an idempotency/dedup key or route on it), even though headers and partition/offset are already exposed for the equivalent purpose.

## Constraints

- `v1alpha1` API stability: any new `TriggerData` field must be additive and `omitempty` — existing FlowRuns and Flow templates must be unaffected.
- Kafka keys are frequently binary (Avro/Protobuf/schema-registry-encoded), not assumed-UTF8 text — the field's encoding must not silently corrupt binary keys.
- Must not change the existing Kafka FlowRun dedup-key naming scheme (`<trigger>-p<partition>-offset-<offset>`) — the message key is informational/interpolation input only, not a dedup input.
- Requires `make generate && make manifests` after the type change; not a hot-file change itself (`api/v1alpha1/flowrun_types.go` is a distinct file per story, not `groupversion_info.go`).

## Rejected Alternatives

- **Assumed-UTF8 string (naive `string(key)` conversion)** — rejected. This is actually the existing pattern for `TriggerData.Body` (a raw `string(payload)` conversion with no encoding safeguard), and it is a real, separate latent bug: a non-UTF8 byte sequence stored in a Kubernetes string field gets silently mangled to `U+FFFD` replacement characters when JSON-marshaled into etcd. Reusing that same pattern for `Key` — a field even more likely to be binary in practice than `Body` — would repeat a known-bad precedent rather than avoid it. (`Body`'s existing gap is out of scope for this story; flagged separately as a follow-up.)
- **Overload the existing `Body`/`BodyTruncated` fields for the key** — rejected: conflates two distinct payload elements (message value vs. key) under one field pair, breaks `docs/api/trigger.md`'s one-field-per-concept convention, and would require disambiguating which one a given value represents at every call site.

## Decision

Add two new fields to `TriggerData` in `api/v1alpha1/flowrun_types.go`:

```go
Key         string `json:"key,omitempty"`
KeyEncoding string `json:"keyEncoding,omitempty"` // "utf8" or "base64"
```

Populate both in `internal/gateway/kafka/handler.go` from `sarama.ConsumerMessage.Key`: if the key is `nil`, leave both fields unset; otherwise check `utf8.Valid(key)` — valid UTF-8 keys are stored literally with `KeyEncoding: "utf8"`, invalid ones are base64-encoded with `KeyEncoding: "base64"`. An explicit encoding field (not a bare bool) makes the value self-describing without cross-referencing docs, and gives a template author or downstream consumer an unambiguous signal for which decoding to apply. Add a `$(trigger.key)` interpolation token alongside the existing `$(trigger.partition)`/`$(trigger.offset)` resolution path, emitting `TriggerData.Key`'s stored string verbatim — the token does not re-decode based on `KeyEncoding`; that stays the caller's responsibility, keeping interpolation itself simple and behaviorally consistent with how `$(trigger.body)` already works. Document `Key`, `KeyEncoding`, and the new token in `docs/api/trigger.md`.

### Tradeoffs

A CEL expression or template consuming a base64-encoded key must decode it itself (no built-in `$(trigger.key.decoded)` helper in this pass) — acceptable, since binary keys needing further processing are already an advanced use case, and adding a decode-aware token is easy to layer on later without a breaking change if real usage demands it.
