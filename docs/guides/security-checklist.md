# Security Checklist for Production Deployments

This page is a runnable checklist for operators preparing to deploy KubeZap in a production environment. Work through each item before going live. Each item links to the relevant guide for configuration details.

> **Minimum for production**
>
> The following four items are the baseline. Do not skip them.
>
> - [ ] [Webhook authentication](#1-webhook-authentication) — unauthenticated endpoints will fire on any request that reaches them
> - [ ] [SSRF protection](#2-ssrf-protection) — verify the HTTP executor blocklist covers your private network ranges
> - [ ] [RBAC scope](#3-rbac-ownnamespace-default) — confirm the operator watches only the namespaces it should
> - [ ] [NetworkPolicy](#4-networkpolicy) — restrict pod-to-pod traffic before exposing gateway endpoints

---

## Checklist

### 1. Webhook authentication

- [ ] **All webhook Triggers must have `spec.webhook.auth` configured.** By default, webhook endpoints accept any request that reaches them. Set an auth type (`hmac`, `bearer`, `oidc`, `basic`, `header-equals`, `ipAllowlist`) on every Trigger before exposing the endpoint outside the cluster.

- [ ] **If using `ipAllowlist`, set `--trusted-proxy-cidrs` on the webhook gateway if — and only if — it sits behind a reverse proxy or load balancer.** By default, the gateway never trusts `X-Forwarded-For`/`X-Real-IP` (a caller reaching it directly could otherwise set either header to any value and bypass the allowlist entirely). If a proxy is in front of the gateway and forwards the real client IP via one of these headers, pass `--trusted-proxy-cidrs` with that proxy's pod/service CIDR so its header is honored; otherwise every request will appear to come from the proxy's own IP and the allowlist will reject everything.

- [ ] **Put a rate limit in front of every externally-reachable webhook Trigger.** `Trigger.spec.webhook.cooldown` is opt-in per-Trigger and only bounds request volume once a request has already reached the gateway (each one still costs a TCP/TLS handshake and an auth check). For real volumetric protection, add rate limiting upstream of the cluster — a WAF rate-based rule, an ingress controller's rate-limit annotations, or API Gateway throttling — so a flood is absorbed before it reaches KubeZap at all. Configure `cooldown` too; the two are complementary, not substitutes for each other.

  Guide: [docs/guides/webhook-security.md](webhook-security.md)

### 2. SSRF protection

- [ ] **Verify the HTTP executor SSRF blocklist covers your private ranges.** The HTTP executor blocks RFC-1918 and link-local CIDRs by default (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `::1/128`, `fc00::/7`). If your environment uses additional private ranges (e.g., a non-standard corporate network), add them with `--http-step-blocked-cidrs` on the controller.

- [ ] **Understand that the software blocklist alone has a DNS-rebinding gap, and confirm the NetworkPolicy defense-in-depth is active (see [§4](#4-networkpolicy)).** The blocklist validates a hostname's resolved address, then the HTTP client resolves the same hostname again to actually connect — an attacker who controls the target's authoritative DNS (realistic whenever a Flow interpolates external/attacker-influenced data into a step URL) can return a safe address for the first lookup and an internal one for the second, bypassing the blocklist. The operator auto-creates a `NetworkPolicy` restricting the executor's egress to the same ranges, which DNS tricks cannot bypass — but only on a `NetworkPolicy`-enforcing CNI (Calico, Cilium, most managed-Kubernetes defaults; **not** plain Flannel). If your CNI doesn't enforce it, add the equivalent restriction via cloud security groups/NACLs on the node subnet instead.

  Guide: [docs/dev/http-executor.md](../dev/http-executor.md)

### 3. RBAC: OwnNamespace default

- [ ] **Confirm the operator's namespace scope is correct.** The default install uses `OwnNamespace` mode — the operator watches only the namespace it is deployed in. This is the recommended setting. For multi-namespace deployments, label each target namespace `kubezap.io/managed=true` and set `WATCH_NAMESPACES=*`. In OwnNamespace and SingleNamespace modes, the operator uses a `Role` (not a `ClusterRole`), which is required for OpenShift restricted SCC compliance.

  Guide: [docs/architecture.md](../architecture.md) — see "Namespace Isolation"

### 4. NetworkPolicy

- [ ] **Deploy the bundled NetworkPolicies before exposing gateway endpoints.** Apply the policies from `config/network-policy/` to restrict ingress to the webhook gateway, limit egress from plugin pods to only their target services, and isolate the controller-to-executor channel.

  ```bash
  kubectl apply -f config/network-policy/
  ```

  Review `config/network-policy/plugin-egress.yaml` and update the placeholder egress CIDRs to match your broker or API endpoints before applying.

  The HTTP executor's `NetworkPolicy` (ingress restricted to the controller pod; egress blocking RFC1918/link-local/CGNAT ranges — see [§2](#2-ssrf-protection)) is auto-created by the operator in every managed namespace, so `config/network-policy/http-executor-ingress.yaml` does not need to be applied manually for that one — it's kept as a reference for auditing or for namespaces the operator doesn't manage. This protection requires a `NetworkPolicy`-enforcing CNI; verify yours enforces it (`kubectl get networkpolicy -A` existing is not proof of enforcement — check your CNI's docs, e.g. Calico/Cilium enforce it, plain Flannel does not).

  Guide: [docs/guides/plugin-security.md](plugin-security.md)

### 5. mTLS for the HTTP executor channel

- [ ] **Enable `--executor-mtls=true` if the cluster does not have a service mesh.** By default, the controller-to-executor RPC channel (`POST /execute`) is plain HTTP over the pod network. On clusters without Istio or Linkerd providing automatic mTLS, pass `--executor-mtls=true` to the controller to enable mutual TLS on this channel.

  Guide: [docs/dev/http-executor.md](../dev/http-executor.md)

### 6. Plugin image digest pinning

- [ ] **Pin plugin images to a verified digest via `spec.plugin.imageDigest`.** If the upstream registry mutates the tag, the next pod restart will pull unvetted code. Setting `imageDigest` causes the controller to deploy `image@digest` instead of the bare tag, making tag mutation irrelevant.

  ```yaml
  spec:
    plugin:
      image: ghcr.io/my-org/kubezap-my-plugin:v1.2.3
      imageDigest: sha256:abcdef1234567890...
  ```

  API reference: [docs/api/integration.md](../api/integration.md)

### 7. CEL cost limits

- [ ] **Review the CEL cost limit for your workload.** The default `--cel-cost-limit=10000` on the controller prevents runaway `when` expressions (such as nested comprehensions on large arrays) from stalling the reconciler. Expressions that exceed the budget are treated as evaluation errors and the step is skipped with reason `EvalError`. Tune this value if you have complex conditions operating on large payloads; lower it further for stricter DoS protection.

  API reference: [docs/api/flow.md](../api/flow.md) — see "CEL cost limits"

### 8. Secret access auditing

- [ ] **Alert on unexpected spikes in `kubezap_secret_accesses_total`.** The controller increments this Prometheus counter for every secret read during FlowRun execution, labeled by `namespace`, `secret`, and `flow`. An unexpected spike may indicate a misconfigured Flow reading secrets at high frequency, or a Flow injected via a compromised webhook Trigger. Set an alert threshold appropriate for your expected FlowRun rate.

  Example PromQL alert:
  ```promql
  increase(kubezap_secret_accesses_total[5m]) > 100
  ```

  Guide: [docs/guides/observability.md](observability.md)

### 9. Redacting sensitive data from FlowRun TriggerData

- [ ] **Use `spec.webhook.redactBody` and `spec.webhook.redactHeaders` to prevent secrets from being persisted in FlowRun objects.** FlowRun objects are stored in etcd and accessible to any principal with `get` on `FlowRun`. If the webhook body or headers contain credentials, configure redaction on the Trigger before deploying.

  ```yaml
  spec:
    webhook:
      redactBody: true        # stores [REDACTED] instead of the full body
      redactHeaders:
        - X-Custom-Token
        - X-My-Api-Key
  ```

  Note: the built-in list (`Authorization`, `X-Api-Key`, `X-Webhook-Secret`, `X-Hub-Signature`, etc.) is always applied — `redactHeaders` extends it.

  API reference: [docs/guides/webhook-security.md](webhook-security.md) — see "Redacting Sensitive Data from FlowRun TriggerData"

### 10. Service mesh for plugin communication

- [ ] **Enable sidecar injection for sensitive plugin deployments.** The plugin `/publish` endpoint is plain HTTP by default. For deployments where credentials or message payloads are sensitive, inject a service mesh sidecar (Istio or Linkerd) into the operator namespace to add automatic mTLS between the controller and plugin pods.

  ```bash
  # Istio
  kubectl label namespace kubezap-system istio-injection=enabled

  # Linkerd
  kubectl annotate namespace kubezap-system linkerd.io/inject=enabled
  ```

  Guide: [docs/guides/plugin-security.md](plugin-security.md) — see "Service Mesh (mTLS)"

---

## Quick-reference table

| # | Item | Minimum? | Default state |
|---|------|----------|---------------|
| 1 | Webhook authentication | Yes | Off — endpoints are unauthenticated |
| 2 | SSRF blocklist verification | Yes | On (RFC-1918 blocked), but custom ranges require manual flag |
| 3 | RBAC scope | Yes | OwnNamespace (safe default) |
| 4 | NetworkPolicy | Yes | Not applied — must be deployed manually |
| 5 | Executor mTLS | No | Off — required only without a service mesh |
| 6 | Image digest pinning | No | Off — tag-based by default |
| 7 | CEL cost limit | No | 10,000 units (tunable) |
| 8 | Secret access alerting | No | Metric emitted; alert not configured |
| 9 | FlowRun body/header redaction | No | Common auth headers auto-redacted; body is not |
| 10 | Service mesh for plugins | No | Off — recommended for sensitive deployments |
