# Plugin publish envelope

> Status: Approved
> Date: 2026-09-10
> Related: `docs/api/integration.md` (Publisher contract), `internal/controller/flowrun_controller.go` (`doPluginPublish`)

## Problem

`docs/api/integration.md`'s Publisher contract documents that the controller calls a plugin's `POST /publish` with a JSON envelope (`{integration, namespace, destination, headers, body}`) and expects a `{"messageId": "..."}`/`{"error": "..."}` response. `doPluginPublish` implements none of this: it POSTs the raw resolved `Body` directly as the HTTP body with `Headers` as literal HTTP headers, never sends `PublishAction.Topic` in any form, discards a successful response entirely, and never parses the documented `{"error"}` shape out of a failure body. Any plugin written to the documented contract is broken against this controller — it can't distinguish envelope from payload and has no way to learn the target topic/queue, the one piece of information the abstraction exists to carry. This is the last gap in an otherwise-working publish path; the Kafka branch is already correct.

## Constraints

- API stability (v1alpha1) — `PublishAction`'s fields (`integrationRef`, `topic`, `body`, `headers`) don't change shape; this is a controller-side wire-protocol fix, not a CRD schema change.
- Must produce exactly the envelope already documented in `docs/api/integration.md` — not free to invent a new wire shape, since any correctly-written plugin already expects the documented one.
- `Topic`/`Headers`/`Body` must go through the same `substituteVars`/`substituteVarsWithSecrets` resolution already applied on the Kafka branch — must not regress to raw, uninterpolated values.
- `retryPolicy`/`maxAttempts`/requeue-vs-fail semantics unchanged — only request/response body shapes change.
- No new external dependency — uses `encoding/json`, already imported.
- A plugin given a non-empty topic always receives the resolved value in the envelope's `destination` field.
- `integration` always equals the Integration CR's `Name`; `namespace` always equals the FlowRun's namespace.
- `Topic`/`Body`/`Headers` values are always resolved through interpolation before being placed in the envelope, never sent as raw templates.
- On a `2xx` response, a returned `messageId` is surfaced as `results["messageId"]`; a response with no/unparseable `messageId` is still success (empty results, not an error).
- On a non-`2xx` response, an `{"error": "..."}` body is used verbatim as the returned error's message; otherwise the raw body is used, unchanged from today's fallback.

## Rejected Alternatives

- **Correct the docs instead of the code** (describe the raw-body/headers behavior actually implemented, drop `destination` from the contract) — `destination` carries the one piece of routing information a plugin needs to publish to the right topic/queue; removing it would permanently break dynamic per-FlowRun destinations, contradicting the whole point of `PublishAction.Topic`, and the already-correct Kafka branch proves topic is meant to be first-class routing data, not folded into the body.
- **Invent a new envelope shape** (e.g. `destination` as an `X-Kubezap-Destination` header instead of a JSON field) — no known plugin exists yet (pre-public, no external consumers), so there's no compatibility reason to deviate from the shape already fully specified and reviewed in `docs/api/integration.md`; matching it is strictly less design work.

## Decision

Rewrite `doPluginPublish` (and its caller, the plugin branch of `executePublishStep`) to construct and send the JSON envelope `{integration, namespace, destination, headers, body}` exactly as documented, with `destination` set to the same `substituteVars`-resolved topic now used on the Kafka branch, and `headers`/`body` resolved through the existing `substituteVarsWithSecrets` calls. Parse the response as `{"messageId": "..."}` on success (surfaced as a step result) and `{"error": "..."}` on failure (used as the error message when present, else the raw body). Smallest change that brings the plugin path to parity with the already-correct Kafka path; no CRD or docs changes needed beyond what's already written.

- Body is now JSON-wrapped, not raw — a plugin author who assumed the raw string would arrive as the literal POST body would break, but no known plugin implementation exists yet, so this cost is believed zero in practice.
- Response parsing is best-effort: a `200 OK` with a non-JSON or missing-`messageId` body still counts as success, matching `messageId`'s documented "optional" framing.
