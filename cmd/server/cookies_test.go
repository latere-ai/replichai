// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// TestSecureCookiesFollowsTheSchemeAndNothingElse checks that the session
// cookie's hardening follows the scheme and takes no override.
// AUTH_INSECURE_COOKIES could once turn Secure off, and because plain HTTP
// already answers false, the only deployment it could have weakened was one
// serving real traffic over TLS.
func TestSecureCookiesFollowsTheSchemeAndNothingElse(t *testing.T) {
	for _, c := range []struct {
		base string
		want bool
	}{
		{"https://replichai.latere.ai", true},
		{"https://localhost:8080", true},
		{"http://localhost:8080", false},
		{"http://127.0.0.1:8080", false},
	} {
		if got := secureCookies(c.base); got != c.want {
			t.Errorf("secureCookies(%q) = %v, want %v", c.base, got, c.want)
		}
	}

	// The removed override must not come back through the environment: an https
	// deployment stays hardened whatever is set around it.
	t.Setenv("AUTH_INSECURE_COOKIES", "true")
	if !secureCookies("https://replichai.latere.ai") {
		t.Error("an environment variable turned Secure off on an https deployment")
	}
}
