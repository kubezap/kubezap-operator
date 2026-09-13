# Checkpoint 2026-09-13 — Review

## Shipped this checkpoint

- STORY-010 — merged (PR #196): `PodDisruptionBudget` reconciliation for the webhook gateway, closing the "no real HA by default" gap.
- STORY-011 — merged (PR #197): hard-cutover of the webhook TLS/mTLS Namespace annotations onto `WebhookGatewayConfig.spec.tls`. `CHANGELOG.md` documents the required migration action.
- STORY-014 — merged (PR #195): execution-latency benchmark harness (KubeZap vs. Argo Workflows), validated live end-to-end on a real Kind cluster.
- STORY-015 — merged (PR #199): benchmark results write-up. **Go/no-go: pursue** "faster/lighter than Pod-per-step" as a differentiator — real numbers now exist (KubeZap ~5-10ms/step via the controller's own Prometheus histogram vs. Argo ~3-4s pod-lifecycle/~23s total per 3-step run, ~57% of which is Argo's own reconciliation cadence, not pod cost).
- STORY-016 — merged (PR #193): broken doc links/anchors from the MkDocs build.
- STORY-017 — merged (PR #198): `goconst` cleanup, 59 real findings fixed, temporary lint exclusion removed.
- STORY-018 — merged (PR #194): fixed the silently-dropped `+kubebuilder:webhook` marker on Trigger's webhook.
- Also: `config/crd/kustomization.yaml` fixed directly (was silently missing the WebhookGatewayConfig CRD from `make install` since STORY-009 merged); `docs/api/trigger.md`'s inbound-mTLS section updated (left stale by STORY-011's footprint).
- This closes **EPIC-004** entirely and brings **EPIC-003** to 4/5 stories done.

## Still open

- STORY-003 — partially done (e2e-failures AC satisfied via PR #174); brittle-test review + write-up remain. **Owner priority for next stretch.**
- STORY-007 — blocked on STORY-003.
- STORY-012 — unblocked (all 3 dependencies Done). **Owner priority for next stretch.**
- STORY-019 — groomed, ready to dispatch (FlowRun webhook marker, `failurePolicy: Ignore`).
- STORY-020 — groomed, ready to dispatch (design record for FlowRun step-timing precision).
- STORY-021 — blocked on STORY-020, not yet real-footprinted.
- A Backlog Candidate item still needs picking for this stretch's third focus area (owner chose "groom something from Backlog Candidates" without specifying which yet — likely the outbound-TLS-annotation doc/code mismatch or the observability-guide validation gap).
- STORY-004/006's admin-only follow-ups (Pages source, Discussions) — still intentionally deferred to public launch, no change.
- Community-profile `issue_template` gap — still `Open`, still deliberately deferred, no change.

## What worked / what to change

- **Rebase-before-merge became a recurring necessity this checkpoint, not an edge case.** Three separate branches (STORY-011, STORY-015, the final status-sync PR) each needed a rebase onto a `main` that had moved since their worktree was created, because dispatched agents and my own review/merge passes happen asynchronously against a fast-moving `main`. All three rebases were clean or had trivial (same-content) conflicts once resolved — worth treating "rebase onto current main immediately before merging, not just before dispatching" as a standing step, not something to notice only when GitHub flags a conflict.
- **A PR can show "conflicting" for reasons that have nothing to do with real content conflicts.** PR #198 showed a stale conflict because a GitHub-side error during merge left the PR's own record out of sync with `main` even though the content had landed — confirmed via `gh api .../commits/{sha}/pulls` returning no PR association for the merge commit. Diagnosing this (empty `git diff` against main) before assuming a real conflict avoided redoing already-landed work.
- **Dispatched agents continue to independently catch real bugs outside their own footprint** (STORY-014 found the CRD-kustomization gap and an Argo upstream RBAC issue; STORY-015 found and fixed a `jq` argument-length bug in the harness) — the "flag it, don't silently fix it unless it blocks a valid result" instruction from the dispatch brief is working as intended.
- **A benchmark's raw data needs a sanity check against the underlying mechanism before trusting it.** The owner's two follow-up questions this session (sub-second KubeZap timing, Argo's 30s/run) each surfaced a real methodological issue — `metav1.Time` quantization, and Argo's reconciliation-cadence lag — neither of which the harness's own output made obvious on its own. Worth treating "does this number make architectural sense" as a standing check before reporting benchmark data, not just after being asked.

## Backlog re-groom notes

- STORY-019 (FlowRun webhook marker) and STORY-020/021 (FlowRun step-timing precision, design + implementation split like STORY-008/009) added to `EPIC-002` from `follow-ups.md` entries, both resolved with a quick owner decision rather than left open.
- `EPIC-002`'s Status flipped back to `In Progress` (was briefly `Done` between STORY-017/018 landing and this checkpoint's 3 new stories).
- `EPIC-003`/`EPIC-004` Status headers updated: EPIC-004 fully closed, EPIC-003 at 4/5.
