# Contributing to KubeZap

## Prerequisites

| Tool      | Version    | Purpose                              |
| --------- | ---------- | ------------------------------------ |
| Go        | 1.24+      | Build and test                       |
| Docker    | any recent | Build container images               |
| kubectl   | 1.27+      | Cluster interaction                  |
| kustomize | v5+        | Manifest generation                  |
| kind      | any recent | E2E test cluster                     |
| k3s       | any recent | Local development cluster (optional) |
| make      | any        | Build targets                        |

Install the pinned code-generation tools used by the Makefile:

```bash
make controller-gen
make kustomize
```

## Building

```bash
make build          # compile the operator binary
make docker-build IMG=ghcr.io/kubezap/controller:latest
docker build -t ghcr.io/kubezap/webhook-gateway:latest -f cmd/webhook-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/kafka-gateway:latest   -f cmd/kafka-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/amqp-gateway:latest    -f cmd/amqp-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/nats-gateway:latest    -f cmd/nats-gateway/Dockerfile .
```

After any change to types in `api/`, regenerate before building:

```bash
make generate && make manifests
```

## Running unit tests

```bash
make test
```

This runs all non-e2e tests under `internal/` using `envtest` (a local Kubernetes API server — no cluster required). Coverage is written to `cover.out`.

To run a focused subset:

```bash
go test ./internal/controller/... -v -run TestFlowRun
```

## Running e2e tests

E2e tests require a Kind cluster. The `make test-e2e` target creates one automatically if it does not already exist:

```bash
make test-e2e
```

This will:

1. Create a Kind cluster named `kubezap-test-e2e` (if absent).
2. Build the controller and webhook-gateway images.
3. Load those images into the Kind cluster.
4. Install CRDs, deploy the controller, and run the full Ginkgo suite.
5. Tear down the cluster after the run.

To skip cluster creation (e.g. you have the cluster already running from a previous run):

```bash
KIND_CLUSTER=kubezap-test-e2e go test ./test/e2e/ -v -ginkgo.v
```

### Skipping optional scenarios

| Env var                              | Effect                                                       |
| ------------------------------------ | ------------------------------------------------------------ |
| `CERT_MANAGER_INSTALL_SKIP=true`     | Skip CertManager install (if already present in the cluster) |
| `KAFKA_BOOTSTRAP_SERVERS=` _(unset)_ | Kafka Trigger tests are skipped automatically                |
| `SKIP_WEBHOOK_E2E=true`              | Skip the Webhook→Transform→HTTP→MockEndpoint scenario        |

## Local development with k3s

k3s uses its own containerd instance, so images built with Docker must be imported before deploying.

Use a git SHA tag instead of `:latest` — with `imagePullPolicy: IfNotPresent`, k3s caches by tag. A unique tag per build ensures the new image is always picked up.

```bash
TAG=$(git rev-parse --short HEAD)

# Build
make docker-build IMG=ghcr.io/kubezap/controller:$TAG
docker build -t ghcr.io/kubezap/webhook-gateway:$TAG -f cmd/webhook-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/kafka-gateway:$TAG   -f cmd/kafka-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/amqp-gateway:$TAG    -f cmd/amqp-gateway/Dockerfile .
docker build -t ghcr.io/kubezap/nats-gateway:$TAG    -f cmd/nats-gateway/Dockerfile .

# Import into k3s containerd
docker save ghcr.io/kubezap/controller:$TAG      | sudo k3s ctr images import -
docker save ghcr.io/kubezap/webhook-gateway:$TAG | sudo k3s ctr images import -
docker save ghcr.io/kubezap/kafka-gateway:$TAG   | sudo k3s ctr images import -
docker save ghcr.io/kubezap/amqp-gateway:$TAG    | sudo k3s ctr images import -
docker save ghcr.io/kubezap/nats-gateway:$TAG    | sudo k3s ctr images import -

# Deploy
make deploy IMG=ghcr.io/kubezap/controller:$TAG \
  WEBHOOK_GATEWAY_IMAGE=ghcr.io/kubezap/webhook-gateway:$TAG \
  KAFKA_GATEWAY_IMAGE=ghcr.io/kubezap/kafka-gateway:$TAG \
  AMQP_GATEWAY_IMAGE=ghcr.io/kubezap/amqp-gateway:$TAG \
  NATS_GATEWAY_IMAGE=ghcr.io/kubezap/nats-gateway:$TAG
```

