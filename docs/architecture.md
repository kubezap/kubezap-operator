# KubeZap Architecture

This document describes the runtime architecture of KubeZap — specifically how the operator, gateway pods, and flow engine interact at deployment time.

---

## Contents

- [Component Overview](#component-overview)
- [Why Separate Gateway Pods](#why-separate-gateway-pods)
- [Webhook Gateway](#webhook-gateway)
- [Kafka Gateway](#kafka-gateway)
- [HTTP Executor](#http-executor)
- [Gateway Configuration: CRD-Watching](#gateway-configuration-crd-watching)
- [Gateway ServiceAccount and RBAC](#gateway-serviceaccount-and-rbac)
- [Trigger → Flow Invocation](#trigger-flow-invocation)
- [Scaling](#scaling)
- [Namespace Isolation](#namespace-isolation)
- [Multi-Tenancy](#multi-tenancy)
- [Container Images](#container-images)
- [Deployment Lifecycle](#deployment-lifecycle)
- [Adding New Trigger Types](#adding-new-trigger-types)
- [Kubernetes Resource Event Triggers](#kubernetes-resource-event-triggers)

---

## Component Overview

KubeZap is composed of five distinct runtime components, each with its own binary and container image:

```
  External Events         Gateway Layer                  Control Plane
  ───────────────         ─────────────                  ─────────────

  HTTP Request  ────────► webhook-gateway ──┐            kubezap-controller
                          (one per ns)      │               │
  Kafka Message ────────► kafka-gateway   ──┼──► FlowRun ───┤ watches & executes
                          (one per cluster) │    CRDs       │
  AMQP Message  ────────► amqp-gateway    ──┤               │ creates & manages
                          (one per broker)  │               ▼
  NATS Message  ────────► nats-gateway    ──┤         gateway Deployments
                          (one per server)  │               │
                                │           │               │ POST /execute (RPC)
                                │ watch     │               ▼
                                └──────────►┘        http-executor pod
                           Trigger CRDs              (one per namespace;
                                                      no RBAC, SSRF guard)
                                            ──────────────────────────────────
                          Prometheus metrics + OpenTelemetry traces
```

| Component                 | Binary                        | Purpose                                                                                                                                                                                                                                 |
| ------------------------- | ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kubezap-controller`      | `cmd/main.go`                 | Kubernetes operator. Reconciles all CRDs, manages gateway Deployments, executes Flows via FlowRun.                                                                                                                                      |
| `kubezap-webhook-gateway` | `cmd/webhook-gateway/main.go` | HTTP server. Watches Trigger CRDs and dynamically registers webhook routes. Creates FlowRun on each request.                                                                                                                            |
| `kubezap-kafka-gateway`   | `cmd/kafka-gateway/main.go`   | Kafka consumer. Watches Trigger CRDs and manages topic subscriptions. Creates FlowRun on each message.                                                                                                                                  |
| `kubezap-amqp-gateway`    | `cmd/amqp-gateway/main.go`    | AMQP consumer (RabbitMQ, Azure Service Bus, etc.). One Deployment per broker Integration per namespace.                                                                                                                                 |
| `kubezap-nats-gateway`    | `cmd/nats-gateway/main.go`    | NATS JetStream consumer. One Deployment per NATS server Integration per namespace.                                                                                                                                                      |
| `kubezap-http-executor`   | `cmd/http-executor/main.go`   | HTTP step executor: receives fully-resolved HTTP requests from controller via internal `POST /execute` RPC, executes with SSRF blocklist, returns results. Minimal RBAC (no secrets, no RBAC management). One Deployment per namespace. |

The controller and gateways are separate processes deployed as separate Kubernetes `Deployment` resources. The controller creates and manages the gateway Deployments.

---

## Why Separate Gateway Pods

The controller pod runs the Kubernetes reconciliation loop. Mixing trigger handling (HTTP serving, Kafka consuming) into the controller introduces several problems:

- **Resource contention**: A spike in webhook traffic or a slow Kafka consumer could starve the controller's reconciliation goroutines.
- **Scaling coupling**: You cannot scale webhook handling independently from the controller. The controller uses leader election and should typically run with 2–3 replicas, not the 5–10 you might want for high-throughput webhooks.
- **Crash isolation**: A bug in a Kafka consumer should not bring down the controller that manages all CRD state.
- **Image complexity**: Different trigger types have different dependencies (Kafka requires a client library; the HTTP server doesn't). Keeping them separate keeps images lean.

The tradeoff is more moving parts. The operator handles this by creating and lifecycle-managing the gateway Deployments itself — you never create them manually.

---

## Webhook Gateway

The webhook gateway is a lightweight HTTP server that handles all inbound webhook triggers within a namespace. The operator creates one `Deployment` per namespace where webhook `Trigger` resources exist.

### How it works

```
  Controller        Webhook Gateway           Trigger CRD      FlowRun CRD     HTTP Client
      │                    │                       │                │               │
      │── create Dep. ────►│                       │                │               │
      │                    │── watch ─────────────►│                │               │
      │                    │◄── Trigger added ─────┤                │               │
      │                    │   path=/hooks/orders  |                │               │
      │                    │   [registers route]   |                │               │
      │                    │                       │                │               │
      │                    │◄── POST /hooks/orders {"orderId": "123"} ──────────────┤
      │                    │── lookup Trigger ────►│                │               │
      │                    │── create FlowRun ─────────────────────►│               │
      │                    │── 202 Accepted ───────────────────────────────────────►│
      │◄── watch FlowRun ───────────────────────────────────────────│               │
      │── execute Flow ────►                                        │               │
```

### Route registration

The gateway uses a dynamic HTTP router. When a `Trigger` CRD with `type: webhook` is created, the gateway registers the path. When the `Trigger` is deleted or `enabled: false` is set, the path is deregistered. No restart required.

### Request handling

On each incoming request the gateway:

1. Looks up the matching `Trigger` by path
2. Checks cooldown policy (reads and updates `status.currentInvocationCount`)
3. Parses the request body based on `Content-Type`
4. Creates a `FlowRun` CR with the trigger payload as input params
5. Returns `202 Accepted` immediately — flow execution is asynchronous

---

## Kafka Gateway

The Kafka gateway manages consumer subscriptions for all Kafka pub/sub triggers referencing a given `Integration`. The operator creates one `Deployment` per distinct Kafka cluster (Integration) per namespace.

**Why one gateway per Kafka cluster, not one per namespace:**

- Consumer group coordination happens at the Kafka broker level. All consumers for topics on the same cluster benefit from being in the same process for group rebalancing efficiency.
- Different Kafka clusters have different TLS configs, credentials, and bootstrap servers. Separating by cluster keeps configuration clean.
- A single gateway can subscribe to multiple topics on one cluster, which is how Kafka clients are designed to work.

### How it works

```
  Controller        Kafka Gateway          Trigger CRD      Kafka Broker     FlowRun CRD
      │                   │                    │                │                │
      │── create Dep. ───►│                    │                │                │
      │                   │── watch ──────────►│                │                │
      │                   │◄── Trigger added ──┤                │                │
      │                   │   topic=orders.created              │                │
      │                   │── subscribe ───────────────────────►│                │
      │                   │                    │                │                │
      │                   │◄── message received ────────────────┤                │
      │                   │── create FlowRun ───────────────────────────────────►│
      │                   │   [commit offset]  │                │                │
      │◄── watch FlowRun ────────────────────────────────────────────────────────│
      │── execute Flow ───►                                                      │
```

### Topic subscription management

When a `Trigger` with `type: kafka` referencing this gateway's `Integration` is:
- **Created**: gateway adds the topic to its consumer subscriptions
- **Deleted**: gateway unsubscribes from the topic
- **`enabled: false`**: gateway pauses consumption (does not commit offsets)

If multiple Triggers reference the same topic, the gateway manages a single consumer and fans out to multiple FlowRuns.

### Offset commit strategy

Offsets are committed after the `FlowRun` CR is successfully persisted to Kubernetes — not after the flow completes. This provides **at-least-once delivery** semantics:

- If the gateway crashes *before* creating the FlowRun, the message is re-delivered and a new FlowRun is created. ✓
- If the gateway crashes *after* creating the FlowRun but *before* committing the offset, the message is re-delivered. The gateway deduplicates using the Kafka offset as part of the FlowRun name (e.g. `order-events-p0-offset-12345`), so the second delivery finds the existing FlowRun and skips creation. ✓

Flows should still be designed to be idempotent as a defence-in-depth measure.

---

## HTTP Executor

Flow steps of `type: http` don't call out to the target URL from the controller — the controller resolves all secrets and builds the fully-substituted request in-memory, then hands it off to `kubezap-http-executor`, a separate pod with **no RBAC, no secrets access, and no cluster management**, over an internal `POST /execute` RPC. One `http-executor` Deployment runs per managed namespace.

This split limits the blast radius of an SSRF bypass: even if a DNS-rebinding attack gets past the SSRF blocklist, the attacker gains execution in a pod that can't read secrets or talk to the Kubernetes API, not in the controller pod. The channel between the controller and the executor is protected by a mandatory `NetworkPolicy` (ingress restricted to the controller pod) and optional mTLS (`--executor-mtls=true`) for clusters without a service mesh.

See [HTTP Executor — Design Contract](architecture/http-executor.md) for the full RPC contract (request/response schemas, error codes), SSRF details, and certificate rotation.

---

## Gateway Configuration: CRD-Watching

Gateways are configured by watching `Trigger` CRDs directly — there is no intermediate ConfigMap that the controller has to keep synchronized. Both gateways embed a `controller-runtime` client and use an informer to watch the Trigger API.

**Why CRD-watching instead of a ConfigMap:**

| Approach                  | Pros                                                                 | Cons                                                                                 |
| ------------------------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| **CRD-watching** (chosen) | Single source of truth, reactive, no sync lag, no intermediate state | Gateway needs K8s API access, slightly more complex                                  |
| ConfigMap push            | Simple gateway                                                       | Controller must keep ConfigMap in sync, eventual consistency, extra reconciler logic |
| gRPC/REST from controller | Rich protocol                                                        | Tight coupling, harder to run gateways independently                                 |

The gateway RBAC needs `get/list/watch` on `Trigger` resources in its namespace. The operator creates a `ServiceAccount`, `Role`, and `RoleBinding` for each gateway Deployment it provisions.

---

## Gateway ServiceAccount and RBAC

Each gateway Deployment runs under a dedicated ServiceAccount, not the controller's
ServiceAccount. The controller creates these resources alongside the gateway Deployment.

### Webhook Gateway ServiceAccount

**Name:** `kubezap-webhook-gateway` (in the same namespace as the gateway Deployment)

**Role permissions required:**

| API Group               | Resource   | Verbs              | Why                                  |
| ----------------------- | ---------- | ------------------ | ------------------------------------ |
| `automation.kubezap.io` | `triggers` | `get, list, watch` | Route registration from Trigger CRDs |
| `automation.kubezap.io` | `flowruns` | `create`           | FlowRun creation on webhook arrival  |

The controller creates a `Role` (not `ClusterRole`) with these permissions and a
`RoleBinding` to the gateway ServiceAccount. This keeps the gateway's blast radius
scoped to its own namespace.

**Implementation:** `internal/controller/gateway_deployment.go` is responsible for
creating/updating the ServiceAccount, Role, and RoleBinding as part of the gateway
Deployment reconciliation. These resources share the gateway Deployment's lifecycle:
created when the first webhook Trigger appears in a namespace, deleted when the last one
is removed.

**Required controller RBAC additions:**

The controller itself needs permission to create/manage these resources:
```yaml
# +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
# +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
# +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
```

### Verifying Gateway Permissions

```bash
kubectl auth can-i create flowruns \
  --as=system:serviceaccount:default:kubezap-webhook-gateway -n default
# Expected: yes
```

### Plugin ServiceAccount

Plugin Deployments (type=plugin Integrations) do **not** receive automatic RBAC from the
operator. The plugin image is user-supplied and its permissions are the cluster admin's
responsibility. See [Plugin Trust Model](api/integration.md#plugin-integration-type).

---

## Trigger → Flow Invocation

When a gateway receives a trigger event, it creates a `FlowRun` CR. The controller watches `FlowRun` resources and executes the flow.

```yaml
# Created by the webhook gateway when POST /hooks/orders is received
apiVersion: automation.kubezap.io/v1alpha1
kind: FlowRun
metadata:
  name: order-received-1741954332-xk92p  # generated
  namespace: automation
  ownerReferences:
    - apiVersion: automation.kubezap.io/v1alpha1
      kind: Trigger
      name: order-received
spec:
  flowRef:
    name: process-order
  triggerRef:
    name: order-received
    type: webhook
  params:
    orderId: "ORD-9921"
    customerName: "Acme Corp"
  triggerPayload: '{"orderId": "ORD-9921", "customerName": "Acme Corp"}'
status:
  phase: Running
  startTime: "2026-03-14T10:32:11Z"
```

**Why FlowRun rather than direct invocation:**

- **Decoupled**: the gateway doesn't need to know how to execute flows
- **Observable**: every execution is a Kubernetes resource — `kubectl get flowruns` shows history
- **Reliable**: if the controller restarts mid-execution, it picks up in-progress FlowRuns on restart
- **Auditable**: FlowRuns are created as owner-referenced to the Trigger, so `kubectl get flowruns --field-selector spec.triggerRef.name=order-received` shows all executions for a given trigger

The gateway returns `202 Accepted` to the caller as soon as the FlowRun is created. Flow execution is fully asynchronous.

### Scale Limitations

KubeZap is designed for **workflow orchestration** — multi-step flows with HTTP calls, retries, conditional branching, and wait steps — not high-throughput stream processing. It's not the right tool for stateless message transforms at very high throughput (use Kafka Streams or Flink), sub-second per-event latency requirements, or aggregation/windowing/stream joins (use Flink).

All FlowRun state lives in Kubernetes CRDs (etcd), which gives `kubectl` observability, crash recovery, and Kubernetes-native deduplication with no external dependencies — but bounds practical scale:

| Dimension          | Practical limit          | Notes                                                                                  |
| ------------------- | ------------------------- | --------------------------------------------------------------------------------------- |
| Ingest rate         | ~hundreds/min sustained   | etcd write throughput and storage quota bound this                                      |
| etcd object size    | ~10–50 KB per FlowRun     | Spec + full step status + 4 KB body snapshot                                            |
| etcd storage quota  | 2 GB default              | ~40,000–200,000 FlowRuns before GC must keep pace                                       |
| GC list scan        | O(n) per trigger          | `enforceMaxFlowRunsByPhase` lists all FlowRuns per trigger on each terminal reconcile    |

FlowRun history is managed entirely via TTL and count-based GC policies (see [Garbage Collection](api/flowrun.md#garbage-collection)); at high ingest rates, tuning these aggressively (short TTLs, low `maxSucceeded`/`maxFailed`) is required to avoid etcd storage pressure. At enterprise Kafka scale (10,000+/min), KubeZap CRD storage will exhaust etcd regardless of GC tuning — front it with a stream processor that filters/aggregates messages, invoking KubeZap only for the subset that needs true workflow orchestration. See [scale-limitations.md](design/scale-limitations.md) for the decision to keep FlowRun state in etcd rather than add an external database.

---

## Scaling

### Webhook Gateway

The webhook gateway is stateless with respect to request routing — each request is handled independently and immediately converted to a FlowRun CRD. Cooldown state (invocation counts) is persisted to the Trigger CRD status via the Kubernetes API, not in pod memory, so multiple replicas can safely share it. The gateway scales horizontally without coordination.

The operator creates an `HorizontalPodAutoscaler` for each webhook gateway Deployment. Default configuration:

```yaml
spec:
  minReplicas: 1
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```

You can override HPA settings per namespace via the namespace's `WebhookGatewayConfig` (`spec.hpa.{minReplicas,maxReplicas,targetCPUUtilization}`); see `docs/api/webhookgatewayconfig.md`.

All replicas share the same `Service`, so load is distributed by the Service's kube-proxy load balancing. For more sophisticated load balancing (e.g., sticky sessions, request-rate-based), put a Gateway API `HTTPRoute` in front.

### Kafka Gateway

Kafka scaling is partition-bounded. The optimal number of replicas equals the number of partitions on the subscribed topics — additional replicas beyond that sit idle in the consumer group.

**Recommended**: use [KEDA](https://keda.sh) with the Kafka scaler to auto-scale the Kafka gateway based on consumer group lag:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: kafka-gateway-scaler
  namespace: automation
spec:
  scaleTargetRef:
    name: kubezap-kafka-gateway-prod-cluster
  minReplicaCount: 1
  maxReplicaCount: 12   # match your topic partition count
  triggers:
    - type: kafka
      metadata:
        bootstrapServers: kafka.prod.svc:9092
        consumerGroup: kubezap-automation
        topic: orders.created
        lagThreshold: "50"
```

KEDA is supported for partition-bounded scaling of Kafka gateways via the Kafka scaler. See the [Integration CRD](api/integration.md) for configuration details.

### Controller

The controller uses leader election and should run with 2–3 replicas. Only the leader actively reconciles; standbys are hot-standby. Do not scale the controller for throughput — it is not on the hot path for request handling.

---

## Namespace Isolation

| Resource                   | Scope      | Notes                                                                                          |
| -------------------------- | ---------- | ---------------------------------------------------------------------------------------------- |
| Trigger, Flow, FlowRun     | Namespaced | Each namespace has independent CRDs                                                            |
| Webhook Gateway Deployment | Namespaced | One per namespace where webhook Triggers exist                                                 |
| Broker gateway Deployment  | Namespaced | One per (namespace × broker Integration); separate gateway for each of `kafka`, `amqp`, `nats` |
| Controller                 | Namespaced | Watches its own namespace by default, or an explicit namespace list; runs in `kubezap-system`  |

The controller watches CRDs in all namespaces and creates gateway Deployments within each namespace where they are needed. If all Triggers in a namespace are deleted, the controller garbage-collects the gateway Deployments.

---

## Multi-Tenancy

### Isolation boundaries

Gateway pods are per-namespace — tenant A's webhook traffic never touches tenant B's gateway pod. The **operator controller** is the shared component whenever it is configured to watch more than its own namespace: watching an explicit list of tenant namespaces from one controller widens its RBAC to cover each of them, and its compromise or misconfiguration could then affect every namespace it watches.

For teams within the same organization (separate namespaces for dev/staging/prod, or team-per-namespace), watching a short, explicit list of namespaces from one controller is generally acceptable. For true multi-tenant scenarios — different organizations sharing a cluster, regulated environments with strict data segregation, or SaaS platforms — the controller should be isolated per tenant, using its default, most isolated mode: watching only its own namespace.

### Watch namespace modes

KubeZap supports two watch-mode shapes, controlled by the `WATCH_NAMESPACES` environment variable on the controller Deployment (standard Operator SDK / controller-runtime pattern): the operator watches either its own namespace, or an explicit comma-separated list of one or more namespaces.

| Mode                | `WATCH_NAMESPACES` value      | Default? | Use case                                                        |
| ------------------- | ----------------------------- | -------- | ---------------------------------------------------------------- |
| **OwnNamespace**    | `""` (empty) or operator's ns | **Yes**  | Default; maximum isolation, least privilege (OLM default)        |
| **SingleNamespace** | `"tenant-a"`                  | No       | One operator installation per tenant group                       |
| **MultiNamespace**  | `"ns1,ns2,ns3"`               | No       | Operator serves an explicit, known set of tenant namespaces      |

> **Security note:** The default is OwnNamespace (least privilege). Every mode — including MultiNamespace — grants the operator a namespace-scoped `Role` (never a `ClusterRole`) for its own reconciler permissions: for MultiNamespace, the operator is provisioned one `Role`+`RoleBinding` pair per listed namespace, so its RBAC grant always matches exactly the namespaces it actually watches.

These map directly to [OLM install modes](https://olm.operatorframework.io/docs/advanced-tasks/operator-scoping-with-operatorgroups/), which is required for OperatorHub certification.

### Isolation tiers

#### Tier 1 — Namespace isolation (shared operator)

```
  ┌─────────────────────────────────────────────────────────────────┐
  │ kubezap-system                                                  │
  │   kubezap-controller  (WATCH_NAMESPACES="tenant-a,tenant-b")    │
  └──────────────┬────────────────────────┬───────────────────────── ┘
                 │ manages & watches       │ manages & watches
                 ▼                         ▼
  ┌──────────────────────┐   ┌──────────────────────┐
  │ tenant-a namespace   │   │ tenant-b namespace   │
  │   webhook-gateway    │   │   webhook-gateway    │
  │   Triggers / Flows   │   │   Triggers / Flows   │
  └──────────────────────┘   └──────────────────────┘
```

- One controller installation, one namespace-scoped `Role` + `RoleBinding` per tenant namespace it watches
- Gateway pods are separate per namespace — no cross-tenant traffic mixing
- Suitable for: teams within an organization, dev/staging/prod isolation, internal platforms

**Remaining shared component:** The controller pod itself. A platform team administers it.

#### Tier 2 — Namespace-scoped operator (per-tenant operator)

```
  ┌───────────────────────────────┐   ┌───────────────────────────────┐
  │ tenant-a namespace            │   │ tenant-b namespace            │
  │                               │   │                               │
  │   kubezap-controller-a        │   │   kubezap-controller-b        │
  │   WATCH_NAMESPACES=tenant-a   │   │   WATCH_NAMESPACES=tenant-b   │
  │         │ manages             │   │         │ manages             │
  │         ▼                     │   │         ▼                     │
  │   webhook-gateway             │   │   webhook-gateway             │
  │   Triggers / Flows            │   │   Triggers / Flows            │
  └───────────────────────────────┘   └───────────────────────────────┘
           no shared components between tenants
```

- Each tenant installs their own operator, watching only their namespace(s)
- No shared control plane component between tenants
- Controller uses namespace-scoped `Role` instead of cluster-scoped `ClusterRole`
- CRDs must be pre-installed by a cluster administrator (CRDs are always cluster-scoped in Kubernetes)
- Suitable for: true multi-tenant SaaS, strict compliance (SOC2, PCI), separate organizations sharing a cluster

**Remaining shared component:** The CRD definitions themselves (cluster-scoped). CRD schema changes require cluster-admin access, so operator upgrades involving CRD changes need coordination. This is standard Kubernetes multi-tenancy behavior and is not unique to KubeZap.

#### Tier 3 — Separate clusters

Complete isolation at the infrastructure level. Not a KubeZap concern but worth documenting as the option for the strictest regulatory requirements.

### RBAC implications by mode

| Mode                           | Controller needs                                                                    | Gateway needs                    |
| ------------------------------ | ------------------------------------------------------------------------------------ | --------------------------------- |
| MultiNamespace                 | One namespace-scoped `Role` + `RoleBinding` per listed namespace — no `ClusterRole`  | `Role` in each managed namespace |
| SingleNamespace / OwnNamespace | Namespace-scoped `Role` only — no `ClusterRole` needed                               | `Role` in the watched namespace  |

Regardless of how many namespaces `WATCH_NAMESPACES` lists, the Helm chart and OLM bundle always provision `Role`/`RoleBinding` pairs — never a `ClusterRole`/`ClusterRoleBinding` — for the operator's own reconciler permissions. This is important for OpenShift environments where cluster admins are reluctant to grant ClusterRoles to tenant-managed operators.

### OLM install modes

The OLM bundle generated by `make bundle` supports OwnNamespace, SingleNamespace, and MultiNamespace — matching the watch modes above, since KubeZap always resolves a watch scope down to the operator's own namespace or an explicit namespace list. AllNamespaces is not supported. OLM's CSV schema requires every install mode to be listed explicitly (supported or not), so the CSV declares all four:

```yaml
# In bundle/manifests/kubezap.clusterserviceversion.yaml
spec:
  installModes:
    - type: OwnNamespace
      supported: true
    - type: SingleNamespace
      supported: true
    - type: MultiNamespace
      supported: true
    - type: AllNamespaces
      supported: false
```

### Configuring watch namespaces

**Via Helm (recommended):**
```yaml
# values.yaml
controller:
  watchNamespaces: ""                     # OwnNamespace (default — least privilege)
  # watchNamespaces: "tenant-a"           # SingleNamespace
  # watchNamespaces: "tenant-a,tenant-b"  # MultiNamespace
```

**Via raw manifests:**
```yaml
# config/manager/manager.yaml
env:
  - name: WATCH_NAMESPACES
    value: ""   # or "ns1,ns2"
```

> For SingleNamespace/MultiNamespace via raw manifests specifically, this env var alone is not enough — RBAC for each additional namespace must be applied separately, **before** the manager starts (or restarts) with that namespace in the list. See `config/rbac/namespaced_role.yaml`'s header comment for the exact commands and why the ordering matters (a missing Role in even one watched namespace stalls the manager's cache sync, and therefore reconciliation, everywhere — not just in that namespace). The Helm chart and OLM bundle both provision this automatically; only the raw-manifest path requires it by hand.

**Via OLM Subscription:**
```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: kubezap
  namespace: tenant-a
spec:
  channel: stable
  name: kubezap
  source: community-operators
  config:
    env:
      - name: WATCH_NAMESPACES
        value: "tenant-a"
```

### Network policy recommendations for Tier 1

When running in shared-operator mode, use NetworkPolicy to enforce namespace boundaries at the network layer:

```yaml
# Restrict the webhook gateway to only accept traffic from within the namespace
# and from the ingress controller
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kubezap-webhook-gateway
  namespace: tenant-a
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/component: webhook-gateway
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx  # or your ingress namespace
    - from:
        - podSelector: {}  # in-namespace traffic
  egress:
    - {}  # allow all egress (flows make outbound HTTP calls)
```

---

## Container Images

All images are built from the same repository:

| Image                     | Entry point                   | Base                        |
| ------------------------- | ----------------------------- | --------------------------- |
| `kubezap/controller`      | `cmd/main.go`                 | `distroless/static:nonroot` |
| `kubezap/webhook-gateway` | `cmd/webhook-gateway/main.go` | `distroless/static:nonroot` |
| `kubezap/kafka-gateway`   | `cmd/kafka-gateway/main.go`   | `distroless/static:nonroot` |
| `kubezap/amqp-gateway`    | `cmd/amqp-gateway/main.go`    | `distroless/static:nonroot` |
| `kubezap/nats-gateway`    | `cmd/nats-gateway/main.go`    | `distroless/static:nonroot` |

All images share the same version tag. The controller references gateway images by tag when creating Deployments. The image tag can be overridden at operator install time via Helm values or OLM subscription config.

### Dockerfile structure

```
Dockerfile                   # controller image (existing)
Dockerfile.webhook-gateway   # webhook gateway image
Dockerfile.kafka-gateway     # kafka gateway image
Dockerfile.amqp-gateway      # AMQP gateway image
Dockerfile.nats-gateway      # NATS gateway image
```

All follow the same multi-stage pattern: build in `golang:1.24`, run in `distroless/static:nonroot`.

---

## Deployment Lifecycle

### Gateway Deployment creation

When the controller reconciles a `Trigger` with `type: webhook`, it:

1. Checks whether a webhook gateway `Deployment` exists in that namespace
2. If not, creates one (Deployment + Service + ServiceAccount + Role + RoleBinding + HPA)
3. The gateway picks up the new Trigger via its CRD watch and registers the route

The controller uses `ownerReferences` so that if the controller's own `Namespace` is deleted, gateway resources are garbage-collected.

### Gateway Deployment updates

When the operator is upgraded, the controller updates the gateway Deployments to the new image tag. This triggers a rolling update of gateway pods.

### Gateway Deployment deletion

When the last `Trigger` of a given type is deleted from a namespace, the controller deletes the corresponding gateway Deployment (and all its associated resources).

---

## Adding New Trigger Types

To add support for a new trigger type (e.g., NATS, RabbitMQ, S3 events):

1. Add the new `type` enum value to `TriggerSpec`
2. Add a new spec struct for the trigger config (e.g., `NATSTrigger`)
3. Create a new gateway binary in `cmd/nats-gateway/`
4. Add a new Dockerfile
5. Update the controller's reconciler to create/manage the new gateway Deployment type
6. Add the new trigger type as a new top-level `type` value in `TriggerSpec` and a corresponding spec struct (following the `kafka`, `amqp`, `nats` pattern)

The gateway pattern is designed so each new trigger type is a self-contained binary. Adding NATS support does not require changes to the webhook or Kafka gateways.

---

## Kubernetes Resource Event Triggers

Kubernetes resource event triggers — firing a Flow when a Pod is created, a ConfigMap changes, a custom resource is updated — do **not** need a separate gateway image.

The controller already maintains an informer cache connected to the Kubernetes API server via `controller-runtime`. Adding resource event triggers means extending the controller to also watch arbitrary resource types declared in Trigger CRDs, and creating a `FlowRun` when a matching event occurs.

```
  User              Kubernetes API         kubezap-controller        FlowRun CRD
    │                     │                        │                      │
    │── kubectl apply ───►│ Trigger CR created     │                      │
    │   type: resource    │── inform controller ──►│                      │
    │   kind: Pod         │                        │── add Pod informer ─►│(k8s API)
    │   event: create     │                        │                      │
    │                     │                        │                      │
    │                     │── Pod "my-pod" added ─►│                      │
    │                     │                        │ match against        │
    │                     │                        │ Trigger selectors    │
    │                     │                        │── create FlowRun ───►│
    │                     │                        │   params: {name, namespace, labels...}
```

**Why no separate gateway:**
- The controller already has an open, cached connection to the API server — a separate process would open a redundant connection
- `controller-runtime` dynamic watches are the native pattern for this; it is designed to add watchers at runtime
- Resource event handling is low-latency and CPU-light — no reason to scale it independently from the controller

**When a separate gateway would be needed**: if you wanted to trigger on events from a *remote* cluster. Cross-cluster resource triggers would need a gateway that connects to the remote cluster's API server. That is a future concern.

### Resource Trigger spec

> **Status:** Implemented in `internal/controller/trigger_controller.go` and `internal/controller/resource_watcher.go`. Alpha-stability — see `docs/api/trigger.md` for the full list of known limitations.

```yaml
apiVersion: automation.kubezap.io/v1alpha1
kind: Trigger
metadata:
  name: pod-ready-handler
  namespace: automation
spec:
  type: resource
  resource:
    apiVersion: v1
    kind: Pod
    # Scope: namespace of the Trigger, or specify a different namespace
    namespace: production
    # Optional: only match resources with these labels
    labelSelector:
      matchLabels:
        app: my-service
    # Events to watch: create, update, delete, or any combination
    events: [create, update]
    # Optional: only fire when these fields change (for update events)
    watchFields:
      - ".status.phase"
      - ".status.conditions"
  flowRef:
    name: handle-pod-ready
```

The trigger body is the full resource object's JSON (no wrapper key), navigable the same way as any JSON trigger body:
- `$(trigger.body.metadata.name)` — resource name
- `$(trigger.body.status.phase)` — field from the resource

**Not accessible from step interpolation**: `eventType` (`ADDED`/`MODIFIED`/`DELETED`) is recorded on the FlowRun's `spec.triggerData` but has no `$(trigger.eventType)` interpolation syntax today. There is also no captured "previous object" for update events — only the current resource state is available.

