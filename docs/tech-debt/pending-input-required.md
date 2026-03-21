# Pending Input Required

This file collects decisions that require owner input before work can proceed.
The `/backlog` skill reads `<!-- BACKLOG-PROMPT -->` blocks at the start of each session
and surfaces the open questions.

---

## Review 2026-03-21

### Decisions needed from owner

<!-- BACKLOG-PROMPT -->
**Q: Should `docs/api/mock-endpoint.md` be deleted, left as a deprecated stub, or retained as a full archived doc?**
Why it matters: The MockEndpoint CRD was removed (schedule §4–7 complete). The original `docs/api/mock-endpoint.md` is still a full doc page describing the removed feature. New contributors reading it may not realise it is gone. The replacement guide at `docs/guides/mocking-http-endpoints.md` now covers the topic.
Options: Delete the file entirely / Replace contents with a one-paragraph redirect stub pointing to `docs/guides/mocking-http-endpoints.md` / Leave as-is (archived reference)
<!-- BACKLOG-PROMPT -->

**Q2 — RESOLVED (2026-03-21 investigation):** `type: resource` trigger is **alpha/experimental**, not production-ready.

Investigation findings from `internal/controller/resource_watcher.go`:

- **Known bug — naive pluralization** (`line 113`): Resource type is inferred as `strings.ToLower(kind) + "s"`. Fails silently for irregular plurals: `Ingress` → `ingresss` (wrong), `NetworkPolicy` → `networkpolicys` (wrong). Fix requires using the discovery API to look up the correct plural form.
- **No retry on cache sync failure** (`line 158–162`): If the informer fails to sync (transient RBAC issue, API server blip), the watcher goroutine exits permanently. The trigger stays "registered" in the map but is effectively dead until the Trigger is touched and the reconciler re-registers it.
- **FlowRun name collision risk** (`line 207`): Names use Unix timestamp at second precision with no random suffix. Two events for the same resource+eventtype in the same second collide silently (second event's FlowRun is dropped).
- **No cooldown mechanism**: Rapidly-updated resources (e.g., Pod status churn) with no `watchFields` filter will create a FlowRun for every update. Other trigger types have `maxInvocations`/`window` cooldown — resource triggers do not.

**Action taken:** Updated `CLAUDE.md` Trigger Types section to label resource trigger as "(alpha)". Added known issues to `docs/schedule.md` §11. `docs/architecture.md` already updated to "implemented, status under review". Example 6 README can be updated once the pluralization bug is fixed.

<!-- BACKLOG-PROMPT -->
**Q: Should a CEL expression cache eviction policy be added to `flowrun_controller.go`?**
Why it matters: The CEL compiled-program cache (`sync.Map`) in `flowrun_controller.go` has no size bound or TTL. In long-running operators with many distinct `when` expressions, this is a slow memory leak. The fix (LRU cache or TTL eviction) is non-trivial and carries risk of cache-miss performance regression if implemented incorrectly.
Options: Add LRU eviction with configurable max-size flag (e.g. `--cel-cache-size`, default 1000) / Add TTL-based eviction (entries expire after N minutes) / Leave unbounded cache (acceptable for current scale) / Disable cache entirely and recompile on each reconcile (simplest but slower)
<!-- BACKLOG-PROMPT -->
