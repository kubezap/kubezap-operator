# OIDC-Secured Webhook

This example demonstrates a KubeZap webhook trigger protected by OIDC/JWT authentication,
using [Dex](https://dexidp.io) as an in-cluster identity provider. No external IdP required
— the entire example is self-contained.

---

## What you'll build

```
curl -H "Authorization: Bearer <jwt>" POST /hooks/oidc-secured
        |
        v (webhook gateway validates JWT: signature, audience, requiredClaims)
  +---------------------------+
  | handle-authenticated-     |  HTTP POST to Mockoon /authenticated-request
  | request                   |
  +---------------------------+
```

Key properties:
- JWT validated against Dex JWKS endpoint (auto-refreshed every 15 minutes)
- `audience: kubezap-demo` enforced
- `requiredClaims: {role: api-consumer}` enforced — tokens without this claim are rejected
- Requests without a valid Bearer token receive `401 Unauthorized`

---

## Prerequisites

- Kubernetes cluster with KubeZap installed
- `kubectl` and `curl`
- Optional: `jwt-cli` or `jq` for decoding JWT payloads

---

## Setup

Apply all manifests:

```bash
kubectl apply -k examples/oidc-webhook/
```

Wait for Dex and Mockoon to become ready:

```bash
kubectl rollout status deploy/dex -n default
kubectl rollout status deploy/mockoon -n default
```

---

## Obtaining a JWT from Dex

Port-forward the Dex service:

```bash
kubectl port-forward svc/dex 5556:5556 -n default
```

Request a token using the resource owner password grant:

```bash
TOKEN=$(curl -s -X POST http://localhost:5556/dex/token \
  -d "grant_type=password" \
  -d "username=admin@example.com" \
  -d "password=password" \
  -d "client_id=kubezap-demo" \
  -d "client_secret=kubezap-demo-secret" \
  -d "scope=openid profile email" | jq -r .access_token)

echo $TOKEN
```

Verify the token contains the expected claims:

```bash
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq .
# Expected: { "sub": "...", "email": "admin@example.com", "role": "api-consumer", ... }
```

> **Note:** The Dex static password config in this example sets `role: api-consumer` via
> the connectorData. If the claim is not present, update the Dex ConfigMap `config.yaml`
> to include it in the token response, or use a Dex OAuth2 client with the claim in the
> token template. For a self-contained demo, any valid JWT from the configured issuer
> satisfying audience + requiredClaims will be accepted.

---

## Calling the webhook

Port-forward the KubeZap webhook gateway:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Send an authenticated request:

```bash
curl -X POST http://localhost:8080/hooks/oidc-secured \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from oidc"}'
# Expected: HTTP 202 Accepted
```

---

## Watching execution

```bash
kubectl get flowruns -n default -w
```

Once a FlowRun appears, inspect it:

```bash
kubectl describe flowrun -n default -l trigger-name=oidc-secured-hook | head -60
```

The `handle-authenticated-request` step should show phase `Succeeded`.

---

## Testing rejection

**No token:**
```bash
curl -X POST http://localhost:8080/hooks/oidc-secured \
  -H "Content-Type: application/json" \
  -d '{"message":"no auth"}'
# Expected: HTTP 401 Unauthorized
```

**Invalid token:**
```bash
curl -X POST http://localhost:8080/hooks/oidc-secured \
  -H "Authorization: Bearer invalid.jwt.token" \
  -H "Content-Type: application/json" \
  -d '{"message":"bad token"}'
# Expected: HTTP 401 Unauthorized
```

---

## Inspecting Mockoon captured requests

```bash
kubectl exec -n default \
  $(kubectl get pod -n default -l app=mockoon -o jsonpath='{.items[0].metadata.name}') \
  -- wget -q -O - http://localhost:3000/mockoon-admin/logs | jq .
```

---

## Cleaning up

```bash
kubectl delete -k examples/oidc-webhook/
```

---

## Production notes

- Replace Dex with your existing OIDC provider (Keycloak, Okta, Auth0, Azure AD). Update
  the `issuer` field in the Trigger's `spec.webhook.auth.oidc` block.
- Use HTTPS for the issuer URL in production. Dex can be deployed with TLS via cert-manager.
- The `requiredClaims` field is optional but recommended — it ensures only tokens with
  the expected `role` value can trigger flows. Use it for coarse-grained authorization.
- For fine-grained routing based on claim values, combine OIDC auth with `type: transform`
  steps and CEL `when` conditions on step outputs.
- JWKS keys are cached and refreshed at `jwksRefreshInterval` (default 15m). Decrease this
  if your provider rotates keys frequently.
