# WebhookGatewayConfig CRD

> Status: Approved
> Related: `internal/controller/gateway_deployment.go`, `internal/controller/trigger_controller.go`, `internal/webhook/trigger_webhook.go`, `docs/guides/webhook-security.md`, `docs/overview.md`, `planning/backlog/epics/EPIC-003-webhook-gateway-config-crd.md`, `planning/backlog/stories/STORY-008-webhookgatewayconfig-design-record.md`

## 1. Problem Statement

Per-namespace webhook gateway configuration is split across two undiscoverable, unvalidated mechanisms:

1. **Namespace annotations** (`kubezap.io/webhook-tls-secret`, `kubezap.io/webhook-mtls-ca-secret`) control the gateway's inbound TLS/mTLS serving. Read directly as `ns.Annotations["..."]` in `internal/controller/trigger_controller.go`, with no schema, no `kubectl explain`, and no validation — a typo in the annotation key silently no-ops instead of erroring.
2. **Hardcoded Go constants** control HPA behavior. `internal/controller/gateway_deployment.go`'s `desiredWebhookGatewayHPA` always sets CPU target 70%, min replicas 1, max replicas 10, for every namespace, with no per-namespace override at all.

Separately — a real gap independent of the above — there is no `PodDisruptionBudget` for the webhook gateway Deployment today. Combined with the hardcoded floor of 1 replica, a namespace running at low traffic has zero redundancy during a rollout or node drain, despite the HPA existing.

User impact: an operator cannot express "this namespace's webhook gateway needs custom TLS, a wider autoscaling range, and PDB protection" without hand-editing annotations (no validation, easy to typo) and living with globally-fixed HPA behavior they cannot override per namespace.

## 2. Constraints

