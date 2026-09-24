// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"errors"
	"strings"
	"testing"

	"latere.ai/x/pkg/hostsandbox"
)

// TestEveryRefusalNamesItsRemedy checks that every refusal this program can
// print is reachable with nothing installed, names the install commands for
// the platform, and names the other driver.
func TestEveryRefusalNamesItsRemedy(t *testing.T) {
	cases := []struct {
		platform   hostsandbox.Platform
		driver     hostsandbox.DriverName
		condition  hostsandbox.Condition
		components []string
		want       []string
	}{
		{hostsandbox.Darwin, hostsandbox.Host, hostsandbox.ConditionMissing, []string{"srt"},
			[]string{"npm install -g @anthropic-ai/sandbox-runtime", "brew install ripgrep", "srt echo ok", "--sandbox container"}},
		{hostsandbox.Linux, hostsandbox.Host, hostsandbox.ConditionMissing, []string{"bubblewrap", "socat"},
			[]string{"apt-get install -y bubblewrap", "apt-get install -y socat", "--sandbox container"}},
		{hostsandbox.Darwin, hostsandbox.Host, hostsandbox.ConditionMissing, []string{"claude"},
			[]string{"npm install -g @anthropic-ai/claude-code", "claude login", "claude --version"}},
		{hostsandbox.Linux, hostsandbox.Host, hostsandbox.ConditionMissing, []string{"codex"},
			[]string{"npm install -g @openai/codex", "codex login"}},
		{hostsandbox.Darwin, Container, hostsandbox.ConditionMissing, []string{"podman"},
			[]string{"brew install podman", "podman machine init", "podman info", "--sandbox host"}},
		{hostsandbox.Linux, Container, hostsandbox.ConditionMissing, []string{"podman"},
			[]string{"apt-get install -y podman", "--sandbox host"}},
		{hostsandbox.Linux, Container, hostsandbox.ConditionMissing, []string{"docker"},
			[]string{"apt-get install -y docker.io", "docker info"}},
		{hostsandbox.Darwin, Container, ConditionStopped, []string{"podman"},
			[]string{"podman machine start", "never initialized", "podman info"}},
		{hostsandbox.Darwin, Container, ConditionStopped, []string{"docker"},
			[]string{"open -a Docker", "docker info"}},
		{hostsandbox.Linux, Container, ConditionStopped, []string{"podman"},
			[]string{"systemctl --user start podman"}},
		{hostsandbox.Darwin, Container, ConditionImage, []string{"ghcr.io/latere-ai/replichai-harness@sha256:abc"},
			[]string{"podman pull ghcr.io/latere-ai/replichai-harness@sha256:abc", "REPLICHAI_HARNESS_IMAGE"}},
		{hostsandbox.Darwin, Container, ConditionImage, nil, []string{"--image <ref>", "REPLICHAI_HARNESS_IMAGE", "--sandbox host"}},
	}
	for _, c := range cases {
		err := NotReady(c.platform, c.driver, c.condition, c.components...)
		if !errors.Is(err, hostsandbox.ErrNotReady) {
			t.Fatalf("%s/%s does not unwrap to ErrNotReady", c.platform, c.condition)
		}
		message := err.Error()
		for _, want := range c.want {
			if !strings.Contains(message, want) {
				t.Errorf("%s/%s/%v message missing %q:\n%s", c.platform, c.driver, c.components, want, message)
			}
		}
	}
}

// TestWindowsIsRefusedWithAReason checks that Windows is refused for either
// driver, naming WSL2 instead of offering the other driver, which is equally
// unavailable; and that an unsupported platform offers no alternative either.
func TestWindowsIsRefusedWithAReason(t *testing.T) {
	for _, driver := range []hostsandbox.DriverName{hostsandbox.Host, Container} {
		err := NotReady(hostsandbox.Windows, driver, hostsandbox.ConditionMissing, "srt")
		if err.Condition != hostsandbox.ConditionUnsupported {
			t.Errorf("%s on windows = %s", driver, err.Condition)
		}
		if message := err.Error(); !strings.Contains(message, "WSL2") || strings.Contains(message, "--sandbox") {
			t.Errorf("windows refusal for %s:\n%s", driver, message)
		}
	}
	if message := NotReady(hostsandbox.Linux, Container, hostsandbox.ConditionUnsupported, "podman").Error(); strings.Contains(message, "--sandbox") {
		t.Errorf("an unsupported platform offers the other driver:\n%s", message)
	}
	if Alternative(hostsandbox.Linux, "remote") != "" {
		t.Error("an unknown driver has an alternative")
	}
}
