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

const AnthropicManaged managed.RuntimeName = "anthropic-managed"

type AnthropicConfig struct {
	APIKey  string
	BaseURL string
}

type Config struct {
	MemoryStore *managedmemory.Store
	Anthropic   AnthropicConfig
}

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
