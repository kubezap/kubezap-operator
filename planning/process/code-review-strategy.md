# Code Review Strategy

This document defines how code reviews are conducted in KubeZap. The goal is high-signal, low-noise reviews that catch real bugs — not full-repo scans.

---

## Why Full-Repo Reviews Are Inefficient

A full-repo review:
- Produces too many findings to act on in one session
- Conflates cosmetic issues with real bugs
- Wastes capacity on code that hasn't changed
- Produces results that age out before fixes land

Instead: **targeted reviews with a specific invariant or concern as the entry point.**

---

## Review Targets

Each review session should name one target from the list below. The target defines the scope and the prompt template to use.

### 1. Invariant Violation Check

**When to use:** After any change to reconciliation logic or state transitions.

**Scope:** The modified controller file(s) and their direct callers.

**Prompt template:**
```
Review [file] for violations of the following invariants:
- [list invariants from the relevant design doc]
Find any code path where an invariant could be violated. Report file:line, the violation,
and the triggering condition. Do not report style issues.
```

---

### 2. Idempotency Audit

**When to use:** Before merging any reconciler change.

**Scope:** The `Reconcile()` function and helpers it calls.

**Prompt template:**
```
Audit [reconciler file] for idempotency violations. A reconcile loop must produce
the same outcome when run twice against the same cluster state. Find any case where
a second reconcile of the same object would: create a duplicate resource, overwrite
a field set by a previous reconcile, or fail due to a resource already existing.
Report file:line and describe the trigger condition.
```

---

### 3. Reconciliation Safety Check

**When to use:** Before any reconciler ships to production.

**Scope:** Controller file under review.

**Prompt template:**
```
Review [file] for reconciliation safety issues. Specifically look for:
- Status updates that are not retried on conflict (missing StatusClient.Update retry)
- Missing requeue on transient errors (returning nil on non-NotFound errors)
- Panics or fatal calls inside the reconcile loop
- Goroutine leaks (goroutines started without context cancellation)
- Missing finalizer removal before object deletion
Report file:line and severity (P0/P1/P2).
```

---

### 4. Spec vs Implementation Mismatch

**When to use:** After updating a CRD type or its documentation.

**Scope:** The `_types.go` file, its corresponding doc page, and the controller that reads those fields.

**Prompt template:**
```
Compare [api/v1alpha1/foo_types.go] with [docs/api/foo.md] and [internal/controller/foo_controller.go].
Find: fields documented but not implemented, fields implemented but not documented,
validation markers that contradict controller behavior, and status fields set by the
controller that are not described in the doc. Report each mismatch as: location, type
(doc-only / impl-only / marker-mismatch / status-gap), and severity.
```

---

### 5. Security Boundary Audit

**When to use:** Any change touching auth, secrets, RBAC, or network paths.

**Scope:** The changed file(s) and adjacent handler code.

**Prompt template:**
```
Audit [file] for security boundary violations. Check for:
- Credentials or secret values logged or written to etcd
- Non-constant-time comparisons of secrets or tokens
- Missing input validation on external inputs (webhook bodies, Kafka payloads)
- SSRF risks in outbound HTTP paths
- RBAC rules that grant more than least-privilege
Report file:line, the risk, and the exploit scenario.
```

---

### 6. Error Handling Completeness

**When to use:** After adding new I/O paths (HTTP calls, Kubernetes API calls, Kafka).

**Scope:** The new function and its callers.

**Prompt template:**
```
Review [file] for incomplete error handling. Find:
- Errors that are silently dropped (assigned to _ or ignored)
- Transient errors that immediately fail instead of requeueing
- Errors that produce misleading status messages (e.g. "step failed" when the real
  cause is a network timeout)
- Missing context propagation (context.Background() used where ctx should be passed)
Report file:line and describe what fails silently.
```

---

### 7. Resource Leak Detection

**When to use:** After changes that create goroutines, open connections, or allocate caches.

**Scope:** The changed file(s).

**Prompt template:**
```
Audit [file] for resource leaks. Look for:
- Maps or slices that grow without bounds (no eviction, no max size)
- Goroutines started without a done channel or context cancellation
- HTTP clients or Kafka producers created per-call instead of once at startup
- Timers or tickers not stopped on shutdown
Report file:line and describe the leak condition and its steady-state impact.
```

---

### 8. State Transition Correctness

**When to use:** After any change to FlowRun or step phase assignment.

**Scope:** `internal/controller/flowrun_controller.go` phase assignment sites.

**Prompt template:**
```
Review [file] against the FlowRun state model in docs/architecture/flowrun-state-model.md.
Find any phase assignment that:
- Transitions to an invalid next state
- Skips a required intermediate state
- Assigns a terminal phase to a non-terminal step or FlowRun
- Overwrites a terminal phase (Succeeded/Failed/Cancelled) with a non-terminal one
Report file:line, the invalid transition, and the triggering code path.
```

---

## Process Rules

1. **Name the invariant or concern before starting.** "Review the controller" is not a target. "Check flowrun_controller.go for idempotency violations in the wait step path" is.
2. **Limit scope to files touched by the change + direct callers.** Do not expand to unrelated files mid-review.
3. **P0 findings block merge.** P1 findings get a Story filed before merge (via `/groom-backlog`). P2 findings get logged in `planning/backlog/follow-ups.md`.
4. **Record findings directly as backlog entries** — a `follow-ups.md` entry for anything not fixed immediately, or a new Story for anything sized enough to need one — rather than a standalone tech-debt doc.

---

## References

- [FlowRun State Model](../../docs/architecture/flowrun-state-model.md)
- [Pre-Merge Checklist](pre-merge-checklist.md)
- [planning/backlog/backlog.md](../backlog/backlog.md)
