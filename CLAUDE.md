# KubeZap — CLAUDE.md

## Project Schedule

See [`docs/schedule.md`](docs/schedule.md) for the current task checklist, prioritized by feature/module. Start here at the beginning of each session to know what has been done and what is next.

## Project Overview

KubeZap is an enterprise-grade Kubernetes operator providing declarative workflow automation inspired by Zapier. Users define automations via CRDs instead of a web UI. Think "Zapier meets Camunda, but Kubernetes-native."

**Long-term goal**: Build to a level suitable for acquisition by a large company that can commercialize it. Target: OperatorHub-published, enterprise-ready, pluggable marketplace of integrations.

## Claude Interaction Guidelines

Claude should behave as a senior Kubernetes platform architect assisting with KubeZap.

Primary responsibilities when assisting with this project:

- architecture design
- CRD schema design
- controller-runtime patterns
- distributed workflow execution
- reliability and scalability
- Kubernetes operator best practices

Claude should avoid spending large amounts of capacity on:

- trivial syntax edits
- formatting changes
- variable renaming
- repeated analysis of large code blocks
- incremental code rewrites

Claude should encourage the following workflow when implementing features:

1. Architecture discussion
2. Design/specification
3. Implementation task breakdown
4. Code review

If a request jumps directly to implementation without design context, Claude should suggest a short design discussion first.

## Tech Stack

- Go 1.24
- Kubebuilder v4 (`sigs.k8s.io/controller-runtime v0.21`)
- Operator SDK (OLM bundle generation scaffolded)
- Ginkgo v2 + Gomega (testing)
- OpenTelemetry + Prometheus (observability)
- Domain: `kubezap.io`, API group: `automation.kubezap.io`
- Local cluster: k3s

## Runtime Architecture

Four separate binaries/images — see `docs/architecture.md` for full design:

| Binary                               | Image                     | Purpose                                                                                                               |
| ------------------------------------ | ------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `cmd/main.go`                        | `kubezap/controller`      | Kubernetes operator: reconciles CRDs, manages gateway Deployments, delegates HTTP steps to executor. **Does NOT make outbound HTTP calls.** |
| `cmd/webhook-gateway/main.go`        | `kubezap/webhook-gateway` | HTTP server: watches Trigger CRDs, registers routes dynamically, creates FlowRuns                                     |
| `cmd/kafka-gateway/main.go`          | `kubezap/kafka-gateway`   | Kafka consumer: watches Trigger CRDs, manages topic subscriptions, creates FlowRuns                                   |
| `cmd/http-executor/main.go`          | `kubezap/http-executor`   | HTTP step executor: receives fully-resolved HTTP requests from controller via internal `POST /execute` RPC, executes with SSRF blocklist, returns results. Minimal RBAC (no secrets, no RBAC management). One Deployment per namespace. |
| `cmd/main.go` (controller, extended) | —                         | Kubernetes resource event triggers handled IN the controller via dynamic informers — no separate gateway image needed |

Key decisions:
- Gateways are separate pods managed by the controller — NOT embedded in the controller pod
- One webhook gateway Deployment per namespace (shared across all webhook Triggers in that namespace)
- One Kafka gateway Deployment per (namespace × Kafka Integration/cluster)
- HTTP executor receives fully-resolved requests (with credentials) from controller via internal HTTP RPC (`POST /execute`); credentials never written to etcd. Channel secured by NetworkPolicy (mandatory) + mTLS (opt-in via `--executor-mtls=true`, for clusters without a service mesh)
- Gateways configure themselves by watching Trigger CRDs directly (no intermediate ConfigMap)
- Gateway → Flow communication via `FlowRun` CRD (controller watches and executes)
- FlowRun naming: webhook `<trigger>-<timestamp>-<random>`, kafka `<trigger>-p<partition>-offset-<offset>` (dedup key), cron `<trigger>-<scheduled-time>`
- FlowRun GC: `spec.ttlAfterFinished` per FlowRun, operator-level `--flowrun-ttl-succeeded` / `--flowrun-ttl-failed` flags (defaults 24h/72h), or `spec.maxFlowRuns` on Trigger; annotate with `kubezap.io/retain=true` to exempt
- Publish step action: `type: publish` with `integrationRef` + `topic` + `body` — controller calls plugin's `/publish` endpoint
- HPA on webhook gateway; KEDA recommended for Kafka gateway (partition-bounded scaling)
- `WATCH_NAMESPACES` env var controls scope: empty = OwnNamespace (default, least privilege), `*` = AllNamespaces (secrets restricted to `kubezap.io/managed=true` namespaces), comma-list = MultiNamespace, single value = SingleNamespace
- OwnNamespace/SingleNamespace modes use Role (not ClusterRole) — important for OpenShift and OperatorHub certification
- All four OLM install modes must be supported in the CSV bundle
- Webhook auth: HMAC, bearer token, OIDC/JWT, Basic, mTLS, API-key header, IP allowlist — all per-Trigger, configured via spec.webhook.auth (see docs/guides/webhook-security.md)
- Observability: Prometheus metrics + structured JSON access logs + OTel traces (see docs/guides/observability.md)
- Source IPs: in structured access logs only — NOT as Prometheus label values (cardinality). `/24`-bucketed source_range on ip_blocked metric only.
- ServiceMonitor: NOT auto-created by operator — user responsibility. Documented in observability guide.

