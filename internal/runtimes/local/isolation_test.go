// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	goruntime "runtime"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/host"
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

// Under the real srt, a Codex stage on the operator's login keeps its
// temporary files in its own session, cannot write the directory srt shares
// between stages, and cannot read the shell snapshots in the operator's Codex
// directory, which hold every variable the operator's shell exports. The
// harness binary is a stand-in that reports what it can reach, and the home
// directory is a fake one holding a dummy snapshot, so no real login, model or
// credential is involved.
//
// It runs where srt is installed and is skipped elsewhere.
func TestACodexStageUnderSrtSharesNoTemporaryDirectoryAndReadsNoSnapshot(t *testing.T) {
	// Checked before anything is created, so a machine without srt is left
	// untouched. The driver's own preflight below also needs the stand-in.
	if _, err := exec.LookPath("srt"); err != nil {
		t.Skipf("the host sandbox is not installed here: %v", err)
	}
	// A unix socket path is limited to 104 bytes on macOS, and srt binds
	// its sockets under the stage's temporary directory, which is inside
	// the session. A root under the test's own temporary directory is too
	// long, so the sessions live under a short one.
	root, err := os.MkdirTemp("/tmp", "rh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	snapshots := filepath.Join(home, ".codex", "shell_snapshots")
	if err := os.MkdirAll(snapshots, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "dummy-snapshot-value"
	if err := os.WriteFile(filepath.Join(snapshots, "operator.sh"), []byte("export DUMMY_SECRET="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The stand-in for codex, first on the PATH the stage inherits.
	probe := filepath.Join(local.SharedTempDir, filepath.Base(root))
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// The runtime asks the harness for its version outside the sandbox,
	// so the stand-in answers that and does nothing else.
	script := "#!/bin/sh\n" +
		"[ \"$1\" = --version ] && echo 'codex-cli 0.0.0' && exit 0\n" +
		"echo \"args: $*\"\n" +
		"echo \"tmpdir: $TMPDIR\"\n" +
		"cat \"$HOME/.codex/shell_snapshots/operator.sh\" 2>/dev/null || echo 'snapshot: denied'\n" +
		"mkdir -p '" + probe + "' 2>/dev/null && echo 'shared: writable' || echo 'shared: denied'\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, sharedExisted := os.Stat(local.SharedTempDir)
	t.Cleanup(func() {
		_ = os.Remove(probe)
		if errors.Is(sharedExisted, os.ErrNotExist) {
			_ = os.Remove(local.SharedTempDir)
		}
	})

	driver := host.New(host.Config{Home: home, HarnessBinary: "codex", Lookup: os.LookupEnv})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Skipf("the host sandbox is not installed here:\n%v", err)
	}
	registry, err := harness.NewRegistry(harness.NewCodex())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := local.NewCatalog(codexLoginCatalog(), registry, noCredentials)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewCodex(), Catalog: catalog,
		Root: filepath.Join(root, "sessions"), Lookup: os.LookupEnv,
		Machine: harness.Machine{Platform: goruntime.GOOS, Home: home, TempDir: "/tmp", UID: os.Getuid()},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: "Follow the method.",
		Task:         "Reproduce the result.",
		Sandbox:      managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkNone}},
	}
	ref := start(t, runtime, session)
	var status managed.Status
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if status, err = runtime.Poll(context.Background(), ref); err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if ref = status.Ref; status.State.Terminal() {
			break
		}
	}
	if !status.State.Terminal() {
		t.Fatalf("the session never finished: %+v", status)
	}
	events, err := runtime.Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, event := range events {
		lines = append(lines, event.Content)
	}
	output := strings.Join(lines, "\n")

	dir := sessionDir(t, ref)
	if !strings.Contains(output, "tmpdir: "+dir+string(filepath.Separator)) {
		t.Errorf("the stage's temporary directory is not inside its session %s:\n%s", dir, output)
	}
	if strings.Contains(output, "shared: writable") || !strings.Contains(output, "shared: denied") {
		t.Errorf("the stage could write the shared temporary directory:\n%s", output)
	}
	if strings.Contains(output, secret) || !strings.Contains(output, "snapshot: denied") {
		t.Errorf("the stage read the operator's shell snapshot:\n%s", output)
	}
	if !strings.Contains(output, harness.CodexNoShellSnapshot) {
		t.Errorf("codex was not told to take no shell snapshot:\n%s", output)
	}
}
