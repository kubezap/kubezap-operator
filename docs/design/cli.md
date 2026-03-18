# KubeZap CLI Design

## Overview

`kubezap` is a read-only command-line tool for inspecting KubeZap resources in a Kubernetes cluster. It provides richer views than `kubectl get` for KubeZap-specific resources, with FlowRun history querying as the primary use case.

`kubectl get flowruns` surfaces only the fields declared in `+kubebuilder:printcolumn` markers: flow name, phase, and age. It cannot filter by trigger, filter by phase, show per-step timelines, or live-tail completions. `kubezap history` fills this gap.

**Scope**: read-only. All commands are queries; no write, patch, or delete operations are performed. Write operations (editor-backed `kubezap create`, template instantiation) are deferred to a future release.

**Primary use cases**:

1. Query FlowRun history with filters (trigger name, flow name, phase, time window)
2. Inspect a single FlowRun with a per-step execution timeline
3. Live-tail FlowRun completions during active development
4. View enriched status tables for Triggers, Flows, and Integrations

**Distribution**: Released as a `kubectl-kubezap` binary so it is invocable as both `kubezap <command>` (standalone) and `kubectl kubezap <command>` (kubectl plugin convention). Multi-platform binaries are produced via Goreleaser and released alongside each operator image tag.

---

## Command Reference

### `kubezap history` — List FlowRuns

```
kubezap history [-n <namespace>] [--trigger <name>] [--flow <name>]
                [--phase <phase>] [--since <duration>] [-o <format>]
```

Lists FlowRun resources with optional filters. Results are sorted by creation timestamp descending (newest first).

**Flags**:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--namespace` | `-n` | current context namespace | Namespace to query. Use `-n ""` or `--all-namespaces` for all namespaces. |
| `--all-namespaces` | `-A` | `false` | Query all namespaces. |
| `--trigger` | | | Filter by originating trigger name (`spec.triggerRef.name`). |
| `--flow` | | | Filter by flow name (`spec.flowRef.name`). |
| `--phase` | | | Filter by phase. Valid values: `Pending`, `Running`, `Succeeded`, `Failed`, `Cancelled`. |
| `--since` | | | Only show FlowRuns created within this duration (e.g., `1h`, `30m`, `24h`). |
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml`. |
| `--watch` | `-w` | `false` | Live-tail: watch for FlowRun completions and print new rows as they arrive. |

**Default table output**:

```
NAME                              TRIGGER            FLOW              PHASE      DURATION   AGE
order-123-20260318-abc4f          order-webhook      order-router      Succeeded  0.45s      2h
order-124-20260318-dd12c          order-webhook      order-router      Failed     1.23s      2h
nightly-20260318-020000           nightly-report     generate-report   Succeeded  12.3s      6h
kafka-orders-p0-offset-18842      order-events       process-order     Succeeded  0.89s      3h
```

**Column definitions**:

| Column | Source | Notes |
|--------|--------|-------|
| `NAME` | `metadata.name` | |
| `TRIGGER` | `spec.triggerRef.name` | `-` if not set |
| `FLOW` | `spec.flowRef.name` | |
| `PHASE` | `status.phase` | |
| `DURATION` | `status.completionTime - status.startTime` | `-` if not yet complete |
| `AGE` | `metadata.creationTimestamp` | Human-readable elapsed time |

**Examples**:

```bash
# List all FlowRuns in the current namespace
kubezap history

# Filter by trigger name and show only failures
kubezap history --trigger order-webhook --phase Failed

# Show FlowRuns from the last 2 hours across all namespaces
kubezap history -A --since 2h

# Output as JSON
kubezap history --flow order-router -o json

# Live-tail completions
kubezap history --watch
```

---

### `kubezap history <flowrun-name>` — Single FlowRun Detail

```
kubezap history <flowrun-name> [-n <namespace>] [-o <format>]
```

Displays detailed information for a single FlowRun including trigger metadata, overall timing, and a per-step execution timeline.

**Flags**:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--namespace` | `-n` | current context namespace | Namespace of the FlowRun. |
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml`. |