## Core CRDs

| CRD           | Purpose                                                                   | Docs                      |
| ------------- | ------------------------------------------------------------------------- | ------------------------- |
| `Trigger`     | Event source (webhook/cron/kafka/resource) → references a Flow            | `docs/api/trigger.md`     |
| `Flow`        | Ordered steps with conditional logic and data transforms                  | `docs/api/flow.md`        |
| `FlowRun`     | Execution instance created by gateways; controller picks up and runs      | `docs/api/flowrun.md`     |
| `Integration` | External system connections and credentials; subscriber + publisher roles | `docs/api/integration.md` |
| `Step`        | Optional reusable/observable action unit                                  | Future — not yet designed |

## Trigger Types

Built-in:
1. **Webhook** — HTTP endpoint exposed by the operator
2. **Cron** — Scheduled execution
3. **Kafka** — Consumer on a topic (dedicated gateway; KEDA-scalable)
4. **AMQP** — RabbitMQ, ActiveMQ Artemis, Azure Service Bus, IBM MQ (beta)
5. **NATS** — NATS JetStream (beta)
6. **Resource** — Kubernetes resource events via dynamic informers (alpha; see known limitations in `docs/tech-debt/`)

Planned: GCP Pub/Sub, Solace (non-AMQP), S3/Git events, additional brokers via plugin model

## Plugin / Extensibility Model

- `Integration` CRD supports `type: plugin` — operator manages the plugin Deployment, grants it namespace-scoped RBAC
- Plugin contract (subscriber): watch Trigger CRDs → create FlowRun CRDs when events arrive; dedup key in FlowRun name
- Plugin contract (publisher): expose `POST /publish` HTTP endpoint on `spec.plugin.publisherPort` (default 8090)
- Plugin health check: `GET /healthz` → 200 (used as Deployment readiness probe)
- Operator injects: `KUBEZAP_NAMESPACE`, `KUBEZAP_INTEGRATION_NAME`, `KUBEZAP_PUBLISHER_PORT`, `KUBEZAP_LOG_LEVEL`
- Secrets referenced in `spec.plugin.secretRefs` are injected as env vars via `envVarMappings`
- Built-in types: `kafka` (`kubezap/kafka-gateway` image), `amqp` (`kubezap/amqp-gateway` image, beta), `nats` (`kubezap/nats-gateway` image, beta)
- Plugin image trust model: operator does not verify images — document this as a security consideration
- Future: marketplace/catalog of community integration plugins

## Flow Design Goals

- Flexible but user-friendly
- Steps have typed inputs/outputs; outputs chain to downstream step inputs
- Conditional logic and data transformations are first-class features
- Design approach: prototype iteratively, document-then-implement, settle on final shape before committing to a stable API

## Development Philosophy

- **Documentation-driven development**: Define interfaces in docs/specs first, implement against them. Iterate between prototypes and docs before finalizing.
- **Design records for non-trivial changes**: Before implementing a new CRD field, controller, gateway, binary, or any change to reconciliation logic, state transitions, security posture, or external dependencies, produce a design record per `docs/guides/design-process.md` (six required sections, filed under `docs/design/`). Skip only for typo fixes, test-only additions, and doc-only changes.
- **Declarative everything**: All configuration via CRDs — no imperative runtime APIs
- **Idempotent and resilient**: All reconcilers must be safe to re-run at any time
- **Observability from day one**: All meaningful operations emit Prometheus metrics + OTel traces
- **Enterprise-grade**: Security contexts, RBAC, HA, multi-namespace from the start

## Developer Workflow

```bash
make generate          # Regenerate DeepCopy and manifests from Go types
make manifests         # Generate CRD YAML from Go type markers
make test              # Unit tests with envtest (run frequently)
make lint              # golangci-lint (run before committing)
make build             # Build operator binary
make docker-build      # Build container image (distroless/static:nonroot)
make deploy            # Deploy to current kubeconfig context (k3s locally)
make test-e2e          # E2E tests via Kind cluster
make bundle            # Generate OLM bundle for OperatorHub
```

