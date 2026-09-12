# HTTP Executor Egress NetworkPolicy (SSRF Defense-in-Depth)

> Status: Draft
> Related: `docs/schedule.md` §37, `internal/controller/executor_reconciler.go`, `internal/executor/http/ssrf.go`, `config/network-policy/http-executor-ingress.yaml`

## 1. Problem Statement

The HTTP executor's software SSRF blocklist (`internal/executor/http/ssrf.go`, `internal/controller/ssrf.go` — intentionally duplicated) has a check-then-connect gap: `checkSSRF` resolves a Flow step's target hostname, validates *those* addresses against the blocklist, then returns; the actual outbound request (`handler.go`'s `http.NewRequestWithContext` → `http.Client.Do`) re-resolves the same hostname independently via the Go stdlib transport. A hostname whose authoritative DNS is attacker-controlled (realistic whenever a Flow interpolates attacker-influenced data into an HTTP step URL — a documented, encouraged pattern) can return a public IP for the validation lookup and a blocked address (e.g. `169.254.169.254`, an in-cluster `10.x` address) for the connection lookup moments later, by setting a very low TTL. This is the standard DNS-rebinding SSRF bypass and defeats the blocklist entirely regardless of how complete its CIDR list is.

Separately: `config/network-policy/http-executor-ingress.yaml` (a manual reference file, applied via the documented `kubectl apply -f config/network-policy/` step in `docs/guides/security-checklist.md` §4) already models an egress rule meant to close exactly this gap at the network layer — but it is missing `169.254.0.0/16` (link-local / cloud-metadata, the single most commonly exploited SSRF target) from its exception list, and more importantly, it is never applied automatically. The NetworkPolicy the operator *does* create automatically for every managed namespace (`reconcileExecutorNetworkPolicy` in `internal/controller/executor_reconciler.go`) only sets `PolicyTypes: [Ingress]` — no egress restriction ships by default anywhere.

## 2. Constraints

- No CRD schema change — this is reconciliation-logic (the auto-created `NetworkPolicy` object's spec) plus a manual-reference-file correction.
- Must not break the HTTP executor's legitimate function: making arbitrary outbound HTTPS calls to external services on behalf of Flow steps. The default egress rule must allow `0.0.0.0/0` minus the blocked ranges (mirroring the existing manual reference file's shape), not an allowlist of specific destinations.
- Must degrade safely on a CNI that doesn't enforce `NetworkPolicy` at all (e.g. plain Flannel): the reconciler must still create the object (Kubernetes accepts it regardless of enforcement), and this limitation must be documented rather than silently assumed away — this control is defense-in-depth on top of the software blocklist, not a replacement assumed to always be enforced.
- Must not require new RBAC beyond what `reconcileExecutorNetworkPolicy` already holds (it already creates/updates `NetworkPolicy` objects; only the `Spec.Egress` content changes).
- DNS egress (UDP/TCP 53) must remain allowed — the executor performs its own DNS resolution.

## 3. Invariants

- Every operator-managed namespace's auto-created executor `NetworkPolicy` has both `Ingress` (unchanged: controller-pod-only) and `Egress` policy types set.
- The egress rule's blocked-range exception list matches `internal/executor/http/ssrf.go`'s `defaultSSRFBlockedCIDRs` (kept in sync deliberately — see Final Decision on how drift is prevented).
- DNS (UDP/TCP port 53, unrestricted destination) is always permitted regardless of the blocked-range exceptions, so hostname resolution itself is never broken by this policy.
- Applying this NetworkPolicy is additive to, never a replacement for, the software SSRF blocklist — both layers remain independently enforced.

## 4. Rejected Alternatives

**A. Fix the DNS-rebinding gap in the software blocklist itself** (e.g. resolve once, pin the validated IP, and force the HTTP transport to dial that literal address while preserving the original `Host` header and TLS SNI).
Rejected for this design: correct only with a custom `net.Dialer`/`DialContext` that must also correctly handle HTTP redirects (each redirect target needs its own re-validated, re-pinned connection) and TLS virtual hosting — meaningfully more complex and higher-regression-risk than a NetworkPolicy addition, for a problem the network layer is structurally immune to (NetworkPolicy filters the actual destination IP of the real packet leaving the pod's network namespace; DNS answers are irrelevant to it, so rebinding cannot defeat it the way it defeats an app-level check). Not rejected permanently — left as a possible follow-up if an operator's CNI genuinely cannot enforce `NetworkPolicy` (see Tradeoffs) — but out of scope here given the lower-risk, higher-leverage fix available.

**B. Only fix the manual reference file (`config/network-policy/http-executor-ingress.yaml`), leave the auto-created policy alone.**
Rejected: the manual file requires an operator to know it exists and run `kubectl apply -f config/network-policy/` as a separate step (`docs/guides/security-checklist.md` §4) — easy to miss, and the checklist item that mentions it doesn't currently cross-reference the SSRF section at all, so nothing signals that this specific `kubectl apply` is what actually closes the DNS-rebinding gap. Making the operator-managed policy correct by default removes the dependency on that manual step being followed.

**C. Allowlist specific external CIDRs per Flow/Integration instead of a blocklist.**
Rejected: KubeZap doesn't know a Flow's target hosts ahead of time (URLs are frequently templated/interpolated at runtime); an allowlist model would require per-Flow network policy generation, a much larger feature with its own design needs, and doesn't fit today's "block known-dangerous ranges, allow everything else" software SSRF model this is meant to complement, not replace.

## 5. Tradeoffs

- This closes the gap only on CNIs that actually enforce `NetworkPolicy` (Calico, Cilium, most managed-Kubernetes default CNIs). Clusters on a non-enforcing CNI (e.g. plain Flannel) get no additional protection from this change — documented explicitly in `docs/guides/security-checklist.md` with a security-group-based fallback recommendation for that case (cloud security groups / NACLs restricting the node's egress to the same ranges, enforced by the cloud provider's network layer instead of the CNI).
- The blocked-CIDR list now exists in two places that must be kept in sync by convention (Go code's `defaultSSRFBlockedCIDRs` and the NetworkPolicy's `except` list) — there is no single source of truth across a Go slice and a Kubernetes YAML spec. Mitigated by a code comment at both locations pointing at each other and this design record; revisit if drift becomes a recurring problem (e.g. generate the NetworkPolicy's except list from the same Go slice at build time).
- A NetworkPolicy default egress-open to `0.0.0.0/0` (minus blocked ranges) is deliberately permissive to match the existing software blocklist's scope — it does not implement per-Integration or per-Flow destination allowlisting, discussed and rejected above.

## 6. Final Decision

Extend `reconcileExecutorNetworkPolicy` to add an `Egress` rule to the operator-created `NetworkPolicy`, blocking the same ranges as `defaultSSRFBlockedCIDRs` (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `100.64.0.0/10`, plus IPv6 equivalents `::1/128`, `fe80::/10`, `fc00::/7`) via an `ipBlock: {cidr: 0.0.0.0/0, except: [...]}` (and a parallel IPv6 `::/0` block), plus an always-open DNS egress rule (UDP/TCP 53). Set `PolicyTypes: [Ingress, Egress]`. Fix the missing `169.254.0.0/16` exception in the manual reference file `config/network-policy/http-executor-ingress.yaml` for consistency. Cross-reference `docs/guides/security-checklist.md`'s SSRF section (§2) and NetworkPolicy section (§4) so the connection between them is explicit, and add the CNI-enforcement caveat plus a cloud-security-group fallback note.

### Addendum (2026-09-12, `docs/schedule.md` §38)

Two corrections found during a live-cluster validation pass, on a k3s cluster that (unlike the assumption implicit in "on a non-enforcing CNI this provides no additional protection") **does** enforce NetworkPolicy — which is exactly what surfaced both:

1. **§3's "Ingress (unchanged: controller-pod-only)" invariant was itself broken, pre-dating this design** — the Ingress rule's podSelector (`app.kubernetes.io/component: controller`) has never matched the real controller-manager pod's labels (`app.kubernetes.io/name: kubezap` + `control-plane: controller-manager`, per `config/manager/manager.yaml`), and it had no `NamespaceSelector` despite the executor and its NetworkPolicy living in a different namespace than the controller. On any NetworkPolicy-enforcing cluster this silently blocks 100% of controller→executor traffic — every HTTP step in every Flow. Fixed alongside this design's own follow-up work; not a re-litigation of this design's own decisions, since Ingress was explicitly out of scope here.
2. **The Egress rule's `Ports: [80, 443]` restriction was never called for by this design's Constraints/Invariants** (the stated intent throughout is a destination-CIDR blocklist), but the implementation added one anyway, which — combined with (1) — meant no Flow step targeting a non-standard port could ever succeed once enforced. Removed the port restriction. Also made the CIDR exceptions conditional on `SSRFAllowClusterInternal` (empty when set), since that existing dev/test flag (`config/dev/manager_dev_patch.yaml`) already disables the equivalent software-layer check, and the network layer silently overriding a flag that's explicitly documented as "allow in-cluster calls" was its own bug.

See `internal/controller/executor_reconciler.go` (`operatorNamespace`, `egressExceptCIDRs`/`egressExceptCIDRsV6`) and the corresponding tests in `executor_reconciler_test.go` for the actual fix.
