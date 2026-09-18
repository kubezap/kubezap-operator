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
  - [Prerequisites](#prerequisites)
  - [Configuration](#configuration)
  - [What the operator does](#what-the-operator-does)
  - [Calling the webhook with a client certificate](#calling-the-webhook-with-a-client-certificate)
  - [Ingress / Route passthrough](#ingress--route-passthrough)
- [API Key Header](#api-key-header)
- [IP Allowlist](#ip-allowlist)
- [Body Size Limits](#body-size-limits)
- [Auth Spec Reference](#auth-spec-reference)
  - [WebhookAuth](#webhookauth)
  - [HMACConfig](#hmacconfig)
  - [BearerConfig](#bearerconfig)
  - [OIDCConfig](#oidcconfig)
  - [WebhookBasicAuth](#webhookbasicauth)
  - [APIKeyConfig](#apikeyconfig)
  - [HeaderEqualsConfig](#headerequalsconfig)
  - [IPAllowlistConfig](#ipallowlistconfig)
- [Limitations](#limitations)

---

## Authentication Methods

| Method            | Best for                                                   | Secret type                  |
| ----------------- | ---------------------------------------------------------- | ---------------------------- |
| HMAC signature    | GitHub, GitLab, Stripe, and most SaaS webhook senders      | Opaque (shared secret)       |
| Bearer token      | Internal services, simple API clients                      | Opaque                       |
| OIDC / OAuth2 JWT | Enterprise SSO, services with identity providers           | OIDC issuer (no secret ref)  |
| Basic auth        | Legacy systems                                             | Opaque (username + password) |
| mTLS              | Service-to-service in zero-trust environments              | TLS Secret (cert + key)      |
| API key header    | Named custom header with a fixed expected value            | Opaque                       |
| IP allowlist      | Network-layer restriction (not auth, but defense in depth) | N/A                          |

Authentication is declared in the `Trigger` spec under `spec.webhook.auth`. When authentication fails, the gateway returns `401 Unauthorized` with no body and does not create a FlowRun.

All auth failures are recorded in `status.lastResult: AuthFailed` and emitted as Prometheus metrics.

> **Note:** Only one authentication type is active at a time — `spec.webhook.auth.type` is a single enum value. To combine IP allowlisting with another auth method, use a NetworkPolicy or Ingress-level allowlist alongside your chosen `type`. Support for layering multiple auth methods is planned for a future release.

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
```

Create the secret:
```bash
kubectl create secret generic github-webhook-secret \
  --from-literal=secret='your-github-webhook-secret' \
  -n automation
```

**How it works**: `hmac.provider` selects the verification scheme (default `github`). The gateway reads the shared HMAC secret from the referenced Secret key, computes the provider's signature over the appropriate signing base, and compares it to the value in the request's signature header using a constant-time comparison (safe against timing attacks).

> **Note:** Only `github` and `slack` providers are implemented today. Per-trigger configuration of an arbitrary signature header name, hash algorithm, encoding, or prefix (for providers like Stripe or Shopify) is planned for a future release.

**Common HMAC providers:**

| Provider | `hmac.provider` | Signature header             | Notes                                                          |
| -------- | --------------- | ----------------------------- | --------------------------------------------------------------- |
| GitHub   | `github` (default) | `X-Hub-Signature-256`       | SHA-256, hex-encoded, `sha256=` prefix, over the raw body       |
| Slack    | `slack`         | `X-Slack-Signature`           | SHA-256, hex-encoded, `v0=` prefix, over `v0:<timestamp>:<body>`, with replay-window enforcement via `X-Slack-Request-Timestamp` |
| Stripe   | *(not yet supported)* | `Stripe-Signature`      | SHA-256, hex-encoded, `v1=` prefix                              |
| Shopify  | *(not yet supported)* | `X-Shopify-Hmac-Sha256` | SHA-256, base64-encoded                                         |

> For GitLab token verification (header equality rather than HMAC), use `type: header-equals` — see [API Key Header](#api-key-header).

### Slack slash commands

Slack signs requests differently from GitHub-style webhooks — the signing base includes a timestamp, and the header/prefix differ. Set `hmac.provider: slack`:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: slack-slash-command
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/slack-slash
    method: POST
    auth:
      type: hmac
      hmac:
        provider: slack
        timestampToleranceSeconds: 300 # default; Slack's own recommendation
        secretRef:
          name: slack-signing-secret
          key: signingSecret
```

The gateway rejects requests whose `X-Slack-Request-Timestamp` is more than `timestampToleranceSeconds` away from the current time, even with a valid signature — this guards against replay of a captured request. See `examples/slack-router/` for a full working example.

---

## Bearer Token

A static bearer token in the `Authorization` header. Simple to configure, suitable for internal service-to-service calls where you control both sides.

```yaml
spec:
  webhook:
    auth:
      type: bearer
      bearer:
        tokenSecretRef:
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
```

The caller sends:
```
Authorization: Bearer <jwt-token>
```

**How it works**: the gateway performs OIDC discovery at `<issuer>/.well-known/openid-configuration` to retrieve the JWKS endpoint, then validates the token signature, `iss` claim, and (when `audience` is set) `aud` claim.

> **Note:** Direct JWKS URI configuration (`jwksUri`), custom required claims (`requiredClaims`), and JWKS cache TTL configuration are planned for a future release. Currently, only `issuer` and `audience` are supported.

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
        usernameKey: username   # key name within the Secret (default: "username")
        passwordKey: password   # key name within the Secret (default: "password")
```

Create the secret with separate username and password keys:
```bash
kubectl create secret generic webhook-basic-auth \
  --from-literal=username='myuser' \
  --from-literal=password='mysecretpassword' \
  -n automation
```

The `secretRef` field is a `LocalObjectReference` (Secret name only). The `usernameKey` and `passwordKey` fields specify which keys within that Secret contain the credentials. Both default to `"username"` and `"password"` respectively, so if your Secret uses those key names, you can omit them:

```yaml
      basic:
        secretRef:
          name: webhook-basic-auth
```

---

## mTLS (Client Certificate)

For zero-trust environments or service meshes where the calling service must authenticate with a client certificate. KubeZap implements mTLS at the transport layer (TLS handshake), not as a per-Trigger auth type.

### Prerequisites

mTLS requires server-side TLS to be enabled first. Both settings are configured via a namespaced **`WebhookGatewayConfig`** object — they apply to the shared webhook gateway Deployment for that namespace.

### Configuration

Create a `WebhookGatewayConfig` object in the namespace referencing the TLS server cert Secret and the CA Secret for client verification:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: WebhookGatewayConfig
metadata:
  name: default
  namespace: my-namespace
spec:
  tls:
    serverSecretRef:
      name: kubezap-webhook-tls    # server cert (tls.crt + tls.key)
    clientCASecretRef:
      name: webhook-client-ca      # client CA (ca.crt)
```

At most one `WebhookGatewayConfig` object may exist per namespace — the object must be named `default`, and Kubernetes' own name-uniqueness rejects a second one. See the [WebhookGatewayConfig CRD reference](../api/webhookgatewayconfig.md) for the full spec/status field reference, the exact rejection error text, and a worked example.

Create the CA secret containing the certificate authority that issued the client certificates:

```bash
kubectl create secret generic webhook-client-ca \
  --from-file=ca.crt=/path/to/client-ca.crt \
  -n my-namespace
```

### What the operator does

When `spec.tls.serverSecretRef` (and, for mTLS, `spec.tls.clientCASecretRef`) are set, the controller:
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

KubeZap supports two auth types for header-based key verification:

**`type: apiKey`** — checks a named header against a secret value. The header name defaults to `X-Api-Key` but is configurable.

```yaml
spec:
  webhook:
    auth:
      type: apiKey
      apiKey:
        secretRef:
          name: my-api-key-secret
          key: apiKey
        header: "X-Api-Key"   # optional, defaults to "X-Api-Key"
```

**`type: header-equals`** — checks any arbitrary header for an exact match. Useful for providers like GitLab that use a non-standard header name.

```yaml
spec:
  webhook:
    auth:
      type: header-equals
      headerEquals:
        header: "X-Gitlab-Token"
        secretRef:
          name: gitlab-webhook-token
          key: token
```

Both types compare the header value to the secret value using a constant-time comparison.

Create the secret:
```bash
kubectl create secret generic my-api-key-secret \
  --from-literal=apiKey='your-secret-api-key-value' \
  -n automation
```

---

## IP Allowlist

Not an authentication mechanism, but a defense-in-depth control. Restrict which source IPs can reach the webhook endpoint. This is best implemented at the network layer (NetworkPolicy, Ingress/Gateway allowlist) rather than inside the gateway, but KubeZap also supports it as a complement to auth:

```yaml
spec:
  webhook:
    auth:
      type: ipAllowlist
      ipAllowlist:
        cidrs:
          - "192.168.1.0/24"
          - "10.0.0.5/32"
          - "203.0.113.0/28"   # GitHub webhook IP range (example)
```

> **Note:** By default, the gateway never trusts `X-Forwarded-For`/`X-Real-IP` — it always uses the TCP peer address, which is the Ingress controller's/load balancer's own IP if you're behind one, not the original client's. This is a deliberate, safe default: if the gateway trusted these headers unconditionally, any caller reaching it directly could set either one to an allowed IP and bypass the allowlist entirely. If you're behind a reverse proxy that forwards the real client IP via `X-Forwarded-For` (or `X-Real-IP`), pass `--trusted-proxy-cidrs` (a comma-separated list of CIDRs) to the webhook gateway with that proxy's pod/service CIDR; the header is then honored only when the immediate connection actually comes from a trusted CIDR, using the right-most `X-Forwarded-For` entry (the address the trusted proxy itself observed — a client can prepend a fake entry, but not overwrite the one the proxy appends). This is gateway-wide, not per-Trigger.

> **Note:** Only one `type` is active per Trigger. To combine IP allowlisting with another auth method (e.g., HMAC + IP restriction), use `type: ipAllowlist` for IP enforcement and apply HMAC verification separately, or enforce IP restrictions at the Ingress/NetworkPolicy layer while using an auth type like `hmac` on the Trigger. Support for layering multiple auth methods is planned for a future release.

---

## Auth Spec Reference

### WebhookAuth

| Field          | Type               | Required    | Description                                                                                                                                                                                                                            |
| -------------- | ------------------ | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `type`         | enum               | **Yes**     | Auth method: `hmac`, `bearer`, `oidc`, `basic`, `apiKey`, `ipAllowlist`, `header-equals`. Omit `auth` entirely for no authentication. mTLS is configured at the transport layer via the namespace's `WebhookGatewayConfig` — see [mTLS (Client Certificate)](#mtls-client-certificate). Only one type is active per Trigger. |
| `hmac`         | HMACConfig         | Conditional | Required when `type: hmac`                                                                                                                                                                                                             |
| `bearer`       | BearerConfig       | Conditional | Required when `type: bearer`                                                                                                                                                                                                           |
| `oidc`         | OIDCConfig         | Conditional | Required when `type: oidc`                                                                                                                                                                                                             |
| `basic`        | WebhookBasicAuth   | Conditional | Required when `type: basic`                                                                                                                                                                                                            |
| `apiKey`       | APIKeyConfig       | Conditional | Required when `type: apiKey`                                                                                                                                                                                                           |
| `ipAllowlist`  | IPAllowlistConfig  | Conditional | Required when `type: ipAllowlist`                                                                                                                                                                                                      |
| `headerEquals` | HeaderEqualsConfig | Conditional | Required when `type: header-equals`                                                                                                                                                                                                    |

### HMACConfig

| Field                       | Type              | Required | Default  | Description                                                              |
| --------------------------- | ----------------- | -------- | -------- | ------------------------------------------------------------------------ |
| `secretRef`                  | SecretKeySelector | **Yes**  | —        | Secret key containing the shared HMAC signing secret                     |
| `provider`                   | enum              | No       | `github` | `github` or `slack` — selects the verification scheme (see table above)  |
| `timestampToleranceSeconds`  | int32             | No       | `300`    | Replay-window tolerance for `provider: slack`; ignored otherwise         |

> **Note:** Per-trigger configuration of an arbitrary signature header name, hash algorithm (`sha1`, `sha256`, `sha512`), encoding (`hex`, `base64`), and signature prefix — for providers beyond `github`/`slack` — is planned for a future release.

### BearerConfig

| Field            | Type              | Required | Description                                       |
| ---------------- | ----------------- | -------- | ------------------------------------------------- |
| `tokenSecretRef` | SecretKeySelector | **Yes**  | Secret key containing the expected bearer token value |

### OIDCConfig

| Field      | Type   | Required | Description                                                                               |
| ---------- | ------ | -------- | ----------------------------------------------------------------------------------------- |
| `issuer`   | string | **Yes**  | OIDC issuer URL. Used for OIDC discovery and `iss` claim validation.                      |
| `audience` | string | No       | Expected `aud` claim value. Recommended — omit only if your provider does not set `aud`.  |

> **Note:** Direct JWKS URI configuration (`jwksUri`), additional required claims (`requiredClaims`), and JWKS cache TTL (`jwksCacheTTL`) are planned for a future release.

### WebhookBasicAuth

| Field         | Type                 | Required | Default      | Description                                                                               |
| ------------- | -------------------- | -------- | ------------ | ----------------------------------------------------------------------------------------- |
| `secretRef`   | LocalObjectReference | **Yes**  | —            | Name of the Secret containing the credentials                                             |
| `usernameKey` | string               | No       | `"username"` | Key within the Secret that holds the username                                             |
| `passwordKey` | string               | No       | `"password"` | Key within the Secret that holds the password                                             |

### APIKeyConfig

| Field       | Type              | Required | Default        | Description                                                |
| ----------- | ----------------- | -------- | -------------- | ---------------------------------------------------------- |
| `secretRef` | SecretKeySelector | **Yes**  | —              | Secret key containing the expected API key value           |
| `header`    | string            | No       | `"X-Api-Key"`  | HTTP header name to check for the API key                  |

### HeaderEqualsConfig

| Field       | Type              | Required | Description                                 |
| ----------- | ----------------- | -------- | ------------------------------------------- |
| `header`    | string            | **Yes**  | HTTP header name to check                   |
| `secretRef` | SecretKeySelector | **Yes**  | Secret key containing the expected header value |

### IPAllowlistConfig

| Field   | Type     | Required | Description                                                                     |
| ------- | -------- | -------- | ------------------------------------------------------------------------------- |
| `cidrs` | []string | **Yes**  | CIDR blocks allowed to call this endpoint (e.g., `["10.0.0.0/8", "1.2.3.4/32"]`) |

---

## Body Size Limits

The webhook gateway applies two independent, differently-sized limits to a request body:

- **Read limit (4MB, not configurable):** the gateway refuses to read more than 4MB from the request body at all, returning `413 Request Entity Too Large`. This exists purely to bound memory use against arbitrarily large uploads and is checked before authentication.
- **Stored/processed limit (64KB by default, configurable via `--max-stored-body-bytes` on the webhook gateway):** independent of, and much smaller than, the read limit — this is how much of the body is actually stored in `FlowRun.spec.triggerData.body` and available to `$(trigger.body...)` interpolation, `trigger.bodyFields` in CEL, and `when` conditions. A body larger than this is silently truncated to the limit and `bodyTruncated: true` is set on the FlowRun.

This truncation applies to the *stored* body, which is the only body every downstream mechanism sees — `$(trigger.body...)` interpolation, CEL's `trigger.bodyFields`, and raw-string `when` conditions (`trigger.body.contains("...")`) all read from the same truncated string. For a JSON (or form-urlencoded) body, truncating mid-structure produces invalid JSON: parsing fails entirely, so `trigger.bodyFields` becomes an empty map and every `$(trigger.body.<field>)` placeholder is left unresolved (literally sent as the placeholder text) rather than just missing one field — **this silently breaks structured field access for the whole payload, not just the part past the cutoff.** The 64KB default comfortably covers most real-world JSON webhook payloads (GitHub push events, Slack interactive payloads, Stripe events), but if your Flow reads any field from the body via interpolation or `bodyFields`, check your actual payload size against the limit — don't assume it's enough for an unusually large integration without verifying. Only raw-string `when` conditions degrade gracefully (they see a truncated but still-valid prefix, with the padding-based bypass risk described above); everything else fails closed to "field not found."

---

## Redacting Sensitive Data from FlowRun TriggerData

By default, KubeZap redacts the following headers before storing them in
`FlowRun.Spec.TriggerData`: `Authorization`, `X-Api-Key`, `X-Webhook-Secret`,
`X-Hub-Signature`, `X-Hub-Signature-256`, `X-Amz-Security-Token`, `Cookie`,
`Set-Cookie`, `X-Auth-Token`, `Proxy-Authorization`.

To redact additional headers or the entire request body, configure
`spec.webhook.redactHeaders` and `spec.webhook.redactBody` on the Trigger:

```yaml
spec:
  webhook:
    redactHeaders:
      - X-Custom-Token
      - X-My-Api-Key
    redactBody: true  # store [REDACTED] instead of the full body
```

The built-in list is always applied — `redactHeaders` extends it, it does not replace it.

When `redactBody: true`, the webhook body is not available via `$(trigger.body.*)` in Flow
step templates. Use this only when the body contains credentials you never want persisted
in etcd.

---

## Limitations

- **Replay attacks**: HMAC verification does not protect against replay attacks by default. For replay protection, require a timestamp header (e.g., `X-Timestamp`) and add a `when` condition on the Flow to reject stale timestamps. Some providers (GitHub, Stripe) include a timestamp in the signature payload.
- **JWT expiry clock skew**: OIDC validation allows a 30-second clock skew by default. This is not currently configurable.
- **Token rotation**: Bearer tokens and API keys do not support rotation without briefly accepting both old and new values. Rotate secrets in Kubernetes and the gateway picks up the new value on the next request.
- **mTLS and shared Ingress**: Inbound mTLS requires TLS passthrough at the Ingress layer. If you are using a shared Ingress that terminates TLS, mTLS to the gateway is not possible — use bearer or HMAC instead.
- **Single auth type per Trigger**: Only one `spec.webhook.auth.type` is active at a time. Combining multiple auth methods (e.g., HMAC + IP allowlist) on a single Trigger is planned for a future release.
