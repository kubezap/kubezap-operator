# Docs Rewrite Plan — 2026-03-24

## Objective

Reorganize `docs/` into a two-tier structure: user-facing content at the top level, internal/contributor content under `docs/dev/`. Rewrite user-facing docs to meet the Confluent-for-Kubernetes quality bar -- clear audience, complete examples, no dev-oriented framing, no stale content.

---

## Classification

| File | Classification | Rewrite Priority |
|------|---------------|-----------------|
| `docs/overview.md` | user-facing | high |
| `docs/architecture.md` | user-facing | high |
| `docs/api/trigger.md` | user-facing | medium |
| `docs/api/flow.md` | user-facing | medium |
| `docs/api/flowrun.md` | user-facing | medium |
| `docs/api/integration.md` | user-facing | medium |
| `docs/api/plugin-contract.md` | user-facing (plugin devs) | medium |
| `docs/guides/getting-started.md` | user-facing | high |
| `docs/guides/exposing-the-webhook-gateway.md` | user-facing | low |
| `docs/guides/webhook-security.md` | user-facing | low |
| `docs/guides/observability.md` | user-facing | medium |
| `docs/guides/cron-triggers.md` | user-facing | low |
| `docs/guides/amqp-setup.md` | user-facing | low |
| `docs/guides/nats-setup.md` | user-facing | low |
| `docs/guides/mocking-http-endpoints.md` | user-facing (dev/test) | low |
| `docs/guides/using-the-cli.md` | user-facing | low |
| `docs/guides/troubleshooting.md` | user-facing | medium |
| `docs/guides/mockoon-deployment.yaml` | user-facing (supporting file) | n/a |
| `docs/contributing.md` | internal/dev-only | low |
| `docs/releasing.md` | internal/dev-only | n/a |
| `docs/schedule.md` | internal/dev-only | n/a |
| `docs/review-latest.md` | internal/dev-only | n/a |
| `docs/groom-latest.md` | internal/dev-only | n/a |
| `docs/security-review-2026-03-24.md` | internal/dev-only | n/a |
| `docs/design/cli.md` | internal/dev-only | n/a |
| `docs/design/scale-limitations.md` | dual: extract user-facing summary | medium |
| `docs/tech-debt/code-debt-2026-03-21.md` | internal/dev-only | n/a |
| `docs/tech-debt/pending-input-required.md` | internal/dev-only | n/a |

---

## Proposed Two-Tier Structure

```
docs/
  overview.md                           # product overview, installation, quick example
  architecture.md                       # runtime architecture for operators
  api/
    trigger.md                          # Trigger CRD reference
    flow.md                             # Flow CRD reference
    flowrun.md                          # FlowRun CRD reference
    integration.md                      # Integration CRD reference
    plugin-contract.md                  # plugin developer contract
  guides/
    getting-started.md                  # first-run tutorial (not a redirect)
    exposing-the-webhook-gateway.md     # Ingress / Gateway API / Route
    webhook-security.md                 # webhook auth methods
    observability.md                    # metrics, logs, traces
    cron-triggers.md                    # cron trigger guide
    amqp-setup.md                       # AMQP gateway setup
    nats-setup.md                       # NATS gateway setup
    mocking-http-endpoints.md           # Mockoon for dev/test
    mockoon-deployment.yaml             # supporting manifest
    using-the-cli.md                    # CLI reference
    troubleshooting.md                  # operational troubleshooting
    scale-considerations.md             # NEW: user-facing scale guidance (extracted)
  dev/
    contributing.md                     # moved from docs/contributing.md
    releasing.md                        # moved from docs/releasing.md
    schedule.md                         # moved from docs/schedule.md
    review-latest.md                    # moved from docs/review-latest.md
    groom-latest.md                     # moved from docs/groom-latest.md
    security-review-2026-03-24.md       # moved from docs/
    design/
      cli.md                            # moved from docs/design/
      scale-limitations.md              # moved from docs/design/ (full internal version)
    tech-debt/
      code-debt-2026-03-21.md           # moved from docs/tech-debt/
      pending-input-required.md         # moved from docs/tech-debt/
```

Key changes from current layout:
- `docs/dev/` absorbs all contributor/session/review files
- `docs/guides/scale-considerations.md` is a new user-facing extract from the internal `design/scale-limitations.md`
- `getting-started.md` becomes a real tutorial, not a redirect to examples
- No files deleted -- everything either stays in place or moves to `docs/dev/`

---

## Rewrite Briefs (User-Facing Docs, by Priority)

