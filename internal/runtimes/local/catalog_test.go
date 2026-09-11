// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
)

func testRegistry(t *testing.T) *harness.Registry {
	t.Helper()
	registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func env(pairs map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

func parse(t *testing.T, body string) CatalogFile {
	t.Helper()
	path := t.TempDir() + "/models.yaml"
	if err := writeFile(path, []byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	file, err := ReadCatalogFile(path)
	if err != nil {
		t.Fatalf("ReadCatalogFile: %v", err)
	}
	return file
}

const goodCatalog = `
gateways:
  anthropic:
    base_url: https://api.anthropic.com
    dialect: anthropic
    key_env: ANTHROPIC_API_KEY
  lux:
    base_url: https://lux.latere.ai/v1
    dialect: openai-responses
    key_env: LUX_API_KEY
  local:
    base_url: http://127.0.0.1:11434/v1
    dialect: openai-chat
models:
  - {provider: anthropic, id: claude-opus-5, gateway: anthropic, harness: claude-code, preferences: [best]}
  - {provider: anthropic, id: claude-sonnet-5, gateway: anthropic, harness: claude-code, preferences: [balanced, fast]}
  - {provider: qwen, id: qwen3-coder-480b, gateway: lux, harness: codex, preferences: [best, balanced]}
  - {provider: meta, id: llama-4-scout, gateway: local, harness: codex, preferences: [fast]}
`

func goodEnv() LookupEnv {
	return env(map[string]string{"ANTHROPIC_API_KEY": "sk-ant", "LUX_API_KEY": "sk-lux"})
}

// One harness's catalogue is the rows for that harness, and every tier
// resolves to exactly one qualified reference.
func TestCatalogResolvesEachTierOnce(t *testing.T) {
	catalog, err := NewCatalog(parse(t, goodCatalog), testRegistry(t), goodEnv())
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if models := catalog.Models(harness.ClaudeCode); len(models) != 2 {
		t.Fatalf("claude-code catalogue = %+v", models)
	}
	for _, c := range []struct{ preference, id string }{
		{"best", "claude-opus-5"}, {"balanced", "claude-sonnet-5"}, {"fast", "claude-sonnet-5"},
	} {
		ref, gateway, err := catalog.Resolve(harness.ClaudeCode, managed.ModelSelector{Preference: c.preference})
		if err != nil || ref.ID != c.id {
			t.Fatalf("Resolve(%q) = %+v, %v", c.preference, ref, err)
		}
		if gateway.Key != "sk-ant" || gateway.Auth != harness.AuthAPIKey {
			t.Errorf("gateway for %q = %+v", c.preference, gateway)
		}
	}
	// A gateway with no key_env is reached with no credential at all, which
	// is how a local server that wants none is used.
	_, gateway, err := catalog.Resolve(harness.Codex, managed.ModelSelector{Preference: "fast"})
	if err != nil || gateway.Auth != harness.AuthNone || gateway.Key != "" {
		t.Fatalf("keyless gateway = %+v, %v", gateway, err)
	}
}

// An exact reference must be declared: a model with no gateway has no
// endpoint to reach it at.
func TestCatalogRefusesAnUndeclaredExactModel(t *testing.T) {
	catalog, err := NewCatalog(parse(t, goodCatalog), testRegistry(t), goodEnv())
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	ref := managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"}
	if got, _, err := catalog.Resolve(harness.ClaudeCode, managed.ModelSelector{Exact: &ref}); err != nil || got != ref {
		t.Fatalf("declared exact model = %+v, %v", got, err)
	}
	undeclared := managed.ModelRef{Provider: "anthropic", ID: "claude-opus-9"}
	_, _, err = catalog.Resolve(harness.ClaudeCode, managed.ModelSelector{Exact: &undeclared})
	if err == nil || !strings.Contains(err.Error(), "model catalogue") {
		t.Fatalf("an undeclared model resolved: %v", err)
	}
	// The row belongs to another harness, so this harness cannot reach it.
	qwen := managed.ModelRef{Provider: "qwen", ID: "qwen3-coder-480b"}
	if _, _, err := catalog.Resolve(harness.ClaudeCode, managed.ModelSelector{Exact: &qwen}); err == nil {
		t.Fatal("a model declared for another harness resolved")
	}
}

// Every way a declaration can be wrong is refused at construction, naming both
// halves of the disagreement.
func TestCatalogRefusesABadDeclaration(t *testing.T) {
	cases := []struct {
		name string
		body string
		env  map[string]string
		want []string
	}{
		{"two best rows for one harness", `
gateways: {a: {dialect: anthropic, auth: harness}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
  - {provider: anthropic, id: claude-sonnet-5, gateway: a, harness: claude-code, preferences: [best]}
`, nil, []string{"claude-opus-5", "claude-sonnet-5", "best"}},
		{"harness and dialect disagree", `
gateways: {a: {base_url: "http://x/v1", dialect: openai-chat, auth: none}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
`, nil, []string{"claude-code", "openai-chat", "anthropic"}},
		{"unset credential", `
gateways: {a: {base_url: "https://x", dialect: anthropic, key_env: MISSING_KEY}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
`, nil, []string{"MISSING_KEY", "not set"}},
		{"unknown gateway", `
gateways: {a: {dialect: anthropic, auth: harness}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: elsewhere, harness: claude-code, preferences: [best]}
`, nil, []string{"elsewhere"}},
		{"unknown harness", `
gateways: {a: {dialect: anthropic, auth: harness}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: nope, preferences: [best]}
`, nil, []string{"nope", "claude-code, codex"}},
		{"a model with no provider", `
gateways: {a: {dialect: anthropic, auth: harness}}
models:
  - {id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
`, nil, []string{"provider and id"}},
		{"auth and key_env disagree", `
gateways: {a: {dialect: anthropic, auth: harness, key_env: SOME_KEY}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
`, map[string]string{"SOME_KEY": "x"}, []string{"one of them is wrong"}},
		{"unknown dialect", `
gateways: {a: {dialect: telepathy, auth: harness}}
models:
  - {provider: anthropic, id: claude-opus-5, gateway: a, harness: claude-code, preferences: [best]}
`, nil, []string{"telepathy"}},
		{"no models at all", "gateways: {a: {dialect: anthropic, auth: harness}}\n", nil, []string{"no models"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewCatalog(parse(t, c.body), testRegistry(t), env(c.env))
			if err == nil {
				t.Fatal("the declaration was accepted")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
		})
	}
}

// With no file written, the default declaration is the harness's own login —
// so a first run on a machine where the harness is already signed in needs no
// configuration and no API key.
// The default anthropic egress a gateway with no base URL falls back to is the
// model endpoint alone. The harness's telemetry service and error reporter are
// not opened — a reproduction does not phone home, and the harness is told not
// to reach them either.
func TestTheDefaultAnthropicEgressIsTheModelEndpointOnly(t *testing.T) {
	hosts := defaultVendorHosts(harness.DialectAnthropic)
	if len(hosts) != 1 || hosts[0] != "api.anthropic.com" {
		t.Fatalf("anthropic vendor hosts = %v, want the model endpoint alone", hosts)
	}
	for _, host := range hosts {
		if strings.Contains(host, "statsig") || host == "sentry.io" {
			t.Errorf("a telemetry host is still opened: %v", hosts)
		}
	}
}

func TestTheDefaultCatalogNeedsNoCredential(t *testing.T) {
	file, err := ReadCatalogFile(t.TempDir() + "/absent.yaml")
	if err != nil {
		t.Fatalf("ReadCatalogFile: %v", err)
	}
	catalog, err := NewCatalog(file, testRegistry(t), env(nil))
	if err != nil {
		t.Fatalf("the default catalogue does not validate: %v", err)
	}
	for _, preference := range []string{"best", "balanced", "fast"} {
		ref, gateway, err := catalog.Resolve(harness.ClaudeCode, managed.ModelSelector{Preference: preference})
		if err != nil || !ref.Valid() {
			t.Fatalf("Resolve(%q) = %+v, %v", preference, ref, err)
		}
		if gateway.Auth != harness.AuthHarness || gateway.Key != "" {
			t.Fatalf("the default gateway wants a credential: %+v", gateway)
		}
	}
	if len(catalog.Credentials()) != 0 {
		t.Error("the default catalogue holds a credential")
	}
}

// Every resolved credential is reported for redaction, because a harness that
// echoes its environment into a tool result would otherwise publish one.
func TestCatalogReportsItsCredentialsForRedaction(t *testing.T) {
	catalog, err := NewCatalog(parse(t, goodCatalog), testRegistry(t), goodEnv())
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	credentials := catalog.Credentials()
	if len(credentials) != 2 || credentials[0] != "sk-ant" || credentials[1] != "sk-lux" {
		t.Fatalf("credentials = %v", credentials)
	}
}

// A malformed file fails loudly; a missing one falls back to the default.
func TestAMalformedCatalogueFailsRatherThanDefaulting(t *testing.T) {
	if _, err := ReadCatalogFile(parseTempFile(t, "gateways: [this is not a map\n")); err == nil {
		t.Fatal("a malformed catalogue was accepted")
	}
}

func parseTempFile(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/models.yaml"
	if err := writeFile(path, []byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}