After any change to types in `api/`, always run: `make generate && make manifests`

## Repository Structure

```
api/v1alpha1/         # CRD Go types — source of truth for CRD schema
cmd/main.go           # Operator entry point, manager setup
internal/controller/  # Reconciler implementations
config/               # Kustomize manifests (CRDs, RBAC, deploy config)
config/samples/       # Example CRs for manual testing
docs/                 # Project documentation (aim for Confluent-for-K8s quality)
hack/                 # Build and codegen scripts
test/                 # Unit and E2E test infrastructure
```

## Coding Conventions

- Standard Go style, enforced by golangci-lint (see `.golangci.yml`)
- Reconcilers use controller-runtime patterns: `ctrl.Result{}` on success, requeue with backoff on transient errors
- Status updates use `metav1.Condition` (standard Kubernetes conditions pattern)
- New CRD checklist:
  1. Define types in `api/v1alpha1/`
  2. Add kubebuilder markers (`+kubebuilder:...`)
  3. Run `make generate && make manifests`
  4. Add RBAC markers to reconciler
  5. Add sample CR in `config/samples/`
  6. Write Ginkgo tests
- Tests use Ginkgo BDD style: `Describe`/`Context`/`It` blocks with Gomega matchers
- **`+kubebuilder:rbac` markers must be free-floating, not attached to a declaration.** They are package-scoped: put them in their own comment block separated by a blank line from the type/func/var below. If they end up inside a declaration's doc comment (e.g. directly above `type FooReconciler struct`), controller-gen **silently ignores them** — no error, `make manifests` just quietly omits those rules and the operator ships missing permissions. This bit us once already (see `docs/schedule.md` §30):

  ```go
  // +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch

  // FooReconciler reconciles ...   ← blank line above is what makes the markers work
  type FooReconciler struct {
  ```

  After adding or changing RBAC markers, always confirm the rule actually landed in `config/rbac/role.yaml` — do not assume `make manifests` picked it up.

## Code Style / Go

- After any code generation or edit in Go files, always run `gofmt -w .` and `goimports -w .` before committing. Never leave formatting or import ordering for a separate fix step.
- golangci-lint enforces style — run `make lint` before committing.

## Testing

- Always run `go test ./...` after implementing features or fixing bugs. Do not commit until tests pass.
- If tests fail, triage whether failures are pre-existing or new before attempting fixes. If pre-existing, note it and move on; do not spend capacity fixing unrelated failures.
- Unit tests use Ginkgo BDD style; E2E tests use Kind/k3s cluster. See `make test` and `make test-e2e`.

## Code Review Strategy

- Follow `docs/guides/code-review-strategy.md` for all code reviews. Never issue an open-ended "review the codebase" or "review this file" prompt — name one target invariant, concern, or review type from the guide (e.g. idempotency audit, state transition correctness, security boundary audit) as the entry point. Targeted reviews catch real bugs; full-repo scans produce too much noise to act on.

## Pre-Merge Checklist

- Before marking any non-trivial feature complete, work through `docs/guides/pre-merge-checklist.md`. All **[GATE]** items (invariant verification, spec drift check, lint/test, documentation) must be satisfied before merge. All **[FILE]** items (test coverage gaps) require a corresponding `docs/schedule.md` entry, not just a mention in the PR description.

## OpenShift / OperatorHub

- OLM bundle generation already scaffolded via Operator SDK
- Security must comply with OpenShift's restricted SCC (already configured: non-root, no privilege escalation, read-only root FS where possible)
- Target both vanilla Kubernetes and OpenShift
- Install paths to support: OLM/OperatorHub, Helm chart, raw manifests

## Documentation Standards

- Target quality: Confluent for Kubernetes operator docs
- All new CRDs get a dedicated doc page covering: purpose, spec fields, status fields, examples, limitations
- API changes documented before implementation (doc-driven development)
- Keep `docs/overview.md` up to date as the project evolves

## Rate Limit Guardrails

**Hard rule: do not start a prompt that is likely to hit API rate limits.** Respond with a warning instead.

Before starting any task, estimate whether it would require:

- More than **5 parallel agents** in a single launch
- More than **~15 total Agent tool calls** across the full task
- Agents that each need to read and edit **10+ large files**
- A chain of agents where each spawns further sub-agents (fan-out trees)

If the task meets any of these criteria, **stop and respond with**:

> "This prompt would likely hit your API rate limits. Here's why: [brief reason]. To avoid that, I suggest breaking it into these smaller steps: [list]. Which part should I start with?"

