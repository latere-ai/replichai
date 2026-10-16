// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

import "strings"

// LookupEnv is the environment reader credential resolution goes through, so a
// caller can pass the process environment or a fixed map in a test.
type LookupEnv func(string) (string, bool)

// ProviderCredentials holds the runtime-scoped credentials and endpoint
// configuration read out of the environment. Every field belongs to exactly one runtime, so nothing here is a
// generic credential another runtime could pick up.
type ProviderCredentials struct {
	AnthropicKey  string
	AnthropicBase string
	// LatereAPIURL and LatereAuthURL are the Latere platform's API origin
	// and its Auth, empty for the production defaults. LatereAPIKey is a
	// person's personal access token, and LatereTokenCommand a command that
	// prints a bearer for the platform's API; either lets a command-line run
	// act as that person.
	LatereAPIURL       string
	LatereAuthURL      string
	LatereAPIKey       string
	LatereTokenCommand string
	// LatereEnvironments are the environments a Latere session may name,
	// from LATERE_ENVIRONMENTS, a comma-separated list such as
	// "base,science". Empty offers the base environment alone, which is
	// the only one a deployment can rely on the platform's catalog holding.
	LatereEnvironments []string
}

// ResolveCredentials reads each runtime's own configuration out of the
// environment. Every name here belongs to exactly one runtime, so a value can
// only ever reach the runtime it was written for and there is no generic
// credential left to scope.
func ResolveCredentials(lookup LookupEnv) ProviderCredentials {
	credentials := ProviderCredentials{}
	credentials.AnthropicKey, _ = lookup("ANTHROPIC_API_KEY")
	credentials.AnthropicBase, _ = lookup("ANTHROPIC_BASE_URL")
	credentials.LatereAPIURL, _ = lookup("LATERE_API_URL")
	credentials.LatereAuthURL, _ = lookup("LATERE_AUTH_URL")
	credentials.LatereAPIKey, _ = lookup("LATERE_API_KEY")
	credentials.LatereTokenCommand, _ = lookup("LATERE_TOKEN_COMMAND")
	if list, ok := lookup("LATERE_ENVIRONMENTS"); ok {
		for name := range strings.SplitSeq(list, ",") {
			if name = strings.TrimSpace(name); name != "" {
				credentials.LatereEnvironments = append(credentials.LatereEnvironments, name)
			}
		}
	}
	return credentials
}
