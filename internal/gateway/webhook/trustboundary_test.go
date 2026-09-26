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

package webhook

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testUntrustedPeer = "203.0.113.9:54321"
	testTrustedProxy  = "192.168.1.5:443"
)

// mustTestTrustedCIDR returns a *net.IPNet for the trusted-proxy CIDR used
// throughout this file's tests.
func mustTestTrustedCIDR(t *testing.T) *net.IPNet {
	t.Helper()
	_, ipNet, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("parsing test CIDR: %v", err)
	}
	return ipNet
}

func TestRealClientIP_NoTrustedProxies_HeadersIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testUntrustedPeer
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Real-IP", "10.0.0.2")

	got := realClientIP(req, nil)
	if got != "203.0.113.9" {
		t.Errorf("realClientIP with no trusted proxies: got %q, want peer address %q (headers must be ignored)", got, "203.0.113.9")
	}
}

func TestRealClientIP_UntrustedPeer_HeadersIgnoredEvenWithConfig(t *testing.T) {
	// A direct attacker (not connecting through any configured proxy) cannot
	// spoof their IP just because *some* trusted-proxy config exists elsewhere.
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testUntrustedPeer // not in the trusted range
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	got := realClientIP(req, trusted)
	if got != "203.0.113.9" {
		t.Errorf("realClientIP from an untrusted peer: got %q, want peer address %q (spoofed header must be ignored)", got, "203.0.113.9")
	}
}

func TestRealClientIP_TrustedPeer_UsesRightmostForwardedForEntry(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy // the trusted proxy itself
	// A client-injected fake entry followed by the address our trusted proxy
	// actually observed (appended, not overwritten).
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 198.51.100.7")

	got := realClientIP(req, trusted)
	if got != "198.51.100.7" {
		t.Errorf("realClientIP from a trusted peer: got %q, want the right-most entry %q, not the client-injected first entry", got, "198.51.100.7")
	}
}

func TestRealClientIP_TrustedPeer_FallsBackToRealIPHeader(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy
	req.Header.Set("X-Real-IP", "198.51.100.7")

	got := realClientIP(req, trusted)
	if got != "198.51.100.7" {
		t.Errorf("realClientIP X-Real-IP fallback: got %q, want %q", got, "198.51.100.7")
	}
}

// legacyRealClientIP is a byte-for-byte copy of realClientIP's algorithm
// exactly as it existed before STORY-032 (multi-hop chain-walking): once the
// immediate peer is trusted, it takes the right-most X-Forwarded-For entry
// unconditionally, with no further inspection of that entry or any others.
// It is kept here, deliberately duplicated rather than refactored away, so
// TestRealClientIP_SingleHop_ByteIdenticalToPreMultihopBehavior can compare
// the new implementation's output against the literal old algorithm — not
// just against a hand-written expected string that could coincidentally
// match new code with a different (but still wrong) bug.
func legacyRealClientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if len(trustedProxies) == 0 || !isTrustedProxy(host, trustedProxies) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := strings.TrimSpace(xri); ip != "" {
			return ip
		}
	}
	return host
}

