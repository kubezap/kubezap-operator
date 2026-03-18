# Securing Webhook Triggers

By default, webhook trigger endpoints are unauthenticated — any caller that can reach the endpoint can fire the trigger. For production use, always configure one of the authentication mechanisms described in this guide.

Authentication is configured per `Trigger`, so different triggers can use different mechanisms. Authentication configuration references Kubernetes Secrets, not inline credentials.

---

## Contents

- [Authentication Methods](#authentication-methods)
- [HMAC Signature Verification](#hmac-signature-verification)
- [Bearer Token](#bearer-token)
- [OIDC / OAuth2 JWT](#oidc--oauth2-jwt)
- [Basic Auth](#basic-auth)
- [mTLS (Client Certificate)](#mtls-client-certificate)
- [API Key Header](#api-key-header)
- [IP Allowlist](#ip-allowlist)
- [Combining Methods](#combining-methods)
- [Auth Spec Reference](#auth-spec-reference)
- [Limitations](#limitations)

---

## Authentication Methods

| Method | Best for | Secret type |
|---|---|---|
| HMAC signature | GitHub, GitLab, Stripe, and most SaaS webhook senders | Opaque (shared secret) |
| Bearer token | Internal services, simple API clients | Opaque |
| OIDC / OAuth2 JWT | Enterprise SSO, services with identity providers | OIDC config or JWKS URL |
| Basic auth | Legacy systems | Opaque (username + password) |
| mTLS | Service-to-service in zero-trust environments | TLS Secret (cert + key) |
| API key header | Simple integrations, custom header names | Opaque |
| IP allowlist | Network-layer restriction (not auth, but defense in depth) | N/A |

Authentication is declared in the `Trigger` spec under `spec.webhook.auth`. When authentication fails, the gateway returns `401 Unauthorized` with no body and does not create a FlowRun.

All auth failures are recorded in `status.lastResult: AuthFailed` and emitted as Prometheus metrics.

---

## HMAC Signature Verification

Used by GitHub, GitLab, Stripe, Shopify, and most SaaS platforms that support webhook signatures. The sender computes an HMAC over the request body using a shared secret and includes it in a header. The gateway verifies the signature before processing.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: github-push
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/github
    method: POST
    auth:
      type: hmac
      hmac:
        secretRef:
          name: github-webhook-secret
          key: secret
        header: "X-Hub-Signature-256"
        algorithm: sha256               # sha1, sha256 (default), sha512
        prefix: "sha256="               # prefix stripped before comparison
```

Create the secret:
```bash
kubectl create secret generic github-webhook-secret \
  --from-literal=secret='your-github-webhook-secret' \
  -n automation
```

**How it works**: the gateway computes `HMAC-SHA256(body, secret)`, hex-encodes it, prepends the prefix, and compares it to the value in the specified header using a constant-time comparison (safe against timing attacks).

**Common HMAC configurations by provider:**

| Provider | Header | Algorithm | Prefix |
|---|---|---|---|
| GitHub | `X-Hub-Signature-256` | `sha256` | `sha256=` |
| GitLab | `X-Gitlab-Token` | (token equality, not HMAC) | — |
| Stripe | `Stripe-Signature` | `sha256` | `v1=` |
| Shopify | `X-Shopify-Hmac-Sha256` | `sha256` | `""` (base64, not hex) |

> For GitLab token verification (header equality rather than HMAC), use `type: header-equals` — see [API Key Header](#api-key-header).

---

## Bearer Token

A static bearer token in the `Authorization` header. Simple to configure, suitable for internal service-to-service calls where you control both sides.

```yaml
spec:
  webhook:
    auth:
      type: bearer
      bearer:
        secretRef:
          name: my-webhook-token
          key: token
```

Create the secret:
```bash
# Generate a secure random token
TOKEN=$(openssl rand -hex 32)
kubectl create secret generic my-webhook-token \
  --from-literal=token="$TOKEN" \
  -n automation
```

The caller must send:
```
Authorization: Bearer <token>
```

---

## OIDC / OAuth2 JWT

Validates a JWT bearer token issued by an OIDC provider (Keycloak, Okta, Azure AD, Google, Auth0, etc.). The gateway fetches the JWKS from the provider's discovery endpoint and validates the token's signature, expiry, issuer, and audience.

```yaml
spec:
  webhook:
    auth:
      type: oidc
      oidc:
        issuer: "https://keycloak.internal/realms/my-org"
        audience: "kubezap"
        # Optional: require specific claims
        requiredClaims:
          - claim: "roles"
            value: "kubezap-trigger"
```

The caller sends:
```
Authorization: Bearer <jwt-token>
```

**How it works**: the gateway performs OIDC discovery at `<issuer>/.well-known/openid-configuration` to retrieve the JWKS endpoint, then validates the token. JWKS keys are cached and refreshed on a configurable interval.

**For providers that do not expose an OIDC discovery endpoint**, specify the JWKS URL directly:

```yaml
spec:
  webhook:
    auth:
      type: oidc
      oidc:
        jwksUri: "https://auth.internal/.well-known/jwks.json"
        issuer: "https://auth.internal"
        audience: "kubezap"
```

**Azure AD example:**
```yaml
oidc:
  issuer: "https://login.microsoftonline.com/<tenant-id>/v2.0"
  audience: "<your-app-client-id>"
```

**Okta example:**
```yaml
oidc:
  issuer: "https://your-org.okta.com/oauth2/default"
  audience: "api://default"
```

---

## Basic Auth

HTTP Basic authentication (`Authorization: Basic <base64(user:pass)>`). Supported for compatibility with legacy systems. Not recommended for new integrations — use bearer tokens or OIDC instead.

```yaml
spec:
  webhook:
    auth:
      type: basic
      basic:
        secretRef:
          name: webhook-basic-auth
          key: credentials       # value must be "username:password"
```

Or split into separate keys:
```yaml
      basic:
        usernameSecretRef:
          name: webhook-basic-auth
          key: username
        passwordSecretRef:
          name: webhook-basic-auth
          key: password
```

Create the secret:
```bash
kubectl create secret generic webhook-basic-auth \
  --from-literal=credentials='myuser:mysecretpassword' \
  -n automation
```

---

## mTLS (Client Certificate)

For zero-trust environments or service meshes where the calling service must authenticate with a client certificate. KubeZap implements mTLS at the transport layer (TLS handshake), not as a per-Trigger auth type.

### Prerequisites

mTLS requires server-side TLS to be enabled first. Both settings are configured via **Namespace annotations** — they apply to the shared webhook gateway Deployment for that namespace.

### Configuration

Annotate the Namespace with the TLS server cert Secret and the CA Secret for client verification:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-namespace
  annotations:
    kubezap.io/webhook-tls-secret: "kubezap-webhook-tls"       # server cert (tls.crt + tls.key)
    kubezap.io/webhook-mtls-ca-secret: "webhook-client-ca"     # client CA (ca.crt)
```

Create the CA secret containing the certificate authority that issued the client certificates:

```bash
kubectl create secret generic webhook-client-ca \
  --from-file=ca.crt=/path/to/client-ca.crt \
  -n my-namespace
```

### What the operator does

When both annotations are present, the controller:
1. Mounts `kubezap-webhook-tls` as a read-only volume at `/etc/webhook-tls`
2. Mounts `webhook-client-ca` as a read-only volume at `/etc/webhook-mtls-ca`
3. Starts the gateway with `--tls-cert-file`, `--tls-key-file`, and `--mtls-ca-file` flags
4. The gateway sets `tls.Config.ClientAuth = tls.RequireAndVerifyClientCert` with the provided CA pool

All connections without a valid client certificate are rejected at the TLS handshake — the webhook handler never receives the request.

### Calling the webhook with a client certificate

```bash
curl --cert client.crt --key client.key --cacert server-ca.crt \
  https://webhooks.example.com/hooks/my-trigger \
  -H 'Content-Type: application/json' \
  -d '{"event": "test"}'
```

### Ingress / Route passthrough

mTLS requires TLS passthrough at the Ingress/Route layer so client certificates are forwarded intact to the gateway pod. Re-encrypt or edge termination strips client certs.

For Kubernetes Ingress with NGINX:
```yaml
metadata:
  annotations:
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
```

For OpenShift Route:
```yaml
spec:
  tls:
    termination: passthrough
```

---

## API Key Header

Validates that a specific header contains an expected value. Useful for custom API key schemes and providers like GitLab (which uses token equality rather than HMAC).

```yaml
spec:
  webhook:
    auth:
      type: header-equals
      headerEquals:
        header: "X-API-Key"         # or "X-Gitlab-Token", "X-Custom-Auth", etc.
        secretRef:
          name: my-api-key-secret
          key: apiKey
```

The gateway compares the header value to the secret value using a constant-time comparison.

For GitLab webhooks:
```yaml
      headerEquals:
        header: "X-Gitlab-Token"
        secretRef:
          name: gitlab-webhook-token
          key: token
```

---

## IP Allowlist

Not an authentication mechanism, but a defense-in-depth control. Restrict which source IPs can reach the webhook endpoint. This is best implemented at the network layer (NetworkPolicy, Ingress/Gateway allowlist) rather than inside the gateway, but KubeZap also supports it as a complement to auth:

```yaml
spec:
  webhook:
    auth:
      ipAllowlist:
        - "192.168.1.0/24"
        - "10.0.0.5/32"
        - "203.0.113.0/28"   # GitHub webhook IP range (example)
```

IP allowlist can be combined with any other auth type.

> Note: The source IP seen by the gateway is the pod-network IP, which may be the Ingress controller's cluster IP rather than the original client IP if you are using an Ingress. Configure your Ingress to forward `X-Forwarded-For` and set `spec.webhook.auth.trustedProxies` to tell the gateway which proxy IPs to trust.

```yaml
spec:
  webhook:
    auth:
      ipAllowlist:
        - "203.0.113.0/28"
      trustedProxies:
        - "10.0.0.0/8"   # cluster-internal proxy IPs
```

---

## Combining Methods

Multiple auth requirements can be combined. All specified methods must pass.

**HMAC + IP allowlist (belt and suspenders for GitHub webhooks):**
```yaml
spec:
  webhook:
    auth:
      type: hmac
      hmac:
        secretRef:
          name: github-secret
          key: secret
        header: "X-Hub-Signature-256"
        algorithm: sha256
        prefix: "sha256="
      ipAllowlist:
        - "192.30.252.0/22"    # GitHub webhook IP ranges
        - "185.199.108.0/22"
        - "140.82.112.0/20"
        - "143.55.64.0/20"
```

**OIDC + IP allowlist (internal service with identity):**
```yaml
spec:
  webhook:
    auth:
      type: oidc
      oidc:
        issuer: "https://keycloak.internal/realms/platform"
        audience: "kubezap"
      ipAllowlist:
        - "10.0.0.0/8"     # internal network only
```

---

## Auth Spec Reference

### WebhookAuth

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | enum | No | Auth method: `hmac`, `bearer`, `oidc`, `basic`, `apiKey`, `ipAllowlist`. Omit for no authentication. mTLS is configured at the transport layer via Namespace annotations — see [mTLS (Client Certificate)](#mtls-client-certificate). |
| `hmac` | HMACAuth | Conditional | Required when `type: hmac` |
| `bearer` | BearerAuth | Conditional | Required when `type: bearer` |
| `oidc` | OIDCAuth | Conditional | Required when `type: oidc` |
| `basic` | BasicAuth | Conditional | Required when `type: basic` |
| `apiKeySecretRef` | SecretKeySelector | Conditional | Secret key containing the API key value. Used when `type: apiKey` |
| `apiKeyHeader` | string | No | Header name to check (default: `X-Api-Key`). Used when `type: apiKey` |
| `ipAllowlist` | []string | No | CIDR ranges allowed to call this endpoint. Combinable with any `type`. |
| `trustedProxies` | []string | No | CIDR ranges of trusted proxy IPs for X-Forwarded-For header processing |

### HMACAuth

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `secretRef` | SecretKeyRef | **Yes** | — | Secret containing the HMAC signing key |
| `header` | string | **Yes** | — | Request header containing the signature |
| `algorithm` | enum | No | `sha256` | Hash algorithm: `sha1`, `sha256`, `sha512` |
| `prefix` | string | No | `""` | Prefix stripped from the header value before comparison (e.g., `"sha256="`) |
| `encoding` | enum | No | `hex` | Signature encoding: `hex` or `base64` |

### BearerAuth

| Field | Type | Required | Description |
|---|---|---|---|
| `secretRef` | SecretKeyRef | **Yes** | Secret containing the expected bearer token value |

### OIDCAuth

| Field | Type | Required | Description |
|---|---|---|---|
| `issuer` | string | Conditional | OIDC issuer URL. Used for discovery and `iss` claim validation. Required if `jwksUri` is not set. |
| `jwksUri` | string | Conditional | Direct JWKS endpoint URL. Required if `issuer` does not support OIDC discovery. |
| `audience` | string | No | Expected `aud` claim value. Recommended. |
| `requiredClaims` | []ClaimRequirement | No | Additional claims that must be present and match the specified value |
| `jwksCacheTTL` | duration | No | How long to cache JWKS keys (default `1h`) |

### BasicAuth

| Field | Type | Required | Description |
|---|---|---|---|
| `secretRef` | SecretKeyRef | Conditional | Secret containing `username:password` string. Mutually exclusive with username/password refs. |
| `usernameSecretRef` | SecretKeyRef | Conditional | Secret key containing the username |
| `passwordSecretRef` | SecretKeyRef | Conditional | Secret key containing the password |

### HeaderEqualsAuth

| Field | Type | Required | Description |
|---|---|---|---|
| `header` | string | **Yes** | HTTP header name to check |
| `secretRef` | SecretKeyRef | **Yes** | Secret containing the expected header value |

---

## Limitations

- **Replay attacks**: HMAC verification does not protect against replay attacks by default. For replay protection, require a timestamp header (e.g., `X-Timestamp`) and add a `when` condition on the Flow to reject stale timestamps. Some providers (GitHub, Stripe) include a timestamp in the signature payload.
- **JWT expiry clock skew**: OIDC validation allows a 30-second clock skew by default. This is not currently configurable.
- **Token rotation**: Bearer tokens and API keys do not support rotation without briefly accepting both old and new values. Rotate secrets in Kubernetes and the gateway picks up the new value on the next request.
- **mTLS and shared Ingress**: Inbound mTLS requires TLS passthrough at the Ingress layer. If you are using a shared Ingress that terminates TLS, mTLS to the gateway is not possible — use bearer or HMAC instead.
