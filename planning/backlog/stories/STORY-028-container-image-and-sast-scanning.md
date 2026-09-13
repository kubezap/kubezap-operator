# STORY-028: Container image CVE scanning + static analysis (CodeQL)

**Epic:** EPIC-002 — Post-Release Hardening & Feature Backlog
**Status:** Backlog — not yet groomed/footprinted
**Size:** M

## Description

CVE tracking today (documented in `SECURITY.md`'s Dependency Vulnerability Management section) covers two mechanisms: **Dependabot** (outdated Go modules, GitHub Actions, and Docker base images) and **`govulncheck`** (`.github/workflows/ci.yml`, reachability-aware Go dependency scanning). Both only look at *declared* dependencies — neither scans the actual built container images for OS-package CVEs introduced between base-image bumps (e.g. a CVE disclosed in a distroless/glibc package the day after the last Dependabot bump merged), and neither performs static analysis (SAST) on KubeZap's own Go source.

Found during a readiness review (2026-09-13, "how are we tracking CVEs other than dependabot?") — owner confirmed this is a real gap worth closing before public launch, not just a status check.

## Acceptance Criteria

- [ ] A container image scanner (Trivy, via `aquasecurity/trivy-action`, is the natural default — CNCF project, no license/account friction, first-class GHA support) runs against all 6 built images (`controller`, `webhook-gateway`, `kafka-gateway`, `amqp-gateway`, `nats-gateway`, `http-executor`) on every push to `main` and every PR that touches a `Dockerfile`. Fail the job on `CRITICAL`/`HIGH` findings with no known-false-positive suppression already documented; land it in `.github/workflows/ci.yml` alongside the existing `govulncheck` job, or a new `image-scan.yml` if build-matrix reuse from `publish-latest.yml` makes that cleaner — decide at implementation time.
- [ ] CodeQL (`github/codeql-action`, GitHub's own first-party SAST, free for public repos) is enabled for the Go source, either as a new workflow or via the repo's native Code Scanning setup — whichever avoids duplicating build steps already in `ci.yml`.
- [ ] Both scanners' results surface as GitHub Code Scanning alerts (Security tab), not just CI job pass/fail — matches how Dependabot findings are already visible today.
- [ ] `SECURITY.md`'s Dependency Vulnerability Management section updated to describe both new mechanisms alongside the existing Dependabot/`govulncheck` bullets, plus a triage-ownership line consistent with the existing "5 business days" convention.
- [ ] A test run (a deliberately outdated/vulnerable base image on a scratch branch, or a known-CVE'd indirect Go import) confirms each scanner actually fires and produces a visible alert — not just that the workflow runs green with nothing to find.

## File / Module Footprint

- `.github/workflows/` (new job or new file — decide at implementation time per the first AC)
- `SECURITY.md`

## Dependencies

- Depends on: none
- Blocks: none

## Notes

Source: `planning/backlog/follow-ups.md` (2026-09-13, from a readiness-review question, not a story/epic surfacing it). No design record needed — this adds CI tooling only, no change to runtime behavior, reconciliation logic, or the operator's own security posture (per `planning/process/design-process.md`'s trigger list); consistent with how STORY-017/018/022 (also CI/tooling/test additions) skipped one.
