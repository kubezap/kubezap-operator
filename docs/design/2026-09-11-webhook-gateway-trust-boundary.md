# Webhook Gateway Trust Boundary Hardening

> Status: Draft
> Related: `docs/schedule.md` §37, `internal/gateway/webhook/{handler,accesslog}.go`, `cmd/webhook-gateway/main.go`

## 1. Problem Statement

An adversarial review of the webhook ingestion path (the only KubeZap component routinely exposed directly to the internet) found three issues in `cmd/webhook-gateway`'s trust boundary, none requiring any Trigger/Flow misconfiguration to exploit:

1. **`ipAllowlist` webhook auth is trivially bypassable.** `realClientIP` (`accesslog.go`) unconditionally honors `X-Forwarded-For`/`X-Real-IP` from the request itself, with no concept of a trusted proxy. `authenticateRequest`'s `ipAllowlist` case (`handler.go`) calls it directly to decide access. Any caller who can reach the gateway — which, per this project's own threat model, is the point of a webhook — can send `X-Forwarded-For: <any-allowed-ip>` and be treated as that IP. Even behind a well-behaved reverse proxy that *appends* the real client IP, the code takes the *first* comma-separated entry, so a client-injected fake entry still wins. This auth type currently provides no real protection against a direct attacker. (Zero existing test coverage for `ipAllowlist` or this function, confirmed by grep.)
2. **No HTTP server timeouts on the public listener.** `cmd/webhook-gateway/main.go`'s `srv := &http.Server{Addr: ..., Handler: ...}` sets no `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`, leaving it open to Slowloris-style connection exhaustion (an attacker opens many connections and trickles bytes, or none, to hold goroutines/file descriptors open indefinitely). `cmd/http-executor/main.go` — an *internal*, far less exposed service — already sets all four; the actually-public-facing binary is the one missing them.
3. **The 4096-byte cap on stored `TriggerData.Body` is hardcoded and undocumented — and its failure mode is worse than "just a smaller prefix."** `handler.go` truncates the body used for Flow processing to 4096 characters (independent of, and much smaller than, the 4MB read limit that exists to bound memory use), with no way to configure or discover it. Every downstream mechanism — `$(trigger.body...)` interpolation, CEL's `trigger.bodyFields`, and raw-string `when:` conditions — reads from this same truncated string. For a JSON (or form-urlencoded) body, truncating mid-structure produces invalid JSON: parsing fails entirely, so `bodyFields` becomes an empty map and every `$(trigger.body.<field>)` placeholder is left unresolved (sent literally, not just missing one field) — this silently breaks structured field access for the *whole* payload the moment it crosses 4KB, not just the part past the cutoff. Real-world JSON webhook payloads (GitHub, Slack, Stripe) routinely exceed 4KB. Only raw-string `when:` conditions degrade "gracefully" (they see a truncated but still-valid prefix, which is where the padding-based branch-shifting risk described below applies).

## 2. Constraints

- No CRD schema change — this is gateway-binary behavior only.
- **Default behavior must not silently break existing deployments that put a reverse proxy in front of the gateway and rely on its IP showing up in logs/metrics** — but "logging" and "authorization" have different risk profiles: it is acceptable (and correct) for the *safe* default to require an explicit opt-in before `ipAllowlist` auth trusts a proxy header, even though this could be a behavior change for anyone unknowingly relying on the current broken trust-everything behavior for a security control. This is the intended fix, not a regression to guard against.
- The webhook auth trust-boundary fix must not affect `hmac`/`bearer`/`apiKey`/`oidc`/`basic`/`header-equals` auth types at all — they don't depend on source IP.
- Timeouts must not be so aggressive that a legitimate slow client (large body over a slow connection) gets cut off mid-request; the existing 4MB body-read limit already bounds how long a full read can reasonably take.
- No new RBAC, no new CRD field, no new external dependency.

## 3. Invariants

- When no trusted-proxy CIDRs are configured (the default), `X-Forwarded-For`/`X-Real-IP` are **never** consulted for any purpose that affects an authorization decision — `realClientIP` always returns the TCP peer address.
- When trusted-proxy CIDRs are configured, a header is only honored when the immediate TCP peer (`r.RemoteAddr`) is itself within that trusted set, and the address taken from `X-Forwarded-For` is the right-most (nearest-to-us) entry — the one the trusted hop itself observed and cannot be overwritten by anything the original client injected further left in the chain.
- The webhook gateway's public listener always has non-zero `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`.
- The stored-body truncation limit has a documented, discoverable value and a way to change it without a code change.

## 4. Rejected Alternatives

**A. Strip `X-Forwarded-For`/`X-Real-IP` handling entirely, always use `r.RemoteAddr`.**
Rejected: breaks `ipAllowlist` for the (very common) case where the gateway legitimately sits behind an ingress controller/load balancer and the real client IP is only available via a forwarded header — that's the actual, intended use case for the feature. The fix is to trust the header only from a configured, known proxy hop, not to remove the capability.

