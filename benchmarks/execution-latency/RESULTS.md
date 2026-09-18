# Execution Latency Benchmark — Results

## 1. Methodology

Full methodology, scenario definition, and run-parameter justification: [`METHODOLOGY.md`](./METHODOLOGY.md). In short: a webhook-triggered 3-step sequential HTTP flow, both systems calling the same zero-added-latency in-cluster Mockoon target, 200 sequential firings per system (KubeZap via its `Trigger`/`Flow`/`FlowRun` path, Argo via a classic `container`-template `steps` Workflow pinned to `v4.1.3`), `kubectl top pod` resource sampling every 5s against a 15s-resolution metrics-server. This document does not restate that methodology — only the results and their caveats.

Both arms completed cleanly: **0/200 failed firings on either system.** Actually-installed versions: KubeZap controller `ghcr.io/kubezap/controller:latest` (image digest as built by this run), Argo `quay.io/argoproj/workflow-controller:v4.1.3` (matches the pin).

## 2. Per-step latency

### 2.1 KubeZap — the raw per-run JSON is not usable at face value

`FlowRun.status.steps[].startTime`/`completionTime` are `metav1.Time`, which serializes to whole-second RFC3339: **594 of 600 step observations (200 runs × 3 steps) in the raw per-run JSON read as an exact 0.000s duration**; the remaining 6 (1%) read as exactly 1.000s, which is the same quantization artifact landing on the other side of a wall-clock second boundary by chance, not a real 1-second step. Computing p50/p95 from this data would report `p50 = p95 = 0.000s` for every step position and pooled — technically the arithmetic result, but not a trustworthy or meaningful number, and reporting it without qualification would be a worse outcome than not reporting a number at all.

**Working number: the controller's own Prometheus histogram.** `kubezap_step_duration_seconds` (`internal/metrics/metrics.go`, recorded via real `time.Since()` at full precision in `internal/controller/flowrun_controller.go`, not derived from `metav1.Time`) is exposed on the controller's `:9090` `/metrics` endpoint and was scraped after the full run:

| Metric | Value |
|---|---|
| Count | 716 observations |
| Sum | 4.1105s |
| **Mean** | **5.74ms** |
| p50 (bucket-bounded) | in `(5ms, 10ms]` — 8.0% of observations ≤5ms, 99.0% ≤10ms |
| p95 (bucket-bounded) | also in `(5ms, 10ms]` — `prometheus.DefBuckets`' finest granularity below 10ms is only the single 5ms boundary, so p50 and p95 cannot be distinguished further than "both above 5ms, both at or below 10ms" from bucket counts alone |

Caveat on this number, stated plainly: this histogram is a **cumulative counter since controller-pod start** (~4h17m uptime at scrape time), not scoped to exactly this benchmark's 200×3=600 firings — it also includes earlier `--smoke` validation runs and residual observations from harness-build-out testing (716 total vs. 600 expected from this run alone). All of those observations hit the identical code path against the identical zero-added-latency target, so this does not bias the estimate — if anything the larger n makes it a more stable mean/bucket estimate than a clean 600-sample slice would give — but it means this is not a number isolated purely to this run's 200 firings, and there was no way to snapshot a "before" baseline after the fact to subtract it out.

**Bottom line for KubeZap:** true per-step dispatch latency is on the order of **5–10ms**, consistent with the architecture's bet (in-memory resolve + one RPC to an already-running `http-executor`, no pod creation). The raw per-run JSON's apparent "0.000s" is an artifact of `metav1.Time`, not evidence of zero real cost — do not read it as `p50=p95=0` in any absolute sense.

### 2.2 Argo — needs to be reported as two numbers, not one

Per-step **pod lifecycle** (`status.nodes[].startedAt`→`finishedAt` for the three `curl-ok`-template Pod nodes; whole-second resolution but true values are multi-second so this is not a quantization problem here):

| | step1 | step2 | step3 | pooled (n=600) |
|---|---|---|---|---|
| p50 | 3.0s | 3.0s | 3.0s | **3.0s** |
| p95 | 4.0s | 4.0s | 4.0s | **4.0s** |
| mean | 3.35s | 3.38s | 3.40s | 3.37s |