// TestRealClientIP_SingleHop_ByteIdenticalToPreMultihopBehavior is the
// STORY-032 regression test: for every single-hop scenario the old test
// suite already covered (and a couple of additional single-hop edge cases),
// the new chain-walking realClientIP must return the exact same string as
// legacyRealClientIP, the literal pre-change algorithm above. This proves
// byte-identical output by direct comparison against old code, not just by
// asserting a plausible-looking constant.
func TestRealClientIP_SingleHop_ByteIdenticalToPreMultihopBehavior(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}

	cases := []struct {
		name           string
		remoteAddr     string
		forwardedFor   string
		realIP         string
		trustedProxies []*net.IPNet
	}{
		{
			name:         "no trusted proxies configured, headers present",
			remoteAddr:   testUntrustedPeer,
			forwardedFor: "10.0.0.1",
			realIP:       "10.0.0.2",
		},
		{
			name:           "untrusted peer, trusted proxies configured",
			remoteAddr:     testUntrustedPeer,
			forwardedFor:   "10.0.0.1",
			trustedProxies: trusted,
		},
		{
			name:           "trusted peer, client-injected first entry plus real right-most entry",
			remoteAddr:     testTrustedProxy,
			forwardedFor:   "10.0.0.1, 198.51.100.7",
			trustedProxies: trusted,
		},
		{
			name:           "trusted peer, single X-Forwarded-For entry",
			remoteAddr:     testTrustedProxy,
			forwardedFor:   "198.51.100.7",
			trustedProxies: trusted,
		},
		{
			name:           "trusted peer, no X-Forwarded-For, falls back to X-Real-IP",
			remoteAddr:     testTrustedProxy,
			realIP:         "198.51.100.7",
			trustedProxies: trusted,
		},
		{
			name:           "trusted peer, no headers at all, falls back to peer",
			remoteAddr:     testTrustedProxy,
			trustedProxies: trusted,
		},
		{
			name:           "trusted peer, empty X-Forwarded-For value, falls back to X-Real-IP",
			remoteAddr:     testTrustedProxy,
			forwardedFor:   "",
			realIP:         "198.51.100.7",
			trustedProxies: trusted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.forwardedFor)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}

			got := realClientIP(req, tc.trustedProxies)
			want := legacyRealClientIP(req, tc.trustedProxies)
			if got != want {
				t.Errorf("realClientIP (new) = %q, legacyRealClientIP (old) = %q — single-hop output must be byte-identical", got, want)
			}
		})
	}
}

// TestRealClientIP_MultiHop_TwoTrustedHops verifies a 2-hop trusted chain
// (e.g. WAF -> ingress -> gateway, both WAF and ingress trusted) resolves to
// the real client IP: the right-most entry is the trusted hop's own address
// (popped), and the walk then returns the next entry left, which is not
// itself a trusted proxy.
func TestRealClientIP_MultiHop_TwoTrustedHops(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}
	realClient := "198.51.100.7"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy // the nearest trusted hop (the gateway's TCP peer)
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, 192.168.1.20", realClient))

	got := realClientIP(req, trusted)
	if got != realClient {
		t.Errorf("2-hop trusted chain: got %q, want real client %q", got, realClient)
	}
}

// TestRealClientIP_MultiHop_ThreeTrustedHops verifies a 3-hop trusted chain
// (CDN -> WAF -> ingress -> gateway, all three intermediate hops trusted)
// resolves to the real client IP by popping trusted entries right-to-left
// until it hits the one that isn't.
func TestRealClientIP_MultiHop_ThreeTrustedHops(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}
	realClient := "198.51.100.7"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy // the nearest trusted hop (the gateway's TCP peer)
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("%s, 192.168.1.20, 192.168.1.21", realClient))

	got := realClientIP(req, trusted)
	if got != realClient {
		t.Errorf("3-hop trusted chain: got %q, want real client %q", got, realClient)
	}
}

// TestRealClientIP_MultiHop_UntrustedEntryInMiddleStopsWalk verifies the walk
// stops at the first non-trusted entry encountered scanning right-to-left,
// even when there are further (irrelevant) entries to its left. This matters
// because an entry that isn't itself a trusted proxy is, by construction,
// treated as the real client as observed by the nearest trusted hop — the
// walk must not continue past it looking for something else.
func TestRealClientIP_MultiHop_UntrustedEntryInMiddleStopsWalk(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}
	const untrustedMiddleEntry = "203.0.113.55"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy // the nearest trusted hop
	// Right-most is a trusted hop (popped); next-left is NOT a trusted proxy
	// (stop and return it); left of that is a further entry that must never
	// be reached.
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.99, %s, 192.168.1.20", untrustedMiddleEntry))

	got := realClientIP(req, trusted)
	if got != untrustedMiddleEntry {
		t.Errorf("untrusted-entry-in-the-middle: got %q, want the walk to stop at %q rather than continuing past it", got, untrustedMiddleEntry)
	}
}

