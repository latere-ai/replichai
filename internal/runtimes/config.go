// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

// LookupEnv is the environment reader credential resolution goes through, so a
// caller can pass the process environment or a fixed map in a test.
type LookupEnv func(string) (string, bool)

// ProviderCredentials holds the runtime-scoped credentials and endpoint
// configuration read out of the environment. Every field belongs to exactly one runtime, so nothing here is a
// generic credential another runtime could pick up.
type ProviderCredentials struct {
	AnthropicKey  string
	AnthropicBase string
}

// ResolveCredentials reads each runtime's own configuration out of the
// environment. Every name here belongs to exactly one runtime, so a value can
// only ever reach the runtime it was written for and there is no generic
// credential left to scope.
func ResolveCredentials(lookup LookupEnv) ProviderCredentials {
	credentials := ProviderCredentials{}
	credentials.AnthropicKey, _ = lookup("ANTHROPIC_API_KEY")
	credentials.AnthropicBase, _ = lookup("ANTHROPIC_BASE_URL")
	return credentials
}
