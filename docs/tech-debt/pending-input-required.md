# Pending Input Required

This file collects decisions that require owner input before work can proceed.
The `/backlog` skill reads `<!-- BACKLOG-PROMPT -->` blocks at the start of each session
and surfaces the open questions.

---

## Lint Deferred Items — 2026-09-11

<!-- ANSWERED -->
**Q: The 12 remaining `make lint` findings (§36) were suppressed via `//nolint`/a path-scoped exclusion rather than fixed by refactoring — is that suppression the permanent answer, or should the bigger refactor happen?**

**Answer (2026-09-11):** (B) for goconst — introduce a typed `Phase` enum. (A) for gocyclo — leave the 5 `nolint:gocyclo` suppressions in place indefinitely; do not split the functions.

Implemented: four named string types added in `api/v1alpha1` (`FlowRunPhase`, `StepPhase`, `FailurePolicy`, `OnFailureAction`), all 4 affected struct fields retyped, every real call site across `internal/controller`, `internal/cli`, `internal/metrics`, and `cmd/kubezap` converted from bare string literals to the named constants. CRD schema `enum:` lists confirmed byte-identical before/after (only a purely additive `description:` picked up on the two `Phase` fields). The `goconst` path exclusion was removed from `.golangci.yml` — no longer needed, since the literals themselves are gone rather than suppressed. `gocyclo` suppressions are untouched, as decided. Design record: `docs/design/2026-09-11-typed-phase-enums.md`. Schedule reference: `docs/schedule.md` §36.
<!-- ANSWERED -->

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

| Option                                | Pro                                              | Con                                                                                    |
| ------------------------------------- | ------------------------------------------------ | -------------------------------------------------------------------------------------- |
| Unbounded cache (chosen)              | Zero complexity, zero new deps, optimal hit rate | Grows unboundedly under throwaway-expression workloads (unlikely)                      |
| LRU eviction                          | Strict memory bound                              | Adds dependency (`golang-lru`), recompile cost on eviction, per-access lock contention |
| TTL eviction                          | Handles deleted/replaced Flows naturally         | Recompile cost for infrequent Flows (e.g. daily crons), more complex implementation    |
| Disable cache (`--disable-cel-cache`) | Zero memory, simplest code path                  | Recompile on every reconcile — negligible CPU cost (~µs), acceptable for debugging     |

**Implementation:** `DisableCELCache bool` field on `FlowRunReconciler` + `--disable-cel-cache` flag in `cmd/main.go`. When true, cache reads and writes are both skipped; every `when` evaluation recompiles from source.

**Files changed:** `internal/controller/flowrun_controller.go`, `cmd/main.go`.

---

## Review 2026-03-21 (backlog session)

<!-- ANSWERED -->
**Q: Resource watcher naive pluralization fix — should the discovery API be used at registration time?**

**Answer (2026-03-21):** Option 2 — Use discovery API, fail-open. Try discovery first; if it fails, fall back to naive `+s` pluralization with a warning log.

File: `internal/controller/resource_watcher.go` (line ~113), `cmd/main.go` (constructor wiring).
<!-- ANSWERED -->

---

## Architecture Review — 2026-03-21

<!-- ANSWERED -->
**Q1: FlowRun reconciler execution model — execute one step per reconcile or keep current all-steps-in-one-loop?**

**Answer (2026-03-21):** Do before OperatorHub submission. Refactor to one-step-per-reconcile.

Schedule: §12b
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q2: Parallel steps — fix implementation to match docs, or update docs to document sequential behavior?**

**Answer (2026-03-21):** Fix the implementation. Steps with the same `runAfter` set should execute in parallel as documented.

Schedule: §12b
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q3: `type: pubsub` refactor timing — do before OperatorHub (v1alpha1 is unstable) or defer to v1beta1?**

**Answer (2026-03-21):** Do before submission. Promote `kafka`/`amqp`/`nats` to top-level trigger types now while the API is explicitly unstable.

