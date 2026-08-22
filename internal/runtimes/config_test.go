package runtimes

import "testing"

func TestCredentialsComeFromTheRuntimesOwnVariables(t *testing.T) {
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
