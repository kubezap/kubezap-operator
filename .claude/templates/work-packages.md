# Work Packages — YYYY-MM-DD

Produced by `/plan-parallel`. Each package is either dispatched alone or in parallel with other packages in this same batch — never with a package it overlaps.

## Candidate stories considered

- STORY-xxx — footprint: `path/...`
- STORY-xxx — footprint: `path/...`

## Conflict analysis

Pairwise footprint overlap check. Any overlap forces serialization (ordered, not parallel). Cross-check against `CLAUDE.md`'s Parallel Agent Guidelines hot-files list too — a story touching `cmd/main.go`, `api/v1alpha1/groupversion_info.go`, `go.mod`/`go.sum`, `config/rbac/*.yaml`, or `docs/schedule.md`'s successor (`planning/backlog/backlog.md`) needs a wiring pass, not parallel dispatch.

| Story | Overlaps with | Resolution |
|---|---|---|
| STORY-xxx | none | parallel-safe |
| STORY-xxx | STORY-yyy (`shared/path`) | serialize: xxx before yyy |

## Work packages (dispatch plan)

### WP-1 (parallel-safe with WP-2, WP-3)
- STORY-xxx

### WP-2 (parallel-safe with WP-1, WP-3)
- STORY-xxx

### WP-3 (must follow WP-1 — shared footprint)
- STORY-xxx
