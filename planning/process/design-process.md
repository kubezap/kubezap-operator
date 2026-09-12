# Design Process

Every non-trivial feature or architectural change in KubeZap must produce a design record before implementation begins. This document defines the required structure and enforcement rules.

---

## When This Applies

Use this process for any change that:

- Adds or modifies a CRD field or type
- Introduces a new controller, gateway, or binary
- Changes reconciliation logic, state transitions, or error handling
- Affects security posture (auth, RBAC, network, secrets)
- Introduces a new external dependency

Skip for: typo fixes, test additions to existing coverage, documentation-only changes.

---

## Required Sections

Every design record must include all of the following. Omitting a section blocks implementation.

### 1. Problem Statement

What is broken, missing, or insufficient? What is the user impact?

> Be specific. "Improve reliability" is not a problem statement. "FlowRun steps with `failurePolicy: Continue` incorrectly transition to `Failed` when any step fails" is.

### 2. Constraints

Hard boundaries the design must not violate:

- Kubernetes compatibility requirements
- RBAC and security invariants
- API stability guarantees (v1alpha1 → no field removals without deprecation)
- Performance budgets (e.g. reconcile loop must not block > 1s)
- External compatibility (OLM, OpenShift SCC, OperatorHub policies)

### 3. Invariants

Properties that must always hold after the change is merged. Written as testable assertions.

Examples:
- A FlowRun in `Succeeded` or `Failed` phase must never transition to `Running`.
- An executor Deployment must exist in a namespace before any FlowRun step executes an HTTP step.
- Credentials must never be written to etcd; they are resolved in-process and forwarded via internal RPC only.

### 4. Rejected Alternatives

At least one alternative considered and the reason it was ruled out. Include:
- What the alternative was
- Why it was rejected (constraint violated, complexity, risk, etc.)

This section prevents the same alternatives from being re-proposed in the future.

### 5. Tradeoffs

What does the chosen approach give up? Be honest. Examples:
- Increased memory usage for reduced reconcile latency
- Added operational complexity for improved security isolation
- Slightly less ergonomic API for better long-term evolvability

### 6. Final Decision

One paragraph stating the approach selected and why it best satisfies the constraints and invariants given the tradeoffs.

---

## File Location and Naming

Place design records in `docs/design/` using the filename format:

```
docs/design/<YYYY-MM-DD>-<slug>.md
```

Example: `docs/design/2026-04-08-flowrun-gc-policy.md`

Every record's header, immediately after the `#` title, must be:

```
> Status: Draft | Review | Approved | Superseded by docs/design/<file>.md
> Related: <files, docs, or backlog items this decision touches>
```

After creating or updating a record's Status, add or update its row in `docs/design/README.md`'s index (one line: date, title, status, one-sentence summary).

---

## Lifecycle

| Stage       | Action                                                                 |
|-------------|------------------------------------------------------------------------|
| Draft       | Author fills all six sections; shares for review                       |
| Review      | At least one peer confirms constraints and invariants are sound        |
| Approved    | Implementation may begin; design record is committed to the branch     |
| Superseded  | If a later design replaces this one, add a header: `> Superseded by docs/design/YYYY-MM-DD-<slug>.md` |

---

## References

- [CLAUDE.md](../../CLAUDE.md) — Development Philosophy section
- [planning/backlog/backlog.md](../backlog/backlog.md) — current backlog
