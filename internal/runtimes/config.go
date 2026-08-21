package runtimes

import (
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
)

type LookupEnv func(string) (string, bool)

type Selection struct {
	Runtime     managed.RuntimeName
	LegacyAlias bool
}

// ResolveSelection applies the shared CLI/server precedence. The caller owns
// the product default; the contract and catalog do not.
func ResolveSelection(lookup LookupEnv, catalog *Catalog, productDefault managed.RuntimeName) (Selection, error) {
	runtimeValue, runtimeSet := lookup("REPLICHAI_RUNTIME")
	providerValue, providerSet := lookup("PROVIDER")
	if runtimeSet && strings.TrimSpace(runtimeValue) == "" {
		runtimeSet = false
	}
	if providerSet && strings.TrimSpace(providerValue) == "" {
		providerSet = false
	}
	chosen := string(productDefault)
	legacyAlias := false
	if providerSet {
		chosen = providerValue
		legacyAlias = true
	}
	if runtimeSet {
		chosen = runtimeValue
	}
	canonical, err := catalog.Canonical(chosen)
	if err != nil {
		return Selection{}, err
	}
	if runtimeSet && providerSet {
		legacyCanonical, legacyErr := catalog.Canonical(providerValue)
		if legacyErr != nil {
			return Selection{}, legacyErr
		}
		if canonical != legacyCanonical {
			return Selection{}, fmt.Errorf("REPLICHAI_RUNTIME %q conflicts with deprecated PROVIDER %q", runtimeValue, providerValue)
		}
	}
	return Selection{Runtime: canonical, LegacyAlias: legacyAlias && !runtimeSet}, nil
}

type ProviderCredentials struct {
	AnthropicKey  string
	AnthropicBase string
}

// ResolveCredentials keeps the generic legacy credential scoped to the legacy
// Anthropic selector. It can never become a credential for another runtime.
func ResolveCredentials(lookup LookupEnv, selection Selection) (ProviderCredentials, []string) {
	credentials := ProviderCredentials{}
	credentials.AnthropicKey, _ = lookup("ANTHROPIC_API_KEY")
	credentials.AnthropicBase, _ = lookup("ANTHROPIC_BASE_URL")
	var warnings []string
	if selection.Runtime == "anthropic-managed" && selection.LegacyAlias {
		if credentials.AnthropicKey == "" {
			credentials.AnthropicKey, _ = lookup("REPLICHAI_API_KEY")
			if credentials.AnthropicKey != "" {
				warnings = append(warnings, "REPLICHAI_API_KEY is deprecated; use ANTHROPIC_API_KEY")
			}
		}
		if credentials.AnthropicBase == "" {
			credentials.AnthropicBase, _ = lookup("REPLICHAI_API_BASE")
			if credentials.AnthropicBase != "" {
				warnings = append(warnings, "REPLICHAI_API_BASE is deprecated; use ANTHROPIC_BASE_URL")
			}
		}
	}
	return credentials, warnings
}
