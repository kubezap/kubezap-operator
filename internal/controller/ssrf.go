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

package controller

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// defaultSSRFBlockedCIDRs is the set of CIDRs blocked by default for HTTP step
// outbound requests. Covers RFC1918 private ranges, loopback, link-local
// (including cloud metadata endpoints at 169.254.169.254), and IPv6 equivalents.
var defaultSSRFBlockedCIDRs = mustParseCIDRs([]string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16", // link-local — includes AWS/GCP/Azure metadata IPs
	"0.0.0.0/8",
	"::1/128",
	"fe80::/10",     // IPv6 link-local
	"fc00::/7",      // IPv6 unique local
	"100.64.0.0/10", // CGNAT / shared address space (RFC6598)
})

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
// Used to process the --http-step-blocked-cidrs flag value.
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
			return nil, fmt.Errorf("invalid CIDR %q in --http-step-blocked-cidrs: %w", cidr, err)
		}
		result = append(result, ipNet)
	}
	return result, nil
}

// checkSSRF validates that rawURL is safe to connect to from an HTTP step.
// It rejects:
//   - URLs whose hostname ends in ".svc.cluster.local" (in-cluster service endpoints)
//   - URLs that resolve to any IP in the blocked CIDR list
//
// blockedCIDRs defaults to defaultSSRFBlockedCIDRs when nil. Pass a
// non-nil slice (constructed via ParseCIDRList) to add operator-configured ranges.
//
// DNS resolution uses the provided context for timeout control. Callers should
// ensure ctx has a reasonable deadline to prevent long DNS waits during reconcile.
func checkSSRF(ctx context.Context, rawURL string, blockedCIDRs []*net.IPNet) error {
	if blockedCIDRs == nil {
		blockedCIDRs = defaultSSRFBlockedCIDRs
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("SSRF check: invalid URL: %w", err)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("SSRF check: URL has no hostname")
	}

	// Reject in-cluster service DNS names directly — no DNS lookup needed.
	if strings.HasSuffix(hostname, ".svc.cluster.local") ||
		strings.HasSuffix(hostname, ".svc.cluster.local.") {
		return fmt.Errorf("SSRF check: requests to in-cluster service endpoints (.svc.cluster.local) are not permitted from HTTP steps; use a plugin Integration instead")
	}

	// If the hostname is already an IP literal, check it directly.
	if ip := net.ParseIP(hostname); ip != nil {
		if blocked, cidr := isBlockedIP(ip, blockedCIDRs); blocked {
			return fmt.Errorf("SSRF check: target IP %s is in blocked range %s", ip, cidr)
		}
		return nil
	}

	// Resolve hostname and check every returned address.
	var resolver net.Resolver
	addrs, err := resolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		// Treat DNS failure as a block — we cannot verify safety.
		return fmt.Errorf("SSRF check: DNS resolution failed for %q: %w", hostname, err)
	}

	for _, addr := range addrs {
		if blocked, cidr := isBlockedIP(addr.IP, blockedCIDRs); blocked {
			return fmt.Errorf("SSRF check: hostname %q resolves to blocked IP %s (range %s)", hostname, addr.IP, cidr)
		}
	}
	return nil
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