Do not silently start the work and let agents fail mid-execution — that wastes more usage than stopping upfront.

**Safe thresholds (proceed without warning):**

- ≤ 5 parallel agents launched at once
- Sequential tasks of any length (rate limits apply per unit time, not total)
- Read-only research agents (they consume far fewer tokens than code-writing agents)

**When in doubt, ask** — a one-line check-in ("this looks large, want me to split it?") is always better than a half-finished parallel run.

## Capacity Guardrails

Claude should actively help conserve conversation capacity.

If a prompt would require large token usage but provide limited value, Claude should:

1. Explain why the request is inefficient
2. Suggest a more efficient prompt
3. Ask the user for smaller or more focused inputs

Examples of inefficient patterns:

- repeatedly pasting large files
- analyzing entire repositories
- many incremental code edits
- generating large code blocks unnecessarily

Preferred alternatives:

- summarize relevant code sections
- focus on specific components or functions
- generate implementation plans instead of full code
- break large tasks into smaller steps

Claude should avoid unnecessary verbosity unless detailed explanation is explicitly requested.

Before generating large outputs, Claude should consider whether a smaller architectural discussion or implementation plan would be more efficient.

## Autonomy

- Operate autonomously: read, edit, create files, run `make` targets without asking first
- Check in before: major architectural decisions, anything touching git remote (push, PR), destructive operations
- Use parallel sub-agents for independent tasks (e.g., writing a CRD type while writing its docs simultaneously)

## Parallel Agent Guidelines

Parallel agents are powerful but create merge conflicts when they touch shared files. Follow these rules every time agents are spawned. The `/backlog` skill implements this protocol automatically — read it as the reference implementation.

### Step 1 — Audit file ownership before spawning

Before creating any agents, list every file each task will need to read **and write**. Tasks that share any writable file must be serialized, not parallelized.

**Hot files in this repo** — always treat these as serialized (one agent at a time, or handled by a dedicated wiring agent after all others finish):

- `cmd/main.go` — controller registration, scheme setup, flags
- `cmd/webhook-gateway/main.go`, `cmd/kafka-gateway/main.go`, etc. — gateway entry points
- `api/v1alpha1/groupversion_info.go` — scheme registration
- `go.mod` / `go.sum` — dependency changes
- `config/rbac/role.yaml`, `config/rbac/namespaced_role.yaml` — regenerated by `make manifests`
- `docs/schedule.md` — only one agent should mark items complete

**Safe to parallelize** — agents can work on these simultaneously without conflict:

- Distinct files in `internal/controller/` (one controller per agent)
- Distinct files in `internal/gateway/` (one gateway package per agent)
- Distinct files in `api/v1alpha1/` (one `_types.go` file per agent, never `groupversion_info.go`)
- Distinct doc files in `docs/`
- Distinct test files in `internal/controller/` or `test/`
- Distinct files in `config/samples/`

### Step 2 — Use worktree isolation for code-changing agents

When spawning an Agent tool call that will make code changes, set `isolation: "worktree"`. This gives each agent a clean filesystem copy and prevents mid-flight conflicts. Each agent must work in its own git worktree or branch — never share a working directory between parallel agents.

Do NOT use worktree isolation for read-only agents (research, code review, docs reading).

### Step 3 — Designate a wiring agent for shared entry points

If parallel agents each add new controllers, schemes, flags, or dependencies, do NOT let each agent edit `cmd/main.go` or `go.mod`. Instead:

1. All parallel agents complete their own package work and stop before touching shared entry points.
2. A single **wiring agent** runs last (sequentially), reads what each parallel agent produced, and wires everything into `cmd/main.go` and updates `go.mod`/`go.sum` in one pass.

### Step 4 — Sequential merge with validation gate

After all parallel agents complete, merge their work into main one branch at a time via **sequential rebase** (not merge commits) to avoid conflicts on hot files like `cmd/main.go`:

```bash
# after each merge:
go build ./...
# after all merges:
make test   # use make test, not go test ./... -count=1 — the latter includes E2E and times out
```

Do not merge the next branch until the current merge builds clean. Run `make test` once after all branches are merged (not after each — it is slow).

### Step 5 — Post-merge codegen

Run codegen once after all branches are merged, not once per agent:

```bash
make generate && make manifests
go build ./...
```

Running `make generate` inside parallel agents produces conflicting generated files. Only run it after all code changes are in.

### API rate limit awareness in parallel agents

When running parallel agents, stagger API-intensive operations (`go mod tidy`, `make test`, full test suites) so they do not all fire concurrently. If a sub-agent hits a rate limit, let the other agents complete first, then retry the failed agent.
