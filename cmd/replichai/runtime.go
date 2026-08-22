package main

import (
	"context"
	"os"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
)

const defaultCLIRuntime = "anthropic-managed"

func runtimeCatalog(rawName string) (*runtimes.Catalog, managed.RuntimeName, error) {
	base, err := builtin.Catalog(builtin.Config{})
	if err != nil {
		return nil, "", err
	}
	name, err := base.Canonical(rawName)
	if err != nil {
		return nil, "", err
	}
	credentials := runtimes.ResolveCredentials(os.LookupEnv)
	catalog, err := builtin.Catalog(builtin.Config{Anthropic: builtin.AnthropicConfig{
		APIKey: credentials.AnthropicKey, BaseURL: credentials.AnthropicBase,
	}})
	return catalog, name, err
}

func controllerFor(ctx context.Context, rawName string, interval time.Duration) (*managed.Controller, managed.Runtime, error) {
	catalog, name, err := runtimeCatalog(rawName)
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