Within that pod-lifecycle window, the **dispatch-to-execution** sub-delta (pod `startedAt` → `main` container's `running`/`terminated.startedAt` — the scheduling+image-resolution+container-start tax specifically): pooled p50 = **1.0s**, p95 = **2.0s** (n=600).

**Separately, and larger: the gap *between* steps.** `step2.startedAt − step1.finishedAt` and `step3.startedAt − step2.finishedAt` (n=400 gaps across 200 runs): p50 = **7.0s**, mean = **6.65s**. This is Argo's workflow-controller not advancing the `steps` DAG to the next stage immediately after a pod finishes — visible directly as a real, reproducible gap, not scheduling variance (min 6.0s, max 8.0s — a tight, consistent band, not a noisy tail). Per-run total wall-clock (`step1.startedAt` → `step3.finishedAt`, n=200): p50 = 23.0s, mean = 23.42s. The arithmetic checks out exactly: `3×3.37 (pod lifecycle) + 2×6.65 (inter-step gaps) = 23.42s = measured mean total`. **The inter-step reconciliation cadence (~13.3s of the 23.4s total, ~57%) is the larger contributor to Argo's total time in this scenario — not the per-step pod cost itself (~10.1s, ~43%).** This cadence is plausibly a controller resync/poll-interval characteristic that may be tunable in a production Argo deployment rather than an irreducible property of "running a container per step" — this benchmark did not attempt to tune it, and no claim here should imply "Pod-per-step is inherently a 23-second-per-3-steps architecture."

**Bottom line for Argo:** per-step-pod cost alone is ~3–4s; the workflow-level total the caller actually experiences is ~23s, roughly 57% of which is Argo's own inter-step reconciliation cadence rather than pod overhead. Both numbers matter for different claims — cite the pod-lifecycle figure for "cost of a container per step," cite the total for "what a user waits for."

### 2.3 The gap

- Per-step dispatch: KubeZap ~5–10ms vs. Argo pod-lifecycle ~3–4s → roughly **300–800x**.
- End-to-end (3 steps): KubeZap `kubezap_flowrun_duration_seconds` mean = 112.29s/209 = **537ms** (p50 in the `(0.5s,1s]` bucket, p95 in `(1s,2.5s]`; same cumulative-counter caveat as §2.1 applies) vs. Argo total mean **23.42s** → roughly **44x**.

Both framings are legitimate — the per-step number is the "raw architecture" comparison, the end-to-end number is closer to what an operator actually experiences (and folds in KubeZap's own controller-reconcile overhead, which is not zero either — 537ms is not itself a "pure dispatch" number, it includes informer/requeue latency on the KubeZap side, the same category of cost as Argo's inter-step gap on the Argo side).

## 3. Resource overhead

Sampled via `kubectl top pod` every 5s across each system's full sequential run window (`kubezap-resource-samples.json`, `argo-resource-samples.json`).

**KubeZap** (namespace `kubezap-bench`, ~3-minute sampling window, 21 samples/pod, 100% capture rate — both are steady-state Deployments present for the whole window):

| Pod | Peak CPU | Peak Memory |
|---|---|---|
| `kubezap-http-executor` | 19m | 14Mi |
| `kubezap-webhook-gateway` | 3m | 24Mi |

(`bench-mockoon`, the shared echo target, is excluded — it isn't part of KubeZap.)

**Argo** (namespace `argo-bench`, ~107-minute sampling window, 1207 samples/pod):

| Pod | Peak CPU | Peak Memory |
|---|---|---|
| `argo-server` (control plane) | 3m | 82Mi |
| `workflow-controller` (control plane) | 6m | 49Mi |
| the 600 per-run `curl-ok` step pods | **not measurable by this method** | **not measurable by this method** |

**Capture-rate finding, exactly as METHODOLOGY.md §3.2 predicted this asymmetry would occur: 0 of the 600 ephemeral per-step pods were ever captured by a `kubectl top pod` sample.** `pods_created_count=602` (600 step pods + the 2 control-plane pods, all distinct names ever seen via `kubectl get pods`), `pods_captured_count=2` (only the two long-lived control-plane pods — capture rate 0.33%). Each step pod's ~3–4s total lifecycle is shorter than metrics-server's 15s refresh cadence often enough that this sampling method essentially never catches one in the act. **Conclusion: no valid apples-to-apples "per-step resource cost" comparison is possible from this data.** The only real comparison available is steady-state control-plane footprint (KubeZap ~22m CPU / ~38Mi combined vs. Argo ~9m CPU / ~131Mi combined) — small and not the architecturally interesting number for a Pod-per-step engine; do not present it as "Argo's per-step footprint is small," since per-step pods were never actually observed.

