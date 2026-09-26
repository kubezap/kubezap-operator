# Certificate Revocation Checking (CRL) for Verified Webhook Client Certs

> Status: Draft
> Related: `internal/gateway/webhook/handler.go`, `cmd/webhook-gateway/main.go`, `docs/api/webhookgatewayconfig.md`, `docs/guides/webhook-security.md`

## 1. Problem Statement

`kubezap.io/webhook-mtls-ca-secret` (inbound webhook mTLS client-cert auth, `cmd/webhook-gateway/main.go:197-198`) verifies a presented client certificate's chain and expiry via Go's stdlib `tls.Config{ClientCAs, ClientAuth: tls.RequireAndVerifyClientCert}` — confirmed by reading the actual handshake config, not assumed. Go's stdlib TLS handshake never checks certificate revocation (CRL or OCSP) on its own; that requires explicit additional code. So today, a compromised or otherwise-revoked client cert remains accepted by KubeZap until it naturally expires — there is no way to cut off a specific presented cert before then short of rotating the entire trusted CA (which invalidates every other still-valid cert issued by it too).

## 2. Constraints

- Scoped to `kubezap.io/webhook-mtls-ca-secret` only — the client certs verified there are externally issued (by whatever CA the operator configured as trusted), unlike `--executor-mtls` and the new plugin-publisher mTLS design (`docs/design/plugin-publisher-mtls.md`), both of which verify KubeZap-generated, KubeZap-rotated certs with no external revocation concept (compromise recovery there is just re-rotation, already built in). This design does not extend to those two channels.
- Must not require live, per-connection network access to an external endpoint on the webhook auth hot path — a hard requirement given this project's stated OpenShift/enterprise target includes intentionally air-gapped or network-restricted deployments (worth confirming with the owner if a specific customer commitment exists, but treated as a real constraint here regardless, since it's the harder requirement to retrofit later if ignored now).
- No new external dependency — CRL parsing uses stdlib `crypto/x509` (`ParseRevocationList`), matching every other cert-handling code path in this project (`internal/certutil`).
- Must not change `kubezap.io/webhook-mtls-ca-secret`'s existing behavior when no CRL is configured (default: identical to today, no revocation checking) — this is a strictly additive, opt-in feature.
- No cluster-wide RBAC; any new watched resource stays namespace-scoped.

## 3. Invariants

