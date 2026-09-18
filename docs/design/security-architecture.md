# Security Architecture

> Status: Approved
> Date: 2026-03-24
> Related: `internal/controller/flowrun_controller.go`, `cmd/http-executor/main.go`, `internal/executor/http/`, `api/v1alpha1/trigger_types.go`, `api/v1alpha1/integration_types.go`, `internal/gateway/redact/`, `internal/metrics/metrics.go`

This document records the security-relevant architecture decisions made across the operator, gateways, and executor. It is written as decisions with rationale, not as an audit report — for how a specific SSRF-adjacent decision here was later hardened further, see [HTTP Executor Egress NetworkPolicy](executor-egress-networkpolicy.md) and [Webhook Gateway Trust Boundary Hardening](webhook-gateway-trust-boundary.md).

## SSRF protection: blocklist + a separate HTTP executor

HTTP step URLs go through variable substitution (`$(steps.x.outputs.url)`, `$(trigger.body.callback)`) before being requested — without protection, a Flow could be made to reach cloud metadata endpoints (`169.254.169.254`), `localhost` sidecars/kubelet, the Kubernetes API server, or internal RFC1918 ranges. Because the controller's own ServiceAccount has broad RBAC, a response relayed from the API server would be especially dangerous.

Two-layer mitigation:

**Layer 1 — URL blocklist.** Resolve DNS before connecting; reject if the resolved IP falls in a blocked range (RFC1918, link-local, loopback, metadata IPs). Block schemes other than `http://`/`https://`. `--http-step-blocked-cidrs` extends the default list.

**Layer 2 — separate HTTP executor.** HTTP step execution is extracted from the main controller into a dedicated `kubezap/http-executor` binary/image (`cmd/http-executor/main.go`, `internal/executor/http/`). The main controller — which holds broad RBAC (secrets, deployments, roles, RBAC management) — never makes outbound HTTP calls itself; it delegates to an executor pod with minimal RBAC:

| Component | Secrets access | RBAC management | Outbound HTTP | CRD access |
|-----------|---------------|-----------------|---------------|------------|
| Main controller | get/list/watch (scoped) | roles, rolebindings | **None** | Full CRUD on all KubeZap CRDs |
| HTTP executor | **None** | **None** | Yes (with blocklist) | flowruns:get/update only |

One HTTP executor Deployment runs per namespace (like the webhook gateway). Even if the blocklist is bypassed (e.g. via DNS rebinding), the executor pod has no ServiceAccount token with secrets access — see the [full RPC contract and current SSRF defaults](../architecture/http-executor.md).

### Credential flow: controller-to-executor RPC

The executor has no secrets RBAC, but HTTP steps need auth credentials (bearer tokens, API keys, basic auth). The controller resolves all `$(secrets.*)` placeholders and Integration auth in-memory, then sends the **fully-resolved HTTP request** to the executor over an internal HTTP call. Credentials transit over the wire but are never written to etcd.

```
Controller                              Executor
   │                                       │
   ├─ resolve $(secrets.api.token)         │
   ├─ build full HTTP request spec         │
   ├──── POST /execute ────────────────────►│
   │     {url, method, headers (with       │
   │      resolved Bearer token),          │
   │      body, timeout_ms}               │
   │                                       ├─ SSRF blocklist check (DNS resolve → IP check)
   │                                       ├─ execute outbound HTTP call
   │◄──── {status_code, headers, body} ────┤
   ├─ write result to FlowRun status       │
```

**Why internal RPC over alternatives:**

| Approach | Credentials in etcd | Executor needs secrets RBAC | Complexity |
|----------|:---:|:---:|---|
| **Internal RPC (chosen)** | No | No | One HTTP endpoint |
| Ephemeral Secret | Yes (briefly) | Yes (`get`, `delete`) | Secret lifecycle management |
| Per-step Pod (Argo-style) | No (kubelet injects) | No | Pod creation overhead per step; scheduler latency |

