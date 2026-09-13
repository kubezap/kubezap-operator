# STORY-021: Implement sub-second step-timing visibility on FlowRun

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Backlog — blocked on STORY-020 (design record)
**Size:** S — real AC/footprint pending STORY-020's decisions

## Description

Implements STORY-020's design record: gives `FlowRun` (and/or `StepRunStatus`) sub-second-precision visibility into real step execution time, so KubeZap's actual per-step latency (~5-10ms per STORY-015's benchmark) is visible on the object itself rather than only via a Prometheus scrape.

## Acceptance Criteria

Placeholder — cannot be finalized until STORY-020 answers (a) timestamp-precision vs. explicit-duration-field, and (b) whether `FlowRunStatus`'s top-level timing gets the same treatment. Re-groom once STORY-020 is Approved.

## File / Module Footprint

- `api/v1alpha1/flowrun_types.go` (new field(s) on `StepRunStatus` and/or `FlowRunStatus`)
- `internal/controller/flowrun_controller.go` (populate the new field(s) from the `time.Since()` value already computed for `metrics.StepDuration`)
- `make generate && make manifests` (CRD schema regeneration)
- Ginkgo tests in `internal/controller/flowrun_controller_test.go`

## Dependencies

- Depends on: STORY-020 (design record)
- Blocks: none

## Notes

Not yet groomed with real AC — per `/groom-backlog`'s own rule, a story without a real footprint can't be parallel-planned. Groom immediately after STORY-020 lands.