Schedule: §12a
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q4: Secret value in FlowRun status error messages — P0 (block public release) or P1 (fix before GA)?**

**Answer (2026-03-21):** P0. Must fix before any public or OperatorHub release.

Schedule: §12c
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q5: Dashboard / monitoring UI — CLI-based, web-based, or both? Read-only.**

**Answer (2026-03-21):** Option 3 — Both. CLI for day-to-day operator use; minimal web UI for demos and stakeholder visibility. Build CLI first, then web UI.

Schedule: §15
<!-- ANSWERED -->

---

## Pre-Public Review — 2026-03-22

<!-- ANSWERED -->
**Q: Go module rename — what is the public org/repo name?**

The module path `github.com/kubezap/kubezap-operator` must be renamed before any public release. This rename touches `go.mod`, every `.go` file import path, the CSV `repository` field, Goreleaser download URLs, and all documentation links.

**Answer (2026-03-22):** New org `kubezap` on GitHub, repo name `kubezap-operator`. New module path: `github.com/kubezap/kubezap-operator`. Rename all import paths, `go.mod`, CSV `repository` field, and documentation links accordingly.

Schedule reference: §16 P0 — "Go module path rename"
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q: WebhookAuth struct — nested YAML sub-keys vs flat Go fields?**

**Answer (2026-03-22):** Option 1 — Restructure Go types to match the nested doc style. Add dedicated structs `HMACConfig`, `BearerConfig`, `OIDCConfig`, `APIKeyConfig`, `IPAllowlistConfig`, `MTLSConfig` nested inside `WebhookAuth`. This is the more idiomatic Kubernetes API design and produces cleaner, more readable YAML for users. Update the handler to read from the new nested fields.

Schedule reference: §16 P1 — "docs/api/trigger.md + webhook-security.md: WebhookAuth YAML examples"
<!-- ANSWERED -->

---

## Review 2026-03-22

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Should the resource watcher context detachment bug (resource_watcher.go:95) be promoted to P0?**
**Answer (2026-03-22):** P0. Fix before any public release. Promoted in `docs/schedule.md` §16 P0.
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q: Should gateway Deployment health probes be P1 (before OperatorHub) or P2?**
**Answer (2026-03-22):** P1. Required before OperatorHub submission. Promoted in `docs/schedule.md` §16 P1.
<!-- ANSWERED -->

---

## Security Design Review — 2026-03-24

> Full review: `docs/security-review-2026-03-24.md`
> Schedule items: `docs/schedule.md` §17

### Decisions needed from owner

<!-- ANSWERED -->
**Q1: Should cross-namespace FlowRef be removed from v1alpha1?**

**Answer (2026-03-24):** Option A — Remove cross-namespace FlowRef entirely. `FlowRef.Namespace` field removed from the API; FlowRuns always execute Flows in their own namespace. Cross-namespace flows can be revisited in v1beta1 with a proper authorization model (e.g., FlowGrant CRD).

Schedule: §17 P0
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q2: Should webhook triggers require authentication by default?**

**Answer (2026-03-24):** Option B — ValidatingWebhookConfiguration that emits an admission warning (not rejection) when a Trigger with `type: webhook` is created/updated without `spec.webhook.auth`. Non-breaking; raises visibility without blocking dev workflows.

Schedule: §17 P1
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q3: Should plugin Integrations support image digest pinning?**

**Answer (2026-03-24):** Option A — Add optional `spec.plugin.imageDigest` field. When set, operator validates resolved digest at reconcile time before creating/updating the plugin Deployment. Opt-in; no enforcement for users who don't set it.

Schedule: §17 P2
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q4: Should AllNamespaces mode remain the default?**

**Answer (2026-03-24):** Hybrid of A + C. Default to OwnNamespace (least privilege out of the box). In AllNamespaces mode, restrict secrets RBAC to namespaces labeled `kubezap.io/managed=true`. This gives both a safe default and a scoped multi-namespace option.

Schedule: §17 P0
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q5: SSRF protection model — blocklist or allowlist?**

