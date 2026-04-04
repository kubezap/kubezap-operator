# Documentation Review Context — 2026-04-04

This document provides context and instructions for agents executing the
documentation review task (§21 in schedule).

---

## Scope

A comprehensive review of the KubeZap documentation to find:
- Inaccuracies (doc-reality mismatches, outdated content)
- Missing coverage (features implemented but undocumented)
- Broken cross-references (links to files that don't exist)
- Confusing or incomplete examples
- Style inconsistencies that harm readability
- Missing docs for recently implemented features

---

## Documentation Structure

```
docs/
├── overview.md               # User-facing entry point
├── architecture.md           # Internal architecture reference
├── contributing.md           # Dev workflow
├── releasing.md              # Release process
├── schedule.md               # Project backlog (not user docs)
├── api/
│   ├── trigger.md            # Trigger CRD reference
│   ├── flow.md               # Flow CRD reference
│   ├── flowrun.md            # FlowRun CRD reference
│   └── integration.md        # Integration CRD reference
├── guides/
│   ├── webhook-security.md   # Webhook auth guide
│   ├── observability.md      # Metrics + traces guide
│   ├── troubleshooting.md    # Troubleshooting guide
│   ├── mocking-http-endpoints.md  # Mockoon dev guide
│   └── security-checklist.md # OperatorHub security checklist
├── dev/
│   └── http-executor.md      # Internal executor design doc
├── design/
│   └── ...                   # Design specs
└── tech-debt/
    └── ...                   # Internal tracking docs (not user docs)
```

---

## Known Issues — Do NOT Re-Report

These were fixed in the 2026-03-27 review cycle:
- WATCH_NAMESPACES default contradiction across docs (fixed)
- FlowReference cross-namespace field documentation (fixed)
- http-executor.md status banner "Draft" (fixed to "Implemented")
- Flow.md missing CEL cost limits (fixed)
- trigger.md Resource Trigger alpha callout missing (fixed §18 P1)
- security-checklist.md missing (created §18 P1)

---

## Review Methodology

1. For each doc file, check:
   - Code examples are valid (YAML is valid, kubectl commands match current API)
   - All referenced files/docs exist (no broken links)
   - Feature descriptions match current implementation
   - Status markers (alpha/beta/stable) are accurate
   - The `docs/api/*.md` files cover all spec fields in `api/v1alpha1/*_types.go`

2. Cross-check:
   - `docs/api/trigger.md` vs `api/v1alpha1/trigger_types.go`
   - `docs/api/flow.md` vs `api/v1alpha1/flow_types.go`
   - `docs/api/flowrun.md` vs `api/v1alpha1/flowrun_types.go`
   - `docs/api/integration.md` vs `api/v1alpha1/integration_types.go`

3. Check for missing docs:
   - Recently added features that may not have been documented
   - CLI commands (kubezap watch/history/triggers/flows) — verify `docs/` covers these
   - Web dashboard (`--enable-ui`) — verify coverage in overview.md or guides

4. Check examples:
   - Each `examples/*/README.md` — steps should work with current operator version
   - Verify that examples reference correct CRD API versions
   - Verify any YAML examples against current API schema

---

## Output Format

Create `docs/tech-debt/doc-review-results-YYYY-MM-DD.md` with:

```markdown
# Documentation Review Results — YYYY-MM-DD

## HIGH — Inaccuracy or missing critical content
- [FILE] Description of problem + fix recommendation

## MEDIUM — Confusing, incomplete, or outdated
- ...

## LOW — Style, polish, minor gaps
- ...

## Files reviewed with no issues
- ...

## Schedule additions
- [List of items to add to schedule.md]
```

Then update `docs/schedule.md` and optionally fix LOW/MEDIUM issues inline.

---

## Notes for Reviewing Agent

- Use the Explore agent or research-analyst for initial pass
- **Opus model recommended** — catching subtle doc-reality mismatches benefits from
  deep reasoning and cross-referencing ability
- Focus on doc-reality mismatches first (these cause real user confusion)
- Style issues are low priority unless they're misleading
- The target quality bar is "Confluent for Kubernetes operator docs" — benchmark
  against that standard when assessing completeness
- Cross-reference with the existing security review doc at
  `docs/security-review-2026-03-24.md` to avoid duplicating security analysis
