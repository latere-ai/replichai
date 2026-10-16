// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestUsageListsEveryRegisteredCommand checks that the usage text is generated
// from the registry: every subcommand appears in it with its own synopsis and
// summary, and every subcommand that appears is one dispatch knows.
func TestUsageListsEveryRegisteredCommand(t *testing.T) {
	stdout, _, err := capture(t, "help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, c := range commands() {
		line := "replichai " + c.name
		if c.synopsis != "" {
			line += " " + c.synopsis
		}
		if !strings.Contains(stdout, line) {
			t.Errorf("usage does not show %q:\n%s", line, stdout)
		}
		if !strings.Contains(stdout, c.summary) {
			t.Errorf("usage does not say what %s does (%q)", c.name, c.summary)
		}
		if _, _, err := capture(t, c.name, "-h"); err != nil {
			t.Errorf("%s is listed but not dispatchable: %v", c.name, err)
		}
	}
	for _, c := range commands() {
		if _, ok := lookup(c.name); !ok {
			t.Errorf("lookup(%q) failed for a registered command", c.name)
		}
	}
}

// TestEveryCommandPrintsItsOwnFlags checks that `replichai <command> -h`
// prints the command's own flags from its own flag set, exits cleanly, and
// names the command's synopsis. The flags asserted here are the ones the old
// hand-written help had drifted from: it omitted them, or showed a legacy one
// as canonical.
func TestEveryCommandPrintsItsOwnFlags(t *testing.T) {
	expected := map[string][]string{
		"audit":     {"-out", "-runtime", "-method", "-interval", "-timeout", "-max-revisions", "-publish", "-owner", "-invite", "-project", "-detach", "-model", "-sandbox", "-harness", "-engine", "-image", "-models", "-session-root", "-retention"},
		"run":       {"-out", "-runtime", "-method", "-parallel", "-interval", "-timeout", "-max-revisions", "-publish", "-owner", "-invite", "-resume", "-sandbox", "-harness", "-engine", "-image", "-models", "-session-root", "-retention"},
		"resolve":   {"-timeout"},
		"publish":   {"-out", "-live", "-verdict", "-method", "-timeout"},
		"finish":    {"-session", "-session-ref", "-runtime", "-method", "-invite", "-verdict", "-interval", "-timeout", "-sandbox", "-harness"},
		"login":     {"-auth-url", "-no-browser"},
		"whoami":    nil,
		"logout":    nil,
		"org":       {"-personal"},
		"sessions":  {"-session-root", "-older-than", "-dry-run"},
		"project":   nil,
		"push":      {"-project"},
		"providers": nil,
		"models":    {"-runtime", "-sandbox", "-harness", "-engine", "-image", "-models", "-session-root", "-retention"},
		"rates":     {"-runtime", "-database-url", "-json"},
	}
	for _, c := range commands() {
		flags, known := expected[c.name]
		if !known {
			t.Errorf("%s is registered but this test does not know its flags; add it", c.name)
			continue
		}
		_, stderr, err := capture(t, c.name, "-h")
		if err != nil {
			t.Errorf("%s -h: %v", c.name, err)
		}
		if !strings.Contains(stderr, "usage: replichai "+c.name) {
			t.Errorf("%s -h does not print its usage line:\n%s", c.name, stderr)
		}
		for _, flag := range flags {
			if !strings.Contains(stderr, "\n  "+flag+" ") && !strings.Contains(stderr, "\n  "+flag+"\n") {
				t.Errorf("%s -h does not list %s:\n%s", c.name, flag, stderr)
			}
		}
		if flags == nil && strings.Contains(stderr, "flags:") {
			t.Errorf("%s -h lists flags it does not have:\n%s", c.name, stderr)
		}
	}
	// A subcommand's -h reaches its flags wherever it is typed, and the
	// runtime flag says where its value comes from.
	_, stderr, err := capture(t, "audit", "some-paper", "-h")
	if err != nil || !strings.Contains(stderr, "REPLICHAI_RUNTIME") {
		t.Errorf("audit <paper> -h = %v:\n%s", err, stderr)
	}
}

// TestCommandsWithoutArgumentsRefuseThem checks that a subcommand that takes no
// arguments refuses them rather than ignoring them, because an argument to
// `whoami` is most likely a typo.
func TestCommandsWithoutArgumentsRefuseThem(t *testing.T) {
	for _, name := range []string{"whoami", "logout", "providers", "rates"} {
		_, stderr, err := capture(t, name, "stray")
		if err == nil || !strings.Contains(stderr, "takes no arguments") {
			t.Errorf("%s stray = %v, %q; want a refusal", name, err, stderr)
		}
	}
}

// models lists each Effort level beside the preference it is stored as and
// the model it runs, so a person can see what --model accepts.
func TestModelsListsTheEffortLevels(t *testing.T) {
	stdout, _, err := capture(t, "models", "--runtime", "memory")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sustained  (best)", "thorough   (balanced)", "quick      (fast)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("models output lacks %q:\n%s", want, stdout)
		}
	}
	if _, _, err := capture(t, "models", "--runtime", "nonesuch"); err == nil {
		t.Error("an unknown runtime listed models")
	}
	if _, _, err := capture(t, "models", "extra"); err == nil {
		t.Error("a positional argument was accepted")
	}
}
