// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/runtimes"
)

// A hosted runtime that mounted nothing keeps the session's files on its own
// side. The host fetches them while the session is still held, and commits
// them once into a repository it can push, as it pushes a local session's.
func TestTheHostBuildsTheWorkspaceOfAHostedSession(t *testing.T) {
	runtime := managedmemory.NewWithStore(managedmemory.NewStore(), managedmemory.Config{
		Files: map[string][]byte{"README.md": []byte("# r\n"), "src/run.py": []byte("print(5)\n")},
	})
	router, err := runtimes.NewRouter(runtime.Name(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "workspace")
	var built string
	hooks := managed.Hooks{Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
		var err error
		built, err = hostWorkspace(ctx, runtime, evidence, dir)
		return err
	}}
	spec := managed.SessionSpec{
		Model: managed.ModelSelector{Preference: "best"}, Instructions: "i", Task: "t",
		Sandbox: managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkNone}},
	}
	if _, err := managed.NewController(router).Execute(context.Background(), spec, hooks); err != nil {
		t.Fatal(err)
	}
	if built != dir {
		t.Fatalf("workspace = %q", built)
	}
	body, err := os.ReadFile(filepath.Join(dir, "src", "run.py"))
	if err != nil || string(body) != "print(5)\n" {
		t.Fatalf("src/run.py = %q, %v", body, err)
	}
	log, err := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
	if err != nil || !strings.Contains(string(log), "the work of memory session") {
		t.Fatalf("log = %q, %v", log, err)
	}
}
