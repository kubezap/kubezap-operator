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

<!-- BACKLOG-PROMPT -->
**Q: Is `type: resource` Kubernetes resource-event trigger considered production-ready or still experimental?**
Why it matters: `trigger_controller.go` handles `type: resource` triggers and `cmd/main.go` wires the `ResourceWatcher`. The implementation appears complete. However `docs/architecture.md` still labels the resource trigger spec as "(planned)", and Example 6's README notes it "requires kubernetes trigger type — not yet implemented". The schedule item is marked `[x]`. Clarifying the status will let us update docs accurately and remove conflicting language.
Options: Mark as production-ready (update architecture.md + Example 6 README) / Mark as experimental/alpha (add caveat in trigger.md and architecture.md) / Leave as-is and defer doc updates
<!-- BACKLOG-PROMPT -->

<!-- BACKLOG-PROMPT -->
**Q: Should a CEL expression cache eviction policy be added to `flowrun_controller.go`?**
Why it matters: The CEL compiled-program cache (`sync.Map`) in `flowrun_controller.go` has no size bound or TTL. In long-running operators with many distinct `when` expressions, this is a slow memory leak. The fix (LRU cache or TTL eviction) is non-trivial and carries risk of cache-miss performance regression if implemented incorrectly.
Options: Add LRU eviction with configurable max-size flag (e.g. `--cel-cache-size`, default 1000) / Add TTL-based eviction (entries expire after N minutes) / Leave unbounded cache (acceptable for current scale) / Disable cache entirely and recompile on each reconcile (simplest but slower)
<!-- BACKLOG-PROMPT -->
