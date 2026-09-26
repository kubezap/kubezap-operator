# HTTP Executor Egress NetworkPolicy (SSRF Defense-in-Depth)

> Status: Approved
> Date: 2026-09-11
> Related: `internal/controller/executor_reconciler.go`, `internal/executor/http/ssrf.go`, `config/network-policy/http-executor-ingress.yaml`

## Problem

The HTTP executor's software SSRF blocklist has a check-then-connect gap: `checkSSRF` validates a step's target hostname against the blocklist, then the actual outbound request (`http.Client.Do`) re-resolves that hostname independently via the Go stdlib transport. A hostname with attacker-controlled DNS (realistic whenever a Flow interpolates attacker-influenced data into a step URL) can return a public IP for validation and a blocked address (e.g. `169.254.169.254`, in-cluster `10.x`) for the connection moments later via a low TTL — the standard DNS-rebinding bypass, defeating the blocklist regardless of its CIDR list. A manual reference NetworkPolicy (`config/network-policy/http-executor-ingress.yaml`) already models a network-layer fix, but is missing `169.254.0.0/16` from its exceptions and is never applied automatically — the operator's auto-created `NetworkPolicy` (`reconcileExecutorNetworkPolicy`) only sets `PolicyTypes: [Ingress]`, so no egress restriction ships by default anywhere.

## Constraints

- No CRD schema change — reconciliation-logic (the auto-created `NetworkPolicy` spec) plus a manual-reference-file correction.
- Default egress must allow `0.0.0.0/0` minus blocked ranges (mirroring the manual file's shape), not a destination allowlist — the executor's legitimate function is arbitrary outbound HTTPS to external services.
- Must degrade safely on a non-enforcing CNI (e.g. plain Flannel): the reconciler still creates the object; this is defense-in-depth on top of the software blocklist, not an assumed-always-enforced replacement — document the limitation.
- No new RBAC beyond what `reconcileExecutorNetworkPolicy` already holds (already creates/updates `NetworkPolicy`; only `Spec.Egress` content changes).
- DNS egress (UDP/TCP 53) must remain allowed — the executor does its own resolution.
- Every operator-managed namespace's auto-created executor `NetworkPolicy` gets both `Ingress` and `Egress` policy types.
- The egress blocked-range exception list matches `defaultSSRFBlockedCIDRs`, kept in sync by convention (cross-referencing code comments), not a single generated source.
- This NetworkPolicy is additive to, never a replacement for, the software SSRF blocklist — both layers stay independently enforced.

## Rejected Alternatives

- **Fix the DNS-rebinding gap in the software blocklist itself** (resolve once, pin the validated IP, dial that literal address) — correct only with a custom redirect-aware `Dialer` and TLS-SNI handling, meaningfully more complex and regression-prone than a NetworkPolicy addition for a problem the network layer is structurally immune to (it filters the real destination IP; DNS answers are irrelevant to it). Left as a possible follow-up only if a CNI can't enforce NetworkPolicy. **2026-09-26: that follow-up shipped** — see `docs/design/ssrf-dns-rebinding-transport-fix.md`. Reassessed and reversed on this one point: k3s (this project's own supported local/dev/test platform) defaults to Flannel, a non-enforcing CNI, and Flow HTTP step URLs support `$(trigger.body.*)` interpolation, making the attacker-controlled-hostname scenario reachable by an unauthenticated webhook caller, not just a trusted Flow author — a materially different threat model than assumed here. This record's NetworkPolicy decision itself is untouched and remains authoritative as defense-in-depth; only this one Rejected-Alternatives bullet's "don't bother, network layer already covers it" conclusion no longer holds as the sole mitigation.
- **Only fix the manual reference file, leave the auto-created policy alone** — requires an operator to know the file exists and run `kubectl apply -f config/network-policy/` as an easy-to-miss separate step with no cross-reference to the SSRF docs; making the operator-managed policy correct by default removes that dependency.
- **Allowlist specific external CIDRs per Flow/Integration instead of a blocklist** — KubeZap doesn't know a Flow's target hosts ahead of time (URLs are frequently templated at runtime); per-Flow policy generation is a much larger feature that doesn't fit today's block-known-dangerous-ranges software model this is meant to complement.

## Decision

Extend `reconcileExecutorNetworkPolicy` to add an `Egress` rule to the operator-created `NetworkPolicy`, blocking the same ranges as `defaultSSRFBlockedCIDRs` (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `100.64.0.0/10`, plus IPv6 `::1/128`, `fe80::/10`, `fc00::/7`) via `ipBlock: {cidr: 0.0.0.0/0, except: [...]}` (and a parallel IPv6 `::/0` block), plus an always-open DNS egress rule (UDP/TCP 53). Set `PolicyTypes: [Ingress, Egress]`. Fix the missing `169.254.0.0/16` exception in the manual reference file for consistency, and cross-reference `docs/guides/security-checklist.md`'s SSRF (§2) and NetworkPolicy (§4) sections with a CNI-enforcement caveat and cloud-security-group fallback note.

A 2026-09-12 live-cluster validation pass on an enforcing (k3s) cluster surfaced two corrections: (1) the Ingress rule's podSelector (`app.kubernetes.io/component: controller`) never matched the real controller-manager pod's labels and had no `NamespaceSelector` despite executor/controller living in different namespaces — silently blocking all controller→executor traffic on any enforcing cluster; fixed alongside this work even though Ingress was out of this design's original scope. (2) The implementation had added an unspecified `Ports: [80, 443]` egress restriction not called for by this design, which combined with (1) blocked any step targeting a non-standard port — removed. CIDR exceptions were also made conditional on `SSRFAllowClusterInternal` (empty when set), matching the existing dev/test flag's software-layer behavior. See `internal/controller/executor_reconciler.go` (`operatorNamespace`, `egressExceptCIDRs`/`egressExceptCIDRsV6`) and `executor_reconciler_test.go`.

- Closes the gap only on CNIs that enforce `NetworkPolicy` (Calico, Cilium, most managed-K8s default CNIs) — non-enforcing CNIs (plain Flannel) get no additional protection; documented with a cloud security-group/NACL fallback recommendation.
- The blocked-CIDR list now exists in two places (Go's `defaultSSRFBlockedCIDRs` and the NetworkPolicy's `except` list), kept in sync by convention rather than a single source of truth — mitigated by cross-pointing code comments; revisit (e.g. generate the NetworkPolicy list from the Go slice at build time) if drift becomes a recurring problem.
