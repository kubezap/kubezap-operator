# Controller-Side mTLS for the Plugin Publisher Channel

> Status: Draft
> Related: `docs/guides/plugin-security.md`, `docs/api/plugin-contract.md`, `internal/certutil`, `internal/controller/executor_reconciler.go` (mTLS precedent), `docs/design/security-architecture.md`, `STORY-034`

## 1. Problem Statement

The controller calls a `type: plugin` Integration's `POST /publish` endpoint over plain HTTP. `docs/guides/plugin-security.md`'s "Controller-Side mTLS (Roadmap)" section documents this as a known gap, with a service mesh (Istio/Linkerd) as the only current mitigation — no first-party option exists for clusters without one, unlike the controller→HTTP-executor RPC channel, which already has `--executor-mtls=true` for exactly this situation (`docs/design/security-architecture.md`'s Layer 2). This story formalizes the same protection for the plugin publisher channel, which carries the same class of risk (a plugin's publish payload can include resolved data en route to an external system) without the executor channel's other mitigation (a mandatory NetworkPolicy — plugin egress policy exists as a documented, opt-in sample, not enforced by default the way the executor's is).

## 2. Constraints

- No change to `GET /healthz`'s behavior as a Kubernetes readiness probe target: kubelet's Pod `readinessProbe` `httpGet` action has **no mechanism to present a client certificate**, so `/healthz` cannot require mutual TLS without breaking the plugin Deployment's own built-in readiness check. Only `/publish` (controller-initiated) may require mTLS.
- Must not assume the operator controls the plugin's source code or build — per `CLAUDE.md`'s plugin trust model ("operator does not verify images"), plugins are third-party binaries. Any mechanism requiring the plugin process to *do* something (terminate TLS, read specific files) needs an explicit contract change the plugin author must implement, unlike `--executor-mtls`, where both endpoints are first-party code.
- Reuse `internal/certutil`'s CA/leaf-certificate issuance primitives (already used by both the executor-mTLS mechanism and `internal/webhookcerts`) rather than reimplementing cert generation, PEM encoding, or rotation logic.
- No cluster-wide RBAC; Secret storage for generated certs stays namespace-scoped, consistent with every other credential this project manages.
- Must not silently break plugins that haven't adopted the new contract fields — a plugin Integration that doesn't opt in continues working exactly as today, plain HTTP.
- No new external dependency (stdlib `crypto/tls`/`crypto/x509` only, matching `certutil`).

## 3. Invariants

- A plugin Integration with `spec.plugin.mtls.enabled` unset or `false` behaves identically to today: plain HTTP `/publish`, no cert Secret generated or mounted.
- When enabled, the controller presents a client certificate on every `/publish` call to that Integration's plugin, and only accepts a server certificate signed by that same Integration's generated CA — never another Integration's, and never the executor's or webhook admission's CA.
- `GET /healthz` never requires a client certificate, regardless of `spec.plugin.mtls.enabled` — kubelet's readiness probe must keep working unmodified.
- Each opted-in Integration's CA/cert material is generated and rotated independently of every other Integration's — a compromised or expired cert for one plugin Integration has no effect on another's trust chain.
- Generated private key material is never written anywhere other than the per-Integration Secret the plugin Deployment mounts — never logged, never in a CRD spec/status field.

## 4. Rejected Alternatives

**A single global `--plugin-mtls=true` controller flag, mirroring `--executor-mtls` exactly.** Rejected: the executor is first-party code — once the flag exists, every executor build understands the mounted cert unconditionally. Plugins are heterogeneous third-party binaries of unknown, individually-varying TLS readiness. A global flag would either break every plugin that hasn't implemented the (new) contract fields yet, the moment an operator enables it, or need per-plugin capability detection that's strictly more complex than just making the opt-in per-Integration in the first place.

