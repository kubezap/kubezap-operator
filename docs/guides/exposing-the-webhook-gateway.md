# Exposing the Webhook Gateway

The KubeZap webhook gateway runs as a Kubernetes `Deployment` and `Service` inside your cluster. External callers — GitHub, Stripe, CI systems, internal services from other namespaces — need a route to reach it. This guide covers every standard exposure pattern from local development to production.

---

## Contents

- [How webhook URLs are formed](#how-webhook-urls-are-formed)
- [In-cluster access](#in-cluster-access)
- [Development: port-forward](#development-port-forward)
- [Production: LoadBalancer Service](#production-loadbalancer-service)
- [Production: Ingress](#production-ingress)
  - [nginx Ingress](#nginx-ingress)
  - [TLS with cert-manager (Ingress)](#tls-with-cert-manager-ingress)
- [Production: Kubernetes Gateway API (recommended)](#production-kubernetes-gateway-api-recommended)
  - [Basic HTTPRoute](#basic-httproute)
  - [TLS with cert-manager (Gateway API)](#tls-with-cert-manager-gateway-api)
- [Production: OpenShift Route](#production-openshift-route)
  - [Edge TLS termination](#edge-tls-termination)
  - [Re-encrypt (TLS to the gateway pod)](#re-encrypt-tls-to-the-gateway-pod)
  - [Passthrough (mTLS)](#passthrough-mtls)
- [TLS on the gateway itself](#tls-on-the-gateway-itself)
- [Source IP preservation](#source-ip-preservation)
- [Multi-namespace deployments](#multi-namespace-deployments)
- [Limitations](#limitations)

---

## How webhook URLs are formed

The path for a webhook trigger is set in `spec.webhook.path` on the `Trigger` CR. The gateway automatically prepends `/hooks/` if not already present.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: github-push
  namespace: default
spec:
  type: webhook
  webhook:
    path: /github/push       # served at /hooks/github/push
    method: POST
  flowRef:
    name: handle-push
```

The full external URL for the above trigger would be:

```
https://webhooks.example.com/hooks/github/push
```

Paths must be unique within a namespace. If two triggers define the same path, the second registration is rejected and the trigger is set to `Ready=False`.

The gateway service is named `kubezap-webhook-gateway` and is created in the same namespace as the triggers it serves.

```bash
# Service in the same namespace as your Triggers
kubectl get svc kubezap-webhook-gateway -n default
```

---

## In-cluster access

Services in the same namespace can call the gateway directly without any Ingress:

```bash
curl -X POST http://kubezap-webhook-gateway.default.svc.cluster.local:8080/hooks/github/push \
  -H "Content-Type: application/json" \
  -d '{"ref": "refs/heads/main"}'
```

Use the full DNS form (`<service>.<namespace>.svc.cluster.local`) from other namespaces.

---

## Development: port-forward

The fastest way to test webhooks locally:

```bash
kubectl port-forward svc/kubezap-webhook-gateway 8080:8080 -n default
```

Then send requests to `http://localhost:8080/hooks/<path>`.

For external services (GitHub webhooks, Stripe, etc.) that cannot reach `localhost`, use [ngrok](https://ngrok.com/) or [smee.io](https://smee.io/):

```bash
# ngrok — exposes localhost:8080 as a public HTTPS URL
ngrok http 8080
# Use the printed https://<id>.ngrok.io URL as your webhook target
```

---

## Production: LoadBalancer Service

The simplest production option for cloud environments. The operator creates a `ClusterIP` Service; patch it to `LoadBalancer` to get a cloud-provisioned external IP:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kubezap-webhook-gateway
  namespace: default
  annotations:
    # cloud-specific annotations — examples:
    service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
spec:
  type: LoadBalancer
  ports:
    - name: http
      port: 80
      targetPort: 8080
    - name: https
      port: 443
      targetPort: 8080   # TLS termination at the gateway — see "TLS on the gateway itself"
  selector:
    app: kubezap-webhook-gateway
```

> **Note:** The operator owns this Service and will reconcile it back to `ClusterIP` on the next reconcile if you patch the `spec.type` field directly. To make the LoadBalancer type persistent, configure it in the operator's Helm values or Kustomize overlay (field: `webhookGateway.serviceType`). This is tracked as a planned feature — for now, annotating the Service and accepting the reconcile loop is the workaround, or use Ingress/HTTPRoute which are separate objects the operator does not own.

---

## Production: Ingress

### nginx Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubezap-webhooks
  namespace: default
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "4m"   # match gateway body limit
    nginx.ingress.kubernetes.io/proxy-read-timeout: "60"
spec:
  ingressClassName: nginx
  rules:
    - host: webhooks.example.com
      http:
        paths:
          - path: /hooks/
            pathType: Prefix
            backend:
              service:
                name: kubezap-webhook-gateway
                port:
                  number: 8080
```

**For mTLS passthrough** (when the gateway handles TLS and client cert validation itself):

```yaml
annotations:
  nginx.ingress.kubernetes.io/ssl-passthrough: "true"
```

### TLS with cert-manager (Ingress)

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kubezap-webhook-tls
  namespace: default
spec:
  secretName: kubezap-webhook-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - webhooks.example.com
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubezap-webhooks
  namespace: default
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "4m"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - webhooks.example.com
      secretName: kubezap-webhook-tls
  rules:
    - host: webhooks.example.com
      http:
        paths:
          - path: /hooks/
            pathType: Prefix
            backend:
              service:
                name: kubezap-webhook-gateway
                port:
                  number: 8080
```

---

## Production: Kubernetes Gateway API (recommended)

The [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) is GA as of Kubernetes 1.28 and is the recommended long-term replacement for Ingress. It is controller-agnostic — the same `HTTPRoute` manifest works with Envoy Gateway, Istio, nginx, Cilium, Traefik, and HAProxy. It also works on OpenShift 4.10+ via the OpenShift Gateway API support, making it the single pattern that covers both vanilla Kubernetes and OpenShift.

Install the Gateway API CRDs if not already present:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
```

### Basic HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kubezap-webhooks
  namespace: default
spec:
  parentRefs:
    - name: prod-gateway         # name of your Gateway resource
      namespace: infra           # namespace where the Gateway lives
  hostnames:
    - webhooks.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /hooks/
      backendRefs:
        - name: kubezap-webhook-gateway
          port: 8080
```

The `Gateway` resource is cluster infrastructure (usually owned by a platform team). KubeZap only requires an `HTTPRoute` pointing at its Service — no changes to the `Gateway` are needed beyond permitting cross-namespace references if the `Gateway` is in a different namespace.

**Cross-namespace reference** — if your `Gateway` is in a different namespace, add a `ReferenceGrant`:

```yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-kubezap-webhooks
  namespace: default          # namespace of the Service (kubezap-webhook-gateway)
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: infra        # namespace where the HTTPRoute lives
  to:
    - group: ""
      kind: Service
      name: kubezap-webhook-gateway
```

### TLS with cert-manager (Gateway API)

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kubezap-webhook-tls
  namespace: infra             # same namespace as the Gateway
spec:
  secretName: kubezap-webhook-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - webhooks.example.com
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: prod-gateway
  namespace: infra
spec:
  gatewayClassName: eg          # Envoy Gateway example; use your controller's class name
  listeners:
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - name: kubezap-webhook-tls
      allowedRoutes:
        namespaces:
          from: All
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kubezap-webhooks
  namespace: default
spec:
  parentRefs:
    - name: prod-gateway
      namespace: infra
      sectionName: https
  hostnames:
    - webhooks.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /hooks/
      backendRefs:
        - name: kubezap-webhook-gateway
          port: 8080
```

---

## Production: OpenShift Route

OpenShift Routes are supported on all OpenShift 4.x clusters. For new deployments on OpenShift 4.10+, the Gateway API approach above is also available and provides portability if you later migrate clusters.

### Edge TLS termination

TLS is terminated at the OpenShift router; traffic between the router and the gateway pod is plain HTTP. This is the simplest option.

```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: kubezap-webhooks
  namespace: default
spec:
  host: webhooks.apps.cluster.example.com
  path: /hooks/
  to:
    kind: Service
    name: kubezap-webhook-gateway
    weight: 100
  port:
    targetPort: http             # port name on the Service
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
```

### Re-encrypt (TLS to the gateway pod)

Use this when the gateway is configured with its own TLS certificate (`--tls-cert-file` / `--tls-key-file`). The router terminates the external TLS connection and opens a new TLS connection to the gateway.

```yaml
spec:
  tls:
    termination: reencrypt
    destinationCACertificate: |
      -----BEGIN CERTIFICATE-----
      <CA cert that signed the gateway's TLS cert>
      -----END CERTIFICATE-----
```

### Passthrough (mTLS)

Use this when the gateway handles client certificate validation itself (mTLS — see [Securing Webhook Triggers](webhook-security.md#mtls-client-certificate)). The router passes the TLS stream through unchanged; the gateway terminates TLS.

```yaml
spec:
  tls:
    termination: passthrough
  # path must be empty for passthrough — the router cannot inspect the HTTP path
  # Remove the "path: /hooks/" field; all traffic on the hostname goes to the gateway
```

> **Important:** OpenShift passthrough routes cannot match on HTTP path (the router has no visibility into the encrypted stream). Configure a dedicated hostname for mTLS webhook traffic and omit `spec.path`.

---

## TLS on the gateway itself

By default the webhook gateway serves plain HTTP on port 8080. To enable HTTPS on the gateway pod (required for mTLS or re-encrypt termination modes):

1. Create a TLS Secret:

   ```bash
   # Using cert-manager or your own CA, create a TLS secret named kubezap-webhook-tls
   ```

2. Set the TLS flags on the gateway Deployment (managed by the operator — configure via Helm values or Kustomize patch):

   ```yaml
   # Kustomize patch on the webhook-gateway Deployment
   - op: add
     path: /spec/template/spec/containers/0/args/-
     value: "--tls-cert-file=/tls/tls.crt"
   - op: add
     path: /spec/template/spec/containers/0/args/-
     value: "--tls-key-file=/tls/tls.key"
   ```

   When TLS flags are set, the gateway upgrades its listener to HTTPS. The Service port name changes from `http` to `https`. Update your Ingress, HTTPRoute, or Route `targetPort` accordingly.

3. For mTLS (client certificate validation), also set `--mtls-ca-file` pointing at the CA bundle that signed allowed client certificates, and configure `spec.webhook.auth.type: mtls` on the Trigger.

See [Securing Webhook Triggers](webhook-security.md) for the full auth configuration reference.

---

## Source IP preservation

Many webhook senders sign requests with HMAC and may require the real source IP for IP allowlist validation. By default, Kubernetes and ingress controllers replace the source IP with the node's IP.

**Preserve real source IP via `X-Forwarded-For`:**

Most ingress controllers and load balancers append the original client IP to `X-Forwarded-For`. The webhook gateway reads this header for structured access logs and IP allowlist checks. Ensure your ingress controller is configured to pass it through (nginx does this by default; AWS ALB sets `X-Forwarded-For` automatically).

**PROXY protocol (nginx Ingress):**

For L4 load balancers (NLB) that support PROXY protocol:

```yaml
# On the nginx IngressClass or ConfigMap:
use-proxy-protocol: "true"
```

**ExternalTrafficPolicy: Local (LoadBalancer Service):**

If using a `LoadBalancer` Service directly, set `externalTrafficPolicy: Local` to preserve the source IP at the node level. This avoids SNAT but requires the load balancer to route to nodes that have a gateway pod running.

---

## Multi-namespace deployments

The operator creates one `kubezap-webhook-gateway` Service and Deployment **per namespace** in which webhook Triggers exist. Each namespace has its own independent gateway instance.

If you want a single external hostname to serve triggers from multiple namespaces, create one Ingress/HTTPRoute per namespace pointing at that namespace's gateway Service:

```yaml
# Namespace: team-a
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kubezap-webhooks-team-a
  namespace: team-a
spec:
  parentRefs:
    - name: prod-gateway
      namespace: infra
  hostnames:
    - webhooks.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /hooks/
      backendRefs:
        - name: kubezap-webhook-gateway
          namespace: team-a
          port: 8080
```

Trigger path uniqueness is enforced within a namespace. Triggers in `team-a` and `team-b` may both define `/hooks/deploy` without conflict — they reach different gateway instances.

---

## Limitations

- **`serviceType` not yet configurable via operator flags** — the operator always creates the gateway Service as `ClusterIP`. Use an Ingress, HTTPRoute, or a separate `LoadBalancer` Service (patched manually or via a Kustomize overlay) for external access.
- **Single port per gateway** — the gateway listens on one port (HTTP or HTTPS). It is not possible to serve both HTTP and HTTPS simultaneously on different ports from the same gateway instance.
- **OpenShift passthrough requires a dedicated hostname** — path-based routing is not possible in TLS passthrough mode; see the [Passthrough](#passthrough-mtls) section above.
- **Source IP in Prometheus labels** — source IP is intentionally NOT used as a Prometheus label (cardinality). It appears in structured JSON access logs and in `/24`-bucketed `source_range` on the `kubezap_webhook_ip_blocked_total` metric only. See [Observability](observability.md).