- Must not change gateway behavior for any namespace that doesn't opt in — a namespace with no `WebhookGatewayConfig` object must behave identically to today (HPA min=1/max=10/target=70%, no PDB, TLS driven by whatever the annotations currently say up until this ships).
- New CRD, so: namespaced scope (matches every other KubeZap CRD's default), RBAC additions go through the existing per-mode Role/ClusterRole generation (`config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml`), and it must be registered in `api/v1alpha1/groupversion_info.go` and `cmd/main.go` (both hot files per `CLAUDE.md`'s Parallel Agent Guidelines — sequential wiring only).
- No change to `Trigger`/`Flow`/`FlowRun`/`Integration` schemas — this is a new, independent CRD, not a field added to an existing one.
- TLS secret references must follow the existing `corev1.LocalObjectReference`-to-a-same-namespace-Secret convention already used by `Integration.spec.kafka.tls.{caSecretRef,clientCertSecretRef}` — no cross-namespace secret references (consistent with this project's existing no-cross-namespace-Secret-reference posture elsewhere).
- All four OLM install modes (OwnNamespace/SingleNamespace/MultiNamespace/AllNamespaces) must keep working — the CRD's RBAC must fit the existing Role-vs-ClusterRole split by watch scope.

## 3. Invariants

- A namespace with no `WebhookGatewayConfig` object reconciles the webhook gateway Deployment with exactly today's defaults: HPA min=1, max=10, target-CPU=70%, no `PodDisruptionBudget`, and TLS driven by nothing (see Final Decision on the annotation cutover — after this ships, the annotations are no longer read at all, object-absent and annotations-present both mean "use defaults").
- At most one `WebhookGatewayConfig` object may exist per namespace at any time — a second `create` in a namespace that already has one is rejected at admission time, never silently accepted or silently ignored.
- `spec.hpa.minReplicas`, when set, only rejects non-positive values (`<= 0`) — no upper-bound coupling to "real HA," since 1 replica remains a legitimate, currently-default, non-HA choice for a low-traffic namespace.
- `spec.podDisruptionBudget.minAvailable`, when unset, means "no PodDisruptionBudget is created for this namespace's gateway" — matching today's actual behavior (none exists). Setting it is the only way to opt in.
- Once this change ships, `kubezap.io/webhook-tls-secret` and `kubezap.io/webhook-mtls-ca-secret` are no longer read anywhere in the codebase — `internal/controller/trigger_controller.go`'s annotation lookups are removed, not left as a secondary fallback path.

## 4. Rejected Alternatives

**A. Fixed required singleton name (e.g. every namespace's config object must be named `default`).**
Rejected (owner decision, 2026-09-12): simpler to implement (no new webhook), but produces a worse failure mode — a second object under any other name would simply be silently ignored by the controller (which only ever looks up the fixed name), the same class of "typo silently no-ops" problem this CRD exists to fix for the annotations it replaces. An explicit admission-time rejection gives a clear, immediate error instead of silent inconsistency between what a user thinks is configured and what the controller actually reads.

**B. Deprecate-and-warn migration (both annotations and CRD work; CRD wins on conflict; annotation usage logs a deprecation warning).**
Rejected (owner decision, 2026-09-12): this project has not gone public yet (`EPIC-001` is still in progress) — there are no external users depending on the annotations today, so there is no real backward-compatibility obligation to protect. A dual-read-with-precedence-rules implementation is meaningfully more code (and more surface for the exact kind of silent-misconfiguration bug this CRD is meant to eliminate: which one "wins" when both are set is itself a new way to get it wrong) for a compatibility guarantee nothing currently needs. Hard cutover is simpler and strictly safer here, specifically because of the timing (pre-launch).

**C. Reject `spec.hpa.minReplicas < 2` outright (hard-enforce real HA).**
Rejected (owner decision, 2026-09-12): would be a behavior change from today's actual, currently-shipping default (min=1) — anyone writing a `WebhookGatewayConfig` that intentionally mirrors today's settings for a low-traffic namespace would be rejected by the CRD they're supposed to be able to use as a drop-in typed replacement for "no config at all." The invariant this CRD promises (absent-config behaves like today) would be violated the moment `spec.hpa.minReplicas` is set to the same value that's already the implicit default.

**D. Non-blocking CEL warning (not rejection) when `minReplicas < 2`.**
Considered as a middle ground between A/no-validation and C/hard-reject. Rejected (owner decision, 2026-09-12) in favor of plain passthrough: adds a CEL validation marker and a warning-message UX surface for a nudge that's arguably better delivered as documentation (the `docs/api/` page for this CRD, from STORY-012) than as apply-time noise — especially since 1 replica is this CRD's own inherited default, not a mistake to warn about.

## 5. Tradeoffs

- The admission webhook for singleton enforcement is new code (a `ValidatingWebhookConfiguration` entry plus a `Validate{Create,Update}` implementation, following the existing pattern in `internal/webhook/trigger_webhook.go`) that a fixed-name convention would have avoided entirely. Judged worth it for the clearer failure mode (see Rejected Alternative A).
- Hard cutover means this change is not safely revertible without a follow-up migration if it turns out some deployment *was* relying on the annotations by the time this ships — accepted because the project isn't public yet, but this tradeoff would need re-litigating (a new design record, not silently reopening this one) if the timeline slips past public launch.
- No enforced HA minimum means a namespace can still ship with a single gateway replica and no `PodDisruptionBudget` by simply not setting `spec.hpa.minReplicas`/`spec.podDisruptionBudget.minAvailable` — this CRD makes real HA *possible* and *typed*, it does not make it the default. Closing that gap is left to documentation (STORY-012) and operator judgment, not the API schema.

## 6. Final Decision

A new namespaced CRD `WebhookGatewayConfig` (`api/v1alpha1/webhookgatewayconfig_types.go`), one per namespace, with `spec.tls.{serverSecretRef,clientCASecretRef}` (both `corev1.LocalObjectReference`, replacing the two annotations), `spec.hpa.{minReplicas,maxReplicas,targetCPUUtilization}` (all `*int32`, defaulting to today's hardcoded 1/10/70 when the object or field is absent), and `spec.podDisruptionBudget.minAvailable` (`*intstr.IntOrString`, no PDB created when nil). A new validating webhook (mirroring `internal/webhook/trigger_webhook.go`'s registration pattern) rejects a `create` when another `WebhookGatewayConfig` already exists in the same namespace, regardless of name. `spec.hpa.minReplicas` is validated only to be a positive integer — no minimum-for-HA enforcement, plain passthrough otherwise. The two existing Namespace annotations are removed from `internal/controller/trigger_controller.go` in the same change (hard cutover, no transition window) — this CRD is a straight replacement, not an addition alongside them.
