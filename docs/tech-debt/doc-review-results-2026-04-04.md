# Documentation Review Results — 2026-04-04

## HIGH — Inaccuracy or missing critical content

- [docs/api/integration.md] **IntegrationStatus fields diverge from Go types**: Doc lists `gatewayDeployments` ([]GatewayDeploymentRef), `connectedTriggers` (integer), and `phase` values `Ready/Degraded/Failed`. Go types have `GatewayDeploymentName` (string), no `connectedTriggers`, and phase enum `Ready/Degraded/Pending`. These are user-visible fields shown in `kubectl describe`. Recommendation: align doc table with Go type; if the richer status is planned, mark the extra fields as "planned" or implement them.

- [docs/api/integration.md] **KafkaIntegrationSpec doc lists fields not in Go types**: `producerConfig` and `consumerConfig` (map[string]string) are documented but do not exist in `api/v1alpha1/integration_types.go`. Users following the docs will get CRD validation errors. Recommendation: remove from docs or implement the fields.

- [docs/api/integration.md] **PluginIntegrationSpec doc lists fields not in Go types**: `replicas`, `resources`, `config`, and `imagePullSecrets` are documented but not in the Go type. Only `image`, `imageDigest`, `publisherPort`, `secretRefs`, and `env` exist. Recommendation: remove phantom fields from docs or implement them.

- [docs/guides/webhook-security.md] **HMAC config fields diverge from Go types**: Doc shows `header`, `algorithm`, `prefix`, and `encoding` fields on HMACConfig. Go type only has `secretRef` (SecretKeySelector). Either the Go type needs these fields or the docs describe planned-but-unimplemented functionality. Recommendation: align; this directly affects users configuring HMAC auth.

- [docs/guides/webhook-security.md] **OIDC config fields diverge from Go types**: Doc shows `requiredClaims`, `jwksUri`, `jwksCacheTTL`. Go type only has `issuer` and `audience`. Recommendation: align.

- [docs/guides/webhook-security.md] **Bearer auth doc uses different field name**: Doc shows `bearer.secretRef` but Go type uses `bearer.tokenSecretRef`. Users copying the doc example will get validation errors. Recommendation: fix doc examples to use `tokenSecretRef`.

- [docs/guides/webhook-security.md] **Basic auth doc describes different API shape**: Doc shows `secretRef` (combined user:pass string) and `usernameSecretRef`/`passwordSecretRef` (split), but Go type uses `secretRef` (LocalObjectReference) + `usernameKey`/`passwordKey` pattern. These are structurally different. Recommendation: align doc examples with Go type `WebhookBasicAuth`.

- [docs/guides/webhook-security.md] **`trustedProxies` field not in Go types**: Documented on WebhookAuth and referenced in examples, but not present in the Go type. Recommendation: implement or remove from docs.

## MEDIUM — Confusing, incomplete, or outdated

- [docs/api/trigger.md] **Missing `flowRunGC` field in TriggerSpec table**: Go type has `FlowRunGC *FlowRunGCPolicy` but the trigger.md spec table did not list it. Users can't discover per-trigger GC configuration from the API reference. **Fixed inline**: yes.

- [docs/api/trigger.md] **Missing `rateLimit`, `redactHeaders`, `redactBody` in WebhookTrigger table**: These three fields exist in Go types but were absent from the WebhookTrigger spec table. **Fixed inline**: yes.

- [docs/api/trigger.md] **Missing `cooldown` in ResourceTrigger table**: Go type has `Cooldown *metav1.Duration` on ResourceTrigger but doc table omits it. **Fixed inline**: yes.

- [docs/api/flowrun.md] **FlowRunStatus `phase` includes `Waiting`**: Doc listed `Waiting` as a FlowRun phase but the Go enum is `Pending;Running;Succeeded;Failed;Cancelled`. `Waiting` is only a step-level phase. **Fixed inline**: yes.

- [docs/api/flowrun.md] **Missing `observedGeneration` in FlowRunStatus table**: Present in Go type but absent from docs. **Fixed inline**: yes.

- [docs/api/flowrun.md] **TriggerData table missing resource-event fields**: Go type has `eventType`, `resourceName`, `resourceNamespace`, `resourceAPIVersion`, `resourceKind` but these were absent from the TriggerData table. **Fixed inline**: yes.

- [docs/api/flow.md] **Limitations section says FlowRun is "planned"**: The `FlowRun` CRD is fully implemented. The limitation text was stale from an earlier version. **Fixed inline**: yes.

- [docs/api/flow.md] **FlowStatus.lastResult lists `PartialFailure`**: Not in Go type enum. Go type has only string field without enum constraint. Removed `PartialFailure` from doc to match observed values. **Fixed inline**: yes.

- [docs/api/flow.md] **Execution model says "or the specified namespace"**: Cross-namespace FlowRefs are not supported in v1alpha1; this text contradicts the policy documented elsewhere. **Fixed inline**: yes.

- [docs/overview.md] **AMQP example uses wrong field `queue`**: Go type `AmqpTrigger` uses `topic`, not `queue`. **Fixed inline**: yes.

- [docs/overview.md] **NATS example uses nonexistent field `durableName`**: Not in `NatsTrigger` Go type. **Fixed inline**: yes.

- [docs/overview.md] **Resource trigger example uses wrong API fields**: `watchEvents: [Modified]` should be `events: [update]`; `field`/`value` syntax doesn't exist — should use `watchFields` with dot-notation. **Fixed inline**: yes.

