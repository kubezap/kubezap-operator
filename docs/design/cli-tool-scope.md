# CLI Tool Scope

> Status: Approved
> Date: 2026-09-18
> Related: `cmd/kubezap/`, `internal/cli/`, `docs/guides/using-the-cli.md`

## Problem

`kubectl get flowruns` only surfaces the fields declared in `+kubebuilder:printcolumn` markers (flow name, phase, age) — it can't filter by trigger, filter by phase, show a per-step execution timeline, or live-tail completions. Operators debugging a Flow have to piece this together from multiple `kubectl` commands.

## Constraints

- No new hard dependency beyond what's already in `go.mod` (`client-go`, `controller-runtime`) plus a CLI framework.
- Must resolve kubeconfig/context the same way `kubectl` does, with no separate auth story.

## Rejected Alternatives

- **Full read/write CLI now** (`kubezap create`, editor-backed templates) — a write path needs its own authorization/validation design; shipping read-only first closes the observability gap without blocking on that.
- **A Job/CronJob-based reporting sidecar** — can't reuse a local kubeconfig/context the way a standalone binary can, and adds a cluster-side component for what's fundamentally a client-side query tool.

## Decision

Ship `kubezap` as a single `kubectl-kubezap` binary, invocable standalone (`kubezap <command>`) or as a kubectl plugin (`kubectl kubezap <command>`), since kubectl discovers any `kubectl-*` binary on `$PATH`. Read-only for v1 — `history`, `triggers`, `flows`, `integrations`, `watch`, `version` — covering the FlowRun-history and status-inspection gap `kubectl get` leaves; write operations are deferred to a future release. See `docs/guides/using-the-cli.md` for usage and `internal/cli/` for the implementation.
