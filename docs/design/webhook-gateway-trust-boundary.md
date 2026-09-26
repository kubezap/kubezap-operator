# Webhook Gateway Trust Boundary Hardening

> Status: Approved
> Date: 2026-09-11
> Related: `internal/gateway/webhook/{handler,accesslog}.go`, `cmd/webhook-gateway/main.go`

## Problem

An adversarial review of the webhook gateway — the only KubeZap component routinely exposed directly to the internet — found three trust-boundary issues, none requiring any Trigger/Flow misconfiguration to exploit: `ipAllowlist` auth is trivially bypassable, since `realClientIP` unconditionally honors `X-Forwarded-For`/`X-Real-IP` from the request itself with no concept of a trusted proxy; the public listener sets no HTTP server timeouts, leaving it open to Slowloris-style connection exhaustion; and the hardcoded 4096-byte cap on stored `TriggerData.Body` truncates JSON mid-structure, silently breaking `trigger.bodyFields` and every `$(trigger.body.*)` placeholder for any real-world payload (GitHub, Slack, Stripe) that exceeds 4KB — not just the part past the cutoff.

## Constraints

- No CRD schema change — gateway-binary behavior only.
- The safe default must not keep trusting proxy headers for authorization — `ipAllowlist` requires an explicit opt-in (`--trusted-proxy-cidrs`) before honoring a header. This is the intended fix, not a regression to guard against, even though it changes behavior for deployments unknowingly relying on the current broken trust-everything default.
- Must not affect `hmac`/`bearer`/`apiKey`/`oidc`/`basic`/`header-equals` auth types — they don't depend on source IP.
- Timeouts must not cut off a legitimate slow client mid-request; the existing 4MB body-read limit already bounds worst-case read time.
- No new RBAC, no new CRD field, no new external dependency.
- When no trusted-proxy CIDRs are configured (default), `X-Forwarded-For`/`X-Real-IP` are never consulted for any authorization decision.
- When configured, a header is honored only when the immediate TCP peer is itself within the trusted set, using the right-most `X-Forwarded-For` entry — the one the trusted hop itself observed.
- The public listener always has non-zero `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`.
- The stored-body truncation limit is documented and changeable without a code change.

## Rejected Alternatives

- **Strip `X-Forwarded-For`/`X-Real-IP` handling entirely, always use `r.RemoteAddr`** — breaks `ipAllowlist` for the common case of the gateway sitting behind an ingress controller/load balancer, which is the feature's actual intended use case.
- **Trust any `X-Forwarded-For` present, take the last entry unconditionally** — without validating the immediate peer, an attacker connecting directly still controls the "last" entry by simply not chaining through any proxy. The trusted-peer check is the actual security boundary, not optional.
- **Auto-detect trusted proxies from Kubernetes Service/Ingress objects** — needs new RBAC and is CNI-specific, for a problem an explicit CIDR flag solves consistently with every other network-facing flag in this project (`--http-step-blocked-cidrs`, `--ssrf-allow-in-cluster`).
- **Make the stored-body limit unlimited (up to the 4MB read cap)** — `TriggerData` lives in the `FlowRun` CRD in etcd; sizing the default that large risks large `FlowRun` objects at scale.
- **Keep the 4096 default, only make it configurable** — truncating mid-JSON produces an invalid document, so `bodyFields` and every `$(trigger.body.*)` placeholder silently stop resolving *anything* once a JSON body crosses 4KB, not just fields past the cutoff — and real webhook payloads routinely exceed that. Raising the default to 65536 fixes the common case without opting into unlimited storage.

## Decision

Add a `--trusted-proxy-cidrs` flag (default empty) to `webhook-gateway`. `realClientIP` takes an explicit trusted-proxy list: with an empty list it always returns the TCP peer's host; with a non-empty list it returns the peer's host unless the peer itself is within `trustedProxies`, in which case it returns the right-most `X-Forwarded-For` entry (falling back to `X-Real-IP`, then the peer). Threaded into both `authenticateRequest` and `AccessLogMiddleware`. Add `ReadHeaderTimeout: 10s`, `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s` to the public listener in `cmd/webhook-gateway/main.go` (mirroring `http-executor`'s existing values). Add a `--max-stored-body-bytes` flag (default `65536`, up from the hardcoded `4096`) threaded into `WebhookHandler`, documented in `docs/api/flowrun.md` and `docs/guides/webhook-security.md`.

- Operators relying on `ipAllowlist` behind a reverse proxy must add `--trusted-proxy-cidrs` after this ships, or `ipAllowlist` starts rejecting all traffic — called out in CHANGELOG/upgrade notes as a required action.
- The trust-chain logic validates only the immediate peer; a multi-hop chain (CDN → WAF → ingress → gateway) isn't validated end-to-end — the same limitation most single-flag trusted-proxy implementations have (e.g. nginx's `set_real_ip_from`), judged sufficient for the common case. **2026-09-26: extended, not reversed** — see `docs/design/webhook-gateway-multihop-trusted-proxy.md` for multi-hop chain-walking; this record's other decisions (timeouts, stored-body limit) are unaffected and remain authoritative.
- Raising the stored-body default from 4096 to 65536 means larger `FlowRun` objects for Triggers with bodies in that range — a behavior change, but a strict improvement over the prior default's silent whole-payload breakage.