**Protocol: HTTP/JSON**, not gRPC — one JSON request object and one JSON response for a single endpoint doesn't justify the protobuf tooling gRPC would add to the build. HTTP is debuggable with `curl`, uses only stdlib `net/http`, and has equivalent mTLS support via `tls.Config`.

### Controller-to-executor channel security

The channel carries resolved secrets (bearer tokens, API keys) in HTTP headers. Two layers:

**Layer 1 — NetworkPolicy (mandatory, always shipped).** The controller reconciler creates a `NetworkPolicy` restricting the executor's ingress to controller pods in the same namespace. Zero code change on the caller side (just manifests), blocks rogue pods from calling the executor, and is the baseline for all deployments. Limitation: depends on CNI enforcement (plain Flannel doesn't enforce it); does not encrypt traffic.

**Layer 2 — mTLS (opt-in, for clusters without a service mesh).** Enabled via `--executor-mtls=true` on the controller: it generates a self-signed CA at startup, issues a server cert for the executor and a client cert for itself, and both sides verify each other's certificate. For clusters with Istio/Linkerd, the service mesh already provides automatic mTLS — the flag is unnecessary there and shouldn't be enabled (double encryption is wasteful).

**Why both, not just one:**

| Threat | NetworkPolicy alone | mTLS alone | Both |
|--------|:---:|:---:|:---:|
| Rogue pod in namespace calls executor | Blocked | Blocked (no client cert) | Blocked |
| CNI doesn't enforce NetworkPolicy | **Exposed** | Blocked | Blocked |
| Traffic sniffing (compromised node) | **Exposed** | Encrypted | Encrypted |
| Stolen ServiceAccount token from outside pod | N/A | Blocked (requires cert) | Blocked |
| Misconfigured NetworkPolicy | **Exposed** | Blocked | Blocked |

NetworkPolicy is cheap and catches the common case; mTLS handles the cases where NetworkPolicy fails.

## Cross-namespace Flow execution: removed

`FlowRun.Spec.FlowRef.Namespace` originally allowed a FlowRun in namespace A to execute a Flow defined in namespace B, fetched via the controller's own ClusterRole with no check that the FlowRun's creator was authorized to access the target namespace — an attacker with write access to one namespace could point a FlowRun at a Flow in a more privileged namespace and have Secrets fetched in that target namespace's context.

**Decision:** removed cross-namespace `FlowRef` entirely — `FlowRef.Namespace` is deleted from the API, and FlowRuns always execute Flows in their own namespace. An admission webhook rejects any FlowRun that still sets it (see `docs/api/trigger.md`'s note on the v1alpha1 restriction). Cross-namespace flows are deferred to v1beta1 with a proper authorization model (e.g. a `FlowGrant` CRD modeled on Gateway API's `ReferenceGrant`).

This also closes the related secret-fetch path: since a Flow's `$(secrets.name.key)` placeholders resolve Secrets from the Flow's own namespace, removing cross-namespace `FlowRef` removes the indirect route to reading another namespace's Secrets through it.

## Secrets RBAC: OwnNamespace default

The controller's ClusterRole (when running in AllNamespaces mode) grants `get/list/watch` on all Secrets cluster-wide — a compromised controller pod would otherwise expose every Secret in the cluster.

**Decision:** default to OwnNamespace (the operator watches and can read Secrets only in its own namespace). In AllNamespaces mode (`WATCH_NAMESPACES=*`), Secrets RBAC is restricted to namespaces labeled `kubezap.io/managed=true`. Combined with the HTTP executor split above, this means even the main controller's Secret access is scoped, and the component that actually makes outbound HTTP calls has no Secret access at all.

## Webhook auth: admission warning, not rejection

A `Trigger` with `type: webhook` and no `spec.webhook.auth` accepts requests from any caller — if the gateway Service is exposed via Ingress or LoadBalancer, that means unauthenticated internet traffic can fire arbitrary Flows.

