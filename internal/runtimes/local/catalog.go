// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package local runs a managed session on the operator's own machine: a
// sandbox around a workspace directory, a coding harness inside it, and a
// model reached through whatever endpoint the operator declares (045).
package local

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
)

// LookupEnv is the environment reader credential resolution goes through, so
// a caller can pass the process environment or a fixed map in a test.
type LookupEnv func(string) (string, bool)

// GatewayConfig is one declared model endpoint. A gateway with no key_env
// authenticates through the harness's own login when auth says so, and
// otherwise not at all, which is how a local server that needs no credential
// is reached.
type GatewayConfig struct {
	BaseURL string `yaml:"base_url"`
	Dialect string `yaml:"dialect"`
	Auth    string `yaml:"auth"`
	KeyEnv  string `yaml:"key_env"`
}

// ModelConfig is one declared model: what it is, where to reach it, which
// harness talks to it, and which preference tiers it answers.
type ModelConfig struct {
	Provider    string   `yaml:"provider"`
	ID          string   `yaml:"id"`
	Gateway     string   `yaml:"gateway"`
	Harness     string   `yaml:"harness"`
	Preferences []string `yaml:"preferences"`
}

// CatalogFile is the operator's declaration, as it is written on disk.
type CatalogFile struct {
	Gateways map[string]GatewayConfig `yaml:"gateways"`
	Models   []ModelConfig            `yaml:"models"`
}

// Catalog is a validated declaration: every model names a gateway that exists
// and a harness that speaks its dialect, every credential has been resolved,
// and no preference resolves two ways within one harness.
//
// The catalog is declared rather than discovered because a gateway in front
// of an open-weights model may serve one model and expose no catalog at all,
// leaving nothing a preference tier could be resolved from.
type Catalog struct {
	entries  []entry
	gateways map[string]harness.Gateway
}

type entry struct {
	ref         managed.ModelRef
	gateway     string
	harness     harness.Name
	preferences []string
}

// DefaultCatalogFile is the declaration used when the operator has written
// none: the harness's own login, which needs no key at all. A first run on a
// machine where `claude login` has already happened therefore needs no
// configuration.
func DefaultCatalogFile() CatalogFile {
	return CatalogFile{
		Gateways: map[string]GatewayConfig{
			"harness-login": {Dialect: string(harness.DialectAnthropic), Auth: string(harness.AuthHarness)},
		},
		Models: []ModelConfig{
			{Provider: "anthropic", ID: "claude-opus-5", Gateway: "harness-login", Harness: string(harness.ClaudeCode), Preferences: []string{"best"}},
			{Provider: "anthropic", ID: "claude-sonnet-5", Gateway: "harness-login", Harness: string(harness.ClaudeCode), Preferences: []string{"balanced"}},
			{Provider: "anthropic", ID: "claude-haiku-4-5", Gateway: "harness-login", Harness: string(harness.ClaudeCode), Preferences: []string{"fast"}},
		},
	}
}

// ReadCatalogFile reads a declaration from disk. A missing file is not an
// error and yields the default declaration, so the common case needs no
// configuration. Any other read error is returned.
func ReadCatalogFile(path string) (CatalogFile, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultCatalogFile(), nil
	}
	//nolint:gosec // the path is the model catalog the operator named
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultCatalogFile(), nil
	}
	if err != nil {
		return CatalogFile{}, fmt.Errorf("read model catalog: %w", err)
	}
	var file CatalogFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return CatalogFile{}, fmt.Errorf("parse model catalog %s: %w", path, err)
	}
	return file, nil
}

// NewCatalog validates a declaration against the installed harnesses and
// resolves every credential. Validation happens here rather than at first use,
// so an operator learns that the catalog is wrong before a paper is fetched
// rather than from a gateway's 400 response partway through a run.
func NewCatalog(file CatalogFile, registry *harness.Registry, lookup LookupEnv) (*Catalog, error) {
	if lookup == nil {
		return nil, errors.New("the model catalog needs an environment reader; configuration arrives from the composition root")
	}
	catalog := &Catalog{gateways: make(map[string]harness.Gateway, len(file.Gateways))}
	for name, config := range file.Gateways {
		gateway, err := resolveGateway(name, config, lookup)
		if err != nil {
			return nil, err
		}
		catalog.gateways[name] = gateway
	}
	if len(file.Models) == 0 {
		return nil, fmt.Errorf("model catalog declares no models")
	}
	// A preference may resolve to at most one model per harness. Two "best"
	// rows is an error rather than an arbitrary pick, because a session is
	// recorded and priced on that choice being determinate.
	claimed := make(map[string]managed.ModelRef)
	for index, model := range file.Models {
		ref := managed.ModelRef{Provider: strings.TrimSpace(model.Provider), ID: strings.TrimSpace(model.ID)}
		if !ref.Valid() {
			return nil, fmt.Errorf("models[%d]: provider and id are both required", index)
		}
		gateway, ok := catalog.gateways[model.Gateway]
		if !ok {
			return nil, fmt.Errorf("models[%d] (%s/%s): no gateway named %q", index, ref.Provider, ref.ID, model.Gateway)
		}
		name := harness.Canonical(model.Harness)
		installed, err := registry.Lookup(string(name))
		if err != nil {
			return nil, fmt.Errorf("models[%d] (%s/%s): %w", index, ref.Provider, ref.ID, err)
		}
		if !harness.Speaks(installed, gateway.Dialect) {
			return nil, fmt.Errorf("models[%d] (%s/%s): harness %q speaks %v, gateway %q speaks %q",
				index, ref.Provider, ref.ID, name, installed.Dialects(), model.Gateway, gateway.Dialect)
		}
		for _, preference := range model.Preferences {
			preference = strings.ToLower(strings.TrimSpace(preference))
			if preference == "" {
				continue
			}
			key := string(name) + "/" + preference
			if previous, exists := claimed[key]; exists && previous != ref {
				return nil, fmt.Errorf("harness %q maps preference %q to two models: %s/%s and %s/%s",
					name, preference, previous.Provider, previous.ID, ref.Provider, ref.ID)
			}
			claimed[key] = ref
		}
		catalog.entries = append(catalog.entries, entry{ref: ref, gateway: model.Gateway, harness: name, preferences: model.Preferences})
	}
	return catalog, nil
}

