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
make docker-build IMG=kubezap/controller:latest
docker build -t kubezap/webhook-gateway:latest -f cmd/webhook-gateway/Dockerfile .
docker build -t kubezap/kafka-gateway:latest   -f cmd/kafka-gateway/Dockerfile .
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
make docker-build IMG=docker.io/kubezap/controller:$TAG
docker build -t docker.io/kubezap/webhook-gateway:$TAG -f cmd/webhook-gateway/Dockerfile .
docker build -t docker.io/kubezap/kafka-gateway:$TAG   -f cmd/kafka-gateway/Dockerfile .

# Import into k3s containerd
docker save docker.io/kubezap/controller:$TAG      | sudo k3s ctr images import -
docker save docker.io/kubezap/webhook-gateway:$TAG | sudo k3s ctr images import -
docker save docker.io/kubezap/kafka-gateway:$TAG   | sudo k3s ctr images import -

# Deploy
make deploy IMG=docker.io/kubezap/controller:$TAG \
  WEBHOOK_GATEWAY_IMAGE=docker.io/kubezap/webhook-gateway:$TAG \
  KAFKA_GATEWAY_IMAGE=docker.io/kubezap/kafka-gateway:$TAG
```

The controller reads `WEBHOOK_GATEWAY_IMAGE` and `KAFKA_GATEWAY_IMAGE` at runtime to know which image to use when creating gateway Deployments.

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
