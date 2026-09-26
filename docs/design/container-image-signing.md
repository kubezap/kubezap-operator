# Container Image Signing/Provenance

> Status: Approved
> Date: 2026-09-19
> Related: `.github/workflows/release.yml`, `SECURITY.md`

## Problem

The 6 published container images (`controller`, `webhook-gateway`, `kafka-gateway`, `amqp-gateway`, `nats-gateway`, `http-executor`) carry no signature or build provenance. Nothing lets a consumer verify a pulled image was actually built by KubeZap's own CI from the source it claims to be, rather than tampered with or substituted at the registry — the same class of risk realized concretely elsewhere this cycle (the `aquasecurity/trivy-action` supply-chain compromise, GHSA-69fq-xp46-6x23). An unsigned image is indistinguishable from a compromised one to anyone consuming it.

## Constraints

- No new hard operational dependency — must not require provisioning, storing, or rotating a long-lived private signing key as a precondition for every release to succeed.
- Must run for both RC and final tags without special-casing (an RC is exactly where you'd want to catch a broken signing setup, not first-discover it on a final release).
- Must not require a paid/self-hosted signing service; consumers must be able to verify with widely available open tooling (`cosign`) against public transparency infrastructure, not a private KMS endpoint only the project controls.
- Must integrate into the existing per-image build-and-push matrix in `.github/workflows/release.yml` without restructuring it.
- OperatorHub/OLM certification does not currently mandate cosign-style image signing for community operators as a hard gate — this is a proactive hardening measure, not a compliance requirement, so the design must not block or complicate the existing OLM bundle/CSV pipeline.

## Rejected Alternatives

- **KMS-backed static keypair** — rejected: requires provisioning a long-lived private key, storing it as a GitHub Actions secret, and owning rotation/revocation. Adds real operational burden without a materially stronger guarantee than keyless signing for this project's actual threat model (registry tampering / build substitution), which keyless OIDC-bound certificates already address directly.
- **SBOM-only (no signing)** — rejected: an SBOM answers "what's in the image," not "who built it." It does nothing to prove an image came from this project's CI rather than being pushed by someone else with registry write access.
- **Full SLSA provenance attestation in the same pass** — deferred, not rejected: valuable, but a materially larger scope (a SLSA generator, a separate attestation format, its own verification docs). Doing signing first establishes the mechanism and consumer-facing verification story; SLSA attestation is a natural, independent follow-on once that's proven out.

## Decision

Adopt `cosign` keyless signing (Sigstore's public-good Fulcio + Rekor instances) tied to the GitHub Actions OIDC identity of the release workflow. Add a signing step immediately after each image push in `.github/workflows/release.yml`'s existing per-image matrix, signing by digest (not tag) so the signature is bound to the exact content pushed. This runs unconditionally for both RC and final tags. A signing failure fails the release job outright — an unsigned image published without any visible signal defeats the entire purpose, so this is a hard gate, not a soft `continue-on-error` step. Document the verification command in `SECURITY.md` (`cosign verify --certificate-identity <workflow-identity> --certificate-oidc-issuer https://token.actions.githubusercontent.com <image>@<digest>`) so a consumer can check a signature before deploying. SLSA provenance attestation is explicitly out of scope for this pass — noted as a candidate follow-up once keyless signing is validated in production.

### Tradeoffs

Verification requires network access to Rekor's public transparency log at verify-time (a minor availability dependency for consumers verifying offline/air-gapped, though the signature itself is still checkable against the embedded certificate without it). Signing failing the release job means a transient Fulcio/Rekor outage can block a release — accepted, since a silently-unsigned image is a worse outcome than a delayed release.
