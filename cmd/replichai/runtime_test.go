// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// TestTheRuntimeResolvesInOneOrder checks the resolution order README.md
// documents: "REPLICHAI_RUNTIME selects the managed execution runtime, and
// --runtime overrides it per command." Before this rule existed, only the
// server read the variable; the CLI hard-coded its default, so the documented
// behaviour was false for every subcommand.
func TestTheRuntimeResolvesInOneOrder(t *testing.T) {
	env := func(value string) func(string) string {
		return func(key string) string {
			if key == runtimeEnv {
				return value
			}
			return ""
		}
	}
	cases := []struct {
		name, flag, env, fallback, want string
	}{
		{"the flag beats the environment", "memory", "local", "anthropic-managed", "memory"},
		{"the environment beats the default", "", "local", "anthropic-managed", "local"},
		{"nothing set is the subcommand's default", "", "", "anthropic-managed", "anthropic-managed"},
		{"each subcommand keeps its own default", "", "", "local", "local"},
		{"whitespace is unset", " ", " ", "memory", "memory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRuntime(tc.flag, env(tc.env), tc.fallback); got != tc.want {
				t.Fatalf("resolveRuntime(%q, env=%q, %q) = %q, want %q", tc.flag, tc.env, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestTheRuntimeVariableReachesEverySubcommand checks that the variable reaches
// every subcommand that takes a runtime through the same function, so none of
// them can hard-code a default again.
func TestTheRuntimeVariableReachesEverySubcommand(t *testing.T) {
	t.Setenv(runtimeEnv, "memory")

	// audit runs on the memory runtime with no --runtime typed.
	stdout, stderr, err := capture(t, "audit", "https://arxiv.org/abs/1",
		"--method", writeMethod(t), "--out", t.TempDir(), "--interval", "0")
	if err != nil {
		t.Fatalf("audit under %s=memory: %v\n%s", runtimeEnv, err, stderr)
	}
	if !strings.Contains(stdout, "verdict:") {
		t.Fatalf("audit did not run on the memory runtime:\n%s", stdout)
	}

	// providers marks the runtime the environment selected as the default.
	stdout, _, err = capture(t, "providers")
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	if !strings.Contains(stdout, "memory (default)") {
		t.Fatalf("providers does not mark the environment's runtime as the default:\n%s", stdout)
	}

	// The flag still takes precedence per command.
	_, _, err = capture(t, "audit", "paper", "--runtime", "nope", "--method", writeMethod(t))
	if err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("--runtime did not override the environment: %v", err)
	}
}
