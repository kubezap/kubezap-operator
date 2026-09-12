# Pre-Merge Checklist

Before any feature branch is considered complete, the author must work through this checklist. Items marked **[GATE]** block merge if not satisfied. Items marked **[FILE]** require a follow-up schedule item to be filed.

---

## 1. Change Summary

Write one paragraph that covers:
- What was changed and why
- Which CRDs, controllers, or gateways were modified
- Which behavior changed (not just which files)

> This is not a git log. It answers: "what does the system do differently now?"

---

## 2. Impacted Components

List every component affected by this change:

| Component | Nature of impact |
|---|---|
| `cmd/main.go` | New controller registered / flag added |
| `api/v1alpha1/` | Field added / removed / type changed |
| `internal/controller/` | Reconcile logic changed |
| `internal/gateway/` | Route handling changed |
| `docs/api/` | Doc updated to match type change |
| `config/crd/` | Manifest regenerated |
| `config/samples/` | Sample updated |
| `go.mod` / `go.sum` | Dependency added / updated |

Only list rows that apply. If a component is not listed, the reviewer can assume it was not touched.

---

## 3. Invariant Verification **[GATE]**

List the invariants relevant to this change (from the design doc or `docs/architecture/flowrun-state-model.md`). For each invariant, state whether it holds and how you verified it.

Example:
```
Invariant: A FlowRun in a terminal phase must never re-enter Running.
Verification: Checked early-return condition at flowrun_controller.go:219. Confirmed
              terminal phase guard runs before any step dispatch.
```

If no invariants apply, state that explicitly. Do not leave this section blank.

---

## 4. Risk Areas

Identify code paths where this change could cause a regression:

- **Data loss risk**: Does this change affect FlowRun status writes? Step result persistence?
- **Security risk**: Does this touch auth, secret handling, or RBAC?
- **Performance risk**: Does this add a new per-reconcile allocation or synchronous I/O?
- **Idempotency risk**: Is the new reconcile logic safe to run twice on the same object?
- **Migration risk**: Does this change a CRD field in a way that breaks existing objects?

For each area that applies, describe the risk and the mitigation.

---

## 5. Test Coverage Gaps **[FILE]**

List any test coverage that is missing after this change. For each gap:
- Describe the scenario not covered
- Note whether it is a unit test gap, E2E gap, or both
- File a schedule item before merging

Coverage expectation:
- Every new state transition has at least one unit test
- Every new reconciler path has at least one happy-path and one error-path test
- Every new CRD validation marker has a test that exercises the invalid case

---

## 6. Spec Drift Check **[GATE]**

Confirm:
- [ ] `make generate && make manifests` was run and `config/crd/` changes are committed
- [ ] All modified `api/v1alpha1/` fields appear in their `docs/api/` counterpart
- [ ] No phantom fields were introduced (doc references field not in Go type)
- [ ] `config/samples/` passes `kubectl apply --dry-run=server`

If any item fails, resolve it before marking the task complete.

---

## 7. Lint and Test **[GATE]**

```bash
make lint
go test ./...
```

Both must pass. Do not bypass lint with `//nolint` unless the suppression is justified in a comment.

---

## 8. Documentation **[GATE]**

- [ ] API docs updated for any field additions, removals, or behavior changes
- [ ] Architecture docs updated if a new component or flow was introduced
- [ ] If a design record exists for this feature (`docs/design/`), mark it as implemented

---

## Merge Readiness Declaration

When all [GATE] items are satisfied and all [FILE] items have schedule entries:

```
Pre-merge checklist complete for <feature-name>.
Gates: all satisfied.
Filed follow-up: <schedule section and item description>.
```

Paste this declaration in the commit message or PR description.

---

## References

- [Design Process](design-process.md)
- [Code Review Strategy](code-review-strategy.md)
- [Spec Drift Detection](spec-drift.md)
- [FlowRun State Model](../../docs/architecture/flowrun-state-model.md)
