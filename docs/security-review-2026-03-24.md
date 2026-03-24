# Security Design Review — 2026-03-24

## Scope

Deep review of the operator's security model, focusing on:

- Secret access and RBAC in multi-namespace deployments
- Cross-namespace trust boundaries
- Input validation and injection vectors
- Gateway and plugin trust models
- Data leakage via CRD status fields

## Executive Summary

The operator has solid security fundamentals: restricted Pod Security Standards, per-component ServiceAccounts, namespace-scoped RBAC option, and comprehensive webhook auth. However, the **AllNamespaces default** creates a wide blast radius that undermines several of these controls. The most critical findings involve **cross-namespace authorization gaps** and **server-side request forgery** in the FlowRun executor.

---

## Critical Findings

### C1. SSRF via HTTP Step URLs

**Location:** `internal/controller/flowrun_controller.go` — `executeHTTPStep()`, `applyHTTPIntegration()`

**Issue:** HTTP step URLs undergo variable substitution (`$(steps.x.outputs.url)`, `$(trigger.body.callback)`) and are passed directly to `http.Client.Do()`. No validation blocks requests to:

- `http://169.254.169.254` (cloud metadata — AWS/GCP/Azure instance credentials)
- `http://localhost:<port>` (sidecar services, kubelet, etcd)
- `http://kubernetes.default.svc.cluster.local` (API server)
- Private RFC1918 ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)

**Impact:** A malicious or compromised Flow definition can exfiltrate cloud IAM credentials, scan the internal network, or attack cluster services. Because the controller pod typically has a ServiceAccount with broad RBAC, responses from the API server are particularly dangerous.

**Severity:** Critical — this is the single highest-impact vulnerability.

**Decision (2026-03-24):** Two-layer mitigation:

**Layer 1 — URL blocklist:** Resolve DNS before connecting; reject if resolved IP is in a blocked range (RFC1918, link-local, loopback, metadata IPs). Block schemes other than `http://` and `https://`. Add `--http-step-blocked-cidrs` flag for customization.

**Layer 2 — Separate HTTP executor controller:** Extract HTTP step execution from the main controller into a dedicated `kubezap/http-executor` binary/image. The main controller (which holds broad RBAC: secrets, deployments, roles, RBAC management) never makes outbound HTTP calls. Instead, it delegates to the HTTP executor pod which has minimal RBAC:

| Component | Secrets access | RBAC management | Outbound HTTP | CRD access |
|-----------|---------------|-----------------|---------------|------------|
| Main controller | get/list/watch (scoped) | roles, rolebindings | **None** | Full CRUD on all KubeZap CRDs |
| HTTP executor | **None** | **None** | Yes (with blocklist) | flowruns:get/update only |

Architecture: One HTTP executor Deployment per namespace (like webhook gateway). Main controller writes HTTP step spec into FlowRun status; executor picks it up, executes, writes result back. Even if SSRF bypasses the DNS blocklist (e.g., via DNS rebinding), the executor pod has no ServiceAccount token with secrets access.

New files: `cmd/http-executor/main.go`, `internal/executor/http/` package. New image: `kubezap/http-executor`.

### C2. Cross-Namespace Flow Execution Without Authorization

**Location:** `internal/controller/flowrun_controller.go:165-174`

**Issue:** `FlowRun.Spec.FlowRef.Namespace` allows a FlowRun in namespace A to execute a Flow defined in namespace B. The controller fetches the Flow using its own ClusterRole — there is no check that the FlowRun creator is authorized to access the target namespace.

Attack scenario:
1. Attacker has write access to namespace `dev` (can create Triggers/Flows/FlowRuns)
2. Attacker creates a FlowRun with `flowRef.namespace: production`
3. Controller fetches and executes the production Flow, potentially accessing production Secrets

**Impact:** Namespace isolation bypass. Secrets referenced by the target Flow are fetched in the target namespace's context.

**Severity:** Critical in multi-tenant deployments; low in single-tenant.

