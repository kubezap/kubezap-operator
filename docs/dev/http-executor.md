# HTTP Executor — Design Contract

> **Status:** Implemented — all six §17 P0 chunks complete as of 2026-03-27. This document reflects the shipped design.

## Overview

The `kubezap/http-executor` is a dedicated sidecar-style binary that makes outbound HTTP calls on behalf of the KubeZap controller. The controller (which holds broad RBAC including secrets read) resolves all secret references in-memory and sends fully-substituted requests to the executor. The executor has **no RBAC, no secrets access, no cluster management** — it only makes outbound HTTP calls with SSRF protection.

This separation limits the blast radius: if SSRF protection is bypassed via DNS rebinding, the attacker gains execution in a pod with minimal RBAC rather than in the controller pod.

### Deployment topology

One `http-executor` Deployment per namespace where KubeZap manages Flows. The controller reconciler creates and manages these Deployments.

```
┌──────────────────────────────────────────────────────────┐
│  Namespace: kubezap-system  (or any managed namespace)   │
│                                                          │
│  ┌────────────────────┐    POST /execute   ┌──────────┐  │
│  │   controller pod   │ ──────────────────▶│ executor │  │
│  │  (holds RBAC,      │                    │   pod    │  │
│  │   resolves secrets)│◀──────────────────  │          │  │
│  └────────────────────┘    JSON response   └──────────┘  │
│                                                          │
│  NetworkPolicy: executor accepts ingress only from       │
│  controller pod (matched by label selector)              │
└──────────────────────────────────────────────────────────┘
```

---

## API

### `POST /execute`

Accepts a fully-resolved HTTP request, executes it against the target, and returns the result.

**Controller resolves before sending:**
- All `$(secrets.*)` references substituted in URL, headers, and body
- Integration auth headers merged
- Base URL applied from Integration if `type: secretUrl`

**The executor receives no Kubernetes credentials and makes no Kubernetes API calls.**

#### Request schema

```json
{
  "method":         "POST",
  "url":            "https://api.example.com/webhook",
  "headers": {
    "Content-Type": "application/json",
    "Authorization": "Bearer <resolved-token>"
  },
  "body":           "{\"key\": \"value\"}",
  "timeoutSeconds": 30,
  "tlsSkipVerify":  false
}
```

| Field | Type | Required | Default | Notes |
|-------|------|----------|---------|-------|
| `method` | string | yes | — | One of: `GET`, `POST`, `PUT`, `PATCH`, `DELETE` |
| `url` | string | yes | — | Fully-resolved URL with secrets substituted. Executor re-validates SSRF. |
| `headers` | map[string]string | no | `{}` | Fully-resolved header values. |
| `body` | string | no | `""` | Raw body string (may be JSON, form data, etc.). |
| `timeoutSeconds` | int | no | `30` | Per-request timeout. Capped at 300 by executor. |
| `tlsSkipVerify` | bool | no | `false` | Skip TLS verification. Only honoured when executor is started with `--allow-tls-skip-verify`. Off by default. |

#### Response schema

HTTP `200 OK` always (transport errors included in body). The response status code reflects the executor's own processing, not the upstream response.

```json
{
  "statusCode": 200,
  "headers": {
    "Content-Type": "application/json"
  },
  "body":      "{\"id\": 42}",
  "truncated": false,
  "error":     ""
}
```

| Field | Type | Notes |
|-------|------|-------|
| `statusCode` | int | HTTP status code from the upstream target. 0 when `error` is set and no response was received. |
| `headers` | map[string]string | Response headers from upstream. Values are the last value for each header name. |
| `body` | string | Response body, UTF-8. Truncated to `bodyLimitBytes` (default 4096 bytes). See `truncated`. |
| `truncated` | bool | `true` when the upstream response body exceeded `bodyLimitBytes` and was truncated. |
| `error` | string | Non-empty when the request could not be completed (SSRF block, DNS error, timeout, TLS error, etc.). When set, `statusCode` is 0 unless a partial response was received. |

#### Error codes in the `error` field

The error string begins with a machine-readable prefix:

