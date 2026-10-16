// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package builtin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

func TestCatalogComposesAdaptersExplicitly(t *testing.T) {
	store := memory.NewStore()
	catalog, err := Catalog(Config{MemoryStore: store, Anthropic: AnthropicConfig{APIKey: "test-key", BaseURL: "https://example.invalid"}})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if got := catalog.Names(); strings.Join(namesOf(got), ",") != "anthropic-managed,latere,local,memory" {
		t.Fatalf("Names = %v", got)
	}
	if runtime, err := catalog.Build(context.Background(), "memory"); err != nil || runtime.Name() != "memory" {
		t.Fatalf("Build(memory) = %v, %v", runtime, err)
	}
	if runtime, err := catalog.Build(context.Background(), string(AnthropicManaged)); err != nil || runtime.Name() != AnthropicManaged {
		t.Fatalf("Build(%s) = %v, %v", AnthropicManaged, runtime, err)
	}
	// The runtime answers only to its own name. "anthropic" named the vendor,
	// not the runtime; an alias that conflates the two would leave a second
	// runtime for the same vendor without a name.
	if _, err := catalog.Build(context.Background(), "anthropic"); err == nil {
		t.Fatal("the removed vendor alias still resolves")
	}
}

func namesOf(names []managed.RuntimeName) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = string(name)
	}
	return out
}

// The Latere runtime is composed with the person it acts as on the command
// line: a personal access token, exchanged at Auth, or a command that prints
// a bearer. With neither it still builds, for a server that names the person
// per session, and a call with no person is refused with what to set.
func TestLatereIsComposedWithThePersonItActsAs(t *testing.T) {
	var exchanged string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, key, _ := r.BasicAuth()
		exchanged = key
		_, _ = w.Write([]byte(`{"access_token":"from-pat","expires_in":900}`))
	}))
	defer auth.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer from-pat" && r.Header.Get("Authorization") != "Bearer from-command" && r.Header.Get("Authorization") != "Bearer from-context" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"network":{"mode":"open"}}`))
	}))
	defer api.Close()
	for name, config := range map[string]LatereConfig{
		"a personal access token": {BaseURL: api.URL, AuthURL: auth.URL, APIKey: "pat_secret"},
		"a token command":         {BaseURL: api.URL, TokenCommand: "printf from-command"},
	} {
		catalog, err := Catalog(Config{Latere: config})
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := catalog.Build(context.Background(), "latere")
		if err != nil {
			t.Fatalf("%s: Build = %v", name, err)
		}
		if capabilities, err := runtime.Capabilities(context.Background()); err != nil || !capabilities.NetworkModes[managed.NetworkOpen] {
			t.Fatalf("%s: Capabilities = %+v, %v", name, capabilities, err)
		}
	}
	if exchanged != "pat_secret" {
		t.Fatalf("Auth was asked with %q", exchanged)
	}
	catalog, err := Catalog(Config{Latere: LatereConfig{BaseURL: api.URL}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := catalog.Build(context.Background(), "latere")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Capabilities(context.Background()); err == nil || !strings.Contains(err.Error(), "LATERE_API_KEY") {
		t.Fatalf("a call with no person = %v", err)
	}
	ctx := WithLatereSignIn(context.Background(), func(context.Context) (string, error) { return "from-context", nil })
	if _, err := runtime.Capabilities(ctx); err != nil {
		t.Fatalf("a call with the person on its context = %v", err)
	}
}

// The local runtime is composed from a sandbox driver, a harness and the
// operator's model catalog. A machine that cannot run the chosen sandbox is
// refused at composition, before a paper is fetched, with instructions for
// fixing it.
func TestLocalIsComposedAndRefusesAnUnusableMachine(t *testing.T) {
	catalog, err := Catalog(Config{Local: LocalConfig{
		Sandbox: sandbox.Container, Engine: "docker", Image: "registry.example/replichai-harness:test",
		Root: t.TempDir(), Lookup: func(string) (string, bool) { return "", false },
	}})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	// Docker is named explicitly, so a machine without it is refused rather
	// than falling back to podman.
	if _, err := catalog.Build(context.Background(), "local"); err != nil {
		if !errors.Is(err, hostsandbox.ErrNotReady) {
			t.Fatalf("Build(local) = %v", err)
		}
		if !strings.Contains(err.Error(), "--sandbox host") {
			t.Errorf("the refusal does not offer the other driver: %v", err)
		}
	}
	// An unknown sandbox names the ones that exist.
	catalog, _ = Catalog(Config{Local: LocalConfig{Sandbox: "vm", Root: t.TempDir(), Lookup: os.LookupEnv}})
	_, err = catalog.Build(context.Background(), "local")
	if err == nil || !strings.Contains(err.Error(), "host, container") {
		t.Fatalf("an unknown sandbox = %v", err)
	}
	// An unknown harness lists the installed ones.
	catalog, _ = Catalog(Config{Local: LocalConfig{Harness: "nope", Root: t.TempDir(), Lookup: os.LookupEnv}})
	_, err = catalog.Build(context.Background(), "local")
	if err == nil || !strings.Contains(err.Error(), "claude-code, codex") {
		t.Fatalf("an unknown harness = %v", err)
	}
}

func TestAnthropicRequiresExplicitCredential(t *testing.T) {
	catalog, _ := Catalog(Config{})
	if _, err := catalog.Build(context.Background(), "anthropic-managed"); err == nil {
		t.Fatal("Anthropic runtime accepted no explicit credential")
	}
}

// The host sandbox needs no image and no engine, so the local runtime composes
// over it on any machine; a models file that does not read, or declares what
// the installed harnesses cannot run, is refused at composition.
func TestLocalComposesOverTheHostSandboxAndRefusesABadModelsFile(t *testing.T) {
	config := LocalConfig{Sandbox: hostsandbox.Host, Root: t.TempDir(), Home: t.TempDir(), Lookup: os.LookupEnv}
	catalog, _ := Catalog(Config{Local: config})
	if runtime, err := catalog.Build(context.Background(), "local"); err != nil || runtime.Name() != "local" {
		t.Fatalf("Build(local) over the host sandbox = %v, %v", runtime, err)
	}
	for name, body := range map[string]string{
		"unparsable":      "models: [",
		"undeclared gate": "models:\n  - provider: anthropic\n    id: m\n    gateway: nowhere\n    harness: claude-code\n",
	} {
		path := t.TempDir() + "/models.yaml"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		bad := config
		bad.ModelsFile = path
		catalog, _ := Catalog(Config{Local: bad})
		if _, err := catalog.Build(context.Background(), "local"); err == nil {
			t.Errorf("a %s models file was accepted", name)
		}
	}
}

// The in-process runtime takes inputs, as a study's runs hand them, so the
// command that runs a study can run on it.
func TestTheMemoryRuntimeTakesInputs(t *testing.T) {
	catalog, _ := Catalog(Config{})
	runtime, err := catalog.Build(context.Background(), "memory")
	if err != nil {
		t.Fatalf("Build(memory): %v", err)
	}
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil || capabilities.InputDir != memory.InputDir {
		t.Fatalf("capabilities = %+v, %v", capabilities, err)
	}
}