**Output format**: see [Step Timeline Format](#step-timeline-format) below.

**Examples**:

```bash
# Show detail for a specific FlowRun
kubezap history order-123-20260318-abc4f

# Show in YAML format (full object)
kubezap history order-123-20260318-abc4f -o yaml
```

---

### `kubezap history --watch` — Live Tail

```
kubezap history --watch [-n <namespace>] [--trigger <name>] [--flow <name>]
```

Opens a watch on FlowRun resources and prints a new table row for each FlowRun that transitions to a terminal phase (`Succeeded`, `Failed`, `Cancelled`). Press Ctrl+C to stop.

The output format is the same as `kubezap history` list output. Filters (`--trigger`, `--flow`) are applied client-side.

---

### `kubezap triggers` — List Triggers

```
kubezap triggers [-n <namespace>] [-o <format>]
```

Lists Trigger resources with enriched status. Surfaces cross-referenced state that requires multiple `kubectl` commands to assemble (last fired time, active FlowRun count, effective GC policy).

**Flags**:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--namespace` | `-n` | current context namespace | Namespace to query. |
| `--all-namespaces` | `-A` | `false` | Query all namespaces. |
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml`. |

**Default table output**:

```
NAME              TYPE      STATUS     LAST FIRED         ACTIVE   GC POLICY
order-webhook     webhook   Accepted   2m ago             3        max:10/50 ttl:24h/72h
nightly-report    cron      Accepted   6h ago             0        ttl:24h/72h (default)
order-events      pubsub    Accepted   30s ago            1        max:10/50 ttl:24h/72h
stale-trigger     webhook   Pending    never              0        default
```

**Column definitions**:

| Column | Source | Notes |
|--------|--------|-------|
| `NAME` | `metadata.name` | |
| `TYPE` | `spec.type` | `webhook`, `cron`, or `pubsub` |
| `STATUS` | First `True` condition reason | `Accepted`, `Pending`, or `Error: <reason>` |
| `LAST FIRED` | `status.lastTriggeredTime` | Human-readable elapsed; `never` if not set |
| `ACTIVE` | Count of FlowRuns with `status.phase=Running` for this trigger | Requires listing FlowRuns by label |
| `GC POLICY` | `spec.flowRunGC` fields | Merged display of count limits and TTLs; `default` if unset |

**Examples**:

```bash
kubezap triggers
kubezap triggers -n production -o json
```

---

### `kubezap flows` — List Flows

```
kubezap flows [-n <namespace>] [-o <format>]
```

Lists Flow resources with enriched status. The `LAST USED` column is derived from the most recent FlowRun referencing each Flow.

**Flags**:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--namespace` | `-n` | current context namespace | Namespace to query. |
| `--all-namespaces` | `-A` | `false` | Query all namespaces. |
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml`. |

**Default table output**:

```
NAME               STEPS   READY   LAST USED
order-router       4       True    2m ago
generate-report    2       True    6h ago
process-order      3       True    30s ago
broken-flow        2       False   never
```

**Column definitions**:

| Column | Source | Notes |
|--------|--------|-------|
| `NAME` | `metadata.name` | |
| `STEPS` | `len(spec.steps)` | |
| `READY` | `Ready` condition status | `True`, `False`, or `Unknown` |
| `LAST USED` | Most recent FlowRun `metadata.creationTimestamp` with matching `spec.flowRef.name` | `never` if no FlowRuns found |

**Examples**:

```bash
kubezap flows
kubezap flows -A -o json
```

---

### `kubezap integrations` — List Integrations

```
kubezap integrations [-n <namespace>] [-o <format>]
```

Lists Integration resources with enriched status including gateway Deployment readiness and plugin health probe results.

**Flags**:

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--namespace` | `-n` | current context namespace | Namespace to query. |
| `--all-namespaces` | `-A` | `false` | Query all namespaces. |
| `--output` | `-o` | `table` | Output format: `table`, `json`, `yaml`. |

**Default table output**:

```
NAME              TYPE     GATEWAY STATUS       PLUGIN HEALTH
kafka-cluster     kafka    Ready (2/2)          n/a
my-rabbitmq       plugin   Ready (1/1)          Healthy
broken-plugin     plugin   Degraded (0/1)       Unhealthy
nats-broker       nats     Ready (1/1)          n/a
```

**Column definitions**:

| Column | Source | Notes |
|--------|--------|-------|
| `NAME` | `metadata.name` | |
| `TYPE` | `spec.type` | `kafka`, `nats`, `amqp`, `plugin`, etc. |
| `GATEWAY STATUS` | Associated Deployment `status.readyReplicas/replicas` | `Ready`, `Degraded`, or `NotFound` |
| `PLUGIN HEALTH` | `GET /healthz` on plugin Service; `n/a` for built-in types | `Healthy`, `Unhealthy`, or `n/a` |

**Examples**:

```bash
kubezap integrations
kubezap integrations -n staging -o yaml
```

---

### `kubezap version` — Version Information

```
kubezap version
```

Prints the CLI version and the operator version (from the controller Deployment image tag).

**Output**:

```
kubezap CLI:      v0.3.0 (git: abc1234)
operator image:   docker.io/kubezap/controller:v0.3.0
namespace:        kubezap-system
```

The operator image tag is read from the `kubezap-controller-manager` Deployment in the namespace where the operator is installed. If the Deployment is not found, the operator version field is omitted.

**Flags**: none (output format is always plain text for this command).

---

## Output Formats

All commands that return resource lists support three output formats via the `--output` / `-o` flag.

### Table (default)

Human-readable columnar output using fixed-width columns. Long values are truncated with `...` to fit column widths. Empty or unavailable fields are shown as `-`.

Column widths are adaptive: each column is sized to the maximum width of its values (with a minimum equal to the column header length), up to a per-column cap.

### JSON (`-o json`)

For list commands: a JSON array of objects, one per resource. Each object contains at minimum the fields shown in the table plus the full `metadata`, `spec`, and `status` from the Kubernetes resource.

For detail commands (`kubezap history <name>`): the full resource object serialized to JSON.

Output is formatted with two-space indentation.

### YAML (`-o yaml`)

Same as JSON but serialized as YAML. For list commands, a YAML list of objects.

---

## Kubeconfig and Context

`kubezap` follows the same kubeconfig resolution order as `kubectl`:

1. `--kubeconfig` flag (explicit path)
2. `KUBECONFIG` environment variable (colon-separated list of paths, merged)
3. `~/.kube/config` (default)
4. In-cluster service account (when running inside a pod)

**Context selection**:

- `--context <name>`: override the active context from the kubeconfig file.
- If `--context` is not specified, the current context in the kubeconfig is used.

**Namespace selection**:

- `-n <namespace>` / `--namespace <namespace>`: override the namespace from the active context.
- `-A` / `--all-namespaces`: query all namespaces (equivalent to `-n ""`).
- If neither flag is specified, the namespace from the active context is used. If the context has no namespace set, `default` is used.

All three flags (`--kubeconfig`, `--context`, `-n`) are persistent flags on the root command and are available for every subcommand.

---

## Step Timeline Format

For `kubezap history <flowrun-name>`, the table output renders a per-step execution timeline after the FlowRun summary header.

### Header

```
FlowRun:   order-123-20260318-abc4f
Flow:      order-router
Trigger:   order-webhook
Phase:     Succeeded
Started:   2026-03-18 14:01:02 UTC
Duration:  0.45s
```

### Step Timeline

```
STEP                  PHASE      STARTED    DURATION   ATTEMPTS   RESULTS
enrich-order          Succeeded  +0.00s     0.31s      1          id=ORD-123 customer=Acme Corp
notify-slack          Succeeded  +0.31s     0.14s      1          -
validate-payload      Skipped    -          -          0          -
```

**Column definitions**:

| Column | Source | Notes |
|--------|--------|-------|
| `STEP` | `status.steps[].name` | |
| `PHASE` | `status.steps[].phase` | `Pending`, `Running`, `Succeeded`, `Failed`, `Skipped`, `Waiting` |
| `STARTED` | `status.steps[].startTime - status.startTime` | Offset from FlowRun start; `-` if not started |
| `DURATION` | `status.steps[].completionTime - status.steps[].startTime` | `-` if not complete |
| `ATTEMPTS` | `status.steps[].attempts` | |
| `RESULTS` | `status.steps[].results` rendered as `key=value` pairs | `-` if empty |

**Phase icons** (used in terminal output when color is supported):

| Phase | Icon |
|-------|------|
| `Succeeded` | `✓` |
| `Failed` | `✗` |
| `Running` | `▶` |
| `Skipped` | `○` |
| `Waiting` | `⏸` |
| `Pending` | `.` |

When color is not supported (non-TTY output or `NO_COLOR` env var set), icons are omitted and the phase name is printed as plain text.

If `status.message` is set on a failed step, it is printed below the step row, indented:

```
  message: HTTP 503 after 3 retries: connection refused
```

---

## Package Structure

```
cmd/kubezap/
  main.go               # Binary entry point: root cobra command, persistent flags, subcommand registration

internal/cli/
  client.go             # BuildClient(): kubeconfig loading, scheme registration, active namespace resolution
  history.go            # history command implementation (list, detail, watch)
  triggers.go           # triggers command implementation
  flows.go              # flows command implementation
  integrations.go       # integrations command implementation
  version.go            # version command implementation
  output/
    table.go            # Table writer abstraction (wraps text/tabwriter or tablewriter)
    json.go             # JSON output formatter
    yaml.go             # YAML output formatter
    timeline.go         # Step timeline renderer for history <name>
    format.go           # Duration, age, phase icon helpers shared across formatters
```

**Import graph**:

```
cmd/kubezap/main.go
  → github.com/spf13/cobra
  → github.com/yourname/kubezap/internal/cli
      → github.com/yourname/kubezap/api/v1alpha1
      → sigs.k8s.io/controller-runtime/pkg/client
      → k8s.io/client-go/tools/clientcmd
```

The CLI imports `api/v1alpha1` types directly (same module), avoiding any versioning or code generation overhead.

---

## Distribution

### Binary Naming

The released binary is named `kubectl-kubezap`. When placed on `$PATH`, it is invocable as:

- `kubezap <command>` (standalone)
- `kubectl kubezap <command>` (kubectl plugin convention — kubectl discovers binaries matching `kubectl-*` on `$PATH`)

Both invocation forms are equivalent; the binary name does not affect behavior.

### Goreleaser Configuration Outline

```yaml
# .goreleaser.yaml (relevant CLI section)
builds:
  - id: kubezap-cli
    binary: kubectl-kubezap
    main: ./cmd/kubezap
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/yourname/kubezap/internal/cli.Version={{.Version}}
      - -X github.com/yourname/kubezap/internal/cli.GitCommit={{.ShortCommit}}

archives:
  - id: kubezap-cli
    builds: [kubezap-cli]
    name_template: "kubectl-kubezap_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
```

### Installation

**Direct download (recommended)**:

```bash
# Linux amd64
curl -Lo kubectl-kubezap https://github.com/yourname/kubezap/releases/latest/download/kubectl-kubezap_linux_amd64.tar.gz
tar -xzf kubectl-kubezap_linux_amd64.tar.gz kubectl-kubezap
chmod +x kubectl-kubezap
sudo mv kubectl-kubezap /usr/local/bin/
```

**kubectl plugin via krew** (future):

```bash
kubectl krew install kubezap
```

**Verify installation**:

```bash
kubezap version
# or
kubectl kubezap version
```

---

## Dependencies

### Already in `go.mod`

| Package | Current status | Role |
|---------|---------------|------|
| `github.com/spf13/cobra v1.8.1` | `indirect` | CLI framework; promote to direct via `go get github.com/spf13/cobra` |
| `sigs.k8s.io/controller-runtime` | direct | Kubernetes client |
| `k8s.io/client-go` | direct | `clientcmd` for kubeconfig loading |
| `k8s.io/apimachinery` | direct | `metav1` types |

### To Add

| Package | Purpose |
|---------|---------|
| `github.com/olekukonko/tablewriter` | Table rendering with column alignment. Add via `go get github.com/olekukonko/tablewriter` when implementing the output layer. Not required for the initial scaffold (stub implementations use `fmt.Printf`). |

### Not Needed

The CLI does not require `controller-runtime/pkg/manager` or any webhook/metrics infrastructure. Only `sigs.k8s.io/controller-runtime/pkg/client` and `k8s.io/client-go/tools/clientcmd` are needed from the Kubernetes stack.

---

## Future Work

The following capabilities are explicitly deferred and outside the scope of the initial implementation.

**`kubezap create`**: Opens `$EDITOR` with a pre-filled YAML template for a given resource kind. Deferred until the read-only command set is stable and the API surface is finalized.

```bash
# Deferred
kubezap create trigger --type webhook --name my-webhook
kubezap create flow --name my-flow
```

**Plugin marketplace search**: `kubezap plugins search <keyword>` — queries a community registry for available Integration plugins. Depends on the marketplace/catalog infrastructure being defined.

**`kubezap logs <flowrun-name>`**: Stream step execution logs from the controller. Requires the operator to expose a log streaming API or emit structured logs queryable by FlowRun name, which is not currently implemented.

**Interactive mode**: A TUI (terminal UI) mode for browsing FlowRun history with keyboard navigation. Not planned for v1 of the CLI.