### High Priority

#### `docs/overview.md`
**Issues:**
- Roadmap section with checkbox items reads like a dev task tracker, not a product changelog. Users want a feature matrix, not sprint history.
- Installation section lists four install methods (raw manifests, Helm, CLI, OLM) interleaved in a confusing order. Helm should be first and primary. Raw manifests should be last.
- CLI installation section is too long for the overview -- belongs in its own guide or in `using-the-cli.md`.
- "Design Goals" section uses internal framing ("reconcilers are designed to..."). Reframe as user-facing feature promises.
- Quick Example is good but could be tighter -- the Flow spec is dense for a first read.
- GHCR image authentication details are operational noise in the overview.
- The link to Getting Started (`../examples/order-router/`) uses a relative path that breaks on hosted doc sites.
- MockEndpoint removal note is historical context irrelevant to new users.

**Rewrite brief:** Restructure as: What is KubeZap (2 paragraphs) -> Core Concepts (keep) -> Quick Example (simplify) -> Installation (Helm first, link out for details) -> Feature Matrix (replace roadmap checkboxes) -> Compatibility -> Next Steps (links to guides). Move CLI install, GHCR auth, and design philosophy to dedicated pages. Remove roadmap checkboxes.

#### `docs/architecture.md`
**Issues:**
- Opens with "This document describes the runtime architecture" -- acceptable but could be more engaging.
- Component table was updated for AMQP/NATS gateways (good), but the ASCII diagram and introductory prose still say "three distinct runtime components" (stale -- there are five binaries, plus the CLI).
- The "Adding New Trigger Types" section is contributor-oriented, not user-facing. Move to `docs/dev/contributing.md`.
- "Future Trigger Types" table mixes implemented and planned items -- confusing.
- Multi-tenancy section is sparse; it describes `WATCH_NAMESPACES` but doesn't give concrete examples of when to use each mode.

**Rewrite brief:** Reframe as an operator's guide to KubeZap runtime topology. Fix component counts. Remove the "Adding New Trigger Types" section (move to contributing guide). Expand multi-tenancy with concrete namespace mode examples. Merge "Future Trigger Types" into a short "Planned" note rather than a full table.

#### `docs/guides/getting-started.md`
**Issues:**
- Currently a redirect stub pointing to `examples/order-router/README.md`. This is the most important page for new users and it has no content of its own.
- A proper getting-started guide should be self-contained: install -> create a trigger -> create a flow -> send an event -> observe the FlowRun -> next steps. It should not require users to clone the repo and `kubectl apply -k` an example directory.

**Rewrite brief:** Write a standalone getting-started tutorial with inline YAML (not kustomize overlays). Steps: (1) install via Helm, (2) create a simple webhook Trigger, (3) create a simple Flow with one HTTP step, (4) port-forward and curl, (5) inspect the FlowRun with kubectl, (6) link to the CLI guide. Keep it under 200 lines. The example directory can remain as a more advanced reference.

### Medium Priority

#### `docs/api/trigger.md`
**Issues:**
- Comprehensive and well-structured, but very long (~688 lines). The Exposing Webhook Triggers section duplicates content from the dedicated guide.
- TLS and mTLS Annotations section is also duplicated from the overview.
- `FlowReference.Namespace` field is documented but will be removed per the security review (section 17 P0). Needs update after that change lands.
- AmqpTrigger field `topic` is labeled "Queue name" -- field name is misleading for AMQP (should clarify it maps to queue, not topic).

**Rewrite brief:** Remove duplicated Exposing and TLS sections -- replace with links to the dedicated guides. Tighten the spec reference tables to be pure reference (no tutorial prose mixed in). After FlowRef.Namespace removal, update accordingly. Fix the AMQP "topic" field description.

#### `docs/api/flow.md`
**Issues:**
- Good structure and examples. No major structural problems.
- Need to verify whether XPath claim was fully removed (tracked in schedule -- marked done).
- `$(trigger.type)` expression reference may still list `pubsub` as a value (tracked in schedule pubsub pass -- marked done).
- Should cross-reference the scale-considerations guide for users wondering about throughput limits.

**Rewrite brief:** Verify pubsub and XPath fixes are applied. Add a brief "Performance Considerations" note linking to scale guidance. Otherwise this doc is close to final quality.

#### `docs/api/flowrun.md`
**Issues:**
- Well-written with good examples and kubectl reference. Close to user-facing quality already.
- Resource trigger FlowRun naming pattern was corrected (good).
- No AMQP/NATS FlowRun examples (only webhook, Kafka, cron).