| Prefix | Meaning |
|--------|---------|
| `ssrf_blocked:` | Target URL rejected by SSRF blocklist (IP in blocked CIDR or `.svc.cluster.local`). |
| `dns_error:` | DNS resolution failed for the target hostname. |
| `timeout:` | Request timed out (context deadline exceeded). |
| `tls_error:` | TLS handshake or certificate verification failed. |
| `upstream_error:` | Connection to upstream refused, reset, or otherwise failed at the transport layer. |
| `invalid_request:` | Malformed executor request (bad method, missing URL, etc.). |

**HTTP status from the executor itself** (not upstream):
- `200` — always, with the result embedded in the JSON body
- `400` — malformed executor request (before attempting the outbound call)
- `500` — internal executor error (not an upstream error)

---

### `GET /healthz`

Returns `200 OK` with body `ok`. Used as Kubernetes `readinessProbe` and `livenessProbe`.

---

## SSRF protection

The executor runs an independent SSRF check before every outbound call. This is a defence-in-depth measure: the controller also checks SSRF before sending, but the executor re-validates to guard against DNS rebinding between the controller's check and the executor's outbound connection.

**Default blocked ranges** (same as controller's `defaultSSRFBlockedCIDRs`):
- `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (RFC1918 private)
- `127.0.0.0/8` (loopback)
- `169.254.0.0/16` (link-local — includes AWS/GCP/Azure metadata IPs)
- `0.0.0.0/8`
- `::1/128`, `fe80::/10`, `fc00::/7` (IPv6)
- `100.64.0.0/10` (CGNAT / RFC6598)
- Hostnames ending in `.svc.cluster.local` (blocked by name, before DNS resolution)

**Additional CIDRs** configurable via `--blocked-cidrs` flag (comma-separated, additive to defaults).

---

## Channel security

### NetworkPolicy (mandatory)

The controller reconciler creates a `NetworkPolicy` in each managed namespace that restricts ingress to the executor pod:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kubezap-executor-ingress
  namespace: <managed-namespace>
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: kubezap-http-executor
      app.kubernetes.io/component: http-executor
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: kubezap
              app.kubernetes.io/component: controller
      ports:
        - protocol: TCP
          port: 8091
  policyTypes:
    - Ingress
```

This policy ensures only the controller pod can reach the executor's port 8091.

**Note:** NetworkPolicy enforcement requires a CNI plugin that supports it (Calico, Cilium, Weave Net, etc.). On clusters without a NetworkPolicy-capable CNI the policy is silently ignored. Operators on such clusters should use mTLS (see below).

### mTLS (opt-in)

Enabled via `--executor-mtls=true` on the controller. When enabled:

1. The controller generates a self-signed CA at startup (in-memory, not persisted to etcd).
2. It issues a server certificate for the executor (signed by that CA) and a client certificate for itself.
3. The server cert + CA cert are mounted into the executor Deployment as a projected `Secret` (created/rotated by the controller reconciler).
4. The executor's HTTP server requires client certificate authentication.
5. The controller's HTTP client presents its client cert and verifies the server cert against the CA.

**Certificate rotation:** The controller regenerates certs on startup. Cert lifetime is 24 hours; the controller rotates the executor Deployment's cert Secret and rolls the executor Deployment every 23 hours. `ExecutorReconciler` handles Secret rotation: `cmd/main.go` starts a goroutine that calls `MTLSBundle.NeedsRotation()` (which returns `true` when expiry is less than 1 hour away, i.e. at the 23h mark) and regenerates the bundle. It then updates both `ExecutorReconciler.MTLSBundle` and `FlowRunReconciler.ExecutorTLSConfig` before the next reconcile writes the fresh Secret.

**DNS SANs:** Server certificates include both `kubezap-http-executor.<ns>.svc.cluster.local` and `kubezap-http-executor.<ns>.svc` as Subject Alternative Names. This covers both the fully-qualified in-cluster DNS name and the short-form service DNS name. `cmd/main.go` passes the correct names to `controller.GenerateMTLSBundle(dnsSANs)` at startup using the operator's own namespace (from the `POD_NAMESPACE` env var or the `--namespace` flag).

**Use case:** Clusters without a NetworkPolicy-capable CNI (e.g., vanilla kubeadm with Flannel) or environments where a service mesh is not available. Clusters with Istio/Linkerd can use their mTLS instead; set `--executor-mtls=false` (default).

---

## Executor binary flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--port` | int | `8091` | Port to listen on. |
| `--blocked-cidrs` | string | `""` | Comma-separated extra CIDRs to block (additive to defaults). |
| `--body-limit-bytes` | int | `4096` | Maximum upstream response body size to return. Bodies larger than this are truncated; `truncated: true` is set in the response. |
| `--mtls` | bool | `false` | Enable mTLS. Requires `--tls-cert-file` and `--tls-ca-file`. |
| `--tls-cert-file` | string | `""` | Path to PEM-encoded server certificate (required when `--mtls=true`). |
| `--tls-key-file` | string | `""` | Path to PEM-encoded server private key (required when `--mtls=true`). |
| `--tls-ca-file` | string | `""` | Path to PEM-encoded CA certificate for client cert verification (required when `--mtls=true`). |
| `--allow-tls-skip-verify` | bool | `false` | Allow callers to request TLS verification skip via `tlsSkipVerify: true`. When `false`, `tlsSkipVerify` in the request is ignored and verification is always enforced. |

---

## Controller-side RPC client

The controller's FlowRun reconciler builds an executor URL from the in-cluster Service it manages:

```
http://kubezap-http-executor.<namespace>.svc.cluster.local:8091/execute
```

(Note: this `.svc.cluster.local` URL is reached by the controller pod itself, not from an HTTP step URL. The SSRF check applies to the *target* of the HTTP step, not to the executor's own address. The controller communicates directly to the executor over the cluster network.)

The controller sends a `POST /execute` with a `30s` timeout (configurable via `--executor-rpc-timeout`). Retries: up to 3 attempts with exponential backoff (500ms, 1s, 2s) for transport errors (5xx, connection refused). No retry on `ssrf_blocked` or `invalid_request` errors.

---

## RBAC

The executor pod requires **no RBAC**. It makes outbound HTTP calls only. No `ServiceAccount` token is mounted (`automountServiceAccountToken: false`).

The controller's existing `ServiceAccount` is used for managing executor `Deployments`, `Services`, `NetworkPolicies`, and (when mTLS is enabled) `Secrets`. RBAC markers to add in chunk [3/6]:

```go
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;update;patch;delete,namespace=true
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete,namespace=true
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete,namespace=true
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete,namespace=true
```

---

## Go package layout

```
cmd/http-executor/
  main.go              # binary entry point: flags, server setup, signal handling

internal/executor/
  http/
    server.go          # HTTP server wiring (chi or net/http ServeMux)
    handler.go         # POST /execute handler
    ssrf.go            # SSRF check (ported from internal/controller/ssrf.go; shared logic extracted to internal/ssrf/ in a later pass)
    types.go           # ExecuteRequest / ExecuteResponse Go structs
    handler_test.go    # Ginkgo unit tests
```

> **Note on SSRF code duplication:** Until the executor is fully wired and the controller's inline HTTP step execution is removed (chunk [4/6]), the SSRF logic will exist in both `internal/controller/ssrf.go` and `internal/executor/http/ssrf.go`. This is intentional — do not attempt to merge them prematurely. After chunk [4/6] lands, the controller copy can be deleted or reduced to a thin wrapper.

---

## Sequence: FlowRun HTTP step execution (after chunk [4/6])

```
FlowRunReconciler.Reconcile()
  └─ executeHTTPStep()
       ├─ substituteVarsWithSecrets()   // secrets resolved in-memory
       ├─ applyHTTPIntegration()        // auth headers merged
       ├─ checkSSRF()                   // pre-send SSRF check (defence-in-depth)
       ├─ POST http://kubezap-http-executor.<ns>.svc:8091/execute
       │    (ExecuteRequest with fully-resolved URL, headers, body)
       └─ map ExecuteResponse → StepRunStatus
```

---

## Open questions

None — all design decisions resolved. See `docs/tech-debt/pending-input-required.md` §Security Design Review 2026-03-24 Q5.
