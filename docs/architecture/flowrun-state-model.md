# FlowRun State Model

This document is the authoritative definition of the FlowRun and step lifecycle. The controller implementation must conform to this model. Any divergence is a bug.

---

## FlowRun Phases

A FlowRun (top-level object) has one of five phases:

| Phase       | Meaning                                                              | Terminal? |
|-------------|----------------------------------------------------------------------|-----------|
| `""`        | Object just created; controller has not yet processed it             | No        |
| `Pending`   | Controller has acknowledged the FlowRun; no step has started         | No        |
| `Running`   | At least one step is executing                                        | No        |
| `Succeeded` | All required steps completed successfully (or were Skipped)          | Yes       |
| `Failed`    | At least one step failed and `failurePolicy` did not allow continuing | Yes       |
| `Cancelled` | FlowRun was deleted while Running; controller cleaned up in-flight work | Yes     |

---

## Step Phases

Each step within a FlowRun has one of six phases:

| Phase       | Meaning                                                              | Terminal? |
|-------------|----------------------------------------------------------------------|-----------|
| `Pending`   | Step has not started; waiting for dependencies or capacity           | No        |
| `Running`   | Step is actively executing (HTTP call in-flight, etc.)               | No        |
| `Waiting`   | Step is a `wait` action paused until a duration elapses              | No        |
| `Succeeded` | Step completed successfully                                          | Yes       |
| `Failed`    | Step encountered an error (network, validation, retry exhausted)     | Yes       |
| `Skipped`   | Step was bypassed because its `when` condition evaluated to false, or an upstream dependency failed under `failurePolicy: Continue` | Yes |

---

## Valid FlowRun Phase Transitions

```
"" ──► Pending ──► Running ──► Succeeded
                     │
                     ├──► Failed
                     │
                     └──► Cancelled   (only via DeletionTimestamp)
```

**Rules:**
- `""` → `Pending`: on first reconcile of a new FlowRun.
- `Pending` → `Running`: when the controller begins executing the first step.
- `Running` → `Succeeded`: when all steps are in a terminal phase and none failed (or `failurePolicy: Continue` absorbed all failures).
- `Running` → `Failed`: when any step is `Failed` and `failurePolicy` is `Abort` (default), after in-flight steps drain.
- `Running` → `Cancelled`: only when `DeletionTimestamp` is set while phase is `Running`.
- **Terminal phases (`Succeeded`, `Failed`, `Cancelled`) must never transition to any other phase.** The controller returns early on these.

---

## Valid Step Phase Transitions

```
Pending ──► Running ──► Succeeded
              │
              ├──► Failed
              │
              └──► Waiting ──► Running   (wait duration elapsed)
                      │
                      └──► Failed        (wait timed out or FlowRun cancelled)

Pending ──► Skipped                      (when condition false, dep failed under Continue policy,
                                           or all runAfter deps were themselves Skipped)
```

**Rules:**
- `Pending` → `Running`: dependencies are met and step is dispatched.
- `Pending` → `Skipped`: `when` CEL condition is false, all unsatisfied deps are `Failed` under `failurePolicy: Continue`, or all of the step's `runAfter` dependencies were themselves `Skipped` (cascade-skip — see `allDepsSkipped` in `flowrun_controller.go`).
- `Running` → `Succeeded` / `Failed`: step execution returns a result.
- `Running` → `Waiting`: step is a `wait` action; recorded as Waiting during the pause interval.
- `Waiting` → `Running`: the wait duration has elapsed and the step re-enters execution.
- `Waiting` → `Failed`: FlowRun is cancelled or the wait deadline is exceeded.
- **A step in a terminal phase (`Succeeded`, `Failed`, `Skipped`) must not be re-dispatched.** The controller checks `existing.Phase != ""` and `existing.Phase != "Pending"` before dispatching.

---

## Invalid / Dangerous Transitions

The following transitions are explicitly forbidden and indicate a controller bug:

