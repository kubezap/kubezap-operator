/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package executorhttp implements the HTTP executor server that executes
// outbound HTTP requests on behalf of the KubeZap controller.
//
// SSRF protection note: this file is an intentional copy of
// internal/controller/ssrf.go adapted to package executorhttp. The duplication
// is deliberate — the executor must be self-contained with no controller-runtime
// imports. After chunk [4/6] lands the controller copy can be reduced. Do not
// attempt to merge them prematurely.
package executorhttp

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// SSRFBlockedCIDRsV4 and SSRFBlockedCIDRsV6 are the authoritative set of CIDRs
// blocked by default for HTTP step outbound requests. Covers RFC1918 private
// ranges, loopback, link-local (including cloud metadata endpoints at
// 169.254.169.254), and CGNAT/IPv6 equivalents.
//
// This is the single source of truth for the SSRF blocklist: it also backs
// internal/controller/executor_reconciler.go's generated NetworkPolicy egress
// "except" list (defense-in-depth against the DNS-rebinding gap in the
// software check below — see that file's doc comment) and is referenced from
// config/network-policy/http-executor-ingress.yaml and
// config/samples/network-policy-executor.yaml for manual cluster auditing.
// Changing these values changes both the software SSRF check and the
// generated NetworkPolicy.
var (
	SSRFBlockedCIDRsV4 = []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16", // link-local — includes AWS/GCP/Azure metadata IPs
		"0.0.0.0/8",
		"100.64.0.0/10", // CGNAT / shared address space (RFC6598)
	}

	SSRFBlockedCIDRsV6 = []string{
		"::1/128",
		"fe80::/10", // IPv6 link-local
		"fc00::/7",  // IPv6 unique local
	}
)

// defaultSSRFBlockedCIDRs is the parsed form of SSRFBlockedCIDRsV4 +
// SSRFBlockedCIDRsV6, used by checkSSRF for the software-level SSRF check.
var defaultSSRFBlockedCIDRs = mustParseCIDRs(append(
	append([]string{}, SSRFBlockedCIDRsV4...),
	SSRFBlockedCIDRsV6...,
))

// mustParseCIDRs parses a slice of CIDR strings and panics on invalid input.
// Intended for package-level initialization of known-good default CIDRs only.
func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("ssrf: invalid default CIDR %q: %v", c, err))
		}
		out = append(out, ipNet)
	}
	return out
}

// ParseCIDRList parses a comma-separated list of CIDR strings. Returns the
// default blocked CIDRs merged with any additional ones from the list.
// Used to process the --blocked-cidrs flag value.
func ParseCIDRList(extraCIDRs string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, len(defaultSSRFBlockedCIDRs))
	copy(result, defaultSSRFBlockedCIDRs)

	if strings.TrimSpace(extraCIDRs) == "" {
		return result, nil
	}

	for _, raw := range strings.Split(extraCIDRs, ",") {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q in --blocked-cidrs: %w", cidr, err)
		}
		result = append(result, ipNet)
	}
	return result, nil
}

