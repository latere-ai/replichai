package runtimes

import (
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

func TestSelectionUsesSharedPrecedenceAndRejectsConflict(t *testing.T) {
	catalog, _ := NewCatalog(
		Entry{Name: "memory", Build: factory(&routerRuntime{name: "memory"})},
		Entry{Name: "anthropic-managed", Aliases: []string{"anthropic"}, Build: factory(&routerRuntime{name: "anthropic-managed"})},
	)
	for _, test := range []struct {
		name       string
		env        map[string]string
		want       managed.RuntimeName
		wantLegacy bool
		wantErr    bool
	}{
		{"default", nil, "memory", false, false},
		{"canonical", map[string]string{"REPLICHAI_RUNTIME": "anthropic-managed"}, "anthropic-managed", false, false},
		{"legacy selector", map[string]string{"PROVIDER": " anthropic "}, "anthropic-managed", true, false},
		{"same selectors", map[string]string{"REPLICHAI_RUNTIME": "anthropic-managed", "PROVIDER": "anthropic"}, "anthropic-managed", false, false},
		{"conflict", map[string]string{"REPLICHAI_RUNTIME": "memory", "PROVIDER": "anthropic"}, "", false, true},
		{"unknown", map[string]string{"REPLICHAI_RUNTIME": "missing"}, "", false, true},
		{"blank ignored", map[string]string{"REPLICHAI_RUNTIME": " "}, "memory", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection, err := ResolveSelection(mapLookup(test.env), catalog, "memory")
			if test.wantErr != (err != nil) || selection.Runtime != test.want || selection.LegacyAlias != test.wantLegacy {
				t.Fatalf("ResolveSelection = %+v, %v", selection, err)
			}
		})
	}
}

func TestCredentialsNeverReuseGenericValuesOutsideLegacyAnthropic(t *testing.T) {
	env := map[string]string{"REPLICHAI_API_KEY": "legacy-key", "REPLICHAI_API_BASE": "legacy-base"}
	credentials, warnings := ResolveCredentials(mapLookup(env), Selection{Runtime: "memory"})
	if credentials.AnthropicKey != "" || credentials.AnthropicBase != "" || len(warnings) != 0 {
		t.Fatalf("memory credentials = %+v, %v", credentials, warnings)
	}
	credentials, warnings = ResolveCredentials(mapLookup(env), Selection{Runtime: "anthropic-managed", LegacyAlias: true})
	if credentials.AnthropicKey != "legacy-key" || credentials.AnthropicBase != "legacy-base" || len(warnings) != 2 {
		t.Fatalf("legacy credentials = %+v, %v", credentials, warnings)
	}
	env["ANTHROPIC_API_KEY"] = "scoped-key"
	env["ANTHROPIC_BASE_URL"] = "scoped-base"
	credentials, warnings = ResolveCredentials(mapLookup(env), Selection{Runtime: "anthropic-managed", LegacyAlias: true})
	if credentials.AnthropicKey != "scoped-key" || credentials.AnthropicBase != "scoped-base" || len(warnings) != 0 {
		t.Fatalf("scoped credentials = %+v, %v", credentials, warnings)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
