# Spec Drift Detection

Spec drift occurs when CRD Go types, generated manifests, documentation, and controller behavior diverge from each other. Undetected drift causes: user-facing doc errors, silent field ignorance, and API contract violations.

This document defines how to detect and resolve drift.

---

## Drift Surfaces

There are four surfaces that must stay in sync:

| Surface | Source of truth | Drift risk |
|---|---|---|
| **Go types** | `api/v1alpha1/*_types.go` | Fields added/removed without updating docs or manifests |
| **Generated manifests** | `config/crd/bases/*.yaml` | `make manifests` not run after type changes |
| **Documentation** | `docs/api/*.md` | Docs written against a planned API that was later revised |
| **Controller behavior** | `internal/controller/*_controller.go` | Fields parsed but never acted on, or acted on but not documented |

---

## Detection Process

Run this process after any CRD type change or before any GA milestone.

### Step 1 — Regenerate and diff manifests

```bash
make generate && make manifests
git diff config/crd/
```

If `config/crd/` has changes after `make manifests`, the manifests were out of date. Commit the regenerated files.

### Step 2 — Cross-check types against docs

For each `api/v1alpha1/*_types.go`:

1. List all exported struct fields and their JSON tags.
2. Open the corresponding `docs/api/*.md`.
3. Verify every field in the Go type appears in the doc (with correct name, type, and optionality).
4. Verify every field in the doc exists in the Go type (no phantom fields).
5. Verify enum values in `+kubebuilder:validation:Enum` match the doc's allowed values.

**Prompt template for targeted review:**
```
Compare api/v1alpha1/[foo]_types.go with docs/api/[foo].md.
Find: fields in the Go type not described in the doc; fields in the doc not present
in the Go type; enum values that differ; validation markers (MinItems, Minimum, Pattern)
that contradict the doc description. Report each as: location, type (missing-in-doc /
phantom-in-doc / enum-mismatch / marker-mismatch), and suggested fix.
```

### Step 3 — Cross-check docs against controller behavior

For each controller in `internal/controller/`:

1. Identify every field it reads from the CRD spec.
2. Verify the doc describes what the controller does with each field.
3. Identify every status field the controller writes.
4. Verify the doc describes each status field.

**Prompt template:**
```
Read internal/controller/[foo]_controller.go. List every field it reads from the
CRD spec and every status field it writes. Then compare against docs/api/[foo].md.
Report fields that are read but undocumented, fields that are documented as having
an effect but are never read by the controller, and status fields written but not
described in the doc.
```

### Step 4 — Validate sample CRs

For each file in `config/samples/`:

```bash
kubectl apply --dry-run=server -f config/samples/
```

Any validation error means the sample diverges from the current CRD schema. Fix the sample.

### Step 5 — Check for removed fields still referenced in docs

```bash
grep -r "fieldName" docs/api/
```

If a field was removed from the Go type but is still mentioned in docs, remove or update the reference.

---

## Drift Severity

| Type | Severity | Action |
|---|---|---|
| Phantom field in doc (documented, not in Go type) | **P0** — users write configs that are silently ignored | Remove from doc or implement the field |
| Field in Go type missing from doc | **P1** — users cannot discover the field | Add to doc |
| Enum mismatch | **P0** — users use invalid values per doc | Align doc with Go type |
| Manifest not regenerated | **P0** — CRD schema in cluster diverges from types | Run `make manifests` |
| Status field undocumented | **P2** — observability gap | Add to doc |
| Sample CR fails dry-run | **P1** — example is broken | Fix sample |

---

## Recurring Schedule

Run the full drift check:

- Before any GA milestone or OperatorHub submission
- After merging any branch that modifies `api/v1alpha1/`
- As a scheduled task — see `docs/schedule.md` §25

The check does not require a full repo scan. Limit scope to files changed in the branch plus their doc counterparts.

---

## References

- [Design Process](design-process.md) — invariants must not drift either
- [Pre-Merge Checklist](pre-merge-checklist.md) — drift check is a pre-merge gate
- [Code Review Strategy — Spec vs Implementation Mismatch](code-review-strategy.md#4-spec-vs-implementation-mismatch)