// checkSSRFPreflight validates the structural properties of rawURL that can
// be checked before any network I/O: that it has a hostname at all, and the
// ".svc.cluster.local" in-cluster-service policy (reject unless
// allowClusterInternal). It deliberately does NOT resolve DNS or check the
// blocked-CIDR list — that step happens exactly once, at dial time, via
// resolveAndValidate (called from Handler.dialContext). Splitting it this way
// closes a DNS-rebinding TOCTOU gap that existed when this function also did
// the resolve-and-check: it validated a hostname's resolved address here,
// then net/http's Transport independently re-resolved the same hostname to
// actually connect, so a low-TTL DNS answer could return a safe address for
// this check and a blocked one for the real connection moments later. See
// docs/design/ssrf-dns-rebinding-transport-fix.md.
//
// allowClusterInternal disables the .svc.cluster.local hostname rejection,
// but only for targets recognized as in-cluster services (the
// ".svc.cluster.local" suffix). It does NOT weaken validation for any other
// target: an IP literal or external hostname (e.g. the 169.254.169.254 cloud
// metadata address) is still blocked at dial time regardless of
// allowClusterInternal. This keeps the flag scoped to "let Flow steps reach
// in-cluster services" rather than "disable SSRF protection," so a single
// dev/test flag can't simultaneously be required to unblock in-cluster test
// fixtures (like Mockoon) and be relied on to still block the metadata
// endpoint from a sibling test. Handler.dialContext re-applies this same
// ".svc.cluster.local" scoping at dial time (see its doc comment) since every
// connection — including redirect-triggered ones to a possibly different
// host — goes through dialContext, not just the original request URL checked
// here.
func checkSSRFPreflight(rawURL string, allowClusterInternal bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("SSRF check: invalid URL: %w", err)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("SSRF check: URL has no hostname")
	}

	isClusterInternal := strings.HasSuffix(hostname, ".svc.cluster.local") ||
		strings.HasSuffix(hostname, ".svc.cluster.local.")

	if isClusterInternal {
		if allowClusterInternal {
			return nil
		}
		return fmt.Errorf("SSRF check: requests to in-cluster service endpoints (.svc.cluster.local) are not permitted from HTTP steps; use a plugin Integration instead")
	}

	return nil
}

// lookupIPAddr resolves a hostname to its IP addresses. It is a package
// variable — rather than a direct net.Resolver{} call inlined into
// resolveAndValidate — so tests can inject a fake resolver to deterministically
// simulate DNS-rebinding scenarios (successive lookups of the same hostname
// returning different addresses) without depending on real DNS or network
// access. Production code never reassigns this; only *_test.go files in this
// package do.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// resolveAndValidate resolves hostname (or parses it as an IP literal) and
// validates every resulting address against blockedCIDRs. It is fail-closed:
// if ANY address is blocked, the whole hostname is rejected and no addresses
// are returned — a hostname is only ever treated as safe when every address
// it resolves to is safe. On success it returns the full validated address
// set so the caller (Handler.dialContext) can pin the connection to one of
// them instead of letting the transport re-resolve and potentially connect to
// a different, unvalidated address.
//
// blockedCIDRs defaults to defaultSSRFBlockedCIDRs when nil. Pass a non-nil
// slice (constructed via ParseCIDRList) to add operator-configured ranges.
//
// This is the single resolution+validation path shared by dial-time
// validation; there is no longer a separate pre-flight resolution, so there
// is exactly one DNS lookup per connection and it is always the one that
// gets checked.
func resolveAndValidate(ctx context.Context, hostname string, blockedCIDRs []*net.IPNet) ([]net.IP, error) {
	if blockedCIDRs == nil {
		blockedCIDRs = defaultSSRFBlockedCIDRs
	}

	// If the hostname is already an IP literal, no resolution is needed.
	if ip := net.ParseIP(hostname); ip != nil {
		if blocked, cidr := isBlockedIP(ip, blockedCIDRs); blocked {
			return nil, fmt.Errorf("target IP %s is in blocked range %s", ip, cidr)
		}
		return []net.IP{ip}, nil
	}

	addrs, err := lookupIPAddr(ctx, hostname)
	if err != nil {
		// Treat DNS failure as a block — we cannot verify safety.
		return nil, fmt.Errorf("DNS resolution failed for %q: %w", hostname, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("DNS resolution for %q returned no addresses", hostname)
	}

	validated := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if blocked, cidr := isBlockedIP(addr.IP, blockedCIDRs); blocked {
			return nil, fmt.Errorf("hostname %q resolves to blocked IP %s (range %s)", hostname, addr.IP, cidr)
		}
		validated = append(validated, addr.IP)
	}
	return validated, nil
}

// isBlockedIP returns true and the matching CIDR string if ip falls within any
// of the provided blocked ranges.
func isBlockedIP(ip net.IP, blockedCIDRs []*net.IPNet) (bool, string) {
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return true, cidr.String()
		}
	}
	return false, ""
}