| Transition | Risk |
|---|---|
| `Succeeded` → `Running` | Double-execution of a completed FlowRun |
| `Failed` → `Running` | Retry without an explicit retry mechanism |
| `Succeeded` → `Failed` | Retroactive failure after user sees success |
| `Cancelled` → `Running` | Execution after deletion |
| Step `Succeeded` → `Running` | Double-execution of a completed step |
| Step `Failed` → `Running` (without retry increment) | Silent retry without accounting |
| Empty phase → `Succeeded` / `Failed` | Skipped `Running` phase; metrics/events missed — **except for the common case described in "Step Phase Observability" below, which is accepted, not a bug** |

### Step Phase Observability

For steps that complete synchronously within a single reconcile — the common case for `http`, `transform`, and `publish` steps, and any `wait` step whose duration hasn't elapsed yet on its first execution — the `Running` phase exists only on an in-memory `StepRunStatus` value that gets overwritten with the terminal phase before the batched `Status().Update()` call for that reconcile wave. It is never independently persisted, so it is not observable via `kubectl get flowrun` or `kubectl get flowrun -w` for these steps. No invariant is violated by this and nothing double-executes — the "empty phase → terminal" row above is the expected, common-case shape of a fast step's status history, not evidence of a skipped write.

`Running` **is** genuinely observable mid-execution for exactly two cases:
- a step retried after an executor transport error (matches the model's retry semantics above), and
- an unfinished `wait` step (phase `Waiting`).

External tooling that expects to observe every step transition through `Running` before it completes — a dashboard, `kubectl get flowrun -w`, alerting on stuck steps — will not see that transition for typical fast steps today. This is a deliberate architectural tradeoff (one batched status write per reconcile wave, not one write per step) rather than an oversight; adding an intermediate status write per step would make `Running` universally observable at the cost of an extra API call per step per reconcile. That tradeoff has not been made — this document only records the current, accepted behavior.

---

## Edge Cases

### Retries

- Retry is handled within the `Running` phase by re-dispatching the same step after a backoff.
- `StepRunStatus.Attempts` must be incremented on each attempt.
- After `MaxRetries` attempts all result in failure, the step transitions to `Failed`.
- The step never transitions back to `Pending` during retry — it stays logically `Running` until exhausted.

### Duplicate FlowRuns (Kafka dedup)

- Kafka-sourced FlowRuns include the partition + offset in the name as a dedup key.
- If a FlowRun with that name already exists in any terminal phase, the gateway must not create a duplicate.
- The controller does not deduplicate on its own — dedup is the gateway's responsibility.

### Partial Failures with `failurePolicy: Continue`

- When a step fails under `failurePolicy: Continue`, downstream steps that `runAfter` it may still execute.
- `dependenciesMet` must accept `Failed` deps as satisfied when the failure policy allows continuation.
- The FlowRun transitions to `Succeeded` if all steps complete (even if some are `Failed`) and `failurePolicy: Continue` is active.
- The FlowRun transitions to `Failed` only if the post-completion check determines the overall policy requires failure. See `flowrun_controller.go:612`.

### Wait Step Re-entry

- On each reconcile, the controller checks all `Waiting` steps to see if the duration has elapsed.
- `StartTime` on a `Waiting` step must be set once and preserved on re-entry. It must not be overwritten on subsequent reconciles. See `flowrun_controller.go:464`.

### FlowRun GC and Terminal Phases

- GC (TTL and count-based) applies to `Succeeded`, `Failed`, and `Cancelled` phases.
- `Cancelled` must be included in GC checks — it is a terminal phase and does not exempt a FlowRun from cleanup.

---

## Conformance Validation

To verify the implementation conforms to this model:

1. **Static check**: search for all `.Phase =` assignments in `flowrun_controller.go` and verify each assignment is a valid transition given the surrounding conditions.
2. **Test coverage**: every valid transition should have at least one unit test; every invalid transition should have a test that confirms it cannot occur.

This validation was last run in full 2026-09-10; re-run it (via a `/groom-backlog`-created Story) whenever `flowrun_controller.go`'s phase-transition logic changes materially, not on a fixed schedule.

---

## References

- Implementation: `internal/controller/flowrun_controller.go`
- Types: `api/v1alpha1/flowrun_types.go`
- State Transition Correctness is one of the required review lenses in this project's internal code-review process (`planning/process/code-review-strategy.md` in the repository — not part of this published docs site)
- [FlowRun API docs](../api/flowrun.md)
