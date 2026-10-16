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
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/publish"
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

// A hosted session's work is pushed as it runs: each snapshot that changed
// something lands on the remote as a commit, and the final handover commits
// on top of the same history, so every push is a fast-forward.
func TestAHostedSessionsWorkIsPushedAsItRuns(t *testing.T) {
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "--initial-branch=main", remote).CombinedOutput(); err != nil {
		t.Fatalf("init the remote: %v: %s", err, out)
	}
	files := map[string][]byte{"notes.md": []byte("reading\n")}
	runtime := managedmemory.NewWithStore(managedmemory.NewStore(), managedmemory.Config{LiveArtifacts: true, PollsBeforeDone: 4, Files: files})
	router, err := runtimes.NewRouter(runtime.Name(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	controller := managed.NewController(router, managed.WithWaitPolicy(managed.WaitPolicy{
		MaxTemporaryErrors: 2, MaxSilence: time.Second, CleanupTimeout: time.Second, SnapshotEvery: time.Nanosecond,
	}))
	dir := filepath.Join(t.TempDir(), "workspace")
	push := publish.LocalPush{RemoteURL: remote}
	remoteLog := func() string {
		out, _ := exec.Command("git", "-C", remote, "log", "--format=%s", "main").CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	pushes := 0
	hooks := managed.Hooks{
		Snapshot: func(ctx context.Context, evidence managed.EvidenceReader) error {
			pushed, err := snapshotWorkspace(ctx, runtime, evidence, dir, push)
			if pushed {
				pushes++
				files["src/run.py"] = []byte("print(5)\n") // the session goes on working
			}
			return err
		},
		Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
			files["results.md"] = []byte("5\n")
			// Retain reads the listing it was handed; read it again so the
			// last change counts, as a real runtime's final listing would.
			artifacts, err := runtime.ListArtifacts(ctx, evidence.Status.Ref)
			if err != nil {
				return err
			}
			evidence.Artifacts = artifacts
			if _, err := hostWorkspace(ctx, runtime, evidence, dir); err != nil {
				return err
			}
			push.Workspace = dir
			_, err = push.Run(ctx)
			return err
		},
	}
	spec := managed.SessionSpec{
		Model: managed.ModelSelector{Preference: "best"}, Instructions: "i", Task: "t",
		Sandbox: managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkNone}},
	}
	if _, err := controller.Execute(ctx, spec, hooks); err != nil {
		t.Fatal(err)
	}
	if pushes != 2 {
		t.Fatalf("%d snapshots pushed, want 2: one per change", pushes)
	}
	log := strings.Split(remoteLog(), "\n")
	if len(log) != 3 || !strings.Contains(log[0], "as it left its working directory") || !strings.Contains(log[2], "so far") {
		t.Fatalf("the remote's history is %q", log)
	}
	tree, _ := exec.Command("git", "-C", remote, "ls-tree", "-r", "--name-only", "main").CombinedOutput()
	if strings.Fields(string(tree))[0] != "notes.md" || !strings.Contains(string(tree), "results.md") || !strings.Contains(string(tree), "src/run.py") {
		t.Fatalf("the remote's tree is %q", tree)
	}
}

// A run that did not succeed names how it ended, beside its verdict.
func TestAFailedRunNamesItsReason(t *testing.T) {
	for status, want := range map[*managed.Status]string{
		{TerminalReason: managed.ReasonSucceeded}: "",
		{}: "",
		{TerminalReason: managed.ReasonCancelled}:                                                   "cancelled",
		{TerminalReason: managed.ReasonRuntimeFailed, Error: "the grader did not return a verdict"}: "runtime_failed: the grader did not return a verdict",
	} {
		if got := outcomeLine(*status); got != want {
			t.Errorf("outcomeLine(%+v) = %q, want %q", *status, got, want)
		}
	}
}
