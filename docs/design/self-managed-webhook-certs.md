# Self-Managed Webhook Admission Certs

> Status: Approved
> Date: 2026-09-18
> Related: `cmd/main.go`, `config/webhook/manifests.yaml`, `hack/gen-webhook-certs.sh`, `config/dev/manager_dev_patch.yaml`, `test/e2e/e2e_suite_test.go`, `benchmarks/execution-latency/scripts/setup.sh`

## Problem

`cmd/main.go` starts a webhook TLS server but nothing in the repo provisions its cert or keeps `ValidatingWebhookConfiguration`'s `caBundle` in sync — there's no `config/certmanager` scaffold and no cert-manager wiring in `config/default`. Without a cert, the controller crash-loops on `make deploy`. The only stand-in, `hack/gen-webhook-certs.sh`, is explicitly "NOT for production use," and its logic is independently duplicated a second and third time in `test/e2e/e2e_suite_test.go` and `benchmarks/execution-latency/scripts/setup.sh` — none of the three patches `caBundle`. There is currently no supported way to run the operator's admission webhooks in production.

## Constraints

- No new hard dependency on cert-manager or any other cluster-installed component — must work for Helm, raw manifests, and OLM/OperatorHub with no external cert-provisioning step.
- Must work identically across all four `WATCH_NAMESPACES` modes and both Role/ClusterRole RBAC shapes.
- Must stay OpenShift restricted-SCC compliant (non-root, no privilege escalation); cert material goes to a Secret and the existing `certwatcher`-watched directory, not arbitrary host paths.
- A cert rotation must never produce a window where the served cert and `caBundle` disagree — `caBundle` updates at the same time as or before the new cert is written, never after.
- Reuse the existing `--webhook-cert-path`/`--webhook-cert-name`/`--webhook-cert-key` flags and `certwatcher.CertWatcher` wiring — only who writes the files it reads changes.
- Cert/key material is never logged; the self-signed CA's private key never leaves the operator's own namespace.
- If cert generation fails on startup, the manager fails fast with a clear error rather than starting the webhook server with no cert.
- This is the *only* cert-provisioning path for the admission webhooks — dev, e2e, benchmark, and production all exercise the same code.

## Rejected Alternatives

- **Standard kubebuilder cert-manager scaffold** (`config/certmanager/` + `cert-manager.io/inject-ca-from`) — turns cert-manager from an optional convenience (already used for `WebhookGatewayConfig` TLS Secrets) into a hard install-time dependency for every install path, and OLM already has its own independent cert-injection mechanism — cert-manager would only be load-bearing for Helm/raw-manifest paths, adding cross-install-method inconsistency rather than removing it.
- **Keep `hack/gen-webhook-certs.sh` as the permanent answer, just fix the stale doc link** — doesn't solve production at all, and the real problem is the triplicated logic with no `caBundle` patching anywhere, not the stale citation.

## Decision

Self-managed, in-process cert lifecycle following the `open-policy-agent/cert-controller` pattern: on startup (and periodically, leader-elected replica only), ensure a self-signed CA and serving cert/key for `kubezap-webhook-service.<namespace>.svc` exist in a Secret in the operator's own namespace; write the serving cert/key to the directory already read by `certwatcher.CertWatcher`; patch the CA's PEM bundle into all three `caBundle` fields on `validating-webhook-configuration`. `hack/gen-webhook-certs.sh`, `config/dev/manager_dev_patch.yaml`, and the duplicated e2e/benchmark provisioning blocks are removed once this lands and is verified across `make deploy`, `make test-e2e`, and a benchmark run.

- New RBAC: `get`/`create`/`update` on `secrets` (own namespace only) and `get`/`update`/`patch` on `validatingwebhookconfigurations` (cluster-scoped — a new *kind* of access, not just new scope).
- Trust root is a self-signed CA the operator generates and holds itself, with no external validation — same trust model as cert-manager's own self-signed `Issuer`; users needing an org-issued CA can still override `--webhook-cert-path` directly.
- Adds a small amount of long-lived code KubeZap now owns (cert gen, Secret I/O, `caBundle` patch, rotation) — offset by deleting three existing duplicated copies of provisioning logic.
