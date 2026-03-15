# KubeZap

**Declarative workflow automation for Kubernetes.** Define event-driven automations as CRDs — no web UI, no proprietary runtime, no vendor lock-in.

Think Zapier meets Camunda, but Kubernetes-native: trigger on webhooks, Kafka messages, or cron schedules; execute multi-step flows with HTTP calls, data transforms, conditional branching, timed waits, and retry policies — all declared in YAML and version-controlled alongside your infrastructure.

---

## Install

### Prerequisites

- Kubernetes 1.27+ (or k3s / OpenShift 4.12+)
- `kubectl` configured against your cluster
- `kustomize` v5+ (or `kubectl` 1.27+ which bundles it)

### Raw manifests (available now)

```bash
# Install CRDs
kubectl apply -k config/crd

# Deploy the operator
kubectl apply -k config/default
```

The controller starts in `kubezap-system`. Verify it is running:

```bash
kubectl get pods -n kubezap-system
```

### Helm chart _(coming in v0.3)_

```bash
helm install kubezap kubezap/kubezap
```

### OperatorHub / OLM _(coming in v0.3)_

Install via the OpenShift OperatorHub catalog or the community OperatorHub.

### Local development with k3s

k3s uses its own containerd instance, so images built with Docker need to be imported before deploying.

```bash
# 1. Build both images
make docker-build IMG=kubezap/controller:latest
docker build -t kubezap/webhook-gateway:latest -f cmd/webhook-gateway/Dockerfile .

# 2. Import them into k3s containerd
docker save kubezap/controller:latest      | sudo k3s ctr images import -
docker save kubezap/webhook-gateway:latest | sudo k3s ctr images import -

# 3. Verify the images are visible to k3s
sudo k3s ctr images ls | grep kubezap

# 4. Deploy
make deploy IMG=kubezap/controller:latest
```

The controller and webhook gateway manifests use `imagePullPolicy: IfNotPresent`, so k3s will use the locally imported images without attempting a registry pull.

When you rebuild after a code change, repeat steps 1–2 then restart the relevant pod:

```bash
# Rebuild and re-import
make docker-build IMG=kubezap/controller:latest
docker save kubezap/controller:latest | sudo k3s ctr images import -

# Restart the controller pod to pick up the new image
kubectl rollout restart deployment/kubezap-controller-manager -n kubezap-system
```

---

## Quick start

Once the operator is running, define a Trigger and a Flow:

```yaml
# trigger.yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: my-webhook
  namespace: default
spec:
  type: webhook
  enabled: true
  webhook:
    path: /hooks/my-webhook
    method: POST
  flowRef:
    name: my-flow
---
# flow.yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: my-flow
  namespace: default
spec:
  steps:
    - name: call-api
      action:
        type: http
        http:
          url: https://httpbin.org/post
          method: POST
          body: '{"event": "$(trigger.payload.event)"}'
```

```bash
kubectl apply -f trigger.yaml -f flow.yaml

# Send a test event
curl -X POST http://<gateway-ip>:8080/hooks/my-webhook \
  -H 'Content-Type: application/json' \
  -d '{"event": "hello"}'

# Inspect the execution
kubectl get flowruns -n default
kubectl get flowrun <name> -o jsonpath='{.status.phase}'
```

For a full walkthrough see [docs/guides/getting-started.md](docs/guides/getting-started.md).

---

## Documentation

| Document | Description |
|----------|-------------|
| [Overview](docs/overview.md) | Architecture, core concepts, CRD reference |
| [Getting Started](docs/guides/getting-started.md) | Step-by-step guide: webhook → transform → conditional notify |
| [Flow CRD](docs/api/flow.md) | Full step action reference (http, transform, publish, wait) |
| [FlowRun CRD](docs/api/flowrun.md) | Execution model, GC, status fields |
| [Integration CRD](docs/api/integration.md) | Kafka, plugin protocol |
| [Webhook Security](docs/guides/webhook-security.md) | HMAC, bearer, OIDC, API-key, IP allowlist, mTLS |
| [Observability](docs/guides/observability.md) | Prometheus metrics, OpenTelemetry traces, access logs |
| [Demo: Kafka Enrichment](docs/guides/kafka-enrichment.md) | Kafka → enrich → conditional route → publish |
| [Demo: Incident Escalation](docs/guides/incident-escalation.md) | Alert webhook → parallel actions → wait → escalate |

---

## Features

- **Trigger types**: webhook (HTTP), cron, Kafka pub/sub
- **Step actions**: HTTP calls, data transforms (CEL), conditional branching, timed waits, Kafka publish
- **Execution**: dependency-ordered DAG, per-step retry with exponential backoff, step + flow timeouts
- **FlowRun GC**: TTL-based cleanup, `kubezap.io/retain` annotation, max FlowRun cap per Trigger
- **Webhook auth**: HMAC, bearer token, OIDC/JWT, API-key header, IP allowlist, mTLS
- **Observability**: Prometheus metrics, OpenTelemetry traces (OTLP/gRPC), structured JSON access logs
- **Multi-namespace**: `WATCH_NAMESPACES` supports AllNamespaces, MultiNamespace, SingleNamespace, OwnNamespace
- **OLM**: all four install modes supported in the CSV bundle
- **Security**: distroless images, non-root, read-only root FS, restricted SCC compliant

---

## License

Apache 2.0
