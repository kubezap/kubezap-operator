# KubeZap vs. Argo Workflows — Per-Step Execution Latency Benchmark Methodology

STORY-013. This document defines *how* to run the benchmark. It does not build a
harness, script, or fixture — that is later work. Everything below is prose
specification: scenario, versions, metrics, timestamps, and run parameters,
written so a later story can implement a harness against it without re-deriving
any of these decisions.

## 0. Why this benchmark exists

KubeZap's README and `docs/overview.md` currently assert that its
controller-resolves-in-memory + RPC-to-`http-executor` model is "lighter" than
Pod-per-step engines like Argo Workflows. That claim has never been measured.
This benchmark produces real numbers so the claim can be confirmed, qualified,
or walked back. It is scoped to a **go/no-go signal on the core architectural
claim**, not a comprehensive performance characterization of either system —
see §4 for why sequential-only, single-flow-at-a-time firing is sufficient for
that narrower goal.

## 1. Scenario

### 1.1 Shared target

Both systems call the same in-cluster echo target so the dependent variable is
purely "orchestration overhead," not variance in a downstream service. Reuse
the pattern already established in `test/e2e/feature_matrix_test.go` (read
for reference, not modified): a `mockoon/cli` Deployment + ConfigMap + Service,
serving a fixed-latency `GET /ok → 200 {"ok":true}` route. The benchmark's
Mockoon environment should mirror `fmMockoonConfigMapYAML`'s shape (one
`environment.json` route, `"latency": 0`, single replica) but should be a
dedicated fixture in the benchmark's own namespace — not the shared e2e
Mockoon instance — so benchmark runs never contend with (or are perturbed by)
the e2e suite.

Both KubeZap's `http-executor` and Argo's per-step pods must reach this same
Service DNS name (`<mockoon-svc>.<ns>.svc.cluster.local:3000/ok`) so network
path length is identical for both systems.

### 1.2 KubeZap side

