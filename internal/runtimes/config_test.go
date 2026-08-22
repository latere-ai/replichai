package runtimes

import "testing"

// The generic names REPLICHAI_API_KEY and REPLICHAI_API_BASE are gone. They
// were readable by whichever runtime happened to be selected, which is exactly
// how one runtime's credential reaches another; nothing may revive them.
func TestCredentialsComeOnlyFromTheRuntimesOwnVariables(t *testing.T) {
	generic := ResolveCredentials(mapLookup(map[string]string{
		"REPLICHAI_API_KEY":  "generic-key",
		"REPLICHAI_API_BASE": "generic-base",
		"PROVIDER":           "anthropic",
	}))
	if generic != (ProviderCredentials{}) {
		t.Fatalf("a generic variable supplied %+v", generic)
	}

	scoped := ResolveCredentials(mapLookup(map[string]string{
		"ANTHROPIC_API_KEY":  "scoped-key",
		"ANTHROPIC_BASE_URL": "scoped-base",
	}))
	if scoped.AnthropicKey != "scoped-key" || scoped.AnthropicBase != "scoped-base" {
		t.Fatalf("scoped credentials = %+v", scoped)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
