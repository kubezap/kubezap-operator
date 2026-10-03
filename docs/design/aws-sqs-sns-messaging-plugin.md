# AWS SQS/SNS Messaging Plugin (EPIC-008 Pilot)

> Status: Draft
> Related: `api/v1alpha1/trigger_types.go`, `internal/controller/trigger_controller.go`, `internal/controller/integration_controller.go`, `docs/api/plugin-contract.md`, `docs/api/integration.md`, `docs/api/trigger.md`, `planning/backlog/epics/EPIC-008-aws-sqs-sns-messaging-plugin.md`

## 1. Problem Statement

KubeZap has no supported path for AWS SQS/SNS-backed event automation. The documented escape hatch is `Integration{type: plugin}`, but two problems block using it for this pilot:

1. **The plugin contract's subscriber role was never wired into the `Trigger` CRD.** `TriggerSpec.Type` has a closed `+kubebuilder:validation:Enum=webhook;cron;kafka;amqp;nats;resource` (verified in `api/v1alpha1/trigger_types.go` and the generated `config/crd/bases/automation.kubezap.io_triggers.yaml`) with no `plugin` value and no generic `spec.plugin` field, and `trigger_controller.go`'s dispatch has no plugin case. Every `type: plugin` example in `docs/api/integration.md` is publisher-role only (a Flow step's `type: publish` action calls `/publish` directly via `integrationRef` — no Trigger involved). `docs/api/plugin-contract.md`'s "Trigger selection" section (`spec.type == <your-integration-type>`) describes behavior the CRD schema does not currently allow — a Trigger CR with `type: plugin` is rejected at admission today.
2. **No reference plugin has ever been built against the contract**, so there's no evidence the contract as written (dedup keys, FlowRun schema, health check, mTLS) is actually sufficient to implement a real, non-trivial broker integration against.

This design record picks a generic fix for (1) — reusable by any future subscriber-role plugin, not AWS-specific — and resolves the AWS-specific decisions needed to actually ship the SQS (subscriber) + SNS (publisher) pilot plugin from `EPIC-008`.

## 2. Constraints

- **API stability (v1alpha1)**: no field removals or breaking changes. Adding `plugin` to `TriggerSpec.Type`'s enum and a new optional `Plugin *PluginTrigger` field is additive and safe for existing Trigger CRs.
- **RBAC**: the plugin pod's RBAC (`triggers`: get/list/watch, `flowruns`: create) is already granted unconditionally to every `type: plugin` Integration's ServiceAccount today (`internal/controller/integration_controller.go`, multiple call sites building this Role) — this design must not require any new operator-side RBAC grant.
- **Plugin trust model**: per existing policy (`docs/api/integration.md`'s Limitations), the operator does not verify plugin images and does not interpret plugin-specific configuration. Any new Trigger-side field for plugin config must stay opaque to the operator — no broker-specific typed fields on `TriggerSpec`.
- **Credentials never persisted to etcd** beyond the referenced Secret itself — same invariant every other Integration/plugin already honors via `spec.plugin.secretRefs`.
- **Performance**: `trigger_controller.go`'s reconcile path for `type: plugin` Triggers must stay O(1) per Trigger — no per-Trigger dynamic informer or polling loop on the operator side (unlike kafka/amqp/nats, which do spawn per-Trigger gateway subscriptions). The plugin pod, not the operator, owns the subscription lifecycle.
- **OpenShift SCC / OLM**: the new plugin container must run under the same restricted-SCC-compliant PodSecurityContext already applied to all plugin Deployments; its image is an additional `relatedImage` entry, not a new bundle mechanism.
- **No new external dependency in the core operator binary**: the AWS SDK for Go v2 is a dependency of the new plugin binary (`cmd/aws-messaging-plugin`) only — it must not be imported by `cmd/main.go`, `internal/controller/`, or any other operator-binary package (the controller does not make outbound calls to external systems; see `CLAUDE.md`'s Runtime Architecture table).

## 3. Invariants

- A Trigger with `type: plugin` referencing an Integration that does not exist, or exists but is not `type: plugin`, never silently no-ops — it surfaces a clear condition/event, mirroring the existing `CredentialResolutionFailed`/`CredentialResolutionSucceeded` pattern (`docs/design/gateway-credential-failure-visibility.md`) already used by kafka/amqp/nats.
- The operator never runs subscription logic for a `type: plugin` Trigger — no dynamic informer, no gateway Deployment beyond the one the Integration already manages. The plugin pod is solely responsible for watching Triggers and creating FlowRuns, per the existing plugin contract.
- `PluginTrigger.Config` values are never interpreted, validated, or defaulted by the operator. A malformed or missing key (e.g. an invalid SQS queue URL) is the plugin's problem to detect and surface via its own logs/health check — consistent with the existing "operator does not verify plugin images" trust boundary.
- AWS credentials (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/optional `AWS_SESSION_TOKEN`) reach the plugin container only via the existing `spec.plugin.secretRefs` + `envVarMappings` mechanism — no new credential-delivery path is introduced.
- SQS message deletion (the offset-commit equivalent) happens only after the corresponding FlowRun create call returns `200 Created` or `409 Conflict`, per the plugin contract's "Offset/cursor commit" ordering requirement — never before.
- The SNS publisher role never resolves a topic name to an ARN on the plugin's behalf — the full ARN is required in the `/publish` request's `destination` field, so no `sns:ListTopics`/`GetTopicAttributes` IAM permission is ever needed by the plugin's credentials.

## 4. Rejected Alternatives

**For the generic Trigger-subscriber wiring:**
- **A broker-specific typed field per plugin** (e.g. `spec.sqs`, `spec.sns`, one per future plugin) — rejected. This is exactly the enum/field explosion the existing type taxonomy deliberately avoids ("intentionally protocol-level, not broker-level," `docs/api/integration.md`); it would require a CRD change for every future plugin, defeating the purpose of `type: plugin` existing at all.
- **Let the operator validate `PluginTrigger.Config` against a plugin-declared schema** (e.g. read a schema from the plugin's `/healthz` or a new endpoint) — rejected as scope creep for this pilot: it would require a new contract addition, a new controller-to-plugin HTTP call, and doesn't fit the existing "operator does not verify images" trust boundary. A malformed config surfacing as a plugin-side error (visible in its logs / a failed health check) is an acceptable pilot-stage tradeoff.

**For AWS credentials:**
- **IRSA (IAM Roles for Service Accounts) for EKS** — rejected for this pilot. IRSA requires the plugin's `ServiceAccount` to carry an `eks.amazonaws.com/role-arn` annotation, but the operator fully owns and reconciles that ServiceAccount object today with no field to pass such an annotation through, and adding one is itself a CRD change beyond this pilot's footprint. Secret-based access keys are sufficient to prove the pattern end-to-end; IRSA is a reasonable follow-up once the pilot ships.

**For SQS dedup key:**
- **Content-hash of the message body** — rejected: unnecessary, since SQS already assigns a globally unique `MessageId` to every accepted message (standard and FIFO alike), and hashing would cost more (reading/hashing the full body) for no dedup benefit over the ID SQS already provides.
- **`MessageDeduplicationId` for FIFO queues, `MessageId` for standard queues (branching by queue type)** — rejected in favor of one uniform rule: `MessageDeduplicationId` is a producer-supplied field not guaranteed to be present on receive for content-based-dedup FIFO queues, whereas `MessageId` is always returned by `ReceiveMessage` regardless of queue type.

**For SNS publish semantics:**
- **Accept a bare topic name and resolve it to an ARN via `sns:ListTopics`/`GetTopicAttributes`** — rejected: requires broader IAM permissions than publish-only, adds a network round trip and a caching-invalidation problem (topic recreated with the same name gets a new ARN), for no real benefit over requiring the ARN the user already has immediately after creating the topic.

**For plugin source/image location:**
- **A separate standalone repository** (e.g. `kubezap/aws-messaging-plugin`) — rejected for the pilot: splitting CI/release/versioning coordination across repos is overhead disproportionate to proving out one pilot plugin. Revisit if/when this plugin graduates (per `docs/api/integration.md`'s Community Plugin Graduation criteria) or attracts external contributors who would rather not need commit access to the core operator repo.

## 5. Tradeoffs

- No AWS workload-identity auth (IRSA) in this pilot — EKS operators wanting to avoid long-lived access keys must wait for a follow-up. Secret-based credentials are at least consistent with how every other Integration/plugin in this codebase already handles credentials today.
- `PluginTrigger.Config` is untyped (`map[string]string`) and unchecked by the operator's admission webhook — a malformed SQS queue URL passes CRD validation and only surfaces once the plugin pod tries to use it. This trades schema safety for genericity across arbitrary future plugins; a typed field would defeat the point of `type: plugin`.
- Requiring the full SNS topic ARN (rather than a friendlier bare name) pushes a small amount of copy/paste burden onto Flow authors, in exchange for zero extra IAM permissions and zero plugin-side ARN-resolution/caching logic.
- Keeping the plugin's source in this repo (rather than a separate repo) couples its release cadence to the operator's — acceptable for a pilot; revisit at graduation scale.
- The operator's Trigger-side validation for `type: plugin` can only catch "Integration missing or wrong type" — it cannot detect AWS-credential failures or SQS-permission errors, which surface only via the plugin's own health check and logs. This is the same visibility boundary every other plugin already has; it is not a regression introduced by this design.

## 6. Final Decision

Add `plugin` to `TriggerSpec.Type`'s enum and a new optional field `Plugin *PluginTrigger{IntegrationRef corev1.LocalObjectReference; Config map[string]string}` to `api/v1alpha1/trigger_types.go`, generic enough for any future subscriber-role plugin to reuse without a further CRD change. `trigger_controller.go` gets a minimal `type: plugin` case: validate the referenced Integration exists and is `type: plugin` (surfacing a clear condition/event on failure, mirroring the existing credential-failure-visibility pattern), then do nothing further — the plugin pod itself (already granted `triggers` get/list/watch and `flowruns` create RBAC unconditionally for any `type: plugin` Integration) owns the entire subscription lifecycle, exactly as the plugin contract already documents.

Ship a single first-party-maintained plugin binary, `cmd/aws-messaging-plugin` (+ `internal/plugin/awsmessaging/`), implementing both roles: an SQS subscriber (dedup key = SQS `MessageId`, uniform across standard and FIFO queues; message deletion only after FlowRun create returns 200/409) and an SNS publisher (`/publish`'s `destination` field is the full topic ARN, required; `idempotencyKey` forwarded as `MessageDeduplicationId` only for `.fifo`-suffixed topics; `headers` forwarded as SNS `MessageAttributes`). AWS credentials are Secret-based (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/optional `AWS_SESSION_TOKEN`) via the existing `spec.plugin.secretRefs`/`envVarMappings` mechanism, with `AWS_REGION` (and an optional `AWS_ENDPOINT_URL` override for LocalStack-backed CI testing) via `spec.plugin.env` — no new credential-delivery mechanism, no IRSA in this pilot. The plugin's source, Dockerfile, and image publishing follow the exact per-binary convention already used by `cmd/kafka-gateway/` et al., living in this repo rather than a separate one for the duration of the pilot.
