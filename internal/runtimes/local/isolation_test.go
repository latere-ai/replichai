// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
)

// codexLoginCatalog declares one Codex model on the operator's own Codex
// login.
func codexLoginCatalog() local.CatalogFile {
	return local.CatalogFile{
		Gateways: map[string]local.GatewayConfig{
			"login": {Dialect: string(harness.DialectOpenAIResponses), Auth: string(harness.AuthHarness)},
		},
		Models: []local.ModelConfig{{
			Provider: "vendor", ID: "gpt-5-codex", Gateway: "login",
			Harness: string(harness.Codex), Preferences: []string{"best"},
		}},
	}
}

// Every stage of every harness, the bootstrap and the judge included, is told
// to keep its temporary files in its own session and is denied srt's shared
// directory. srt hands a stage the shared directory unless CLAUDE_CODE_TMPDIR
// names another, and only the Claude Code adapter used to name one, so every
// Codex run on a machine worked in the same folder and found what earlier runs
// had downloaded and built there.
func TestEveryStageKeepsItsTemporaryFilesInItsOwnSession(t *testing.T) {
	machine := harness.Machine{Platform: "darwin", Home: "/Users/x", TempDir: "/tmp", UID: 501}
	for name, test := range map[string]struct {
		agent harness.Harness
		file  local.CatalogFile
	}{
		"claude code": {harness.NewClaudeCode(), local.DefaultCatalogFile()},
		"codex":       {harness.NewCodex(), codexLoginCatalog()},
	} {
		t.Run(name, func(t *testing.T) {
			driver := localtest.New(localtest.Script{})
			registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
			if err != nil {
				t.Fatalf("NewRegistry: %v", err)
			}
			catalog, err := local.NewCatalog(test.file, registry, noCredentials)
			if err != nil {
				t.Fatalf("NewCatalog: %v", err)
			}
			runtime, err := local.New(local.Config{
				Sandbox: driver, Harness: test.agent, Catalog: catalog,
				Root: t.TempDir(), Lookup: os.LookupEnv, Machine: machine,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
			dir := sessionDir(t, status.Ref)
			stages := driver.Stages()
			if len(stages) < 3 {
				t.Fatalf("want a bootstrap, a worker and a judge, got %v", names(stages))
			}
			for _, stage := range stages {
				tmp := stage.Env["CLAUDE_CODE_TMPDIR"]
				if !strings.HasPrefix(tmp, dir+string(filepath.Separator)) {
					t.Errorf("stage %s is handed the temporary directory %q, outside its session %s", stage.Name, tmp, dir)
				}
				for _, shared := range []string{local.SharedTempDir, "/private" + local.SharedTempDir} {
					if !slices.Contains(stage.DeniedWrites, shared) {
						t.Errorf("stage %s may write the shared temporary directory %s: %v", stage.Name, shared, stage.DeniedWrites)
					}
				}
			}
		})
	}
}
