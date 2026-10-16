// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes

import (
	"slices"
	"testing"
)

func TestCredentialsComeFromTheRuntimesOwnVariables(t *testing.T) {
	scoped := ResolveCredentials(mapLookup(map[string]string{
		"ANTHROPIC_API_KEY":  "scoped-key",
		"ANTHROPIC_BASE_URL": "scoped-base",
	}))
	if scoped.AnthropicKey != "scoped-key" || scoped.AnthropicBase != "scoped-base" {
		t.Fatalf("scoped credentials = %+v", scoped)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// TestLatereEnvironmentsAreAList checks that LATERE_ENVIRONMENTS is read as a
// comma-separated list with its blanks dropped, and that an unset variable
// offers nothing beyond the runtime's default.
func TestLatereEnvironmentsAreAList(t *testing.T) {
	got := ResolveCredentials(func(name string) (string, bool) {
		if name == "LATERE_ENVIRONMENTS" {
			return " base, science ,,gui", true
		}
		return "", false
	}).LatereEnvironments
	if want := []string{"base", "science", "gui"}; !slices.Equal(got, want) {
		t.Fatalf("LatereEnvironments = %q, want %q", got, want)
	}
	if unset := ResolveCredentials(func(string) (string, bool) { return "", false }).LatereEnvironments; unset != nil {
		t.Fatalf("an unset variable gave %q, want none", unset)
	}
}
