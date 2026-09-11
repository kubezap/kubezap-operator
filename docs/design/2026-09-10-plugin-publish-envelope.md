# Plugin publish envelope

## Problem Statement

`docs/api/integration.md`'s Publisher contract documents that the controller calls a plugin's `POST /publish` endpoint with a JSON envelope:

```json
{
    "integration": "<integration-name>",
    "namespace":   "<namespace>",
    "destination": "<topic or queue name>",
    "headers":     { "<key>": "<value>", ... },
    "body":        "<message body string>"
}
```

and that a successful response looks like `{"messageId": "<optional: broker-assigned message ID>"}`.

The actual implementation (`executePublishStep`'s plugin branch, `doPluginPublish` in `internal/controller/flowrun_controller.go`) does none of this. It POSTs the raw, resolved `Body` string directly as the HTTP request body, with `Headers` set as literal HTTP request headers, to a URL built from the Integration name and namespace. `PublishAction.Topic` — the field a Flow author sets to say *where* to publish — is never sent to the plugin in any form: not as `destination`, not as a header, not anywhere. On success, the response body is discarded entirely (`doPluginPublish` returns a bare empty `map[string]string{}`); on failure, the raw response body is embedded in the returned Go error string, but the documented `{"error": "..."}` JSON shape is never parsed out of it.

User impact: any plugin image written to the documented contract is broken against this controller today. It receives an HTTP body it cannot correctly interpret as "the message to publish" (no framing distinguishes envelope from payload) and has no way to learn which topic/queue/subject the Flow author intended — the one piece of information the whole abstraction exists to carry. This was found with zero existing test coverage of `doPluginPublish`, discovered while fixing an unrelated, narrower bug (Kafka-path `Topic` not being `$(...)`-interpolated — see `docs/schedule.md` §31).

This is the last remaining gap in an otherwise-working publish path: the Kafka branch of `executePublishStep` is correct (topic interpolation now fixed, `partition`/`offset` are returned as step results). Only the plugin branch is broken.

## Constraints

- **API stability (v1alpha1)**: `PublishAction`'s existing fields (`integrationRef`, `topic`, `body`, `headers`) do not change shape — this is a controller-side wire-protocol fix, not a CRD schema change. No `make manifests` diff is expected.
- **Plugin contract compatibility**: the fix must produce exactly the envelope already documented in `docs/api/integration.md` (§Publisher contract) — this design does not get to invent a new wire shape, since the documented shape is the one any correctly-written plugin already expects. If a real plugin exists anywhere already coded against this doc, it must keep working.
- **Symmetry with the Kafka path**: `PublishAction.Topic` and `PublishAction.Headers`/`Body` must go through the same `substituteVars`/`substituteVarsWithSecrets` resolution already applied on the Kafka branch (fixed in the immediately preceding commit) — the plugin branch must not regress to raw, uninterpolated values for any of these.
- **Retry/error semantics unchanged**: `retryPolicy`, `maxAttempts`, and the requeue-vs-fail behavior on transport vs. HTTP-status errors must not change — only the request/response bodies change shape.
- **No new external dependency**: JSON envelope construction uses `encoding/json`, already imported throughout this package.

## Invariants

- A plugin implementing the documented Publisher contract, given a Flow step with a non-empty `topic`, always receives that resolved topic value in the envelope's `destination` field.
- The envelope's `integration` field always equals the Integration CR's `Name`, and `namespace` always equals the FlowRun's namespace — never a different namespace, even under cross-namespace FlowRef scenarios (not currently supported per `docs/api/flowrun.md`, but the invariant should hold trivially since publish always targets an Integration in the FlowRun's own namespace).
- `PublishAction.Topic`, `.Body`, and each value in `.Headers` are always resolved through interpolation (topic via `substituteVars`; body/headers via `substituteVarsWithSecrets`, matching the Kafka branch) before being placed in the envelope — never sent as raw templates.
- On a `2xx` response, if the plugin returns `{"messageId": "..."}`, that value is surfaced as a step result (`results["messageId"]`), mirroring the Kafka branch's `partition`/`offset` results. A response with no `messageId` (or an unparseable body) is not an error — an empty results map is still success.
- On a non-`2xx` response, if the body parses as `{"error": "..."}`, that message is used verbatim in the returned Go error; otherwise the raw response body is used, unchanged from today's fallback behavior.

## Rejected Alternatives

**Correct the docs instead of the code.** Rewrite `docs/api/integration.md`'s Publisher contract to describe the raw-body-and-headers behavior actually implemented today, and drop `destination` from the documented contract entirely (perhaps require the topic to be folded into `body` or a header by convention instead).

Rejected because: `destination` is not a cosmetic detail — a plugin cannot route a Kafka-style publish to the correct topic/queue/subject without it, and no other field carries that information today. "Fix the docs" would mean permanently removing the ability to publish to a dynamic, per-FlowRun destination through the plugin path, which contradicts the whole point of `PublishAction.Topic` existing as a distinct, interpolatable field. The Kafka branch (the other publish backend) already proves the product intent: topic is a first-class, per-invocation piece of routing data, not something folded into the body.

**Invent a new envelope shape rather than matching the existing doc.** For example, pass `destination` as an HTTP header (`X-Kubezap-Destination`) instead of a JSON envelope field, to avoid needing to wrap `body` in JSON at all (keeping the wire format closer to today's raw-passthrough behavior).

Rejected because: there is no evidence any plugin exists yet (this is pre-public, no external consumers — same reasoning applied to the `BodyFrom` field removal in this session), so there's no compatibility reason to prefer a shape other than the one already fully specified, exemplified, and reviewed in `docs/api/integration.md`. Matching the existing documented contract is strictly less design work and avoids a second, silent doc/code divergence of the same kind that created this problem in the first place.

## Tradeoffs

- **Body is now JSON-wrapped, not raw**: a plugin author who (incorrectly, against the documented contract) assumed the raw `body` string would arrive as the literal HTTP POST body will need to unwrap it from the envelope's `"body"` field instead. Since no plugin implementation is known to exist yet, this cost is believed to be zero in practice.
- **Slightly more work per publish call**: JSON-marshal the envelope, and (best-effort) JSON-unmarshal the response to extract `messageId`/`error`. Negligible compared to the network round-trip already dominating this path.
- **Response parsing is best-effort, not strict**: a plugin that returns `200 OK` with a non-JSON or missing-`messageId` body is still treated as success (matching the doc's "optional" framing for `messageId`). This means a malformed-but-successful plugin response can't be distinguished from one that's simply omitting `messageId` — considered acceptable since `messageId` itself is documented as optional and advisory only.

## Final Decision

Rewrite `doPluginPublish` (and its caller, the plugin branch of `executePublishStep`) to construct and send the JSON envelope `{integration, namespace, destination, headers, body}` exactly as documented in `docs/api/integration.md`, with `destination` set to the same `substituteVars`-resolved topic value now used on the Kafka branch, and `headers`/`body` resolved through the existing `substituteVarsWithSecrets` calls already present. Parse the response body as `{"messageId": "..."}` on success (surfaced as a step result) and as `{"error": "..."}` on failure (used as the returned error's message when present, falling back to the raw body otherwise). This is the smallest change that makes the plugin publish path match its own documented contract, brings it to parity with the already-correct Kafka path, and requires no CRD or docs changes beyond what's already written.