**Require the plugin author to bring their own certificate** (e.g. `spec.plugin.mtls.certSecretRef` pointing at operator-provided-by-the-author material) rather than the operator generating and injecting one. Rejected as the default mechanism: it pushes a cert-issuance-and-rotation pipeline onto every plugin author who wants mTLS, exactly the operational burden `internal/certutil`'s self-signed rotation pattern exists to spare the executor case from — the same rationale applies here, and the primitive already exists to reuse. (Not ruled out as a *future*, additional advanced option for an author who already runs their own PKI — just not the initial/default path.)

**Extend mTLS to cover `GET /healthz` too, for full-surface mutual TLS on the plugin pod.** Rejected: kubelet's `httpGet` readiness probe action cannot present a client certificate (confirmed Kubernetes API limitation, not a KubeZap constraint), so this would break the Deployment's own health checking. `/healthz` stays outside the mTLS boundary.

**A single shared CA/cert bundle per namespace, covering every plugin Integration in it** (mirroring the executor's per-namespace-not-per-Integration bundle). Rejected: plugins in the same namespace can be different, unrelated third-party vendors/authors — a namespace-shared identity would let a compromised cert from one plugin's pod be replayed against a different plugin's publisher in the same namespace. Per-Integration certs scope blast radius to the one Integration, matching the fact that plugins are explicitly less-trusted, operator-unverified code — a materially different trust posture than the executor (first-party, operator-built), which is why the executor's per-namespace-shared precedent doesn't transfer directly here.

## 5. Tradeoffs

- **Requires plugin-author cooperation** — unlike `--executor-mtls`, this cannot be a zero-plugin-code-change opt-in. A plugin Integration setting `spec.plugin.mtls.enabled: true` only actually gets mTLS once its plugin binary reads the new contract fields and serves TLS on `KUBEZAP_PUBLISHER_PORT` using them; until an author updates their plugin, enabling the field just breaks `/publish` calls with a TLS handshake error against a plain-HTTP listener. `docs/api/plugin-contract.md` needs to document this ordering clearly (update the plugin first, then enable the field) and the Graduation Path story needs a way for a plugin to signal contract-version/capability support.
- **Per-Integration cert management is N times the bookkeeping of the executor's single global bundle** — more Secrets, more entries in the rotation goroutine's tracked set, all built from the same `certutil` primitives so the marginal code is small, but the operational surface (Secrets to inspect, rotation events to reason about) scales with the number of opted-in plugin Integrations, not a constant.
- **`GET /healthz` remains unauthenticated and unencrypted** even when `/publish` mTLS is enabled — not a full zero-trust posture for the plugin pod's entire HTTP surface, an accepted gap driven by kubelet's probe limitation.

## 6. Final Decision

Add a new opt-in CRD field, `spec.plugin.mtls.enabled` (bool, default `false`), to the plugin `Integration` type. When set, the controller generates a per-Integration CA and issues a server certificate (for the plugin) and a client certificate (for itself) using `internal/certutil`'s existing primitives — the same low-level building blocks `internal/webhookcerts` and the executor-mTLS mechanism already use, avoiding any new crypto code. The server cert + CA are injected into the plugin Deployment via a per-Integration Secret mount, alongside new `KUBEZAP_MTLS_*` environment variables (cert/key/CA file paths, extending the existing `KUBEZAP_NAMESPACE`/`KUBEZAP_INTEGRATION_NAME`/`KUBEZAP_PUBLISHER_PORT`/`KUBEZAP_LOG_LEVEL` set in `docs/api/plugin-contract.md`) that a plugin author must read and act on to actually terminate TLS — the contract-change requirement is the deliberate, unavoidable divergence from the `--executor-mtls` precedent, since plugins are third-party code the operator doesn't build. The controller presents its per-Integration client certificate on every `/publish` call and verifies the plugin's server certificate against that same Integration's CA. Certs rotate on the same 23h cadence as the executor-mTLS precedent, tracked per opted-in Integration rather than as a single global bundle. `GET /healthz` is explicitly exempted from mTLS so kubelet's built-in readiness probe keeps working unmodified.