- With no CRL configured, client-cert verification behaves identically to today (chain + expiry only).
- With a CRL configured, a client cert whose serial number appears in the current CRL is rejected, even if its chain and expiry are otherwise valid.
- If the configured CRL's `nextUpdate` timestamp has passed (stale), all client-cert authentications are rejected until a fresh CRL is provided — fail-closed, matching this project's existing convention for security-critical unknowns (`checkSSRF`'s "treat DNS failure as a block" precedent in `internal/executor/http/ssrf.go`).
- KubeZap never fetches a CRL from a network location on its own initiative — the operator provides and refreshes it; the gateway only reads from a resource the operator's own tooling writes to.
- The webhook gateway's own TLS server certificate/key and the trusted CA remain in a Secret (as today); the CRL itself is stored separately as a ConfigMap, since a CRL is public data by design (a CA publishes it for anyone to check) — not sensitive material.

## 4. Rejected Alternatives

**OCSP (live per-connection query) as the primary mechanism.** Rejected: a live network round-trip to an external OCSP responder on every webhook authentication directly violates the air-gapped/offline-cluster constraint, and introduces a new availability dependency plus a fail-open-vs-fail-closed question for a security-critical hot path that CRL checking (operator-pushed, no live fetch) avoids entirely. Go's stdlib also has no built-in OCSP client, so this would be no less new code than the CRL path while being strictly worse for the constraint that matters most here.

**Document this as a service-mesh or ingress-controller responsibility and don't build it — the same posture the interim mitigation for plugin-publisher mTLS uses.** Considered seriously, as the story asked, but rejected here specifically: a service mesh's automatic mTLS (Istio/Linkerd) provides *mesh-internal workload identity* between services inside the cluster — it has no bearing on verifying an *externally-issued* client certificate presented by an outside caller (a GitHub-style webhook source, a partner system), which is `kubezap.io/webhook-mtls-ca-secret`'s actual job. There is no mesh feature that substitutes for this. Unlike the plugin-mTLS case, KubeZap already directly implements the underlying verification itself (`tls.RequireAndVerifyClientCert`) for any deployment that doesn't front the gateway with an ingress/API-gateway doing this instead — leaving that self-owned path structurally unable to check revocation is a real, half-finished feature, not a legitimate "not our job" boundary.

**Store the CRL in a Secret, matching the CA-cert convention.** Rejected: a CRL is public-by-design data — the whole point of a CRL is that a CA publishes it for anyone to check. Storing it as a Secret misrepresents its sensitivity and risks misleading an operator's RBAC/secret-scanning tooling into treating it as confidential.

**KubeZap automatically fetches the CRL from the cert's CRL Distribution Points URL.** Rejected as the default: reintroduces the same live-external-network-dependency problem OCSP has, just on a periodic-refresh cadence instead of per-request — still breaks true air-gapped support unless the operator mirrors that URL internally anyway, at which point manually providing the CRL via ConfigMap is no more operator burden and has no network dependency at all. Left as a possible *additional*, opt-in convenience for non-air-gapped clusters in a later iteration, not the initial mechanism.

## 5. Tradeoffs

- **No automatic freshness guarantee.** KubeZap provides no built-in fetch/refresh; an operator whose own CRL-refresh pipeline silently breaks eventually hits the fail-closed staleness rule above, rejecting all client-cert auth — a real operational burden shifted onto the operator, accepted because it's the only way to support air-gapped clusters without a live network dependency. A staleness metric/alert should ship alongside this so the failure is observable before it becomes an outage, not discovered by it.
- **Fail-closed on staleness turns a CRL-refresh outage into a webhook-auth outage**, not just a silently-degraded security check — a deliberate availability-for-security tradeoff, consistent with this project's existing SSRF fail-closed precedent, but a new failure mode operators must specifically monitor for.
- Scoped narrowly to one of three mTLS surfaces in this project (see Constraints) — an operator or future contributor might reasonably ask "why not the executor/plugin channels too"; the answer (those are KubeZap-issued, short-lived, self-rotated certs with no external-revocation concept) should stay documented here so it isn't re-litigated per-channel later.

## 6. Final Decision

Implement CRL-based revocation checking for `kubezap.io/webhook-mtls-ca-secret` only, sourced from an operator-provided ConfigMap (not fetched by KubeZap), referenced via a new field alongside the existing inbound TLS/mTLS configuration (`WebhookGatewayConfig.spec.tls` — the CRD that already owns this surface per `docs/api/webhookgatewayconfig.md`). `internal/gateway/webhook/watcher.go`'s existing `crcache.Cache` (which already runs a `Trigger` informer and a `Secret` informer, per `docs/design/secret-rotation-watches.md`) gains a third informer for this `ConfigMap` — the same "watch the resource directly, no intermediate polling" pattern already used throughout this project's gateways.

**Wiring detail confirmed during grooming** (`cmd/webhook-gateway/main.go`'s TLS setup builds `tlsCfg` once at process startup and passes it to `srv.ListenAndServeTLS` — there is no existing reload mechanism for the server cert or CA either, confirmed by reading the actual startup code): the watcher does not rebuild `tlsCfg`. Instead, the current parsed CRL is held in a shared, concurrency-safe holder (e.g. `atomic.Pointer[x509.RevocationList]`) that the watcher's ConfigMap event handler updates and that a `tls.Config.VerifyPeerCertificate` closure — installed once, alongside the existing `ClientCAs`/`RequireAndVerifyClientCert` setup — reads on every handshake. This keeps CRL updates live without any change to the existing static-at-startup cert/CA loading model, and without introducing the first dynamic-reload mechanism for that unrelated (already-static) config in the same change.

A stale CRL (past `nextUpdate`) fails closed, rejecting all client-cert auth until refreshed, matching this project's existing fail-closed convention for unverifiable security state. This is scoped to the one channel where it actually matters — externally-issued client certs — and explicitly does not extend to the executor or plugin mTLS channels, whose certs are KubeZap-issued and short-lived enough that re-rotation already serves as their compromise-recovery mechanism.