// resolveGateway reads one gateway's credential out of the environment. A
// gateway naming a variable that is unset fails here, naming the variable,
// rather than producing a stage that cannot reach a model.
func resolveGateway(name string, config GatewayConfig, lookup LookupEnv) (harness.Gateway, error) {
	dialect := harness.Dialect(strings.TrimSpace(config.Dialect))
	switch dialect {
	case harness.DialectAnthropic, harness.DialectOpenAIResponses, harness.DialectOpenAIChat:
	default:
		return harness.Gateway{}, fmt.Errorf("gateway %q declares unknown dialect %q", name, config.Dialect)
	}
	gateway := harness.Gateway{Name: name, BaseURL: strings.TrimSpace(config.BaseURL), Dialect: dialect}
	auth := harness.AuthKind(strings.TrimSpace(config.Auth))
	if auth == "" {
		if strings.TrimSpace(config.KeyEnv) == "" {
			auth = harness.AuthNone
		} else {
			auth = harness.AuthAPIKey
		}
	}
	switch auth {
	case harness.AuthHarness, harness.AuthNone:
		if strings.TrimSpace(config.KeyEnv) != "" {
			return harness.Gateway{}, fmt.Errorf("gateway %q declares auth %q and a key_env; one of them is wrong", name, auth)
		}
	case harness.AuthAPIKey:
		variable := strings.TrimSpace(config.KeyEnv)
		if variable == "" {
			return harness.Gateway{}, fmt.Errorf("gateway %q declares an api key and names no key_env", name)
		}
		value, ok := lookup(variable)
		if !ok || strings.TrimSpace(value) == "" {
			return harness.Gateway{}, fmt.Errorf("gateway %q needs %s, which is not set", name, variable)
		}
		gateway.Key = value
	default:
		return harness.Gateway{}, fmt.Errorf("gateway %q declares unknown auth %q", name, config.Auth)
	}
	gateway.Auth = auth
	return gateway, nil
}

// Models is the catalog for one harness, in the shape the managed contract
// asks a runtime to report.
func (c *Catalog) Models(name harness.Name) []managed.Model {
	models := make([]managed.Model, 0, len(c.entries))
	for _, e := range c.entries {
		if e.harness != name {
			continue
		}
		models = append(models, managed.Model{Ref: e.ref, Preferences: e.preferences})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Ref.ID < models[j].Ref.ID })
	return models
}

// Credentials returns every resolved credential value, so the transcript
// writer can redact them. A harness that echoes its own environment into a
// tool result would otherwise put one into the published evidence.
func (c *Catalog) Credentials() []string {
	var values []string
	for _, gateway := range c.gateways {
		if strings.TrimSpace(gateway.Key) != "" {
			values = append(values, gateway.Key)
		}
	}
	sort.Strings(values)
	return values
}

// Resolve turns a selector into the exact model a session will run on and the
// gateway it is reached through. An exact reference must match a declared row:
// a model the operator never declared has no endpoint to reach it at.
func (c *Catalog) Resolve(name harness.Name, selector managed.ModelSelector) (managed.ModelRef, harness.Gateway, error) {
	if selector.Exact != nil {
		for _, e := range c.entries {
			if e.harness == name && e.ref == *selector.Exact {
				return e.ref, c.gateways[e.gateway], nil
			}
		}
		return managed.ModelRef{}, harness.Gateway{}, fmt.Errorf(
			"harness %q has no declared model %s/%s; add it to the model catalog",
			name, selector.Exact.Provider, selector.Exact.ID)
	}
	preference := strings.ToLower(strings.TrimSpace(selector.Preference))
	var match *entry
	for index, e := range c.entries {
		if e.harness != name {
			continue
		}
		for _, candidate := range e.preferences {
			if strings.ToLower(strings.TrimSpace(candidate)) != preference {
				continue
			}
			if match != nil && match.ref != e.ref {
				return managed.ModelRef{}, harness.Gateway{}, fmt.Errorf(
					"harness %q maps preference %q to multiple models", name, preference)
			}
			match = &c.entries[index]
		}
	}
	if match == nil {
		return managed.ModelRef{}, harness.Gateway{}, fmt.Errorf(
			"harness %q has no model for preference %q", name, preference)
	}
	return match.ref, c.gateways[match.gateway], nil
}
