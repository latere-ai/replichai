package local_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/host"
)

// One real session, against the real thing: srt confining a real Claude Code,
// bootstrap through worker through judge, on this machine.
//
// It needs a credential the sandbox can carry. The harness's own login is not
// one on macOS — it lives in the login Keychain, and opening that to a session
// would open every credential in it — so this test runs where a key is
// supplied and says why where none is.
func TestRealEndToEnd(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("set ANTHROPIC_API_KEY to run one real session end to end")
	}
	home, _ := os.UserHomeDir()
	driver := host.New(host.Config{
		Home: home, HarnessBinary: "claude", Environ: os.Environ(),
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")},
	})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Skipf("not ready: %v", err)
	}
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	// A gateway the sandbox can carry: the key travels in the stage's
	// environment, which srt passes through untouched.
	catalog, err := local.NewCatalog(local.CatalogFile{
		Gateways: map[string]local.GatewayConfig{
			"anthropic": {BaseURL: "https://api.anthropic.com", Dialect: "anthropic", KeyEnv: "ANTHROPIC_API_KEY"},
		},
		Models: []local.ModelConfig{{
			Provider: "anthropic", ID: "claude-sonnet-5", Gateway: "anthropic",
			Harness: "claude-code", Preferences: []string{"balanced"},
		}},
	}, registry, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Logf("session root: %s", root)
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: root, Environ: os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("workspace: %s", capabilities.OutputDir)

	spec := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "balanced"},
		Instructions: "You are terse. Do exactly what the task says and stop.",
		Task:         "Write a file called hello.txt in " + capabilities.OutputDir + " containing exactly the word ok. Then commit it with git. Then say done.",
		Sandbox: managed.SandboxSpec{
			Network: managed.NetworkPolicy{
				Mode:           managed.NetworkAllowlist,
				AllowedDomains: []string{"api.anthropic.com", "statsig.anthropic.com", "sentry.io"},
			},
		},
		Evaluation: &managed.EvaluationSpec{
			Rubric:       "1. **File.** hello.txt exists in the graded directory and contains the word ok.",
			MaxRevisions: 1,
		},
	}
	if err := runtime.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ref, err := runtime.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(6 * time.Minute)
	var status managed.Status
	for time.Now().Before(deadline) {
		status, err = runtime.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		ref = status.Ref
		if status.State.Terminal() {
			break
		}
		time.Sleep(3 * time.Second)
	}
	t.Logf("state=%v reason=%q err=%q", status.State, status.TerminalReason, status.Error)
	t.Logf("model=%+v usage: fresh=%d cached=%d out=%d", status.Model,
		status.Usage.InputFresh, status.Usage.InputCached, status.Usage.Output)
	if status.Verdict != nil {
		t.Logf("verdict=%v: %s", status.Verdict.Grade, strings.SplitN(status.Verdict.Explanation, "\n", 2)[0])
	}
	events, _ := runtime.Events(context.Background(), ref)
	t.Logf("%d transcript events", len(events))
	for _, e := range events {
		if e.Kind == managed.KindMessage || e.Kind == managed.KindToolUse {
			t.Logf("  %-10s %-10s %.70q", e.Role, e.Kind, e.Content)
		}
	}
	artifacts, _ := runtime.ListArtifacts(context.Background(), ref)
	for _, a := range artifacts {
		t.Logf("  artifact: %s", a.Path)
	}
	if status.State != managed.StateDone {
		t.Fatalf("the session did not finish: %v %q", status.State, status.Error)
	}
}
