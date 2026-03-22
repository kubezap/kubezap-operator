# KubeZap Overview

KubeZap is an enterprise-grade Kubernetes operator that provides declarative workflow automation. It lets you connect events to actions using standard Kubernetes custom resources — no web UI, no proprietary runtime, no vendor lock-in.

Events flow into Triggers, Triggers fire Flows, Flows execute ordered Steps — each step can call APIs, transform data, evaluate conditions with CEL expressions, or publish to a message broker. Everything runs inside your cluster and is configured through YAML.

---

## Contents

- [What KubeZap Does](#what-kubezap-does)
- [Core Concepts](#core-concepts)
- [Architecture](#architecture)
- [CRD Overview](#crd-overview)
- [Trigger Types](#trigger-types)
- [Credential Management](#credential-management)
- [TLS and mTLS](#tls-and-mtls)
- [Exposing Webhook Triggers](#exposing-webhook-triggers)
- [Payload Formats](#payload-formats)
- [Quick Example](#quick-example)
- [Design Goals](#design-goals)
- [Installation](#installation)
- [Compatibility](#compatibility)
- [Roadmap](#roadmap)

---

## What KubeZap Does

KubeZap lets you define automation workflows declaratively. A workflow starts from an **event** (an incoming webhook, a Kafka message, a cron schedule) and executes a **flow** — a series of steps that can call HTTP APIs, transform data, apply conditional logic, and chain outputs from one step to the next.

Example use cases:

- Receive a webhook from GitHub and trigger a deployment pipeline
- Consume a Kafka message and fan out to multiple downstream services
- Run a nightly cron job that calls a reporting API and posts results to Slack
- React to Kubernetes resource events and call an external ITSM system

All of this is configured through Kubernetes custom resources, meaning it is version-controlled, auditable, and compatible with GitOps workflows.

---

## Core Concepts

### Trigger

A `Trigger` defines the event source that starts a workflow. It specifies what to listen for and which `Flow` to execute when the event fires.

Supported trigger types: **webhook**, **cron**, **kafka**, **amqp**, **nats**, **resource** (alpha); additional brokers via the `Integration` plugin model — see [Integration CRD](api/integration.md).

### Flow

A `Flow` defines the sequence of steps to execute when a trigger fires. Steps can pass data to each other, evaluate conditions, transform payloads, and call external services. Flows support conditional branching, retry policies, and timeout controls.

### Step

Steps are the individual units of work within a Flow. Each step declares an action (such as an HTTP call), optional conditions for execution, retry behavior, and the outputs it produces for downstream steps.

### Integration

An `Integration` stores connection details and credentials for an external system — a Kafka cluster, a REST API, a database — separately from the flows that use it. This keeps sensitive configuration reusable and out of individual Flow specs.

---

## Architecture

```
  Event Sources            KubeZap Operator
  ─────────────            ──────────────────────────────────────────────
  Webhook (HTTP) ────────► Trigger Controller
  Kafka (Pub/Sub) ───────►   │  watches Trigger CRDs
  Cron (Schedule) ───────►   │
                             ▼
                         Flow Engine
                           │  reads Flow CRDs
                           │  executes steps in dependency order
                           │  evaluates CEL conditions
                           │  passes results between steps
                           ▼
                       Step Executor
                       │            │            │            │
                       ▼            ▼            ▼            ▼
                   HTTP Call    Transform    K8s Job      Plugin
                                            (planned)    (planned)

  ──────────────────────────────────────────────────────────────────
  Observability: Prometheus Metrics  +  OpenTelemetry Traces
```

KubeZap runs as a controller plus purpose-built gateway pods per broker type:

- **`kubezap-controller`** — the Kubernetes operator. Reconciles all CRDs, creates and manages gateway Deployments, and executes Flows when a `FlowRun` CRD is created.
- **`kubezap-webhook-gateway`** — a lightweight HTTP server. The controller creates one Deployment per namespace where webhook Triggers exist. It watches Trigger CRDs directly and registers/deregisters routes dynamically without restarts.
- **`kubezap-kafka-gateway`** — a Kafka consumer. The controller creates one Deployment per Kafka cluster (Integration) per namespace. It subscribes to all topics referenced by Triggers in that namespace.
- **`kubezap-amqp-gateway`** — an AMQP consumer. Supports AMQP 0-9-1 (RabbitMQ) and AMQP 1.0 (ActiveMQ Artemis). One Deployment per AMQP broker (Integration) per namespace.
- **`kubezap-nats-gateway`** — a NATS consumer. Supports NATS Core and JetStream durable consumers. One Deployment per NATS cluster (Integration) per namespace.

Gateways communicate trigger events to the controller by creating `FlowRun` CRDs. The controller watches FlowRuns and executes the referenced Flow. This decoupling means gateways scale independently from the controller, and every execution is a Kubernetes resource you can inspect.

The operator also embeds a **read-only web dashboard** (enable with `--enable-ui`, access via `kubectl port-forward`). It provides live FlowRun execution views, step timelines, trigger and flow lists, with SSE-based live updates.

See [Architecture](architecture.md) for the full design including scaling, namespace isolation, and how to add new trigger types.

---

## CRD Overview

| CRD           | API Group                        | Scope      | Status    |
| ------------- | -------------------------------- | ---------- | --------- |
| `Trigger`     | `automation.kubezap.io/v1alpha1` | Namespaced | Available |
| `Flow`        | `automation.kubezap.io/v1alpha1` | Namespaced | Available |
| `FlowRun`     | `automation.kubezap.io/v1alpha1` | Namespaced | Available |
| `Integration` | `automation.kubezap.io/v1alpha1` | Namespaced | Available |
| `Step`        | `automation.kubezap.io/v1alpha1` | Namespaced | Planned   |

All CRDs are namespaced by default. Cluster-scoped variants are planned for multi-tenant deployments.

`FlowRun` is an execution instance created automatically each time a trigger fires. It persists in etcd with full trigger metadata, step results, and timing — every execution is a Kubernetes resource you can inspect with `kubectl`. See [FlowRun CRD](api/flowrun.md).

`Integration` stores connection details and credentials for external systems (Kafka clusters, message brokers, community plugins) and is referenced by Triggers (subscriber) and Flow steps (publisher). See [Integration CRD](api/integration.md).

> **Note:** The `MockEndpoint` CRD has been removed. See [Mocking HTTP Endpoints](guides/mocking-http-endpoints.md) for the recommended Mockoon-based approach to mock HTTP servers in development and testing.

---

## Trigger Types

### Webhook

KubeZap exposes an HTTP endpoint inside the cluster. External systems (or an Ingress) send requests to this endpoint to fire the trigger. The request payload is made available to the Flow as input data.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: github-webhook
spec:
  type: webhook
  webhook:
    path: /hooks/github
    method: POST
  flowRef:
    name: process-github-event
```

### Cron

Triggers fire on a schedule using standard cron syntax. The trigger passes timing metadata to the Flow.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: nightly-report
spec:
  type: cron
  cron:
    schedule: "0 2 * * *"
  flowRef:
    name: generate-nightly-report
```

### Kafka

KubeZap subscribes to a Kafka topic and fires the trigger for each message consumed. The message payload and metadata (topic, partition, offset, headers) are passed to the Flow.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-events
spec:
  type: kafka
  kafka:
    integrationRef:
      name: kafka-cluster
    topic: orders.created
    consumerGroup: kubezap-order-processor
  flowRef:
    name: process-order
```

### AMQP

Subscribes to an AMQP queue or exchange. Supports AMQP 0-9-1 (RabbitMQ) and AMQP 1.0 (ActiveMQ Artemis, Azure Service Bus, IBM MQ).

```yaml
spec:
  type: amqp
  amqp:
    integrationRef:
      name: rabbitmq-cluster
    queue: orders.created
  flowRef:
    name: process-order
```

### NATS

Subscribes to a NATS subject. Supports NATS Core and JetStream durable consumers.

```yaml
spec:
  type: nats
  nats:
    integrationRef:
      name: nats-cluster
    subject: orders.created
    durableName: kubezap-order-processor
  flowRef:
    name: process-order
```

### Kubernetes Resource Events _(alpha)_

Watches Kubernetes resource events via dynamic informers and fires the trigger when a matching resource is created, updated, or deleted. Useful for ITSM-style automation (e.g., Pod failure → open ticket).

```yaml
spec:
  type: resource
  resource:
    apiVersion: v1
    kind: Pod
    watchEvents: [Modified]
    watchFields:
      - field: status.phase
        value: Failed
  flowRef:
    name: pod-failure-ticket
```

> **Alpha stability.** See [known limitations](../docs/tech-debt/) for the resource trigger — naive pluralization for irregular kinds is handled via discovery API fallback.

### Rate Limiting

All trigger types support a cooldown policy to prevent trigger storms:

```yaml
spec:
  cooldown:
    maxInvocations: 10
    window: "60s"
```

---

## Credential Management

KubeZap supports several approaches for providing credentials and connection details to flows and integrations.

### Kubernetes Secrets (recommended)

Reference any key from a Kubernetes Secret using `$(secrets.<secret-name>.<key>)` interpolation. Secrets are resolved at step execution time and are never stored in Flow specs or status.

```yaml
headers:
  Authorization: "Bearer $(secrets.my-api-credentials.token)"
```

Populate secrets with tools like [External Secrets Operator](https://external-secrets.io) (AWS Secrets Manager, Vault, GCP Secret Manager) or any standard Kubernetes workflow.

### ConfigMaps

Reference non-sensitive configuration from a ConfigMap using `$(configmaps.<configmap-name>.<key>)`:

```yaml
url: "$(configmaps.service-endpoints.orders-api-url)/orders/$(params.orderId)"
```

### Environment Variables

Reference operator environment variables using `$(env.<VAR_NAME>)`. These are set on the KubeZap deployment and are useful for base URLs or cluster-wide configuration:

```yaml
url: "$(env.ORDERS_API_BASE_URL)/orders/$(params.orderId)"
```

### Hardcoded Values (development only)

Credentials can be hardcoded directly in specs for local development and testing. This is **not recommended for production** as values are stored in plain text in the CRD spec.

```yaml
# For development/testing only
headers:
  X-Api-Key: "dev-key-do-not-use-in-prod"
```

Flow steps reference credentials using the same `$(secrets.name.key)` and `$(configmaps.name.key)` syntax. See [Flow CRD → Data and Expressions](api/flow.md#data-and-expressions) for the full interpolation reference.

---

## TLS and mTLS

### Custom Certificate Authorities

When calling services that use a private or self-signed CA, annotate the resource with the name of a Kubernetes Secret containing the CA bundle (`ca.crt`):

```yaml
metadata:
  annotations:
    kubezap.io/tls-ca-secret: "my-internal-ca"
```

The referenced Secret must contain a `ca.crt` key with a PEM-encoded certificate bundle. The operator uses this CA when making outbound HTTPS connections on behalf of that resource.

### Mutual TLS (mTLS)

For outbound connections requiring client certificate authentication, annotate with both the CA and a client certificate secret:

```yaml
metadata:
  annotations:
    kubezap.io/tls-ca-secret: "my-internal-ca"
    kubezap.io/tls-client-cert-secret: "my-client-cert"
```

The client certificate secret must contain `tls.crt` and `tls.key` keys (standard Kubernetes TLS secret format). Use [cert-manager](https://cert-manager.io) to issue and rotate client certificates.

### Server-Side TLS for the Webhook Gateway

The webhook gateway serves plain HTTP by default. To enable HTTPS (required for mTLS, re-encrypt routes, and end-to-end encryption), annotate the **Namespace** with the name of a TLS Secret:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-namespace
  annotations:
    kubezap.io/webhook-tls-secret: "kubezap-webhook-tls"
```

The Secret must contain `tls.crt` and `tls.key` in standard Kubernetes TLS Secret format, compatible with [cert-manager](https://cert-manager.io) `Certificate` resources:

```bash
# Using cert-manager (recommended)
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kubezap-webhook-tls
  namespace: my-namespace
spec:
  secretName: kubezap-webhook-tls
  dnsNames: [webhooks.example.com]
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
EOF
```

When the annotation is present, the controller:
- Mounts the Secret as a read-only volume at `/etc/webhook-tls` in the gateway pod
- Starts the gateway with `--tls-cert-file=/etc/webhook-tls/tls.crt --tls-key-file=/etc/webhook-tls/tls.key`
- Switches liveness/readiness probes to HTTPS scheme
- Renames the Service port from `http` to `https`

> **Annotation is read on every reconcile.** Adding or removing the annotation will update the gateway Deployment on the next Trigger reconcile.

### Inbound mTLS (Mutual TLS)

For zero-trust environments where webhook callers must present a client certificate, enable mTLS by adding a second annotation to the **Namespace**:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-namespace
  annotations:
    kubezap.io/webhook-tls-secret: "kubezap-webhook-tls"       # required: server TLS first
    kubezap.io/webhook-mtls-ca-secret: "webhook-client-ca"     # enables mTLS
```

The CA Secret must contain `ca.crt` with the PEM-encoded CA certificate used to issue client certificates. The gateway sets `tls.Config.ClientAuth = RequireAndVerifyClientCert` — all connections without a valid client cert are rejected at the TLS handshake.

```bash
# Create the CA cert secret (example: self-signed CA)
kubectl create secret generic webhook-client-ca \
  --from-file=ca.crt=/path/to/ca.crt \
  -n my-namespace
```

> **mTLS requires server TLS.** The `kubezap.io/webhook-mtls-ca-secret` annotation is silently ignored if `kubezap.io/webhook-tls-secret` is not set.

> **Ingress passthrough.** If you front the webhook gateway with an Ingress or OpenShift Route, use TLS passthrough mode so client certificates reach the gateway pod. Re-encrypt termination at the Ingress proxy will strip client certs.

### TLS for Development (skip verification)

```yaml
metadata:
  annotations:
    kubezap.io/tls-insecure-skip-verify: "true"
```

> `tls-insecure-skip-verify` is intended for local development only. It disables certificate validation entirely and **must not** be used in production.

---

## Exposing Webhook Triggers

The KubeZap operator runs a webhook HTTP server as a Kubernetes `Service` (`kubezap-webhook-gateway`, one per namespace). In-cluster services can call it directly. For external access, front it with an Ingress, Gateway API HTTPRoute, or OpenShift Route.

See **[Exposing the Webhook Gateway](guides/exposing-the-webhook-gateway.md)** for the full guide covering LoadBalancer, nginx Ingress, Gateway API, OpenShift Route, TLS, cert-manager integration, mTLS passthrough, source IP preservation, and multi-namespace deployments.

Quick-reference examples below:

### Kubernetes Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
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
  tls:
    - hosts: [webhooks.example.com]
      secretName: kubezap-webhook-tls
```

### Kubernetes Gateway API (recommended for new deployments)

The [Gateway API](https://gateway-api.sigs.k8s.io/) is the successor to Ingress and is GA as of Kubernetes 1.28. It provides more expressive routing and better multi-tenancy support.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
  parentRefs:
    - name: my-gateway
      namespace: gateway-system
  hostnames: ["webhooks.example.com"]
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /hooks/
      backendRefs:
        - name: kubezap-webhook-gateway
          port: 8080
```

### OpenShift Route

```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: kubezap-webhooks
  namespace: kubezap-system
spec:
  host: webhooks.apps.cluster.example.com
  path: /hooks/
  to:
    kind: Service
    name: kubezap-webhook-gateway
  port:
    targetPort: 8080
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
```

For re-encrypt TLS (TLS all the way to the operator pod) or passthrough (mTLS), change `termination` to `reencrypt` or `passthrough` respectively.

---

## Payload Formats

The trigger body is available in flow steps and CEL conditions via `$(trigger.body)` (raw) and `$(trigger.body.<field>)` (top-level JSON field).

| Expression                    | Description                           |
| ----------------------------- | ------------------------------------- |
| `$(trigger.body)`             | The full raw request body (string)    |
| `$(trigger.body.<field>)`     | A top-level JSON field from the body  |
| `$(trigger.headers.<header>)` | A request header value (webhook only) |

`$(trigger.body.<field>)` supports **full dot-path traversal** for nested JSON — e.g., `$(trigger.body.order.customer.email)` and array index access `$(trigger.body.items.0.sku)`. Missing paths return an empty string.

For HTTP step responses, `resultMappings` support **JSONPath** (e.g., `$.user.id`) for JSON and **XPath** (e.g., `/response/user/id`) for XML — the syntax is auto-detected from the expression prefix.

See [Flow CRD → Payload Formats](api/flow.md#payload-formats) for the full reference.

---

## Quick Example

The following example receives a webhook, fetches additional data from an API, and posts a notification to Slack — all declared as Kubernetes resources.

**Trigger** — listen for incoming webhooks on `/hooks/orders`:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: order-received
  namespace: automation
spec:
  type: webhook
  webhook:
    path: /hooks/orders
    method: POST
  flowRef:
    name: handle-order
```

**Flow** — enrich the order data and notify:

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Flow
metadata:
  name: handle-order
  namespace: automation
spec:
  description: "Enrich order payload and send Slack notification"
  timeout: "2m"
  params:
    - name: orderId
      required: true
  steps:
    - name: enrich-order
      description: "Fetch full order details from order service"
      action:
        type: http
        http:
          url: "https://orders.internal/api/orders/$(params.orderId)"
          method: GET
          headers:
            Authorization: "Bearer $(secrets.order-api-creds.token)"
          resultMappings:
            id: "$.id"
            customerName: "$.customerName"
      results:
        - name: id
        - name: customerName

    - name: notify-slack
      description: "Post notification to Slack"
      runAfter: [enrich-order]
      when:
        - expression: 'steps.enrich_order.status == "Succeeded"'
      action:
        type: http
        http:
          url: "https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
          method: POST
          body: |
            {"text": "New order received: $(steps.enrich_order.results.id) for $(steps.enrich_order.results.customerName)"}
```

---

## Design Goals

**Declarative and Kubernetes-native**
All configuration is expressed as custom resources. KubeZap integrates naturally with GitOps tooling (Flux, ArgoCD) and standard Kubernetes RBAC.

**Idempotent and safe**
Reconcilers are designed to be re-run safely. Flows track execution state in status subresources. Duplicate trigger firings are handled gracefully via cooldown policies.

**Extensible by design**
The plugin model is based on external webhook calls, making it possible to add integrations in any language or runtime. A marketplace of community integrations is planned.

**Observable from day one**
Every trigger firing, flow execution, and step result is recorded in CRD status and emitted as Prometheus metrics and OpenTelemetry traces. No black-box execution.

**Enterprise-ready**
- mTLS support via cert-manager
- Namespace-scoped RBAC with least-privilege defaults
- Multi-replica deployment with leader election
- OpenShift compatible (restricted SCC compliant)
- OLM / OperatorHub installable
- Multi-tenant: configurable `WATCH_NAMESPACES` supports AllNamespaces, MultiNamespace, SingleNamespace, and OwnNamespace OLM install modes

---

## Installation

### Raw manifests (available now)

```bash
kubectl apply -k config/crd     # install CRDs
kubectl apply -k config/default # deploy the operator
```

### Helm (recommended)

```bash
# AllNamespaces mode (default)
helm install kubezap ./charts/kubezap --namespace kubezap-system --create-namespace

# SingleNamespace mode
helm install kubezap ./charts/kubezap \
  --namespace tenant-a --create-namespace \
  --set watchNamespaces=tenant-a

# Multi-namespace mode
helm install kubezap ./charts/kubezap \
  --namespace kubezap-system --create-namespace \
  --set watchNamespaces="tenant-a,tenant-b"
```

See `charts/kubezap/values.yaml` for all configurable options.

### kubezap CLI

The `kubezap` CLI provides rich FlowRun history and operator status views beyond what `kubectl get` offers.

### GitHub Container Registry (GHCR) image defaults

KubeZap now publishes container images under `ghcr.io/kubezap/*`. The Helm chart and operator defaults are configured to use these values by default. If you are using private GHCR repositories, authenticate first:

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u <user> --password-stdin
```

If you need to override image paths in Helm:

```bash
helm install kubezap ./charts/kubezap --set image.repository=ghcr.io/kubezap/controller --set gatewayImages.webhook=ghcr.io/kubezap/webhook-gateway:latest
```

**Direct download (linux/darwin/windows)**

Download the latest release from [GitHub Releases](https://github.com/kubezap/kubezap-operator/releases) and place the binary in your `$PATH`:

```bash
# Linux amd64
curl -Lo kubezap https://github.com/kubezap/kubezap-operator/releases/latest/download/kubezap_linux_amd64.tar.gz \
  | tar -xz kubezap && chmod +x kubezap && mv kubezap /usr/local/bin/

# macOS (arm64)
curl -Lo kubezap.tar.gz https://github.com/kubezap/kubezap-operator/releases/latest/download/kubezap_darwin_arm64.tar.gz \
  && tar -xz -f kubezap.tar.gz kubezap && chmod +x kubezap && mv kubezap /usr/local/bin/
```

**kubectl plugin installation**

`kubectl-kubezap` is shipped alongside `kubezap` in each release. Rename or symlink it to invoke as `kubectl kubezap`:

```bash
mv kubectl-kubezap /usr/local/bin/kubectl-kubezap
kubectl kubezap version
```

**Build from source**

```bash
make build-cli   # produces bin/kubezap
```

### OperatorHub / OLM _(submission in progress)_

Install via the OpenShift OperatorHub catalog or the community OperatorHub. The OLM bundle is validated (`operator-sdk bundle validate`) and passes the OLM scorecard suite. Community-operators PR in progress.

For a full setup walkthrough including namespace configuration and RBAC see [Getting Started](../examples/order-router/).

---

## Compatibility

| Platform                       | Status                                                        |
| ------------------------------ | ------------------------------------------------------------- |
| Kubernetes 1.27+               | Supported                                                     |
| Kubernetes 1.28+ (Gateway API) | Supported                                                     |
| OpenShift 4.12+                | Supported (tested on OpenShift 4.12+; OLM bundle in progress) |
| k3s                            | Tested (local development)                                    |
| EKS / GKE / AKS                | Compatible (no cloud-specific dependencies)                   |

---

## Roadmap

### v0.1 — MVP ✅

- [x] `Trigger` CRD — webhook, cron, Kafka pub/sub sources
- [x] Webhook HTTP server with dynamic route registration
- [x] Cron scheduler with FlowRun creation
- [x] Kafka gateway with consumer group management
- [x] `Flow` CRD — DAG steps, HTTP actions, CEL conditions, data passing
- [x] `FlowRun` CRD — execution history, GC, status conditions
- [x] `Integration` CRD — Kafka (built-in), plugin protocol
- [x] `MockEndpoint` CRD — removed; replaced by Mockoon (see [mocking guide](guides/mocking-http-endpoints.md))
- [x] Webhook auth — HMAC, bearer, OIDC/JWT, API-key, IP allowlist, mTLS
- [x] Observability — Prometheus metrics, OpenTelemetry traces, structured access logs
- [x] Multi-namespace — `WATCH_NAMESPACES`, all four OLM install modes

### v0.2 — Flow Engine ✅

- [x] CEL `when` expression evaluation
- [x] Skipped step phase with downstream cascade
- [x] Step input/output data passing (`$(steps.<name>.results.<key>)`)
- [x] Data transformation step type (`type: transform`)
- [x] Retry policies with exponential/linear/fixed backoff
- [x] Flow-level and per-step timeout enforcement
- [x] `type: publish` step — routes to Kafka/plugin `/publish` endpoint
- [x] `type: wait` step — blocking pause with restart-safe `ResumeAfter` in status

### v0.3 — Distribution & Observability ✅

- [x] Helm chart
- [x] Multi-platform CLI binaries via Goreleaser
- [x] Additional message brokers (AMQP, NATS)
- [x] OLM bundle validated (`operator-sdk bundle validate` + scorecard pass)
- [x] Metrics port normalization (`:9090` HTTP default across all components)
- [x] `kubezap watch` CLI — live FlowRun execution timeline in terminal
- [x] `type: http` Integration — centralized credentials for HTTP steps
- [x] Web dashboard (read-only, embedded in operator binary, `--enable-ui` flag)
- [ ] OperatorHub community-operators PR — submission in progress

### Future

- [ ] `Step` CRD for reusable step definitions
- [ ] Plugin marketplace and integration catalog
- [ ] OpenLineage support
- [ ] Multi-region HA support

---

## Contributing

See [docs/contributing.md](contributing.md) for development setup, code conventions, and the contribution workflow.