- [docs/architecture.md] **Container images section says "Three images"**: Table lists five (controller, webhook, kafka, amqp, nats) plus http-executor makes six. **Fixed inline**: yes (changed to "All images").

- [docs/architecture.md] **Container images table missing http-executor**: The table lists only 5 images but the component overview earlier lists the http-executor as a sixth binary. **Fixed inline**: no (table is in a different section; adding to schedule).

- [docs/api/integration.md] **Broken link**: `docs/tech-debt/gateway-shutdown-correctness.md` referenced in Limitations section does not exist. Recommendation: create the file or update the reference.

- [docs/architecture.md] **Broken link**: `[Plugin Trust Model](../api/integration.md#trust-model)` — the `#trust-model` anchor does not exist in integration.md. The section is titled "Plugin Integration Type". Recommendation: fix anchor.

## LOW — Style, polish, minor gaps

- [docs/overview.md] **Broken relative link to http-executor.md**: Used `docs/dev/http-executor.md` instead of `dev/http-executor.md`. Since overview.md is in `docs/`, the extra `docs/` prefix breaks the link. **Fixed inline**: yes.

- [docs/overview.md] **Broken relative link to tech-debt**: Used `../docs/tech-debt/` which resolves outside the repo. **Fixed inline**: yes (pointed to trigger.md#resourcetrigger instead).

- [docs/guides/troubleshooting.md] **Link to `using-the-cli.md`**: File exists, link is valid. No issue.

- [docs/guides/webhook-security.md] **Combining Methods section shows `ipAllowlist` as a sibling of `type`**: The Go type only allows one `type` at a time (it's an enum). The "combining methods" section implies multiple auth types can be set simultaneously, but the CRD validation enforces exactly one `type`. This is misleading but complex to fix (may require design discussion). Filed as HIGH above under trustedProxies/ipAllowlist discrepancy.

## Files reviewed with no significant issues

- docs/guides/observability.md — comprehensive, accurate, well-structured
- docs/guides/troubleshooting.md — practical, accurate, links valid
- docs/guides/mocking-http-endpoints.md — accurate, thorough
- docs/guides/security-checklist.md — accurate, links all valid
- docs/dev/http-executor.md — accurate, matches implementation description

## Inline fixes applied

- [docs/api/trigger.md] Added `flowRunGC` field to TriggerSpec table
- [docs/api/trigger.md] Added `rateLimit`, `redactHeaders`, `redactBody` to WebhookTrigger table
- [docs/api/trigger.md] Added `cooldown` to ResourceTrigger table
- [docs/api/flowrun.md] Removed `Waiting` from FlowRunStatus phase list; added `observedGeneration` field
- [docs/api/flowrun.md] Added resource-event fields to TriggerData table
- [docs/api/flow.md] Updated stale "FlowRun planned" text to describe current FlowRun CRD
- [docs/api/flow.md] Removed `PartialFailure` from FlowStatus.lastResult
- [docs/api/flow.md] Removed "or the specified namespace" from execution model
- [docs/overview.md] Fixed AMQP example: `queue` -> `topic`
- [docs/overview.md] Removed nonexistent `durableName` from NATS example
- [docs/overview.md] Fixed resource trigger example to use correct field names (`events`, `watchFields`)
- [docs/overview.md] Fixed broken relative link to http-executor.md
- [docs/overview.md] Fixed broken relative link to tech-debt (now points to trigger.md#resourcetrigger)
- [docs/architecture.md] Changed "Three images" to "All images" (table lists 5+)

## Schedule additions

Items to add to docs/schedule.md under a new section 24, priority P0/P1/P2.

### P0 — Fix before public release

- **webhook-security.md HMAC/OIDC/Bearer/Basic auth field mismatch**: The webhook-security.md guide documents fields (`header`, `algorithm`, `prefix`, `encoding` on HMAC; `requiredClaims`, `jwksUri`, `jwksCacheTTL` on OIDC; `trustedProxies` on WebhookAuth; `secretRef` on Bearer instead of `tokenSecretRef`) that do not exist in the Go types. Users following these docs will create invalid CRs. Either implement the fields in Go types or rewrite the security guide examples to match the current API. This is the highest-impact doc-reality mismatch found.

- **integration.md IntegrationStatus field mismatch**: Doc lists `gatewayDeployments`, `connectedTriggers`, `phase: Failed` that don't match Go types (`GatewayDeploymentName`, no `connectedTriggers`, `phase: Pending`). Align doc with Go types.

- **integration.md phantom spec fields**: KafkaIntegrationSpec (`producerConfig`, `consumerConfig`) and PluginIntegrationSpec (`replicas`, `resources`, `config`, `imagePullSecrets`) are documented but not in Go types. Remove from docs or implement.

### P1 — Fix before GA

- **integration.md broken link**: `docs/tech-debt/gateway-shutdown-correctness.md` does not exist. Create or update reference.

- **architecture.md broken anchor**: `#trust-model` anchor in link to integration.md does not exist. Fix to `#plugin-integration-type`.

- **architecture.md container images table missing http-executor**: Add http-executor row to container images table.

- **webhook-security.md "Combining Methods" section misleading**: Implies multiple auth types can be active simultaneously, but `WebhookAuth.Type` is a single enum. Clarify how IP allowlist combines with other methods (if it does via separate mechanism) or note this is planned.