// TestRealClientIP_MultiHop_WalkCapBounded verifies that a pathological
// X-Forwarded-For header — more trusted-looking entries than
// maxForwardedForHops — is bounded: the walk never inspects more than
// maxForwardedForHops entries, so it does not reach an entry further left
// than the cap allows, even though that entry is not itself trusted. When
// every entry within the bounded window is itself trusted, the left-most
// entry actually examined (not the true left-most entry in the full header)
// is returned.
func TestRealClientIP_MultiHop_WalkCapBounded(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}

	const trustedHopCount = 40 // comfortably more than maxForwardedForHops
	const realClientBeyondCap = "203.0.113.50"

	entries := make([]string, 0, trustedHopCount+1)
	entries = append(entries, realClientBeyondCap) // left-most: the true real client, never reached
	for i := 1; i <= trustedHopCount; i++ {
		entries = append(entries, fmt.Sprintf("192.168.1.%d", i))
	}
	header := strings.Join(entries, ", ")

	// The walk examines exactly maxForwardedForHops entries counting from
	// the right, so it lands on this index of entries (0-based).
	boundaryIdx := len(entries) - maxForwardedForHops
	if boundaryIdx <= 0 {
		t.Fatalf("test setup: boundaryIdx=%d must be > 0 for this test to exercise the cap (entries=%d, cap=%d)", boundaryIdx, len(entries), maxForwardedForHops)
	}
	wantBoundary := entries[boundaryIdx]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = testTrustedProxy
	req.Header.Set("X-Forwarded-For", header)

	got := realClientIP(req, trusted)
	if got == realClientBeyondCap {
		t.Fatalf("walk cap not enforced: realClientIP reached the true left-most entry %q, which is beyond maxForwardedForHops=%d", realClientBeyondCap, maxForwardedForHops)
	}
	if got != wantBoundary {
		t.Errorf("walk cap boundary: got %q, want the left-most entry actually examined within the bounded window %q", got, wantBoundary)
	}
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		got, err := ParseTrustedProxyCIDRs("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("parses a comma-separated list", func(t *testing.T) {
		got, err := ParseTrustedProxyCIDRs("10.0.0.0/8, 192.168.1.0/24")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d CIDRs, want 2", len(got))
		}
	})

	t.Run("rejects an invalid CIDR", func(t *testing.T) {
		if _, err := ParseTrustedProxyCIDRs("not-a-cidr"); err == nil {
			t.Error("expected an error for an invalid CIDR, got nil")
		}
	})
}

// TestAuthenticateRequest_IPAllowlist_SpoofedHeaderRejectedByDefault verifies
// the actual security-relevant path (not just realClientIP in isolation): an
// attacker connecting directly and setting X-Forwarded-For to an allowed IP
// must NOT bypass ipAllowlist auth when no trusted proxies are configured.
func TestAuthenticateRequest_IPAllowlist_SpoofedHeaderRejectedByDefault(t *testing.T) {
	entry := RouteEntry{
		AuthType:    authTypeIPAllowlist,
		IPAllowlist: []string{"192.168.1.0/24"},
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/x", nil)
	req.RemoteAddr = testUntrustedPeer // attacker's real, disallowed IP
	req.Header.Set("X-Forwarded-For", "192.168.1.50")

	status, _ := authenticateRequest(req, nil, entry, "t", nil)
	if status != http.StatusForbidden {
		t.Errorf("expected the spoofed X-Forwarded-For to be ignored and the request rejected (403), got status %d", status)
	}
}

// TestAuthenticateRequest_IPAllowlist_TrustedProxyHonored verifies the allowlist
// still works correctly for the legitimate case: a configured trusted proxy
// forwarding a real client IP that is itself allowed.
func TestAuthenticateRequest_IPAllowlist_TrustedProxyHonored(t *testing.T) {
	trusted := []*net.IPNet{mustTestTrustedCIDR(t)}
	entry := RouteEntry{
		AuthType:    authTypeIPAllowlist,
		IPAllowlist: []string{"198.51.100.0/24"},
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/x", nil)
	req.RemoteAddr = testTrustedProxy // the trusted proxy
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	status, msg := authenticateRequest(req, nil, entry, "t", trusted)
	if status != http.StatusOK {
		t.Errorf("expected the allowed client IP forwarded by a trusted proxy to pass, got status %d (%s)", status, msg)
	}
}
