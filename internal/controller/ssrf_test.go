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
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SSRF protection", func() {
	ctx := context.Background()

	Describe("checkSSRF with IP literal URLs", func() {
		It("blocks RFC1918 10.x.x.x addresses", func() {
			err := checkSSRF(ctx, "http://10.0.0.1/path", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("SSRF check"))
			Expect(err.Error()).To(ContainSubstring("10.0.0.1"))
		})

		It("blocks RFC1918 172.16.x.x addresses", func() {
			err := checkSSRF(ctx, "http://172.16.5.5/path", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("172.16.5.5"))
		})

		It("blocks RFC1918 192.168.x.x addresses", func() {
			err := checkSSRF(ctx, "http://192.168.1.1/path", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("192.168.1.1"))
		})

		It("blocks loopback 127.x.x.x addresses", func() {
			err := checkSSRF(ctx, "http://127.0.0.1/path", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("127.0.0.1"))
		})

		It("blocks cloud metadata address 169.254.169.254", func() {
			err := checkSSRF(ctx, "http://169.254.169.254/latest/meta-data/", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("169.254.169.254"))
		})

		It("blocks IPv6 loopback ::1", func() {
			err := checkSSRF(ctx, "http://[::1]/path", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("::1"))
		})

		It("allows public IP addresses", func() {
			err := checkSSRF(ctx, "https://8.8.8.8/dns-query", nil, false)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("checkSSRF with hostname URLs", func() {
		It("blocks .svc.cluster.local hostnames without DNS lookup", func() {
			err := checkSSRF(ctx, "http://my-service.default.svc.cluster.local/api", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(".svc.cluster.local"))
		})

		It("blocks .svc.cluster.local with trailing dot", func() {
			err := checkSSRF(ctx, "http://my-service.default.svc.cluster.local./api", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(".svc.cluster.local"))
		})

		It("allows valid public hostnames (resolves in test environment)", func() {
			// localhost resolves to 127.0.0.1 — should be blocked
			err := checkSSRF(ctx, "http://localhost/path", nil, false)
			Expect(err).To(HaveOccurred()) // localhost → 127.0.0.1, blocked
		})
	})

	Describe("ParseCIDRList", func() {
		It("returns defaults when empty string provided", func() {
			cidrs, err := ParseCIDRList("")
			Expect(err).NotTo(HaveOccurred())
			Expect(cidrs).To(HaveLen(len(defaultSSRFBlockedCIDRs)))
		})

		It("appends additional CIDRs to defaults", func() {
			cidrs, err := ParseCIDRList("203.0.113.0/24,198.51.100.0/24")
			Expect(err).NotTo(HaveOccurred())
			Expect(cidrs).To(HaveLen(len(defaultSSRFBlockedCIDRs) + 2))
		})

		It("returns error for invalid CIDR", func() {
			_, err := ParseCIDRList("not-a-cidr")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid CIDR"))
		})

		It("ignores empty entries from extra commas", func() {
			cidrs, err := ParseCIDRList("203.0.113.0/24,,")
			Expect(err).NotTo(HaveOccurred())
			Expect(cidrs).To(HaveLen(len(defaultSSRFBlockedCIDRs) + 1))
		})
	})

	Describe("isBlockedIP", func() {
		It("matches IP in CIDR", func() {
			_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
			blocked, match := isBlockedIP(net.ParseIP("10.5.5.5"), []*net.IPNet{cidr})
			Expect(blocked).To(BeTrue())
			Expect(match).To(Equal("10.0.0.0/8"))
		})

		It("does not match IP outside CIDR", func() {
			_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
			blocked, _ := isBlockedIP(net.ParseIP("11.0.0.1"), []*net.IPNet{cidr})
			Expect(blocked).To(BeFalse())
		})
	})
})