**B. Trust any `X-Forwarded-For` present, but take the *last* entry unconditionally (no trusted-proxy config at all).**
Rejected: without validating that `r.RemoteAddr` itself is a proxy we trust, an attacker connecting directly still fully controls the "last entry" by simply not chaining through any proxy — they'd just send `X-Forwarded-For: <spoofed-ip>` as the only (and therefore "last") entry. The trusted-peer check is not optional; it's the actual security boundary.

**C. Auto-detect trusted proxies from Kubernetes Service/Ingress objects.**
Rejected: significant complexity (RBAC to read Ingress/Service resources, cluster-CNI-specific behavior for how source IPs are preserved) for a problem a simple, explicit CIDR flag solves; also inconsistent with how every other network-facing flag in this project works (`--http-step-blocked-cidrs`, `--ssrf-allow-in-cluster`, etc. are all explicit operator-supplied config, not auto-detected).

**D. Make the stored-body limit unlimited (store the full body up to the existing 4MB read cap).**
Rejected: `TriggerData` is stored in the `FlowRun` CRD in etcd; making the default as large as the read cap risks large `FlowRun` objects at scale (many concurrent webhook-triggered Flows) for payload sizes far beyond what any real integration in this project's examples uses.

**D'. Keep the existing 4096 default, only make it configurable.**
Rejected on further analysis during implementation: 4096 isn't just "a smaller prefix" — truncating mid-JSON produces an invalid document, so `trigger.bodyFields` and every `$(trigger.body.<field>)` placeholder silently stop resolving *anything* the moment a JSON body crosses 4KB, not just fields past the cutoff (see Problem Statement). Real webhook payloads from GitHub, Slack, and Stripe routinely exceed 4KB, meaning the previous default would silently break structured field access for a large share of realistic integrations out of the box, not just an edge case. Raising the default to 64KB (`65536`) — six orders of magnitude below `FlowRun`'s practical size concerns, comfortably above typical real-world payloads, and still 60x below the 4MB read cap — fixes the common case without opting into unlimited storage.

## 5. Tradeoffs

- Operators who currently rely on `ipAllowlist` behind a reverse proxy must add a new flag (`--trusted-proxy-cidrs`) pointing at their proxy's pod/service CIDR after this ships, or `ipAllowlist` auth will start rejecting all traffic (since `realClientIP` will report the proxy's own IP, not any client's, once headers are no longer blindly trusted). This is flagged prominently in the CHANGELOG/upgrade notes as a required action for `ipAllowlist` users — the alternative (silently keeping the broken trust-everything behavor) is not acceptable for a security control.
- The multi-hop trust-chain logic (rightmost-entry-from-a-trusted-peer) handles exactly one trusted hop cleanly (the common ingress/ALB-in-front-of-gateway case). A deployment chaining multiple trusted proxies (e.g. CDN → WAF → ingress → gateway) would need each hop to also append correctly and the gateway would still only validate that the *immediate* peer is trusted, not that every hop in the chain is — this is the same limitation most single-flag trusted-proxy implementations have (e.g. it mirrors nginx's `set_real_ip_from` for one configured hop) and is judged sufficient for the common case; a fuller N-hop trust chain is not implemented.
- Making the stored-body limit configurable adds one more flag to document and tune. The default is raised from 4096 to 65536 — a behavior change (larger `FlowRun` objects for Triggers with bodies in that range) for anyone not touching the new flag, but a strict improvement given the previous default's failure mode was silent, whole-payload breakage of structured field access rather than a graceful degradation.

## 6. Final Decision

Add a `--trusted-proxy-cidrs` flag to `webhook-gateway` (default empty). `realClientIP` becomes `realClientIP(r *http.Request, trustedProxies []*net.IPNet) string`: with an empty list it always returns `r.RemoteAddr`'s host; with a non-empty list it returns the peer host unless that peer is itself within `trustedProxies`, in which case it returns the right-most `X-Forwarded-For` entry (falling back to `X-Real-IP`, then the peer). Threaded into both `authenticateRequest` (the security-relevant path) and `AccessLogMiddleware` (logging only, for consistency) via `WebhookHandler` and a new middleware parameter respectively. Add `ReadHeaderTimeout: 10s`, `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s` to the public listener in `cmd/webhook-gateway/main.go` (mirroring `http-executor`'s existing values where the same concern applies). Add a `--max-stored-body-bytes` flag (default `65536`, raised from today's hardcoded `4096` — see Rejected Alternative D') threaded into `WebhookHandler`, and document the limit and its rationale in `docs/api/flowrun.md` and `docs/guides/webhook-security.md`.