The controller reads `WEBHOOK_GATEWAY_IMAGE` and `KAFKA_GATEWAY_IMAGE` at runtime to know which image to use when creating gateway Deployments.

## Architecture orientation

### Binary layout

KubeZap consists of five separate binaries, each with its own container image:

| Binary / entry point            | Image                       | Purpose                                                                                      |
| ------------------------------- | --------------------------- | -------------------------------------------------------------------------------------------- |
| `cmd/main.go`                   | `kubezap/controller`        | Kubernetes operator: reconciles all CRDs, manages gateway Deployments, executes FlowRuns     |
| `cmd/webhook-gateway/main.go`   | `kubezap/webhook-gateway`   | HTTP server: watches Trigger CRDs, registers routes dynamically, creates FlowRuns            |
| `cmd/kafka-gateway/main.go`     | `kubezap/kafka-gateway`     | Kafka consumer: watches Trigger CRDs, manages topic subscriptions, creates FlowRuns          |
| `cmd/amqp-gateway/main.go`      | `kubezap/amqp-gateway`      | AMQP consumer (RabbitMQ, Azure Service Bus, IBM MQ): beta                                    |
| `cmd/nats-gateway/main.go`      | `kubezap/nats-gateway`      | NATS JetStream consumer: beta                                                                |
| `cmd/kubezap/`                  | —                           | CLI tool (`bin/kubezap`), built with `make build-cli`                                        |

The controller is the only binary that interacts with the Kubernetes API for reconciliation. Gateways interact with the Kubernetes API only to watch Trigger CRDs and create FlowRun CRDs. All gateway→controller communication flows through the `FlowRun` CRD — gateways create a FlowRun; the controller picks it up and executes the steps.

### Key packages

| Package                       | Description                                                                                 |
| ----------------------------- | ------------------------------------------------------------------------------------------- |
| `api/v1alpha1/`               | CRD Go type definitions — source of truth for all CRD schemas and kubebuilder markers       |
| `internal/controller/`        | All reconciler implementations (one file per controller, plus shared helpers)               |
| `internal/gateway/webhook/`   | Webhook gateway request handling, route registration, HMAC/bearer/OIDC auth                 |
| `internal/gateway/kafka/`     | Kafka consumer, partition management, offset tracking                                        |
| `internal/gateway/amqp/`      | AMQP gateway (beta)                                                                          |
| `internal/gateway/nats/`      | NATS JetStream gateway (beta)                                                                |
| `internal/metrics/`           | Prometheus metric definitions shared across packages                                         |

### Reconciler entry points

Each controller is registered with the manager via `SetupWithManager`. The four active reconcilers and their source files are:

| Reconciler            | File                                        | Watches                          |
| --------------------- | ------------------------------------------- | -------------------------------- |
| `FlowReconciler`      | `internal/controller/flow_controller.go:147`       | `Flow`                           |
| `TriggerReconciler`   | `internal/controller/trigger_controller.go:187`    | `Trigger`, manages gateway Deployments |
| `FlowRunReconciler`   | `internal/controller/flowrun_controller.go:1302`   | `FlowRun`, executes step graphs  |
| `IntegrationReconciler` | `internal/controller/integration_controller.go:933` | `Integration`, manages plugin Deployments |

### Important design patterns

**One-step-per-reconcile is NOT the pattern here.** The `FlowRunReconciler` executes an entire FlowRun (a directed acyclic graph of steps) to completion (or failure) within a single reconcile call. Each step is executed in dependency order; the reconciler re-queues on transient errors and resumes from the last incomplete step.

**FlowRun CRD as the gateway→controller handoff.** Gateways do not call the controller directly. When an event arrives (HTTP request, Kafka message, AMQP message, cron tick), the gateway creates a `FlowRun` CR. The `FlowRunReconciler` watches for new FlowRuns and picks them up. This decouples gateways from the controller and makes the system resilient to controller restarts.

**Action types.** The valid step action types are validated in `validateFlowSpec` (`internal/controller/flow_controller.go`) and executed in the corresponding `execute*Step` functions in `flowrun_controller.go`:

| Action type   | Validate location         | Execute function              |
| ------------- | ------------------------- | ----------------------------- |
| `http`        | `validateFlowSpec`        | `executeHTTPStep`             |
| `transform`   | `validateFlowSpec`        | `executeStep` (inline)        |
| `publish`     | `validateFlowSpec`        | `executePublishStep`          |
| `wait`        | `validateFlowSpec`        | `executeWaitStep`             |

