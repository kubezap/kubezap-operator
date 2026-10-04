# Plugin Publish Idempotency Key and Trace Propagation

> Status: Approved
> Date: 2026-10-03
> Related: `internal/controller/flowrun_controller.go` (`pluginPublishEnvelope`, `doPluginPublish`, `callExecutor`), `docs/api/plugin-contract.md` (Publisher Contract, Idempotency), `docs/design/plugin-publish-envelope.md` (extends it), `docs/design/aws-sqs-sns-messaging-plugin.md`, `internal/plugin/awsmessaging/publisher.go`

## 1. Problem Statement

`docs/api/plugin-contract.md`'s Idempotency section tells plugin authors to accept an optional `idempotencyKey` and forward it to the broker, because the controller may re-run a publish step (in-step retries per `retryPolicy`, or the step re-executing after controller failover before its result was persisted). But the controller never sends one: `pluginPublishEnvelope` has no such field, and the contract's request schema doesn't list it either (prose only). Likewise, `doPluginPublish` sets only `Content-Type` — unlike `callExecutor`, it never injects W3C trace context, so a plugin's publish work starts a disconnected trace.

The first real plugin (`aws-messaging-plugin`, STORY-073) already implements both sides — it maps `idempotencyKey` to SNS FIFO `MessageDeduplicationId` and forwards an incoming `traceparent` header — so today both features are inert: a retried publish to a FIFO topic is delivered twice (unless the topic has content-based dedup), and publish spans can't be correlated with the FlowRun that caused them.

## 2. Constraints

- **API stability (v1alpha1)**: no CRD change. `PublishAction` keeps its current fields; this is a controller-side wire-protocol addition only.
- **Contract compatibility**: the new envelope field is optional and additive — plugins written to the current contract ignore unknown JSON fields (Go's `encoding/json` default; the contract should say so explicitly). No plugin may be broken by its presence.
- **Broker-agnostic key format**: the key goes to arbitrary brokers via arbitrary plugins. It must satisfy the strictest common caller-supplied-ID rules we know of — SNS/SQS FIFO `MessageDeduplicationId` (≤128 chars, alphanumerics + punctuation), Azure Service Bus `MessageId` (≤128), NATS `Nats-Msg-Id`. `FlowStep.Name` has **no** length or pattern validation today, so the step name cannot be embedded raw.
- **No new dependency**: `crypto/sha256`, `encoding/hex`, and the already-imported OTel propagator only.
- **No change to retry semantics** (`retryPolicy`, `maxAttempts`, requeue-vs-fail) or to the Kafka built-in publish branch.
- **Never leak secrets into the key**: the key must not be derived from the resolved body/headers (which may contain `substituteVarsWithSecrets` output).

## 3. Invariants

- Every plugin `/publish` request carries a non-empty `idempotencyKey`.
- The key is identical across all in-step retry attempts of the same step in the same FlowRun, and across re-execution of that step after controller restart/failover (it depends only on persisted identity: FlowRun UID + step name).
- Two different steps in the same FlowRun, or the same step in two different FlowRuns (including a user-initiated re-run, which creates a new FlowRun with a new UID), always get different keys.
- The key always matches `^kz1-[0-9a-f]{64}$` (68 chars) regardless of step name content or length.
- The key is never computed from body, headers, or any secret-resolved value.
- When an active span/trace context exists on `ctx`, the `/publish` HTTP request carries a W3C `traceparent` header (and `tracestate` if present) produced by the global OTel propagator — the same mechanism `callExecutor` uses. With no trace context, no `traceparent` header is sent (no fabricated trace).
- Trace context travels only as HTTP request headers, never inside the envelope's `headers` map (which is broker-message data).

## 4. Rejected Alternatives

- **Raw `<flowRun.UID>:<step.Name>`** — readable, but `FlowStep.Name` is unvalidated: a long or non-ASCII step name produces a key that SNS FIFO (and other brokers) reject, turning a dedup hint into a permanent 400 publish failure. Hashing removes that failure mode entirely.
- **User-templated `publish.idempotencyKey` CRD field** — gives authors control (e.g. dedup on a business ID from the trigger payload), but it's a CRD change with no current user asking for it. Deferred; if added later it is additive and the derived key remains the default.
- **Hash of the resolved body** — would dedup two intentionally identical messages from different FlowRuns/steps, would change across re-renders when the body interpolates time-varying values, and risks deriving the key from secret-resolved content.
- **Putting `traceparent` in the envelope's `headers` map** — that map is forwarded verbatim to the broker as message headers/attributes; mixing transport-level trace metadata into it changes user-visible message content and collides with user-set headers. HTTP request headers are the standard W3C carrier and what the AWS plugin already reads.
- **Also changing the Kafka built-in publish path** — Kafka's idempotent producer works at the producer-session level, not via a caller-supplied ID, and its trace propagation is a separate concern. Out of scope.

## 5. Tradeoffs

- The key is opaque: an operator debugging a dedup can't read the FlowRun/step out of it, but can recompute it from FlowRun UID + step name using the documented derivation. The `kz1-` prefix versions the derivation so it can change later without ambiguity.
- A step that *intends* to publish the same message twice within one FlowRun can't — but steps don't loop today, so one step = one logical publish per FlowRun. If loop constructs are added later, the derivation must include the iteration index (bump to `kz2-`).
- Dedup windows are broker-defined (SNS/SQS FIFO: 5 minutes). A step re-executed after a long outage may still be delivered twice. This is inherent to caller-supplied-ID dedup and is documented, not solved.

## 6. Final Decision

Add an optional `idempotencyKey` field to `pluginPublishEnvelope` and always populate it with `"kz1-" + hex(sha256(string(flowRun.UID) + "\x00" + step.Name))`, computed once per step execution (outside the retry loop) and passed into `doPluginPublish`. In `doPluginPublish`, call `otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))` on the `/publish` request, mirroring `callExecutor`. Update `docs/api/plugin-contract.md` to list `idempotencyKey` in the request schema (optional; plugins must tolerate unknown fields), document the key's derivation, stability guarantees and dedup-window caveat in the Idempotency section, and state that W3C trace context arrives as HTTP request headers. No CRD change, no new dependency, no change to the Kafka path or to retry semantics.
