// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sandbox holds what replichai adds to the sandbox seam in
// latere.ai/x/pkg/hostsandbox (045a): the container driver's name, the two
// readiness conditions only an engine has, the install table for the engines
// and harnesses this program runs, and the refusal text that names this
// program's own commands and the other driver as the alternative.
//
// The stage vocabulary, the Sandbox interface and the srt driver are the
// shared package's. Nothing here knows about agents, models, grading or git.
package sandbox

import (
	"fmt"
	"strings"

	"latere.ai/x/pkg/hostsandbox"
)

// Container is the container driver's name. hostsandbox.Host is the other.
const Container hostsandbox.DriverName = "container"

// The conditions the container driver adds to the shared package's.
const (
	// ConditionStopped is an engine that is installed but not running. On
	// macOS this is almost always a podman machine that was never started.
	ConditionStopped hostsandbox.Condition = "stopped"
	// ConditionImage is an engine that runs but cannot supply the image, or
	// a run that names no image at all.
	ConditionImage hostsandbox.Condition = "image"
)

// Remedies is the install table for everything this program's drivers need:
// srt and its dependencies from the shared package, the container engines,
// and the harnesses.
func Remedies() hostsandbox.Remedies {
	return hostsandbox.DefaultRemedies().With(hostsandbox.Remedies{
		"podman": {
			Install: map[hostsandbox.Platform][]string{
				hostsandbox.Darwin: {"brew install podman", "podman machine init --cpus 4 --memory 8192 --disk-size 100", "podman machine start"},
				hostsandbox.Linux:  {"sudo apt-get install -y podman         # Debian, Ubuntu", "sudo dnf install -y podman              # Fedora, RHEL"},
			},
			Verify: "podman info",
		},
		"docker": {
			Install: map[hostsandbox.Platform][]string{
				hostsandbox.Darwin: {"brew install --cask docker", "open -a Docker"},
				hostsandbox.Linux:  {"sudo apt-get install -y docker.io      # Debian, Ubuntu"},
			},
			Verify: "docker info",
		},
		"claude": {
			Install: map[hostsandbox.Platform][]string{
				hostsandbox.Darwin: {"npm install -g @anthropic-ai/claude-code", "claude login"},
				hostsandbox.Linux:  {"npm install -g @anthropic-ai/claude-code", "claude login"},
			},
			Verify: "claude --version",
		},
		"codex": {
			Install: map[hostsandbox.Platform][]string{
				hostsandbox.Darwin: {"npm install -g @openai/codex", "codex login"},
				hostsandbox.Linux:  {"npm install -g @openai/codex", "codex login"},
			},
			Verify: "codex --version",
		},
	})
}

// Alternative is the text that names the other driver, so that an operator
// blocked on one is told there is another. Windows gets none, because both
// drivers are equally unavailable there.
func Alternative(platform hostsandbox.Platform, driver hostsandbox.DriverName) string {
	if platform == hostsandbox.Windows {
		return ""
	}
	switch driver {
	case hostsandbox.Host:
		return "Or run in a container instead:  replichai audit … --sandbox container"
	case Container:
		return "Or use the host sandbox, which needs no engine:  replichai audit … --sandbox host"
	default:
		return ""
	}
}

// NotReady builds the refusal for one condition, on one platform, for one or
// more components, in this program's words. It is a pure function, so every
// message this program can print is reachable from a test on a machine with
// none of the components installed.
func NotReady(platform hostsandbox.Platform, driver hostsandbox.DriverName, condition hostsandbox.Condition, components ...string) *hostsandbox.NotReadyError {
	err := Remedies().NotReady(platform, driver, condition, components...)
	err.Alternative = Alternative(platform, driver)
	switch {
	case platform == hostsandbox.Windows:
		err.Remediation = "Windows is not supported. Run replichai inside WSL2, where both\nsandboxes work."
	case condition == hostsandbox.ConditionUnsupported:
		err.Alternative = ""
	case condition == ConditionStopped:
		err.Remediation = stoppedText(platform, components)
	case condition == ConditionImage:
		err.Remediation = imageText(components)
	}
	return err
}

func stoppedText(platform hostsandbox.Platform, components []string) string {
	var b strings.Builder
	remedies := Remedies()
	for _, component := range components {
		fmt.Fprintf(&b, "%s is installed but is not running.\n\n", component)
		switch {
		case component == "podman" && platform == hostsandbox.Darwin:
			b.WriteString("    podman machine start\n\nIf you have never initialized one:\n\n")
			b.WriteString("    podman machine init --cpus 4 --memory 8192 --disk-size 100\n    podman machine start\n")
		case component == "docker" && platform == hostsandbox.Darwin:
			b.WriteString("    open -a Docker\n")
		default:
			fmt.Fprintf(&b, "    systemctl --user start %s\n", component)
		}
	}
	for _, component := range components {
		if verify := remedies[component].Verify; verify != "" {
			fmt.Fprintf(&b, "\nThen confirm with: %s\n", verify)
			break
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// imageText is the remedy for an image the driver cannot use. With no
// component, no image was named at all: none is published, so the operator
// builds one and names it.
func imageText(components []string) string {
	if len(components) == 0 {
		return `No harness image is named, and none is published to pull by default.
Build one, then name it:

    replichai audit … --sandbox container --image <ref>

or set REPLICHAI_HARNESS_IMAGE=<ref>.`
	}
	return fmt.Sprintf(`The engine is running, but the harness image is unavailable.

    podman pull %s

Behind a proxy, pass --image or set REPLICHAI_HARNESS_IMAGE to a mirror
holding the same digest.`, components[len(components)-1])
}
