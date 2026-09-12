// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package builtin is the single composition boundary allowed to import
// concrete managed-session adapters.
package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/legacy"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/provider/anthropic"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/container"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/host"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
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

// LocalConfig holds what the local runtime needs to run a session on this
// machine. Every field has a default, so an operator who says nothing gets the
// host sandbox, the Claude Code harness, and the harness's own login.
type LocalConfig struct {
	// Sandbox selects the isolation mechanism: host (the default) or
	// container. It is not part of the runtime's name, because both satisfy
	// the same contract and a name that encoded the mechanism would have to
	// be migrated to switch to the other.
	Sandbox hostsandbox.DriverName
	// Harness selects the agentic CLI: claude-code (the default) or codex.
	Harness string
	// Engine names the container engine where the container sandbox is
	// selected. Empty tries podman, then docker.
	Engine string
	// Image overrides the harness image.
	Image string
	// ModelsFile is the operator's model catalogue.
	ModelsFile string
	// Root is where session directories are made.
	Root string
	// Retention is what Release does with a session directory.
	Retention string
	// Home is the operator's home directory, denied wholesale by the host
	// sandbox and searched for the harness's own configuration.
	Home string
	// Lookup reads the environment for gateway credentials. It is the only
	// channel through which a stage inherits any of the environment, limited
	// to what hostsandbox.Environment admits. It is required because this
	// package is inside the seam and configuration arrives from the
	// composition root rather than being read here.
	Lookup local.LookupEnv
}

// Config is what a composition root hands this package to compose the
// catalogue. Every field is optional: Config{} still yields a catalogue whose
// names resolve, which is how both binaries validate the operator's choice
// before resolving credentials, and a nil MemoryStore gives the memory runtime
// a fresh one.
type Config struct {
	MemoryStore *managedmemory.Store
	Anthropic   AnthropicConfig
	Local       LocalConfig
}

// Harnesses is the set of agentic CLIs this build can drive. It is the only
// place they are named, as this package is the only place adapters are named.
func Harnesses() (*harness.Registry, error) {
	return harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
}

// buildLocal composes the local runtime from a sandbox driver, a harness, and
// the operator's model catalogue. A missing engine, a missing srt, a harness
// that is not installed, or a catalogue that does not validate fails here,
// before any paper is fetched.
func buildLocal(ctx context.Context, config LocalConfig) (managed.Runtime, error) {
	registry, err := Harnesses()
	if err != nil {
		return nil, err
	}
	selected := config.Harness
	if strings.TrimSpace(selected) == "" {
		selected = string(harness.ClaudeCode)
	}
	agent, err := registry.Lookup(selected)
	if err != nil {
		return nil, err
	}
	file, err := local.ReadCatalogFile(config.ModelsFile)
	if err != nil {
		return nil, err
	}
	catalog, err := local.NewCatalog(file, registry, config.Lookup)
	if err != nil {
		return nil, err
	}
	driver, err := buildSandbox(ctx, config, agent)
	if err != nil {
		return nil, err
	}
	return local.New(local.Config{
		Sandbox: driver, Harness: agent, Catalog: catalog, Lookup: config.Lookup,
		Root: config.Root, Retention: local.Retention(config.Retention),
		Machine: harness.Machine{
			Platform: string(hostsandbox.CurrentPlatform()), Home: config.Home,
			// Use the shared root rather than os.TempDir(). os.TempDir()
			// honours TMPDIR, which a session sets to its own directory,
			// but a harness writes its scratch under the shared root
			// regardless.
			TempDir: "/tmp", UID: os.Getuid(),
		},
	})
}

// buildSandbox picks the isolation mechanism. The host driver is the default
// because it needs no image and no virtual machine and reuses the login
// already on the machine. The container driver must be chosen explicitly and
// provides a pinned toolchain.
func buildSandbox(ctx context.Context, config LocalConfig, agent harness.Harness) (hostsandbox.Sandbox, error) {
	switch hostsandbox.DriverName(strings.ToLower(strings.TrimSpace(string(config.Sandbox)))) {
	case sandbox.Container:
		return container.New(ctx, container.Config{
			Engine: container.Engine(config.Engine), Image: config.Image,
		})
	case hostsandbox.Host, "":
		// The harness's own configuration is not granted here. The harness
		// reports where its login lives through Needs, and the runtime
		// decides whether a session is granted access to it: only a
		// session running on the operator's own sign-in is.
		return host.New(host.Config{
			Home: config.Home, HarnessBinary: agent.Binary(), Lookup: config.Lookup,
		}), nil
	default:
		return nil, fmt.Errorf("unknown sandbox %q; available: host, container", config.Sandbox)
	}
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
		runtimes.Entry{Name: local.Name, Build: func(ctx context.Context) (managed.Runtime, error) {
			return buildLocal(ctx, config.Local)
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
