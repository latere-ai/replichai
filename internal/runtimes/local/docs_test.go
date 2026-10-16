// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"os"
	"strings"
	"testing"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
)

// docGuide is the user-facing guide to running audits locally. Its model
// catalog is read from the document rather than copied here, so an example
// that stops working fails this test rather than misleading a reader.
const docGuide = "../../../docs/local-runs.md"

// The catalog the guide shows an operator validates, and resolves to the
// models the guide says it resolves to.
func TestTheDocumentedCatalogWorks(t *testing.T) {
	body, err := os.ReadFile(docGuide)
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}
	yaml := yamlBlockAfter(t, string(body), "## Choosing a model")

	path := t.TempDir() + "/models.yaml"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	file, err := local.ReadCatalogFile(path)
	if err != nil {
		t.Fatalf("the documented catalog does not parse: %v", err)
	}
	registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	// The two credentials the example names are set; the local gateway names
	// none, which is the case the example exists to show.
	catalog, err := local.NewCatalog(file, registry, func(name string) (string, bool) {
		return "a-credential", name == "ANTHROPIC_API_KEY" || name == "VLLM_API_KEY"
	})
	if err != nil {
		t.Fatalf("the documented catalog does not validate: %v", err)
	}

	for _, c := range []struct {
		harness  harness.Name
		pref, id string
		auth     harness.AuthKind
	}{
		// The guide says a first run uses the login already on the
		// machine, and that an open-weights model behind an
		// OpenAI-compatible gateway pairs with codex.
		{harness.ClaudeCode, "best", "claude-opus-5", harness.AuthHarness},
		{harness.Codex, "best", "qwen3-coder-480b", harness.AuthAPIKey},
		{harness.Codex, "fast", "llama-4-scout", harness.AuthNone},
	} {
		ref, gateway, err := catalog.Resolve(c.harness, managed.ModelSelector{Preference: c.pref})
		if err != nil {
			t.Fatalf("%s/%s: %v", c.harness, c.pref, err)
		}
		if ref.ID != c.id || gateway.Auth != c.auth {
			t.Errorf("%s/%s resolved to %s via %s, want %s via %s",
				c.harness, c.pref, ref.ID, gateway.Auth, c.id, c.auth)
		}
	}
}

// Every flag the guide's reference table names is a flag the CLI has, so the
// table cannot list options that do not exist.
func TestTheDocumentedFlagsExist(t *testing.T) {
	body, err := os.ReadFile(docGuide)
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}
	// The CLI's own help is the authority; the guide is checked against it.
	help, err := os.ReadFile("../../../cmd/replichai/runtime.go")
	if err != nil {
		t.Fatalf("read the flag definitions: %v", err)
	}
	for _, flag := range []string{"sandbox", "harness", "models", "session-root", "retention", "engine", "image"} {
		if !strings.Contains(string(help), `"`+flag+`"`) {
			t.Errorf("the guide documents --%s and the CLI does not define it", flag)
		}
		if !strings.Contains(string(body), "`--"+flag+"`") {
			t.Errorf("--%s is defined and the guide does not document it", flag)
		}
	}
	for _, variable := range []string{
		"REPLICHAI_SANDBOX", "REPLICHAI_HARNESS", "REPLICHAI_MODELS_FILE",
		"REPLICHAI_SESSION_ROOT", "REPLICHAI_SESSION_RETENTION",
		"REPLICHAI_CONTAINER_ENGINE", "REPLICHAI_HARNESS_IMAGE",
	} {
		if !strings.Contains(string(help), variable) {
			t.Errorf("the guide names %s and nothing reads it", variable)
		}
		if !strings.Contains(string(body), variable) {
			t.Errorf("%s is read and the guide does not name it", variable)
		}
	}
}

// yamlBlockAfter is the first fenced yaml block following a heading.
func yamlBlockAfter(t *testing.T, document, heading string) string {
	t.Helper()
	index := strings.Index(document, heading)
	if index < 0 {
		t.Fatalf("the guide has no %q section", heading)
	}
	rest := document[index:]
	open := strings.Index(rest, "```yaml\n")
	if open < 0 {
		t.Fatalf("no yaml example under %q", heading)
	}
	rest = rest[open+len("```yaml\n"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("an unterminated yaml example under %q", heading)
	}
	return rest[:end]
}