**Decision:** a `ValidatingWebhookConfiguration` emits an admission **warning** (not a rejection) when a Trigger with `type: webhook` is created or updated without `spec.webhook.auth`. This raises visibility without blocking dev/test workflows that intentionally run without auth.

## Plugin image trust: optional digest pinning

An `Integration` with `type: plugin` causes the operator to create a Deployment from an arbitrary container image with no verification of signature, digest, or registry origin — a compromised plugin image gets a ServiceAccount with `triggers:get/list/watch` and `flowruns:create` in-namespace, network access to the API server, and potential access to Secrets via `spec.plugin.secretRefs`.

**Decision:** `Integration.spec.plugin.imageDigest` (optional) — when set, the operator validates the resolved digest at reconcile time before creating or updating the plugin Deployment. Opt-in; there is no enforcement for Integrations that don't set it. The operator does not verify plugin images by default — this is documented as a security consideration for the plugin model generally.

## CEL expression cost limits

CEL `when:` expressions are not Turing-complete (no unbounded loops), but nested comprehensions grow combinatorially and can still consume significant CPU evaluating a single expression, stalling the FlowRun controller's work queue.

**Decision:** CEL evaluation runs under a cost budget (`cel.CostEstimator` / `cel.CostLimit()`), configurable via `--cel-cost-limit` (default `10000`, `0` disables the limit).

## Webhook TriggerData redaction

Webhook request headers and bodies are stored in `FlowRun.Spec.TriggerData`, which is readable by anyone with `flowruns:get` RBAC in the namespace and persists in etcd for the FlowRun's GC TTL — a fixed redaction list (`Authorization`, `X-Api-Key`) wouldn't catch custom auth headers or credentials embedded in request bodies.

**Decision:** `Trigger.spec.webhook.redactHeaders` (`[]string`, extends the built-in redacted-header list) and `Trigger.spec.webhook.redactBody` (`bool`, replaces the stored body with `[REDACTED]` entirely) let each Trigger declare what it needs redacted. The redaction logic itself lives in `internal/gateway/redact`, shared across the webhook, Kafka, and AMQP gateways rather than duplicated per gateway type.

## Secret access audit trail

Resolving a `$(secrets.name.key)` placeholder was originally silent — no log entry, event, or metric — making "which Flows accessed Secret X" unanswerable without cluster-level Kubernetes audit logs.

**Decision:** every Secret read during FlowRun step execution increments `kubezap_secret_accesses_total{namespace,secret_name}` (`internal/metrics/metrics.go`), giving a Prometheus-queryable audit signal without requiring audit logging to be enabled at the cluster level.

## Webhook rate limiting

A flood of webhook requests could create unbounded FlowRuns, exhausting etcd storage and the controller's work queue — the Trigger `cooldown` field existed for cron triggers but had no equivalent for webhooks.

**Decision:** `Trigger.spec.webhook.rateLimit` (`maxRequests` + `window`, e.g. `100` per `60s`) is enforced by the webhook gateway in-memory, per route, using a sliding window. This is per gateway replica, not cluster-wide.

## Known gaps

- **Plugin-to-controller `/publish` channel is plain HTTP.** `executePublishStep()` calls a plugin's `/publish` endpoint over plain HTTP — in a cluster without network encryption (no service mesh, no encrypting CNI), publish payloads transit in cleartext. No mTLS option exists for this channel today; recommend Istio/Linkerd sidecar injection for sensitive deployments in the meantime.
- **NetworkPolicy is only auto-created for the HTTP executor.** The reconciler creates a `NetworkPolicy` for the executor automatically (see [HTTP Executor Egress NetworkPolicy](executor-egress-networkpolicy.md)), but the webhook-gateway-ingress, plugin-egress, and controller-egress policies under `config/network-policy/` remain manual reference YAMLs — they are not applied automatically for any namespace.
