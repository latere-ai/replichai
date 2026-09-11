// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package fetch

import (
	"context"
	"net"
)

// withResolver answers name lookups from a table the test supplies, so a test
// can map a hostname to a loopback server without using DNS.
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
