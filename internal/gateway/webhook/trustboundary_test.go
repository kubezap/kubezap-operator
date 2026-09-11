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
	"net"
	"net/http"
	"net/http/httptest"
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
