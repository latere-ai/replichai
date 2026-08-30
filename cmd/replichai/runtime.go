// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

const defaultCLIRuntime = "anthropic-managed"

// localOptions are the choices the local runtime takes, none of which belong
// in its name: the sandbox mechanism, the harness, and where sessions live.
// They are flags with environment defaults, so a batch file and a terminal
// reach the same runtime the same way.
type localOptions struct {
	sandbox   string
	harness   string
	engine    string
	image     string
	models    string
	root      string
	retention string
}

// bind registers the local runtime's flags on a flag set. Every one of them
// has an environment default, so an operator sets a machine's habits once.
func (options *localOptions) bind(fs *flag.FlagSet) {
	fs.StringVar(&options.sandbox, "sandbox", envOr("REPLICHAI_SANDBOX", string(sandbox.Host)),
		"with --runtime local: host (srt) or container (podman, docker)")
	fs.StringVar(&options.harness, "harness", envOr("REPLICHAI_HARNESS", "claude-code"),
		"with --runtime local: the agentic CLI to run — claude-code or codex")
	fs.StringVar(&options.engine, "engine", envOr("REPLICHAI_CONTAINER_ENGINE", ""),
		"with --sandbox container: podman or docker")
	fs.StringVar(&options.image, "image", envOr("REPLICHAI_HARNESS_IMAGE", ""),
		"with --sandbox container: the harness image")
	fs.StringVar(&options.models, "models", envOr("REPLICHAI_MODELS_FILE", defaultModelsFile()),
		"with --runtime local: the model catalogue")
	fs.StringVar(&options.root, "session-root", envOr("REPLICHAI_SESSION_ROOT", defaultSessionRoot()),
		"with --runtime local: where session directories are made")
	fs.StringVar(&options.retention, "retention", envOr("REPLICHAI_SESSION_RETENTION", "keep"),
		"with --runtime local: keep, on-failure, or discard a session directory after release")
}

// config is where the process environment is read and handed down as values.
// Nothing inside the runtime seam reads it: a second, unscoped source of
// configuration in there is how a credential meant for one runtime reaches
// another, whatever the variable is called.
func (options localOptions) config() builtin.LocalConfig {
	home, _ := os.UserHomeDir()
	return builtin.LocalConfig{
		Sandbox: sandbox.DriverName(options.sandbox), Harness: options.harness,
		Engine: options.engine, Image: options.image, ModelsFile: options.models,
		Root: options.root, Retention: options.retention,
		Home: home, Lookup: os.LookupEnv, Environ: os.Environ(),
	}
}

// defaultModelsFile is where an operator's model catalogue lives. A missing
// file is not an error: it yields the harness's own login, so a first run on a
// signed-in machine needs no configuration at all.
func defaultModelsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "replichai", "models.yaml")
}

// defaultSessionRoot is where session directories are made. It is under the
// operator's home rather than a temporary directory, because the workspace is
// the reproduction and an operating system that cleans /tmp would take it.
func defaultSessionRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "replichai-sessions")
	}
	return filepath.Join(home, ".replichai", "sessions")
}

func runtimeCatalog(rawName string, options localOptions) (*runtimes.Catalog, managed.RuntimeName, error) {
	base, err := builtin.Catalog(builtin.Config{})
	if err != nil {
		return nil, "", err
	}
	name, err := base.Canonical(rawName)
	if err != nil {
		return nil, "", err
	}
	credentials := runtimes.ResolveCredentials(os.LookupEnv)
	catalog, err := builtin.Catalog(builtin.Config{
		Anthropic: builtin.AnthropicConfig{APIKey: credentials.AnthropicKey, BaseURL: credentials.AnthropicBase},
		Local:     options.config(),
	})
	return catalog, name, err
}

func controllerFor(ctx context.Context, rawName string, interval time.Duration, options localOptions) (*managed.Controller, managed.Runtime, error) {
	catalog, name, err := runtimeCatalog(rawName, options)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := catalog.Build(ctx, string(name))
	if err != nil {
		return nil, nil, err
	}
	router, err := runtimes.NewRouter(name, runtime)
	if err != nil {
		return nil, nil, err
	}
	policy := managed.DefaultWaitPolicy()
	policy.InitialDelay = interval
	policy.MaxDelay = interval
	return managed.NewController(router, managed.WithWaitPolicy(policy)), runtime, nil
}
