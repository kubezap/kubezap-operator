# Security Policy

## Supported Versions

KubeZap is currently pre-1.0 (`v1alpha1` API). Security fixes are made against the latest released version only; there is no long-term support branch yet.

| Version | Supported |
| ------- | --------- |
| Latest release | :white_check_mark: |
| Older releases | :x: |

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report suspected vulnerabilities privately via **GitHub Security Advisories**: open a draft advisory at [github.com/kubezap/kubezap-operator/security/advisories/new](https://github.com/kubezap/kubezap-operator/security/advisories/new). This keeps the report private until a fix is ready and lets us coordinate a disclosure timeline with you directly in GitHub.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce (a minimal Flow/Trigger/Integration manifest, or request, that demonstrates the issue)
- The KubeZap version (or commit SHA) affected

### What to expect

- **Acknowledgment**: within 3 business days.
- **Initial assessment**: within 7 business days, including whether the report is accepted as a vulnerability and its rough severity.
- **Fix timeline**: depends on severity and complexity; we'll communicate an estimated timeline once the report is triaged, and keep you updated on progress.
- **Disclosure**: we'll coordinate public disclosure timing with you — typically once a fix is released, via a GitHub Security Advisory. Credit is given to reporters who want it.

### Scope

In scope: the operator (`cmd/main.go`), gateways (webhook/kafka/amqp/nats), the HTTP executor, the `kubezap` CLI, and the Helm chart / OLM bundle as shipped in this repository.

Out of scope: vulnerabilities in third-party dependencies (report those upstream — see below for how we track and pick up upstream fixes ourselves), and issues that require an attacker to already have cluster-admin or equivalent privileges within the target cluster.

## Dependency Vulnerability Management

Automated tooling watches for known vulnerabilities in this project's own dependencies (Go modules, GitHub Actions, and each component's base container image):

- **Dependabot** (`.github/dependabot.yml`) opens a weekly PR for any outdated Go module, GitHub Action, or Docker base image, including ones with a known CVE.
- **`govulncheck`** runs on every push to `main` and every PR (`.github/workflows/ci.yml`), scanning for known vulnerabilities in the Go module dependency graph that are actually reachable from KubeZap's own code (not just present in `go.sum`).
- **Trivy** (`aquasecurity/trivy-action`, in the `docker-build` job of `.github/workflows/ci.yml`) scans each of the 6 built container images (`controller`, `webhook-gateway`, `kafka-gateway`, `amqp-gateway`, `nats-gateway`, `http-executor`) for known OS-package CVEs on every push to `main` and every PR — catching vulnerabilities introduced into a base image between Dependabot bumps, not just outdated declared dependencies. The job fails on any `CRITICAL`/`HIGH` finding, and results are also uploaded as SARIF to the GitHub Security tab as Code Scanning alerts.
- **CodeQL** (`github/codeql-action`, `.github/workflows/codeql.yml`) runs static analysis (SAST) on KubeZap's own Go source on every push to `main`, every PR, and a weekly schedule, surfacing findings as Code Scanning alerts in the Security tab.

**Triage process**: a Dependabot security PR, a `govulncheck` CI failure, a Trivy CRITICAL/HIGH image finding, or a CodeQL alert is triaged by a maintainer within 5 business days of appearing. Patch-level bumps with passing CI are merged directly; anything requiring a code change (an API break in the updated dependency, a `govulncheck` finding whose fix isn't a simple version bump, a Trivy finding requiring a base-image change, or a CodeQL alert requiring a source fix) is scheduled based on severity, with critical/high findings prioritized ahead of routine work.