---

## First contribution guide

### Finding work

- Check the GitHub issue tracker for issues labelled `good first issue`.
- The `docs/` directory often has `TODO` or `FIXME` comments where doc improvements are welcome.
- `docs/tech-debt/` lists known limitations — many are good targets for first contributions.

### Running a single test

`make test` runs the entire non-e2e suite. To run a focused subset while iterating, pass a `-run` filter directly to `go test`:

```bash
# Run all tests in the controller package
go test ./internal/controller/... -v

# Run a specific Ginkgo test by description substring
go test ./internal/controller/... -v -run "FlowRun"

# Run tests matching a nested Ginkgo describe/context/it path
go test ./internal/controller/... -v -run "TestControllers/FlowRun"
```

The tests require `envtest` binaries. If you have not set them up yet:

```bash
make setup-envtest
```

This downloads the Kubernetes API server binaries used by envtest into `bin/`.

### Adding a new step action type

Adding a new action type (e.g. `slack`, `email`, `condition`) requires changes in three places. Follow this checklist:

1. **Define the action spec** — add a new optional struct field to `FlowStepAction` in `api/v1alpha1/flow_types.go` (e.g. `Slack *FlowStepSlackAction`). Run `make generate && make manifests` after editing.

2. **Validate the action** — add a `case "slack":` branch to the `switch step.Action.Type` block in `validateFlowSpec` in `internal/controller/flow_controller.go`. Validate all required fields and return a descriptive error for each missing one, following the existing `http` and `publish` patterns.

3. **Implement execution** — add an `executeSlackStep` function to `internal/controller/flowrun_controller.go` following the signature of `executeHTTPStep`. Wire it into the `executeStep` dispatch switch.

4. **Document it** — add the new type to the step action table in `docs/api/flow.md` with all spec fields and an example CR snippet.

5. **Test it** — add Ginkgo `It` blocks to `internal/controller/flowrun_controller_test.go` covering success, failure, and invalid-spec cases.

---

## OLM bundle generation and validation

KubeZap targets OperatorHub via OLM (Operator Lifecycle Manager). The bundle workflow uses Operator SDK.

### Prerequisites

Install `operator-sdk` locally if not already present:

```bash
make operator-sdk
```

This downloads the pinned version (`v1.42.0`) into `bin/operator-sdk`.

### Generate the bundle

```bash
make bundle
```

This runs three steps in sequence:

1. `operator-sdk generate kustomize manifests` — regenerates the ClusterServiceVersion (CSV) from kubebuilder markers.
2. `kustomize build config/manifests | operator-sdk generate bundle` — assembles the full bundle directory under `bundle/`.
3. `operator-sdk bundle validate ./bundle` — validates the generated bundle against OLM's schema rules.

The bundle version is controlled by the `VERSION` variable (default `0.0.1`). To generate a bundle for a specific version:

```bash
make bundle VERSION=0.1.0
```

### Build and push the bundle image

```bash
make bundle-build bundle-push
```

This builds the bundle image tagged as `ghcr.io/kubezap/kubezap-bundle:v<VERSION>` and pushes it. Override `BUNDLE_IMG` to use a different registry:

```bash
make bundle-build bundle-push BUNDLE_IMG=myregistry.io/kubezap-bundle:v0.1.0
```

### Validate the bundle manually

If you want to validate the bundle without regenerating it:

```bash
bin/operator-sdk bundle validate ./bundle
```

Common validation errors:

- **Missing replaces field** — set `spec.replaces` in the CSV when upgrading from a previous version.
- **Unsupported installModes** — all four OLM install modes (`OwnNamespace`, `SingleNamespace`, `MultiNamespace`, `AllNamespaces`) must be listed as supported in the CSV.
- **Icon missing** — add a base64-encoded PNG to `spec.icon` in the CSV for OperatorHub listing display.

---

## Code style

```bash
make lint           # golangci-lint
gofmt -w .
goimports -w .
```

Run `make lint` before committing. Never leave formatting or import ordering for a separate fix step.

## Workflow

1. Architecture discussion (check `docs/` for existing design docs)
2. Design / spec update in `docs/api/` or `docs/guides/`
3. Implementation
4. `make generate && make manifests` if types changed
5. `make test` — must pass before committing
6. `make test-e2e` — run before opening a PR
