# Multi-Hop Trusted-Proxy Chain Support for the Webhook Gateway

> Status: Draft
> Related: `internal/gateway/webhook/{handler,accesslog}.go`, `docs/design/webhook-gateway-trust-boundary.md`, `docs/guides/webhook-security.md`

## 1. Problem Statement

`realClientIP` (`internal/gateway/webhook/accesslog.go`) validates only the immediate TCP peer against `--trusted-proxy-cidrs` before honoring `X-Forwarded-For`, then returns the right-most header entry unconditionally. This is correct for exactly one proxy hop between the client and the gateway, but wrong for a chain of multiple trusted hops (e.g. CDN → WAF → ingress controller → webhook gateway): each hop appends its own observed peer to the header, so the right-most entry is only the *immediately preceding* hop's address — itself another trusted proxy, not the real client. Source-IP-based features that depend on `realClientIP` (`ipAllowlist` auth, access logs, and anything else keyed on client IP) get the wrong address in a multi-hop deployment today.

## 2. Constraints

- `--trusted-proxy-cidrs`'s flag name, default (empty = never trust headers), and single-hop behavior must not change — an existing deployment configured for exactly one real proxy hop must see identical output before and after this change.
- No new CRD field; no new external dependency; no requirement to reconfigure intermediate infrastructure (CDN/WAF/ingress) that the operator may not control.
- Must not introduce unbounded per-request work — a pathological `X-Forwarded-For` header must not force an unbounded walk.
- Must not weaken the existing trust boundary: the header is still never consulted at all unless the immediate TCP peer itself is within `trustedProxies` (unchanged from `docs/design/webhook-gateway-trust-boundary.md`).

## 3. Invariants

- When `--trusted-proxy-cidrs` is empty, `X-Forwarded-For`/`X-Real-IP` are never consulted — unchanged.
- When non-empty and the immediate TCP peer is not itself within `trustedProxies`, the header is never consulted and `r.RemoteAddr` is returned — unchanged.
- When the immediate peer is trusted, `realClientIP` walks `X-Forwarded-For` from the right and returns the first entry that is **not** itself within any `trustedProxies` CIDR; if every entry up to the walk cap is within `trustedProxies`, the left-most entry is returned.
- The walk never inspects more than a fixed, hardcoded maximum number of entries (defense-in-depth against a pathological header) — generous enough that no real deployment's actual proxy chain depth is ever affected by it.
- For a single-hop configuration (exactly one real trusted proxy between client and gateway), the returned client IP is byte-identical to today's single-hop output — this is a behavioral superset, not a breaking change.

## 4. Rejected Alternatives

**Require an explicit `--trusted-proxy-hop-count` flag alongside the CIDR list.** Would let an operator cap trust at a known chain depth independent of CIDR breadth, but adds a second knob that must be kept in sync with the actual proxy topology by hand — get it wrong (e.g. add a CDN in front of an existing load balancer and forget to bump the count) and the walk silently stops one hop too early, misattributing the client IP with no error. CIDR-membership-only chain-walking auto-adapts to a changed topology without an operator remembering a second setting. Rejected in favor of a bounded walk with a generous, non-configurable hard cap purely for pathological-input safety, not as a trust boundary.

**mTLS or a shared-secret header between every hop, verifying each proxy's identity structurally instead of trusting header content once its claimed IP matches a CIDR.** Would close the one real gap in the CIDR-membership model (see Tradeoffs) but requires reconfiguring every intermediate hop, including third-party infrastructure (a CDN vendor) the operator often cannot customize. Disproportionate to the problem and violates the "no requirement to reconfigure infrastructure the operator may not control" constraint. The existing approved trust-boundary record already accepted the CIDR-membership trust model for one hop as "judged sufficient for the common case" — extending the same model further is consistent with that judgment, not a new, weaker one.

**Always trust the left-most `X-Forwarded-For` entry as the client IP, without validating any hop.** This is precisely the vulnerability `docs/design/webhook-gateway-trust-boundary.md` fixed — an untrusted caller reaching the gateway directly controls every entry it sends, including a fabricated left-most one. Rejected outright.

## 5. Tradeoffs

- **Only the immediate TCP peer is structurally verified** (it's the actual socket connection); every other hop in the chain is trusted purely because the header text at that position falls inside a `trustedProxies` CIDR. This is the same limitation every non-mTLS trusted-proxy implementation has (nginx's `realip` module, Express's `trust proxy` list, etc.) — a compromised or misconfigured immediate peer could fabricate intermediate entries that happen to match the trusted CIDR set. Not closeable without the rejected mTLS alternative's infrastructure cost.
- **An operator whose `--trusted-proxy-cidrs` is scoped more broadly than their actual proxy infrastructure risks the walk skipping past a legitimate client's real IP** if it happens to fall inside that range. This risk already exists in the single-hop design (this change doesn't introduce it) but grows slightly with chain-walking, since there are more positions in the header where a too-broad CIDR can cause an incorrect skip. Mitigation is operational, not algorithmic: `docs/guides/webhook-security.md` should keep emphasizing scoping the flag tightly to known proxy IPs, not broad internal ranges.
- Marginally more CPU per authenticated/logged request for a long trusted chain (bounded walk, O(hop count), negligible in practice).

## 6. Final Decision

Extend `realClientIP` to walk `X-Forwarded-For` from the right, popping successive entries while each one is itself within `trustedProxies`, stopping at (and returning) the first entry that isn't — or the left-most entry if the whole bounded chain matches. Add a fixed, generous, hardcoded walk-depth cap as defense-in-depth against a pathological header (not an operator-facing flag — this is a safety bound, not a trust-model parameter). This is a strict generalization of the current single-hop logic: for a one-hop deployment the first right-to-left step immediately hits a non-trusted entry (the real client) and returns exactly what today's code returns, so `--trusted-proxy-cidrs`'s existing semantics, default, and single-hop behavior are unchanged — no new flag, no opt-in, no migration needed for existing deployments. This refines (without reversing) the trust-chain portion of `docs/design/webhook-gateway-trust-boundary.md`, whose timeout and stored-body-limit decisions are untouched and remain authoritative as-is.
