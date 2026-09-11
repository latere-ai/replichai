// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"slices"
	"strings"
	"testing"
)

// TestHostEnvironmentAdmitsOnlyTheAllowlist checks that the environment a
// stage inherits is built from the allowlist and never copied, so that a
// credential exported in the operator's shell does not reach an agent running
// with its permission prompts bypassed, whatever the credential is named.
func TestHostEnvironmentAdmitsOnlyTheAllowlist(t *testing.T) {
	operator := map[string]string{
		"PATH":                  "/usr/bin:/bin",
		"LANG":                  "en_GB.UTF-8",
		"ANTHROPIC_API_KEY":     "sk-ant-secret",
		"OPENAI_API_KEY":        "sk-secret",
		"GITHUB_TOKEN":          "ghp_secret",
		"AWS_SECRET_ACCESS_KEY": "aws-secret",
		"HOME":                  "/Users/x",
		"TERM":                  "xterm-256color",
	}
	lookup := func(name string) (string, bool) {
		value, ok := operator[name]
		return value, ok
	}
	got := HostEnvironment(lookup)
	if !slices.Contains(got, "PATH=/usr/bin:/bin") || !slices.Contains(got, "LANG=en_GB.UTF-8") {
		t.Fatalf("the stage lost what it needs to run: %v", got)
	}
	for _, entry := range got {
		name, value, _ := strings.Cut(entry, "=")
		if !slices.Contains(HostAllowedEnvironment, name) {
			t.Errorf("%s reached the stage and is not on the allowlist", name)
		}
		if strings.Contains(value, "secret") {
			t.Errorf("a credential reached the stage: %s", entry)
		}
		// The driver sets HOME and TERM, so the operator's values are not
		// copied even though a stage will have both.
		if name == "HOME" || name == "TERM" {
			t.Errorf("the operator's %s was inherited rather than set by the driver", entry)
		}
		// An unset variable is absent rather than present and empty, so a
		// stage sees the same thing for an unset locale as for an empty one.
		if name == "TZ" {
			t.Errorf("an unset variable was materialised: %s", entry)
		}
	}
	// A nil lookup yields no environment, and the host driver refuses to
	// launch with one rather than guessing.
	if HostEnvironment(nil) != nil {
		t.Error("a nil lookup produced an environment")
	}
}
