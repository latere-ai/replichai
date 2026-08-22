package runtimes

type LookupEnv func(string) (string, bool)

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
