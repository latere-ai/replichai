// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	goruntime "runtime"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/host"
)

// One real session: srt confining a real Claude Code, from bootstrap through
// worker to judge, on this machine.
//
// It uses the login the operator already has, with no API key and nothing
// configured. That is the case the host sandbox exists to serve and the one
// most likely to break unnoticed, so it is the one driven against the real
// harness.
//
// It is opt-in, because it launches a real, paid, minutes-long agent session:
// it runs only with REPLICHAI_E2E=1 in the environment, and is skipped
// otherwise. To run it:
//
//	REPLICHAI_E2E=1 go test ./internal/runtimes/local/ -run TestRealEndToEnd -v
func TestRealEndToEnd(t *testing.T) {
	runRealSession(t, harness.NewClaudeCode(), "claude")
}

// The same session, driven by the other harness. Codex keeps its login in a
// file rather than a keychain and speaks a different stream format, so the two
// tests exercise different parts of the seam rather than being one test with a
// parameter changed.
//
// Opt-in on the same REPLICHAI_E2E=1 as TestRealEndToEnd, and paid the same
// way. To run it:
//
//	REPLICHAI_E2E=1 go test ./internal/runtimes/local/ -run TestRealEndToEndWithCodex -v
func TestRealEndToEndWithCodex(t *testing.T) {
	runRealSession(t, harness.NewCodex(), "codex")
}

func runRealSession(t *testing.T, agent harness.Harness, binary string) {
	t.Helper()
	// A real agent session is paid and takes minutes, so it does not run as
	// part of an ordinary `go test`: it is asked for explicitly.
	if os.Getenv("REPLICHAI_E2E") != "1" {
		t.Skip("set REPLICHAI_E2E=1 to run the real, paid end-to-end session")
	}
	home, _ := os.UserHomeDir()
	driver := host.New(host.Config{Home: home, HarnessBinary: binary, Lookup: os.LookupEnv})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Skipf("not ready: %v", err)
	}
	registry, _ := harness.NewRegistry(agent)
	// The harness's own login, which is what an operator gets with no
	// configuration at all.
	catalog, err := local.NewCatalog(local.CatalogFile{
		Gateways: map[string]local.GatewayConfig{
			"harness-login": {Dialect: dialectOf(agent), Auth: "harness"},
		},
		Models: []local.ModelConfig{{
			Provider: "vendor", ID: defaultModelOf(agent), Gateway: "harness-login",
			Harness: string(agent.Name()), Preferences: []string{"balanced"},
		}},
	}, registry, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	// A temporary directory the test framework cleans up, rather than a
	// fixed /tmp path where os.RemoveAll would remove whatever it found.
	root := t.TempDir()
	t.Logf("session root: %s", root)
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: agent, Catalog: catalog,
		Root: root, Lookup: os.LookupEnv,
		Machine: harness.Machine{
			Platform: goruntime.GOOS, Home: home, TempDir: "/tmp", UID: os.Getuid(),
		},
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
			// What the audit asks for, resolved against what this driver
			// can enforce.
			Network: managed.NetworkPolicy{
				Mode: managed.NetworkAllowlist, AllowedDomains: audit.ArtifactDomains(),
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

// dialectOf is the wire protocol a harness speaks, for a catalogue built here.
func dialectOf(agent harness.Harness) string { return string(agent.Dialects()[0]) }

// defaultModelOf is a model each harness will accept without configuration.
func defaultModelOf(agent harness.Harness) string {
	if agent.Name() == harness.Codex {
		return "gpt-5.6-sol"
	}
	return "claude-sonnet-5"
}