One `Trigger` (`type: webhook`) referencing one `Flow` with three HTTP steps,
chained via `runAfter` so they execute strictly sequentially (not the
default parallel-unless-declared graph): `step1 runAfter: []`, `step2
runAfter: [step1]`, `step3 runAfter: [step2]`. Each step is `type: http`,
`method: GET`, `url: http://<mockoon-svc>/ok`, no auth (isolate pure dispatch
overhead — Integration/credential resolution is a separate, already-tested
concern per `feature_matrix_test.go`'s bearer-auth axis). A single webhook
POST to the gateway creates one `FlowRun`, which the controller resolves and
executes step-by-step, delegating each HTTP call to the namespace's
`http-executor` Deployment via the internal RPC.

### 1.3 Argo side — researched, not assumed

Argo Workflows offers two distinct mechanisms for an HTTP call step, and they
are **not interchangeable for this benchmark**:

- **`http` template type** (native "HTTP template"). This does *not* run
  per-step pods. It is executed by a shared **Argo Agent** pod (one Agent per
  *workflow*, not per step), which communicates with the workflow controller
  via a `WorkflowTaskSet` CR. The Agent processes each `http` step as a
  request against its own long-lived process — architecturally, this is
  actually a *lot* closer to KubeZap's own "steady-state executor process
  handles many step-dispatches via RPC" model than to "Pod-per-step." Using it
  as the Argo arm of this benchmark would not test the claim we're trying to
  test — it would compare two RPC-to-a-live-process models against each other.
- **Classic `container` (or `script`) template**, one pod per step, using an
  image with an HTTP client (`curlimages/curl`) as the step body. This is
  Argo's original and still-default execution model, and it is the actual
  target of the "Pod-per-step" characterization in KubeZap's own docs.

**Decision: use the classic `container` template model as the primary Argo
arm.** Build a `Workflow` with a `steps` template containing three sequential
stages (three separate `- - name: stepN` entries — a new outer list entry per
stage is what forces sequential execution in Argo's `steps` DAG shorthand;
entries within the same inner list run in parallel), each stage invoking a
distinct `container` template running `curlimages/curl` against
`http://<mockoon-svc>/ok`. This produces exactly three pods per Workflow run,
matching KubeZap's three step-dispatches structurally.

Record the `http` template / Agent model as an **optional secondary
comparison arm** worth running in a follow-up if the primary result is
surprising, but out of scope for the go/no-go call this story enables —
flag this explicitly in any report so nobody later conflates the two Argo
execution models when reading the results.

Note also: as of the pinned version (§2), Argo's classic pod layout by
default includes an `argoexec` init container plus a `wait` sidecar alongside
the `main` container (Emissary executor, the long-standing default). Argo
4.1 introduces an *opt-in* pod layout that drops the init container. Pin the
**default** (opt-in feature disabled) for the primary run, since that's what
the overwhelming majority of current Argo installs actually run today, and
what KubeZap's "lighter than Pod-per-step" claim is implicitly being compared
against. The opt-in leaner layout is a reasonable secondary-arm candidate for
the same follow-up as the `http` template, not this pass.

## 2. Argo Workflows version to pin

**`v4.1.3`**, released 2026-09-11 (per the project's GitHub Releases page,
explicitly marked `Latest`, not a pre-release/RC).

Rationale: Argo Workflows' own release documentation states the project
maintains release branches for only the **two most recent minor lines**, and
that for a "stable" install you should "use the latest patch version" within
whichever minor line you pick. `v4.1.3` is the latest patch on the current
newest minor line (`v4.1`); `v4.0.11` is the parallel latest-patch release on
the immediately preceding supported line, released the same day. `v4.1.3` is
the correct pin under the project's own stated policy.

**Confidence flag**: I could not find an explicit "this is the version we
recommend for new installs" sentence anywhere in Argo's docs beyond the
general "use the latest patch" policy statement — that general policy plus
the GitHub `Latest` release tag is what this pin is based on. Because this
document may be read weeks or months before a harness actually installs
Argo, whoever builds the harness should re-check
`https://github.com/argoproj/argo-workflows/releases` for a newer `4.1.x`
patch before installing, and must record the *actual* installed
`argo-workflows` version string in every run's output JSON (§5.3) regardless
of what's pinned here — never assume the pin stays accurate by the time the
harness runs.

Install both `argo-server`/CLI and the workflow-controller at this same
version (Argo's own compatibility requirement — mismatched versions between
controller and CLI/server are explicitly unsupported).

## 3. Metrics

### 3.1 Per-step p50/p95 latency

**KubeZap**: `FlowRun.status.steps[]` is a `[]StepRunStatus`
(`api/v1alpha1/flowrun_types.go`), each entry carrying `StartTime` and
`CompletionTime` (`*metav1.Time`, both optional, populated once the step
enters/leaves `Running`). Per-step latency = `CompletionTime - StartTime` for
each of the three named steps in that array, per `FlowRun`. Match steps by
their `Name` field (the Flow's step name), not by array index — the array
order should match declaration order but name-matching is the correct,
implementation-independent join key.

**Argo**: `Workflow.status.nodes` is a map (keyed by opaque node ID, not
step name) of node statuses, each carrying `startedAt`/`finishedAt`. Per-step
latency = `finishedAt - startedAt` for each node whose `templateName` (or
`displayName`) matches one of the three curl-step templates. The map
**also** contains a node for the outer `steps` template itself and the
top-level `Workflow` entry node — these must be filtered out (match on
`type: Pod` and `templateName` equal to one of the three known step template
names) so they don't pollute the per-step sample set with whole-workflow
durations.

Compute p50/p95 **per step position** (step1/step2/step3 separately, since a
later position could plausibly be slower if e.g. a shared executor queues
work, or faster on the Argo side if scheduler node/image caches are warm from
the prior pod) **and pooled across all three positions**, both reported.

### 3.2 Per-step resource overhead

Sampled via `kubectl top pod` polling — per the story's explicit instruction
to use this mechanism rather than cgroup/cAdvisor scraping (a legitimate
higher-fidelity alternative, but out of scope here).

**Polling interval — 5 seconds, with an explicit caveat.** `kubectl top pod`
is only as fresh as the cluster's metrics-server cache; metrics-server's own
`--metric-resolution` flag **defaults to 60s**. Given each benchmark run
(three chained HTTP calls against a zero-added-latency echo target) is
expected to complete in low single-digit seconds, a 60s metrics refresh
window means **no polling interval can attribute a resource sample to one
specific run** — the mismatch is structural, not a polling-frequency problem.
Two changes make the measurement meaningful instead of misleading:

1. Before benchmarking, reconfigure the benchmark cluster's metrics-server to
   `--metric-resolution=15s`. This is a documented, supported flag (not a
   hack) and 15s is a reasonable floor — going materially lower increases
   kubelet scrape load for no benefit here, since the metric this produces
   (§3.2 below) is a peak/distribution over a *sustained* run, not a
   per-second time series.
2. **Redefine the measured quantity accordingly**: report *peak pod
   memory/CPU observed via `kubectl top pod` sampled every 5s across the
   entire sequential run of all N firings for a system* (not per individual
   run). 5s is chosen to sample faster than the 15s metrics-server
   resolution — so a changed value is picked up within one poll rather than
   waited-out for up to 15s — while avoiding hammering the API server with a
   sub-1s loop that buys no additional signal (metrics-server won't have
   refreshed yet regardless). This yields "steady-state resource footprint
   under sustained sequential load," which is the honest thing this
   measurement approach can produce — not "resource cost of one step,"
   which it cannot.

**Known asymmetry — flag explicitly, do not average it away.** KubeZap's
`http-executor` is one steady-state Deployment pod that lives for the whole
benchmark; `kubectl top pod` sampling it is straightforward and the peak
reading is trustworthy. Argo's per-step pods are ephemeral — each is created,
runs a curl invocation against a sub-millisecond-latency target, and
terminates, plausibly within a single-digit number of seconds and sometimes
faster than one 15s metrics-server scrape. **Some or many Argo step-pods may
never be observed by `kubectl top pod` at all.** The harness must record, per
system, the count of *distinct pod names* that ever appeared in a `kubectl
top pod` sample versus the count of pods actually created (from `kubectl get
pods` or the Workflow's node list) — report this capture-rate number
alongside the peak memory/CPU figures every time, so a low capture rate for
Argo is visible rather than silently producing a falsely-low "Argo pods use
almost no memory" conclusion.

### 3.3 Cold-start cost

Defined as **two separate deltas per system**, because "cold start" here
actually bundles two different costs that KubeZap's architecture is
specifically betting are different in the two systems:

**(a) Trigger-to-pickup latency** — control-plane dispatch delay, independent
of any pod startup:
- KubeZap: `FlowRun.metadata.creationTimestamp` (set when the webhook gateway
  creates the CR — the earliest reliable, machine-recorded "trigger fired"
  instant) → `FlowRun.status.steps[0].StartTime` (first step, matched by
  declared step order, entering `Running`).
- Argo: `Workflow.metadata.creationTimestamp` (analogous point — workflow
  object creation, whether by `argo submit` or a Sensor/EventSource in a
  fuller pipeline; for this benchmark the harness creates the `Workflow`
  directly, so this is simply the harness's `kubectl apply`/API-create
  timestamp) → the first step-node's `startedAt` in `status.nodes`.

**(b) Dispatch-to-actual-execution latency** — this is the delta the
architectural claim is really about, so it must be captured explicitly
rather than folded into (a):
- KubeZap: time from the controller issuing the `POST /execute` RPC to
  `http-executor` actually beginning work on it. Since `http-executor` is a
  live, already-running pod (steady-state Deployment, not created per
  FlowRun), this delta is expected to be small and should be near-negligible
  — that is the architectural bet under test. If `http-executor` exposes a
  request-received log line or trace span with a timestamp, use that as the
  "execution start" instant; if not available at implementation time, this
  sub-delta may need to be approximated as the gap between the controller's
  step `StartTime` and the step's `CompletionTime` minus the target's known
  response latency, which is a weaker proxy — the harness-building story
  should verify whether `http-executor` emits a usable timestamp before
  falling back to the proxy.
- Argo: time from the step-node's `startedAt` (control-plane decision to run
  this step) to the underlying **Pod's** container actually running —
  `kubectl get pod <pod-name> -o
  jsonpath='{.status.containerStatuses[?(@.name=="main")].state.running.startedAt}'`
  (the `main` container specifically, not the `wait`/`init` containers, since
  that's the one running the curl command). This delta is exactly the
  pod-scheduling + image-pull + container-start tax that Argo pays per step
  and that KubeZap's steady-state executor model is designed to avoid — it
  is the single number most directly relevant to confirming or walking back
  the README's claim.

## 4. Run parameters

### 4.1 Sample size: 200 sequential trigger firings per system

Justification: treat "did this run's value fall in the tail beyond the p95
threshold" as a Bernoulli(0.05) draw. The standard rule of thumb for a
binomial-based quantity to be well-approximated as stable/normally
distributed (used broadly for proportion confidence intervals, e.g. in NIST's
engineering statistics guidance) is that the expected count of "successes" —
here, exceedances of the percentile threshold — should be **at least 10**:
`n × (1 − p) ≥ 10`. For p95: `n × 0.05 ≥ 10 → n ≥ 200`. p50 needs far fewer
(`n × 0.5 ≥ 10 → n ≥ 20`), so p95 is the binding constraint and **200** is
the number this benchmark targets.

This explicitly does **not** claim to produce a stable p99: the same rule
gives `n × 0.01 ≥ 10 → n ≥ 1000`, five times the sample size — out of scope
for this first pass. If a later story wants a defensible p99, budget for
≥1000 firings per system, not 200.

Per-step latency (§3.1) benefits further from this same 200-firing budget
without extra cost: since each run produces 3 step samples, 200 firings
yield 600 step-latency samples per system — comfortably past the 200-sample
threshold even when split by step position (step1/step2/step3 individually
still get 200 samples each).

### 4.2 Sequential (not concurrent) firing

Fire triggers one at a time, waiting for each `FlowRun`/`Workflow` to reach a
terminal phase before submitting the next. This is sufficient for the go/no-
go question this benchmark answers — "is the core per-step dispatch overhead
meaningfully different between the two architectures" — because:

- It isolates per-step/per-run dispatch and pod-startup cost from unrelated
  confounds that concurrent firing would introduce: KubeZap's `http-executor`
  serializing or queueing concurrent RPCs, the Argo workflow-controller's own
  work-queue backlog under concurrent reconciles, or node-level scheduler
  contention from many pods landing at once. All of those are real and
  interesting, but they're throughput/scaling questions, not the base
  per-step-latency question this story is chartered to answer.
- A go/no-go call on the architectural claim only needs to know whether one
  flow, run alone, is faster end-to-end and per-step under one model versus
  the other. If it isn't, concurrent-load characterization becomes moot for
  the "lighter" claim specifically (though still useful for other purposes).
- Concurrent-firing characterization is naturally a **separate follow-up
  story** once this base result exists — it has its own methodology
  questions (how many concurrent firings, how to attribute resource samples
  when multiple runs overlap in the same `kubectl top pod` window, etc.) that
  don't have clean answers reused from this document.

### 4.3 Raw data collection: one JSON file per run

For each of the 200 firings per system, write one JSON file (not just
contribute to a running aggregate), named
`<system>-run-<3-digit-index>.json` (e.g. `kubezap-run-014.json`,
`argo-run-014.json`), so a later story can recompute every statistic
independently if an aggregation bug is suspected. Each file should contain,
at minimum:

- `system`: `"kubezap"` or `"argo"`, plus the actually-installed version
  string of the relevant component (KubeZap controller image tag; Argo
  Workflows version actually installed — see §2's confidence flag).
- `run_index` and a wall-clock `run_started_at` (harness-local timestamp,
  for sequencing/debugging, distinct from the in-cluster timestamps below).
- The full raw object the harness fetched to derive metrics — i.e. the
  complete `kubectl get flowrun <name> -o json` output, or `kubectl get
  workflow <name> -o json` output — stored verbatim, not just the fields the
  harness happened to parse out. This is the specific mechanism that lets a
  later story re-derive metrics independently: if the parsing logic had a
  bug, the raw object is still there to reprocess.
- The derived fields this document defines: trigger-fired timestamp,
  first-step-started timestamp, per-step start/completion timestamps (by
  name/template), and the dispatch-to-execution sub-delta of §3.3(b) with
  whatever raw pod/container timestamp it was derived from.
- For resource overhead (§3.2), which is a *sustained-run* measurement rather
  than a per-run one: store the raw `kubectl top pod` sample series
  (timestamped) in a separate file per system (e.g.
  `kubezap-resource-samples.json`, `argo-resource-samples.json`) covering
  the whole 200-firing sequential window, plus the pod-capture-rate count
  described in §3.2, rather than trying to fold a sustained-load measurement
  into each individual run's file.

## 5. Summary of what a later harness story must still decide

This document intentionally leaves the following to the implementation story,
since they're harness-construction details rather than methodology:
how exactly triggers are fired programmatically (`kubectl apply` loop vs. a
Go test harness), how `kubectl top pod` polling is backgrounded and stopped
cleanly, exact retry/backoff if a firing's terminal phase isn't reached
within some timeout, and where result JSON files are written/retained.