**Rewrite brief:** Add AMQP and NATS FlowRun examples alongside the existing Kafka example. Otherwise minor polish only.

#### `docs/api/integration.md`
**Issues:**
- Could not read full file (token limit), but known issues from review: example used `spec.type: pubsub` (tracked as fixed).
- Beta labels on AMQP/NATS need a definition of what "beta" means for KubeZap (stability guarantee, breaking change policy).
- Plugin graduation section is forward-looking and valuable.

**Rewrite brief:** Verify pubsub terminology fix. Define beta stability level at the top of the doc. Ensure all built-in integration types (kafka, amqp, nats, http, plugin) have complete examples.

#### `docs/api/plugin-contract.md`
**Issues:**
- Excellent technical content for plugin developers. This is the right audience and depth.
- Reference implementation is "forthcoming" -- still a placeholder. Either remove the promise or deliver it.
- Minor: the `FlowRef` in the example code may need updating after cross-namespace FlowRef removal.

**Rewrite brief:** Update FlowRun creation example after FlowRef.Namespace removal. Change "forthcoming" reference implementation note to link to the Kafka gateway source as the canonical reference (it already does, so just remove the "forthcoming" hedge). Otherwise this doc is near final.

#### `docs/guides/observability.md`
**Issues:**
- Phantom metrics were removed per schedule (good).
- Only shows metrics ports for controller, webhook-gateway, and kafka-gateway. AMQP and NATS gateways are missing from the metrics port table.
- The ServiceMonitor note is important and correctly placed.
- Trace structure section may be aspirational vs. implemented -- verify.

**Rewrite brief:** Add AMQP and NATS gateway metrics ports. Verify that all documented trace spans actually exist in the codebase. Add a sample ServiceMonitor YAML for copy-paste convenience.

#### `docs/guides/troubleshooting.md`
**Issues:**
- Good structure (symptom -> diagnose -> fix pattern).
- References to the CLI for debugging are valuable.
- Missing sections for AMQP and NATS gateway troubleshooting.
- Missing section for resource trigger issues (common: RBAC not granted for target resource type).

**Rewrite brief:** Add AMQP/NATS gateway troubleshooting sections. Add resource trigger troubleshooting (RBAC, known alpha limitations). Ensure all troubleshooting steps reference the correct current pod labels and namespace defaults.

#### `docs/design/scale-limitations.md` (extract to user-facing)
**Issues:**
- Contains genuinely useful scale guidance (FlowRun storage limits, etcd quota, throughput expectations) that users need.
- Currently filed under `design/` which is internal. Users deploying at scale will never find it.
- The "Design Intent" framing is appropriate for users -- it sets expectations clearly.

**Rewrite brief:** Create `docs/guides/scale-considerations.md` as a user-facing extract. Keep the design-intent framing, the FlowRun storage limits table, and the "when not to use KubeZap" guidance. Remove internal implementation notes. Keep the full version in `docs/dev/design/` for contributors.

### Low Priority

#### `docs/guides/exposing-the-webhook-gateway.md`
**Issues:** Well-written, comprehensive. Covers all exposure patterns (port-forward, LoadBalancer, Ingress, Gateway API, OpenShift Route, TLS, mTLS passthrough, source IP). Close to Confluent-quality already.
**Rewrite brief:** Minor polish. Verify service name consistency (`kubezap-webhook-gateway` vs `kubezap-webhook-service` -- the latter appears in some older examples in other docs).

#### `docs/guides/webhook-security.md`
**Issues:** Comprehensive auth method coverage. Will need updates after the WebhookAuth struct restructure (schedule section 16 P1). Otherwise solid.
**Rewrite brief:** Update YAML examples after WebhookAuth restructure lands. No structural changes needed.

#### `docs/guides/cron-triggers.md`
**Issues:** Clean tutorial with good examples. No major issues.
**Rewrite brief:** Minor polish only. Verify example Flow YAML matches current spec.

#### `docs/guides/amqp-setup.md`
**Issues:** Well-structured tutorial. May still reference `pubsub` in cross-links (tracked -- marked fixed).
**Rewrite brief:** Verify pubsub cross-references are gone. Otherwise this is close to final.

#### `docs/guides/nats-setup.md`
**Issues:** Same as AMQP -- well-structured, potential stale pubsub references.
**Rewrite brief:** Same treatment as AMQP. Verify pubsub links, otherwise near final.

