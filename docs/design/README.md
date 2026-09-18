# Design Records

One file per non-obvious technical decision. Filenames are `docs/design/<slug>.md`.

A design record is required before implementing a new CRD field, controller, gateway, or binary; a change to reconciliation logic, state transitions, or security posture (auth, RBAC, network, secrets); or a new external dependency.

Every record follows this shape:

```
# <Title>

> Status: Draft | Review | Approved | Superseded by docs/design/<file>.md
> Date: <YYYY-MM-DD>
> Related: <files or docs this decision touches>

## Problem
What's broken, missing, or insufficient, and the user impact. 2-4 sentences —
exploit walkthroughs and bug-report detail belong in the PR/issue, not here.

## Constraints
Bullet list of hard boundaries the design must not violate, and properties that
must hold once it's in place (compatibility, RBAC/security invariants, API
stability, performance budgets, OLM/OpenShift/OperatorHub compatibility, etc).

## Rejected Alternatives
- **<Alternative>** — why it was ruled out, in one line.

## Decision
Short paragraph: the approach chosen, precise enough to implement from. Add a
`Tradeoffs` sub-list only if there's a real cost worth flagging beyond what
Rejected Alternatives already implies.
```

- **Draft** — author has filled in Problem, Constraints, Rejected Alternatives, and Decision, and shared it for review.
- **Review** — at least one peer has confirmed the constraints are sound.
- **Approved** — implementation may begin; the record is committed to the branch.
- **Superseded** — a later record replaces this one; add `Superseded by docs/design/<file>.md` to the header.

When a decision is later reversed, don't edit or delete the old record — write a new one and mark the old one's Status as `Superseded by docs/design/<file>.md`.

Multi-decision reference files are permitted for a single security/architecture surface with many related decisions that a strict one-file-per-decision split would artificially fragment — see [`security-architecture.md`](security-architecture.md), which isn't part of the index below. Default to one file per decision otherwise.

## Index

| Date       | Title                                                                                   | Status   |
| ---------- | ---------------------------------------------------------------------------------------- | -------- |
| 2026-03-18 | [No External Database for FlowRun History](scale-limitations.md)                       | Approved |
| 2026-09-10 | [Flow Parameters](flow-parameters.md)                                                   | Approved |
| 2026-09-10 | [Plugin Publish Envelope](plugin-publish-envelope.md)                                   | Approved |
| 2026-09-11 | [CEL Access to Nested Trigger Body Fields](cel-trigger-body-fields.md)                  | Approved |
| 2026-09-11 | [HTTP Executor Egress NetworkPolicy](executor-egress-networkpolicy.md)                  | Approved |
| 2026-09-11 | [Secret Rotation Detection for Gateway Watchers](secret-rotation-watches.md)            | Approved |
| 2026-09-11 | [Single-Pass Template Interpolation](single-pass-interpolation.md)                      | Approved |
| 2026-09-11 | [Typed Phase/FailurePolicy Enums](typed-phase-enums.md)                                 | Approved |
| 2026-09-11 | [Webhook Gateway Trust Boundary Hardening](webhook-gateway-trust-boundary.md)           | Approved |
| 2026-09-13 | [FlowRun Step-Timing Precision](flowrun-step-timing-precision.md)                       | Approved |
| 2026-09-13 | [HTTP-Step Outbound TLS/CA Support](http-step-outbound-tls.md)                          | Approved |
| 2026-09-18 | [CLI Tool Scope](cli-tool-scope.md)                                                      | Approved |
| 2026-09-18 | [Self-Managed Webhook Admission Certs](self-managed-webhook-certs.md)                   | Approved |
| 2026-09-18 | [WebhookGatewayConfig Singleton via Fixed Name](webhookgatewayconfig-singleton-name.md) | Approved |
