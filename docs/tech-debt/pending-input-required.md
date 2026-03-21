# Pending Input Required

This file collects decisions that require owner input before work can proceed.
The `/backlog` skill reads `<!-- BACKLOG-PROMPT -->` blocks at the start of each session
and surfaces the open questions.

---

## Review 2026-03-21

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Should `docs/api/mock-endpoint.md` be deleted, left as a deprecated stub, or retained as a full archived doc?**
Why it matters: The MockEndpoint CRD was removed (schedule §4–7 complete). The original `docs/api/mock-endpoint.md` is still a full doc page describing the removed feature. New contributors reading it may not realise it is gone. The replacement guide at `docs/guides/mocking-http-endpoints.md` now covers the topic.
Options: Delete the file entirely / Replace contents with a one-paragraph redirect stub pointing to `docs/guides/mocking-http-endpoints.md` / Leave as-is (archived reference)
**Answer (2026-03-21):** Deleted. File confirmed absent from repo. Schedule §11 item marked `[x]`.
<!-- ANSWERED -->

**Q2 — RESOLVED (2026-03-21 investigation):** `type: resource` trigger is **alpha/experimental**, not production-ready.

Investigation findings from `internal/controller/resource_watcher.go`:

- **Known bug — naive pluralization** (`line 113`): Resource type is inferred as `strings.ToLower(kind) + "s"`. Fails silently for irregular plurals: `Ingress` → `ingresss` (wrong), `NetworkPolicy` → `networkpolicys` (wrong). Fix requires using the discovery API to look up the correct plural form.
- **No retry on cache sync failure** (`line 158–162`): If the informer fails to sync (transient RBAC issue, API server blip), the watcher goroutine exits permanently. The trigger stays "registered" in the map but is effectively dead until the Trigger is touched and the reconciler re-registers it.
- **FlowRun name collision risk** (`line 207`): Names use Unix timestamp at second precision with no random suffix. Two events for the same resource+eventtype in the same second collide silently (second event's FlowRun is dropped).
- **No cooldown mechanism**: Rapidly-updated resources (e.g., Pod status churn) with no `watchFields` filter will create a FlowRun for every update. Other trigger types have `maxInvocations`/`window` cooldown — resource triggers do not.

**Action taken:** Updated `CLAUDE.md` Trigger Types section to label resource trigger as "(alpha)". Added known issues to `docs/schedule.md` §11. `docs/architecture.md` already updated to "implemented, status under review". Example 6 README can be updated once the pluralization bug is fixed.

**Q3 — RESOLVED (2026-03-21):** CEL cache stays unbounded. `--disable-cel-cache` flag added as escape hatch.

**Decision:** Leave the cache unbounded. Add `--disable-cel-cache` bool flag for debugging/edge-case override.

**Rationale:**
The cache grows to one entry per distinct `when` expression across all deployed Flows.
For typical deployments this is bounded by the total number of Flow steps and converges to a stable size once Flows stabilise. A compiled `cel.Program` holds only the compiled AST and plan — typically a few KB each. At 10 000 distinct expressions that is ~50–100 MB worst case, which is well within normal operator memory budgets.

The only genuine leak scenario is continuous deployment of Flows with unique, throwaway `when` expressions that are never cleaned up — an atypical usage pattern.

**Trade-offs considered:**

| Option | Pro | Con |
|---|---|---|
| Unbounded cache (chosen) | Zero complexity, zero new deps, optimal hit rate | Grows unboundedly under throwaway-expression workloads (unlikely) |
| LRU eviction | Strict memory bound | Adds dependency (`golang-lru`), recompile cost on eviction, per-access lock contention |
| TTL eviction | Handles deleted/replaced Flows naturally | Recompile cost for infrequent Flows (e.g. daily crons), more complex implementation |
| Disable cache (`--disable-cel-cache`) | Zero memory, simplest code path | Recompile on every reconcile — negligible CPU cost (~µs), acceptable for debugging |

**Implementation:** `DisableCELCache bool` field on `FlowRunReconciler` + `--disable-cel-cache` flag in `cmd/main.go`. When true, cache reads and writes are both skipped; every `when` evaluation recompiles from source.

**Files changed:** `internal/controller/flowrun_controller.go`, `cmd/main.go`.

---

## Review 2026-03-21 (backlog session)

<!-- BACKLOG-PROMPT -->
**Q: Resource watcher naive pluralization fix — should the discovery API be used at registration time?**

Why it matters: `internal/controller/resource_watcher.go:113` uses `strings.ToLower(kind) + "s"` to infer the plural form, which silently fails for irregular plurals (`Ingress` → `ingresss`, `NetworkPolicy` → `networkpolicys`). The correct fix is to call the discovery API to look up the canonical plural form.

Trade-off: The discovery API call adds a round-trip to the API server each time a new Trigger with `type: resource` is registered (once per registration, not per event). In most clusters this is negligible. However:
- It requires a new `discovery.DiscoveryInterface` client injected into `ResourceWatcher`
- It adds a failure path: if the discovery call fails, the watcher must decide whether to fail-open (use the naive guess) or fail-closed (reject the trigger registration)
- It changes the `ResourceWatcher` constructor signature in `cmd/main.go` and `NewResourceWatcher`

Options:
1. **Use discovery API, fail-closed** — registration fails if the resource kind cannot be resolved; Trigger gets a condition `Ready=False` with reason `UnknownResourceKind`. Safest but adds complexity and one error mode.
2. **Use discovery API, fail-open** — try discovery first; if it fails, fall back to naive `+s` pluralization with a warning log. Least breaking, easiest rollout.
3. **Leave as-is** — document the limitation in `docs/api/trigger.md` under `type: resource`; fix only specific known-bad cases (e.g., detect `s`/`x`/`z`/`ch`/`sh` endings for basic English rules). Avoids discovery dep.

**Fallback assumption (if no answer by next session):** Option 2 (fail-open) — tries discovery, logs a warning and falls back to naive suffix if discovery is unavailable. Minimises breaking changes and preserves current behavior for clusters where discovery works.

File: `internal/controller/resource_watcher.go` (line ~113), `cmd/main.go` (constructor wiring).
<!-- BACKLOG-PROMPT -->
