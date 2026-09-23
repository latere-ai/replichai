// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
)

const defaultCLIRuntime = "anthropic-managed"

// runtimeEnv is the environment variable that selects the runtime, as
// README.md documents. --runtime overrides it per command. The server reads
// the same variable.
const runtimeEnv = "REPLICHAI_RUNTIME"

// runtimeFlag registers -runtime on a subcommand's flag set. The flag defaults
// to empty so an unset flag can be told apart from one typed; the value is
// settled after parsing by resolveRuntime, and the help text says where it
// comes from. fallback is the subcommand's own default when neither the flag
// nor the environment names one.
func runtimeFlag(fs *flag.FlagSet, fallback string) *string {
	return fs.String("runtime", "",
		"runtime to use (default $"+runtimeEnv+", then \""+fallback+"\")")
}

// resolveRuntime applies the rule every subcommand shares: the flag, then
// REPLICHAI_RUNTIME, then the subcommand's own default. It is a pure function
// so a test can drive each step; the CLI passes os.Getenv.
func resolveRuntime(flagValue string, getenv func(string) string, fallback string) string {
	return firstNonEmpty(flagValue, getenv(runtimeEnv), fallback)
}

// localOptions holds the settings of the local runtime that are not part of
// its name: the sandbox mechanism, the harness, and where sessions live. They
// are flags with environment defaults, so a batch file and a terminal reach
// the same runtime the same way.
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
// has an environment default, so an operator configures a machine once.
func (options *localOptions) bind(fs *flag.FlagSet) {
	fs.StringVar(&options.sandbox, "sandbox", envOr("REPLICHAI_SANDBOX", string(hostsandbox.Host)),
		"with --runtime local: host (srt) or container (podman, docker)")
	fs.StringVar(&options.harness, "harness", envOr("REPLICHAI_HARNESS", "claude-code"),
		"with --runtime local: the agent CLI to run, claude-code or codex")
	fs.StringVar(&options.engine, "engine", envOr("REPLICHAI_CONTAINER_ENGINE", ""),
		"with --sandbox container: podman or docker")
	fs.StringVar(&options.image, "image", envOr("REPLICHAI_HARNESS_IMAGE", ""),
		"with --sandbox container: the container image for the harness")
	fs.StringVar(&options.models, "models", envOr("REPLICHAI_MODELS_FILE", defaultModelsFile()),
		"with --runtime local: path to the model catalog file")
	fs.StringVar(&options.root, "session-root", envOr("REPLICHAI_SESSION_ROOT", defaultSessionRoot()),
		"with --runtime local: directory where session directories are created")
	fs.StringVar(&options.retention, "retention", envOr("REPLICHAI_SESSION_RETENTION", "keep"),
		"with --runtime local: what happens to a session directory after release: keep, on-failure or discard")
}

// config reads the process environment and passes it down as values. Nothing
// inside the runtime layer reads the environment itself, because a second,
// unscoped source of configuration there would let a credential meant for one
// runtime reach another, whatever the variable is called.
func (options localOptions) config() builtin.LocalConfig {
	home, _ := os.UserHomeDir()
	return builtin.LocalConfig{
		Sandbox: hostsandbox.DriverName(options.sandbox), Harness: options.harness,
		Engine: options.engine, Image: options.image, ModelsFile: options.models,
		Root: options.root, Retention: options.retention,
		Home: home, Lookup: os.LookupEnv,
	}
}

// defaultModelsFile is the path of an operator's model catalog. A missing
// file is not an error: the harness's own login is used instead, so a first
// run on a signed-in machine needs no configuration.
func defaultModelsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "replichai", "models.yaml")
}

// defaultSessionRoot is the directory session directories are created in. It
// is under the operator's home rather than a temporary directory, because the
// workspace is the reproduction and an operating system that cleans /tmp would
// delete it.
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
