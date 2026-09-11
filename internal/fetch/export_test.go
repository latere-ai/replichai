// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package fetch

import (
	"context"
	"net"
)

// withPermit judges resolved addresses with a test's own rule.
func withPermit(permit func(ip net.IP) bool) Option { return func(c *Client) { c.permit = permit } }

// withResolver answers name lookups from a test's own table, so a test can
// stand a hostname in front of a loopback server without touching DNS.
func withResolver(table map[string][]net.IPAddr) Option {
	return func(c *Client) {
		c.resolve = func(_ context.Context, host string) ([]net.IPAddr, error) {
			if addrs, ok := table[host]; ok {
				return addrs, nil
			}
			if ip := net.ParseIP(host); ip != nil {
				return []net.IPAddr{{IP: ip}}, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
	}
}
