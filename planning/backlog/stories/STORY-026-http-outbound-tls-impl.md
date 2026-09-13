# STORY-026: Implement HTTP-step outbound TLS/CA support

**Epic:** EPIC-005 — HTTP Step Outbound TLS/CA Support
**Status:** Done (PR #214)
**Size:** M

## Description

Implements the decisions in `docs/design/2026-09-13-http-step-outbound-tls.md` (STORY-025, Approved): an additive `HttpIntegrationSpec.TLS` field letting an HTTP step trust a private CA bundle and/or present a client certificate for its outbound call.

## Acceptance Criteria

- [ ] `api/v1alpha1/integration_types.go`: add `HttpTLSSpec` type (`CABundleConfigMapRef *corev1.ConfigMapKeySelector`, `ClientCertSecretRef *corev1.LocalObjectReference`) and a `+optional TLS *HttpTLSSpec` field on `HttpIntegrationSpec`. Doc comments per the design record's field descriptions (reused for the eventual `docs/api/integration.md` update in STORY-027, not written here).
- [ ] `internal/executor/http/types.go`: add `TLSCABundle`, `TLSClientCert`, `TLSClientKey string` fields to `ExecuteRequest` (all `omitempty`, inline PEM content — never a reference, per the design record's RBAC constraint).
- [ ] `internal/controller/flowrun_controller.go`: extend `applyHTTPIntegration` to resolve `httpInteg.TLS` when set — read the `ConfigMapKeySelector`'s key for the CA bundle, read the `Secret`'s `tls.crt`/`tls.key` for the client cert (matching how `applyHTTPAuth` already resolves Secret-backed auth), and populate the new `ExecuteRequest` fields. Add a `+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch` marker near the existing `resources=secrets` marker on this file (free-floating, per `CLAUDE.md`'s marker-placement rule — verify it lands in `config/rbac/role.yaml` after `make manifests`, don't assume).
- [ ] `internal/executor/http/handler.go`: extend `httpClient` to accept the new CA-bundle/client-cert parameters (or read them off the already-passed `ExecuteRequest`) and build a `tls.Config` with `RootCAs` (cloned from `x509.SystemCertPool()`, bundle appended — additive, per the design record's explicit invariant, not a fresh empty pool) and `Certificates` (parsed via `tls.X509KeyPair`) when present. Existing `InsecureSkipVerify` logic stays untouched and independent.
- [ ] Client-certificate private key material is never logged and never written to `FlowRun.Status` — verify by inspecting every place `ExecuteRequest`/its fields get logged or persisted, per the design record's Invariants.
- [ ] Unit tests: `internal/executor/http/handler_test.go` — a real private CA + server cert fixture, a step configured to trust it succeeds, a step without the CA bundle configured against the same server fails (negative case), a client-cert-configured step against a server requiring mTLS succeeds. `internal/controller/flowrun_controller_test.go` — `applyHTTPIntegration` resolves `TLS` fields from a fixture `Integration`/`ConfigMap`/`Secret` correctly, including the "no `TLS` configured" no-op path.
- [ ] `make generate && make manifests` — confirm `configmaps` RBAC actually lands in `config/rbac/role.yaml` (per `CLAUDE.md`'s recurring gotcha, don't assume `make manifests` picked up a new marker).
- [ ] Live end-to-end validation on a real cluster (kind/k3s): a private CA, a service presenting a cert signed by it, an `Integration` configured to trust that CA via the new field, an HTTP step succeeding without `--allow-tls-skip-verify` — per the design record's own Success Metric, not just unit tests on the resolution logic in isolation.
- [ ] `go build ./...`, `go vet ./...`, `make test`, `make lint` all pass.

## File / Module Footprint

- `api/v1alpha1/integration_types.go`
- `internal/executor/http/types.go`
- `internal/executor/http/handler.go`
- `internal/controller/flowrun_controller.go`
- `internal/executor/http/handler_test.go`, `internal/controller/flowrun_controller_test.go`
- `config/rbac/role.yaml` (regenerated via `make manifests` — new `configmaps` RBAC rule)
- `config/crd/bases/automation.kubezap.io_integrations.yaml`, `charts/kubezap-operator/crds/automation.kubezap.io_integrations.yaml` (regenerated)

Touches `config/rbac/role.yaml`, one of `CLAUDE.md`'s Parallel Agent Guidelines hot files — fine to dispatch alone (nothing else in this batch touches it), but flag if a future batch pairs this with another RBAC-touching story.

## Dependencies

- Depends on: STORY-025 (design record, Approved)
- Blocks: STORY-027 (docs)

## Notes

Source: `planning/backlog/epics/EPIC-005-http-step-outbound-tls.md`'s Candidate Stories list. No design record needed for this story itself — it implements STORY-025's already-approved decisions rather than making new ones. If implementation surfaces a genuine open question the design record didn't anticipate, stop and flag it rather than deciding unilaterally (the same discipline STORY-009 followed against STORY-008).