#### `docs/guides/mocking-http-endpoints.md`
**Issues:** Thorough guide. Correctly positioned as dev/test tooling. The MockEndpoint removal rationale is useful historical context here (unlike in the overview where it's noise).
**Rewrite brief:** No structural changes. Minor formatting polish.

#### `docs/guides/using-the-cli.md`
**Issues:** Good reference. Version string shows `v0.0.1` which should track the actual release version.
**Rewrite brief:** Update version example to use a realistic version. Add shell completion instructions if supported.

#### `docs/contributing.md`
**Issues:** Recently improved with architecture orientation, first-contribution guide, and OLM bundle docs. This is comprehensive and well-structured for contributors.
**Rewrite brief:** Move to `docs/dev/contributing.md`. Absorb the "Adding New Trigger Types" section from `architecture.md`. No content rewrite needed.

---

## Files to Move to `docs/dev/`

- `docs/contributing.md` -> `docs/dev/contributing.md`
- `docs/releasing.md` -> `docs/dev/releasing.md`
- `docs/schedule.md` -> `docs/dev/schedule.md`
- `docs/review-latest.md` -> `docs/dev/review-latest.md`
- `docs/groom-latest.md` -> `docs/dev/groom-latest.md`
- `docs/security-review-2026-03-24.md` -> `docs/dev/security-review-2026-03-24.md`
- `docs/design/cli.md` -> `docs/dev/design/cli.md`
- `docs/design/scale-limitations.md` -> `docs/dev/design/scale-limitations.md`
- `docs/tech-debt/code-debt-2026-03-21.md` -> `docs/dev/tech-debt/code-debt-2026-03-21.md`
- `docs/tech-debt/pending-input-required.md` -> `docs/dev/tech-debt/pending-input-required.md`

After moves, update all internal cross-references (`CLAUDE.md`, `schedule.md`, etc.) to use new paths. User-facing docs should never link into `docs/dev/`.

---

## Implementation Order

1. **`docs/guides/getting-started.md`** -- This is the highest-impact gap. A new user's first touch with the project is a redirect stub. Writing a real tutorial is the single biggest quality improvement and requires no dependency on pending code changes.

2. **`docs/overview.md`** -- The front page of the project. Restructuring it (feature matrix instead of roadmap checkboxes, Helm-first install, removal of dev-oriented framing) sets the tone for the entire doc set. Also has no code dependencies.

3. **Move internal files to `docs/dev/`** -- Low-effort, high-impact organizational change. Creates a clean separation immediately. Do this early so subsequent rewrites can assume the final directory structure.

4. **`docs/architecture.md`** -- Fix stale component counts, remove contributor-oriented sections, expand multi-tenancy guidance. Depends on the move (step 3) to know where the "Adding New Trigger Types" content lands.

5. **`docs/guides/scale-considerations.md`** (new) -- Extract user-facing content from `design/scale-limitations.md`. Quick win that fills a real gap for production evaluators.

6. **`docs/guides/observability.md`** -- Add AMQP/NATS metrics ports, verify trace spans, add ServiceMonitor example. Important for production readiness evaluation.

7. **`docs/guides/troubleshooting.md`** -- Add AMQP/NATS/resource trigger sections. Practical value for early adopters hitting issues.

8. **`docs/api/trigger.md`** -- Deduplicate Exposing/TLS sections. Blocked on WebhookAuth restructure (schedule section 16 P1) for final YAML examples.

9. **`docs/api/flow.md`** -- Verify fixes, add scale cross-reference. Light touch.

10. **`docs/api/flowrun.md`** -- Add AMQP/NATS examples. Light touch.

11. **`docs/api/integration.md`** -- Define beta stability level, verify examples. Blocked on pubsub terminology verification.

12. **`docs/api/plugin-contract.md`** -- Update after FlowRef.Namespace removal (schedule section 17 P0).

13. **Remaining guides** (webhook-security, cron-triggers, amqp-setup, nats-setup, cli, mocking) -- Minor polish. Lowest effort, lowest impact. Can be done in any order.

---

## Dependencies and Sequencing Notes

- Items 1-5 have no code dependencies and can proceed immediately.
- Items 8 and 12 are blocked on pending code changes (WebhookAuth restructure, FlowRef removal). Write the structural rewrite first; update YAML examples when the code changes land.
- The pubsub terminology pass (schedule section 16 P1) is marked as complete. Verify before touching items 6, 11.
- After all moves in step 3, run a link-checker or grep for broken cross-references (`docs/contributing.md`, `docs/schedule.md`, `docs/tech-debt/`, `docs/design/`).
