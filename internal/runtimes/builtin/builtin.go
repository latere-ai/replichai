// Package builtin is the single composition boundary allowed to import
// concrete managed-session adapters.
package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/legacy"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/provider/anthropic"
	"github.com/latere-ai/replichai/internal/runtimes"
)

// AnthropicManaged names the runtime that executes sessions against
// Anthropic's API, as opposed to the in-process "memory" runtime that answers
// without a vendor. It is the runtime real work runs on, so the server also
// installs it alongside whichever runtime was selected whenever an API key is
// configured. Its factory fails when no key is.
const AnthropicManaged managed.RuntimeName = "anthropic-managed"

// AnthropicConfig holds what the Anthropic runtime needs to reach the vendor.
type AnthropicConfig struct {
	// APIKey is required to build the runtime; the catalogue still lists
	// and resolves the name without it.
	APIKey string
	// BaseURL overrides the vendor endpoint and is empty in normal use.
	BaseURL string
}

// Config is what a composition root hands this package to compose the
// catalogue. Every field is optional: Config{} still yields a catalogue whose
// names resolve, which is how both binaries validate the operator's choice
// before resolving credentials, and a nil MemoryStore simply gives the memory
// runtime a fresh one.
type Config struct {
	MemoryStore *managedmemory.Store
	Anthropic   AnthropicConfig
}

// Catalog composes the runtimes this build ships with. It is the one place
// concrete adapters are named, so every other package routes by name alone.
// Entries are registered whether or not their configuration is present and
// report a missing credential from the factory instead, keeping name
// resolution usable on an unconfigured catalogue.
func Catalog(config Config) (*runtimes.Catalog, error) {
	return runtimes.NewCatalog(
		runtimes.Entry{Name: managedmemory.Name, Build: func(context.Context) (managed.Runtime, error) {
			return managedmemory.NewWithStore(config.MemoryStore, managedmemory.Config{}), nil
		}},
		runtimes.Entry{Name: AnthropicManaged, Build: func(context.Context) (managed.Runtime, error) {
			if strings.TrimSpace(config.Anthropic.APIKey) == "" {
				return nil, fmt.Errorf("ANTHROPIC_API_KEY is required")
			}
			options := []anthropic.Option{anthropic.WithAPIKey(config.Anthropic.APIKey)}
			if config.Anthropic.BaseURL != "" {
				options = append(options, anthropic.WithAPIBase(config.Anthropic.BaseURL))
			}
			backend, err := anthropic.New(options...)
			if err != nil {
				return nil, err
			}
			return legacy.New(legacy.Config{Name: AnthropicManaged, ModelProvider: "anthropic", Provider: backend})
		}},
	)
}
