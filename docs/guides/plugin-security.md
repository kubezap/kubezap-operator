# Plugin Security

This guide covers the security posture of KubeZap plugins and the channel between
the controller and plugin pods. It is a companion to the [webhook security guide](webhook-security.md).

## Overview

KubeZap plugins extend the operator with custom subscriber and publisher behaviour.
A plugin is a pod managed by the KubeZap controller; the controller creates a
`Deployment` for it based on the `Integration` CRD of type `plugin`.

**Publisher channel**: When a Flow step has `type: publish`, the controller resolves
all credentials and calls `POST /publish` on the plugin pod's publisher port (default
`8090`, overridable via `spec.plugin.publisherPort`). This is a plain HTTP call over
the pod network — there is no transport-layer encryption by default.

**Credential injection**: Secrets referenced in `spec.plugin.secretRefs` are injected
into the plugin pod as environment variables via `envVarMappings`. The credentials
are never written to etcd in plaintext — they are resolved from Secret objects at
pod startup.

## Threat Model

| Threat | Impact | Default Mitigations |
|--------|--------|---------------------|
| Compromised plugin pod reads its own env vars | Credential exposure for that integration | Least-privilege SA; short-lived secrets where supported by the provider |
| Network-adjacent pod calls `POST /publish` directly | Arbitrary publish injection | NetworkPolicy restricting ingress to the publisher port |
| Plugin pod makes lateral movement calls to cluster-internal services | Privilege escalation from plugin's SA | Least-privilege SA; NetworkPolicy egress restriction |
| Plugin image tag mutated between operator reconcile cycles | Unvetted code execution | Image digest pinning via `spec.plugin.imageDigest` |
| Controller-to-plugin channel intercepted on the pod network | Credential or payload interception | mTLS via service mesh (recommended for sensitive deployments) |

## Mitigations

### Service Mesh (mTLS)

The highest-value control for the controller-to-plugin channel is a service mesh
with automatic mTLS (Istio or Linkerd). With a sidecar injected into both the
controller pod and each plugin pod, all pod-to-pod traffic is mutually authenticated
and encrypted without any application changes.

**Istio**: label the operator namespace for automatic sidecar injection:

```bash
kubectl label namespace kubezap-system istio-injection=enabled
```

**Linkerd**: annotate the namespace or individual Deployments:

```bash
kubectl annotate namespace kubezap-system linkerd.io/inject=enabled
```

When a service mesh is present, the NetworkPolicy rules below act as an additional
defence-in-depth layer rather than the primary control.

### NetworkPolicy

Apply `config/network-policy/plugin-egress.yaml` to restrict plugin pod traffic:

```bash
kubectl apply -f config/network-policy/plugin-egress.yaml
```

The policy does two things:

1. **Restricts ingress to `publisherPort`**: only the controller pod (matched by
   label `app.kubernetes.io/component: controller`) can call `POST /publish`. This
   prevents any other pod in the namespace from injecting publish requests.

2. **Restricts egress to the external service**: the placeholder CIDR `0.0.0.0/0`
   in the policy must be replaced with the actual CIDR(s) of the broker or API the
   plugin integrates with. This limits lateral movement if the plugin pod is
   compromised.

Before applying, open the file and update the `TODO` items for your environment:

```bash
# Example: Kafka plugin on-prem at 10.100.0.0/24, port 9093
egress:
  - to:
      - ipBlock:
          cidr: 10.100.0.0/24
    ports:
      - protocol: TCP
        port: 9093
```

See the comments in `config/network-policy/plugin-egress.yaml` for per-protocol
examples (Kafka, RabbitMQ, NATS, Slack).

### Image Digest Pinning

By default, the operator deploys plugin images using the tag specified in
`spec.plugin.image`. If the upstream registry mutates the tag, the next pod restart
will pull unvetted code.

Pin the plugin to a known-good image digest using `spec.plugin.imageDigest`:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Integration
metadata:
  name: my-slack-plugin
spec:
  type: plugin
  plugin:
    image: ghcr.io/my-org/kubezap-slack-plugin:v1.2.3
    imageDigest: abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678  # pin to a verified digest (64 hex chars, no "sha256:" prefix)
    publisherPort: 8090
```

When `imageDigest` is set, the controller uses `image@digest` as the container
image reference, making tag mutation irrelevant.

### Least-Privilege RBAC

The operator creates a `ServiceAccount` and a namespace-scoped `Role` for each
plugin. The role grants only the permissions required for the plugin's contract:
watching `Trigger` CRDs and creating `FlowRun` CRDs.

Audit the permissions granted to a plugin's service account:

```bash
kubectl get role -n kubezap-system -l app.kubernetes.io/component=kubezap-plugin
kubectl describe role <plugin-role-name> -n kubezap-system
```

Do not grant cluster-scoped roles to plugin service accounts. If a plugin requests
`ClusterRole` permissions, treat that as a red flag.

## Plugin Image Trust

The KubeZap operator does not verify plugin image signatures or digests before
deploying a plugin. The operator installs whatever image is specified in the
`Integration` CRD. This is a known limitation.

**Recommendations**:

- Only deploy plugin images from registries you control or have audited.
- Enable image admission policies (OPA/Gatekeeper, Kyverno, or the cluster's
  built-in admission webhook) to enforce digest pinning or registry allow-lists.
- Pin images using `spec.plugin.imageDigest` as described above.
- Treat third-party community plugins as untrusted code — review their source
  before deploying.

## Controller-Side mTLS (Roadmap)

Plain HTTP between the controller and the plugin's `/publish` endpoint is a known
limitation. Controller-side mTLS for the publisher channel is not implemented yet.
Until it ships, use a service mesh as described above.

## Related Resources

- `config/network-policy/plugin-egress.yaml` — ready-to-apply NetworkPolicy
- `config/network-policy/controller-egress.yaml` — controller egress policy
- `docs/guides/webhook-security.md` — webhook Trigger authentication options
- `docs/guides/observability.md` — structured logging and tracing for plugin calls