**Answer (2026-03-24):** Blocklist as the baseline (block RFC1918, link-local, loopback, metadata IPs), plus a separate HTTP executor controller architecture. The main controller (which holds broad RBAC) never makes outbound HTTP calls. A dedicated `kubezap/http-executor` pod with minimal RBAC (no cluster-wide secrets, no RBAC management) executes HTTP steps. This limits blast radius even if the blocklist is bypassed via DNS rebinding.

Schedule: §17 P0
<!-- ANSWERED -->

---

## Review 2026-03-27

### Decisions needed from owner

<!-- ANSWERED -->
**Q: Should `StepRunStatus.Attempts` be wired to the actual retry count, or remain hardcoded to 1?**
Why it matters: Currently every step reports `attempts: 1` in FlowRun status regardless of how many retries occurred. This makes FlowRun status useless for debugging retry storms or transient failures. The fix requires threading the attempt count through `executeHTTPStep` and the publish step return path.
Options: (A) Fix it — thread attempt count through; add to next backlog sprint / (B) Leave as-is and document the limitation in FlowRun API docs.
**Answer (2026-03-27):** Option A — fix it. Thread actual attempt count through executeHTTPStep and publish step return path.
<!-- ANSWERED -->

<!-- ANSWERED -->
**Q: Should executor RPC transport failures requeue with exponential backoff instead of failing the step immediately?**
Why it matters: If the http-executor pod is temporarily unavailable (rolling update, OOM), the current behavior is to fail the step permanently in that reconcile cycle. Exponential backoff would allow recovery without user intervention, but adds complexity and may mask persistent failures.
Options: (A) Add `ctrl.Result{RequeueAfter: backoff}` for transport errors (transient) vs. immediate failure for 4xx/5xx from executor (application errors) / (B) Keep current behavior — document that executor restarts cause transient step failures.
**Answer (2026-03-27):** Option A — add exponential backoff requeue for transport errors (pod unavailable, connection refused), immediate failure for 4xx/5xx from executor (application-level errors).
<!-- ANSWERED -->

---

## Manual E2E Validation — 2026-04-04

<!-- ANSWERED -->
<!-- DEFERRED 2026-04-04: owner not sure yet; skip [USER] E2E tasks and proceed with other backlog items. -->
<!-- CONFIRMED DEFERRED 2026-04-05: still deferred, proceed with other backlog items. -->

### Decisions / credentials needed from owner for §19 E2E tasks

**Q: github-autolabel example — GitHub credentials needed**
Why it matters: The `github-autolabel` example requires a GitHub repo with admin access, a Personal Access Token (PAT with `repo` scope), and the gateway exposed externally (ngrok/LoadBalancer) for GitHub to reach it. This cannot be automated without these credentials.
Required inputs:
- GitHub repo path (e.g. `myorg/myrepo`)
- PAT value (or confirmation that owner will create the Secret manually)
- Whether ngrok should be used or if a public endpoint is available
Schedule item: §19 `[USER] E2E — github-autolabel`

**Q: slack-router example — Slack app credentials needed (for real Slack test)**
Why it matters: Full validation of the slack-router example requires a Slack app with a slash command and the app's Signing Secret. The curl-simulation path (`[AUTO]` task) covers HMAC correctness without real Slack, but end-to-end validation requires a real Slack workspace.
Required inputs:
- Slack app Signing Secret
- Whether to skip the real Slack validation or set up a test workspace
Schedule item: §19 `[USER] E2E — slack-router (real Slack)`

**Q: nightly-export example — Slack notification credentials (optional)**
Why it matters: The `notify-slack` step in the nightly-export example requires a Slack incoming webhook URL. If skipped, the step fails with `onFailure: Continue` and the FlowRun still succeeds. Owner may want to validate the Slack notification path or is OK leaving it as a known skip.
Required inputs:
- Whether to provide a Slack webhook URL or skip the notification validation
- If providing: the webhook URL to put in `slack-webhook-secret`
Schedule item: §19 `[AUTO] E2E — nightly-export`
