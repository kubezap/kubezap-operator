# <Title>

> Status: Draft
> Related: <files, docs, or backlog items this decision touches>

## 1. Problem Statement

What is broken, missing, or insufficient? What is the user impact? Be specific — "improve reliability" is not a problem statement.

## 2. Constraints

Hard boundaries the design must not violate: Kubernetes compatibility, RBAC/security invariants, API stability (`v1alpha1` → no field removals without deprecation), performance budgets, external compatibility (OLM, OpenShift SCC, OperatorHub policies).

## 3. Invariants

Properties that must always hold after the change is merged. Written as testable assertions.

## 4. Rejected Alternatives

At least one alternative considered and why it was ruled out (constraint violated, complexity, risk).

## 5. Tradeoffs

What does the chosen approach give up? Be honest about the downsides, not just the upside.

## 6. Final Decision

One paragraph: the approach selected and why it best satisfies the constraints and invariants given the tradeoffs.