**Decision (2026-03-24):** Remove cross-namespace FlowRef entirely. Delete `FlowRef.Namespace` from the API. FlowRuns always execute Flows in their own namespace. Cross-namespace flows deferred to v1beta1 with a proper authorization model (e.g., FlowGrant CRD modeled after Gateway API's ReferenceGrant).

### C3. Cross-Namespace Secret Fetch

**Location:** `internal/controller/flowrun_controller.go:906-916` — `fetchSecretValue()`

**Issue:** Related to C2. When a Flow uses `$(secrets.name.key)` placeholders, the controller fetches the Secret from the **Flow's namespace**, not the FlowRun's namespace. Combined with cross-namespace FlowRef, this means a FlowRun in namespace A can indirectly read Secrets in namespace B by invoking a Flow in namespace B that references those Secrets.

Even without cross-namespace FlowRef, the controller's ClusterRole grants `get/list/watch` on all Secrets cluster-wide. A compromised controller pod exposes every Secret in the cluster.

**Severity:** Critical (cluster-wide secret exposure if controller is compromised).

**Decision (2026-03-24):** Default to OwnNamespace (operator's own namespace only). In AllNamespaces mode (`WATCH_NAMESPACES=*`), restrict secrets RBAC to namespaces labeled `kubezap.io/managed=true`. Combined with the HTTP executor architecture (C1), this means even the main controller's secret access is scoped, and the component making outbound HTTP calls has no secret access at all.

---

## High Findings

### H1. Webhook Triggers Unauthenticated by Default

**Location:** `api/v1alpha1/trigger_types.go` — `WebhookAuth` is optional; `internal/gateway/webhook/handler.go` skips auth if `spec.webhook.auth` is nil.

**Issue:** A Trigger with `type: webhook` and no `spec.webhook.auth` block accepts requests from any caller. The gateway runs in-namespace, but if the Service is exposed via Ingress or LoadBalancer, unauthenticated internet traffic can trigger arbitrary Flows.

**Impact:** Unauthorized Flow execution, potential data exfiltration via HTTP steps, resource exhaustion.

**Decision (2026-03-24):** Option B — ValidatingWebhookConfiguration that emits an admission warning (not rejection) when a Trigger with `type: webhook` is created/updated without `spec.webhook.auth`. Non-breaking; raises visibility without blocking dev workflows.

### H2. Sensitive Data Persisted in FlowRun TriggerData

**Location:** `internal/gateway/webhook/handler.go:353-420`

**Issue:** Webhook request headers and body are stored in `FlowRun.Spec.TriggerData`. The gateway redacts known sensitive headers (`Authorization`, `X-Api-Key`), but:

- Custom auth headers (e.g., `X-Custom-Token`, `X-Webhook-Secret`) are not redacted
- Request bodies may contain credentials (OAuth tokens, API keys in JSON payloads)
- FlowRun objects are readable by anyone with `flowruns:get` RBAC in the namespace
- Data persists in etcd for the TTL duration (default 24h succeeded, 72h failed)

**Impact:** Credential leakage to namespace users who should not see webhook payloads.

**Recommendation:**

1. Make the sensitive headers list configurable (operator flag or Trigger annotation)
2. Add `spec.webhook.redactBody: true` option to store only a hash/truncation of the body
3. Consider field-level RBAC (not natively supported in K8s — would need a webhook or separate Secret)

### H3. No Audit Trail for Secret Access

**Location:** `internal/controller/flowrun_controller.go:1522-1603`

**Issue:** When `$(secrets.name.key)` placeholders are resolved, the Secret fetch is silent — no structured log entry, no Kubernetes Event, no metric. This makes it impossible to answer "which Flows accessed Secret X in the last 24 hours?" without Kubernetes audit logs enabled at the cluster level.

**Impact:** Compliance gap for PCI-DSS, SOC2, HIPAA environments that require secret access audit trails.

**Recommendation:** Add structured log entry at Info level for every secret fetch: `{"event": "secret_access", "secret": "name", "key": "key", "flow": "flow-name", "flowrun": "flowrun-name", "namespace": "ns"}`. Add a `kubezap_secret_accesses_total{namespace,secret,flow}` Prometheus counter.

### H4. Plugin Image Trust — No Verification

**Location:** `internal/controller/integration_controller.go:87-91`

**Issue:** `Integration` with `type: plugin` causes the operator to create a Deployment with an arbitrary container image. The operator performs no verification of image signature, digest, or registry origin.

A compromised or malicious plugin image gets:
- A ServiceAccount with `triggers:get/list/watch` and `flowruns:create` in-namespace
- Network access to the Kubernetes API server
- Potential access to injected Secrets via `spec.plugin.secretRefs`

**Impact:** Supply chain attack vector. Plugin compromise → FlowRun injection → arbitrary HTTP requests via Flows.

**Decision (2026-03-24):** Option A — Add optional `spec.plugin.imageDigest` field. When set, operator validates resolved digest at reconcile time before creating/updating the plugin Deployment. Opt-in; no enforcement for users who don't set it. Long-term: integrate with Sigstore/cosign.

### H5. CEL Expression Resource Exhaustion

**Location:** `internal/controller/flowrun_controller.go` — `evaluateWhen()`

**Issue:** CEL `when` expressions are compiled and evaluated without resource limits. While CEL is not Turing-complete (no unbounded loops), complex expressions with large comprehensions can still consume significant CPU:

```yaml
when:
  expression: "size([1,2,3].map(x, [1,2,3].map(y, [1,2,3].map(z, x*y*z)))) > 0"
```

Nested comprehensions grow combinatorially. A sufficiently complex expression can stall the FlowRun controller's work queue.

**Impact:** DoS of FlowRun processing across the namespace (or cluster in AllNamespaces mode).

**Recommendation:** Add a CEL evaluation cost budget using `cel.CostEstimator` and `cel.CostLimit()` program options. Reject expressions that exceed the budget. Add `--cel-cost-limit` flag (default: 10000).

---

## Medium Findings

### M1. No Rate Limiting on FlowRun Creation

Webhook gateway creates FlowRuns with no per-Trigger rate limit. A flood of webhook requests creates unbounded FlowRuns, exhausting etcd storage and controller work queue. The Trigger `cooldown` field exists for cron triggers but is not enforced for webhooks.

**Recommendation:** Add `spec.webhook.maxConcurrentRuns` and `spec.webhook.rateLimit` (requests/window) to the Trigger CRD. Gateway enforces locally; controller enforces globally via FlowRun count query.

### M2. Plugin-to-Controller Communication Over Plain HTTP

`executePublishStep()` calls `http://kubezap-plugin-<name>.<ns>.svc.cluster.local:<port>/publish` over plain HTTP. In clusters without network encryption (no service mesh, no WireGuard CNI), publish payloads — which may contain sensitive data from Flow step outputs — transit in cleartext.

**Recommendation:** Document as known limitation. Add mTLS support in a future release. For now, recommend Istio/Linkerd sidecar injection for sensitive deployments.

### M3. Inconsistent Header Redaction Across Gateways

Webhook gateway redacts `Authorization` and `X-Api-Key` from TriggerData. Kafka and AMQP gateways store full message headers without redaction. Kafka message headers often carry auth tokens, correlation IDs with embedded credentials, or custom auth headers.

**Recommendation:** Extract the redaction logic into a shared `internal/gateway/redact` package. Apply consistently across all gateway types.

### M4. NetworkPolicies Not Deployed by Default

The operator deployment includes no NetworkPolicy. Gateways and plugins can reach any endpoint in the cluster. The `config/network-policy/allow-metrics-traffic.yaml` is commented out and metrics-only.

**Recommendation:** Ship example NetworkPolicies in `config/network-policy/` covering: (a) controller egress to API server only, (b) webhook gateway ingress from Ingress controller only + egress to API server only, (c) plugin egress to broker + API server only. Document in observability/security guide.

---

## Design Decisions (Resolved 2026-03-24)

All questions resolved. Full rationale in `docs/tech-debt/pending-input-required.md`.

| Question | Decision | Rationale |
|----------|----------|-----------|
| **Q1:** Cross-namespace FlowRef | **Remove entirely** | Eliminates the attack surface. Cross-namespace flows deferred to v1beta1 with FlowGrant CRD. |
| **Q2:** Webhook auth default | **Admission warning** | ValidatingWebhook warns (not rejects) on unauthenticated webhook Triggers. Non-breaking. |
| **Q3:** Plugin image digest | **Optional field** | `spec.plugin.imageDigest` validated at reconcile time when set. Opt-in. |
| **Q4:** Namespace default | **OwnNamespace default + label-restricted AllNamespaces** | Least privilege by default. AllNamespaces mode (`WATCH_NAMESPACES=*`) restricts secrets to `kubezap.io/managed=true` namespaces. |
| **Q5:** SSRF protection | **Blocklist + separate HTTP executor** | DNS-resolved IP blocklist as baseline. HTTP step execution extracted to dedicated `kubezap/http-executor` pod with no secrets RBAC — defense-in-depth against blocklist bypass. |

---

## Strengths Observed

The review also identified strong security practices already in place:

- Pod Security Standards "restricted" profile enforced on controller and all managed Deployments
- Capabilities drop ALL, non-root, read-only root filesystem, seccomp RuntimeDefault
- Per-component ServiceAccounts with minimal namespace-scoped RBAC for gateways
- Constant-time HMAC comparison (already fixed for bearer/apiKey per §16 P1)
- No inline credentials in CRDs — all secrets via SecretKeySelector references
- TLS certificate volumes mounted with mode 0400
- Sensitive header redaction in webhook access logs
- Source IP not used as Prometheus label (cardinality awareness)
- HTTP/2 disabled by default (CVE mitigation)
- OwnNamespace/SingleNamespace RBAC variant available for multi-tenant

---

## Schedule Impact

New items added to `docs/schedule.md` §17 (Security Hardening). Cross-references to this document for rationale.
