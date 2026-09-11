// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Errors this package returns.
var (
	// ErrNotReady reports a machine that cannot run a driver. It is a
	// refusal with instructions attached; the driver never starts partially.
	ErrNotReady = errors.New("local sandbox is not ready")
	// ErrMismatch reports a stage handle routed to the driver that did not
	// launch it.
	ErrMismatch = errors.New("stage handle belongs to another driver")
	// ErrGone reports a stage the driver no longer holds.
	ErrGone = errors.New("stage is gone")
)

// MismatchError names both drivers of a misrouted handle, because knowing
// which pairing went wrong is more useful than knowing only that one did.
type MismatchError struct {
	Expected DriverName
	Actual   DriverName
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("%v: expected %q, got %q", ErrMismatch, e.Expected, e.Actual)
}
func (e *MismatchError) Unwrap() error { return ErrMismatch }

// Condition is the reason a machine is not ready. Each condition needs a
// different instruction (install it, start it, or pull it), so the set is
// closed and the message is chosen from it rather than assembled from an exec
// failure.
type Condition string

// The not-ready conditions.
const (
	// ConditionMissing is a component that is not installed.
	ConditionMissing Condition = "missing"
	// ConditionStopped is a component that is installed but not running. On
	// macOS this is almost always a podman machine that was never started.
	ConditionStopped Condition = "stopped"
	// ConditionImage is an engine that runs but cannot supply the image.
	ConditionImage Condition = "image"
	// ConditionUnsupported is a platform the driver refuses outright.
	ConditionUnsupported Condition = "unsupported"
)

// NotReadyError is a refusal that carries its own remedy. The remediation is
// data on the error rather than text a caller assembles, so the CLI, the batch
// driver and a test all read the same message.
type NotReadyError struct {
	// Driver is the driver that could not start.
	Driver DriverName
	// Condition is the reason it could not start.
	Condition Condition
	// Components are the missing components, in the order they should be
	// fixed.
	Components []string
	// Detail is the underlying output. It is kept for the operator and never
	// parsed.
	Detail string
	// Remediation is the instructions, ready to print.
	Remediation string
	// Alternative names the other driver, so that an operator who cannot use
	// one driver is told there is another.
	Alternative string
}

func (e *NotReadyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "local: the %s sandbox is not ready", e.Driver)
	if len(e.Components) > 0 {
		fmt.Fprintf(&b, " (%s: %s)", strings.Join(e.Components, ", "), e.Condition)
	}
	if e.Remediation != "" {
		b.WriteString("\n\n")
		b.WriteString(e.Remediation)
	}
	if e.Alternative != "" {
		b.WriteString("\n\n")
		b.WriteString(e.Alternative)
	}
	if e.Detail != "" {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(e.Detail))
	}
	return b.String()
}

func (e *NotReadyError) Unwrap() error { return ErrNotReady }

// Platform is an operating system the remediation text is written for. It is
// a parameter rather than a call to runtime.GOOS so that the messages can be
// tested on one machine.
type Platform string

// The platforms. Windows is present so that it can be refused with a reason.
const (
	Darwin  Platform = "darwin"
	Linux   Platform = "linux"
	Windows Platform = "windows"
)

// CurrentPlatform is the platform this binary runs on.
func CurrentPlatform() Platform { return Platform(runtime.GOOS) }

// installs maps one component, on one platform, to the commands that install
// it. A component with no entry for a platform gets a generic line rather than
// an empty message, because a missing table row must not produce a refusal
// with no instructions.
var installs = map[string]map[Platform][]string{
	"srt": {
		Darwin: {"npm install -g @anthropic-ai/sandbox-runtime", "brew install ripgrep"},
		Linux:  {"npm install -g @anthropic-ai/sandbox-runtime"},
	},
	"bubblewrap": {
		Linux: {"sudo apt-get install -y bubblewrap      # Debian, Ubuntu", "sudo dnf install -y bubblewrap          # Fedora, RHEL"},
	},
	"socat": {
		Linux: {"sudo apt-get install -y socat           # Debian, Ubuntu", "sudo dnf install -y socat               # Fedora, RHEL"},
	},
	"rg": {
		Darwin: {"brew install ripgrep"},
		Linux:  {"sudo apt-get install -y ripgrep        # Debian, Ubuntu", "sudo dnf install -y ripgrep             # Fedora, RHEL"},
	},
	"podman": {
		Darwin: {"brew install podman", "podman machine init --cpus 4 --memory 8192 --disk-size 100", "podman machine start"},
		Linux:  {"sudo apt-get install -y podman         # Debian, Ubuntu", "sudo dnf install -y podman              # Fedora, RHEL"},
	},
	"docker": {
		Darwin: {"brew install --cask docker", "open -a Docker"},
		Linux:  {"sudo apt-get install -y docker.io      # Debian, Ubuntu"},
	},
	"claude": {
		Darwin: {"npm install -g @anthropic-ai/claude-code", "claude login"},
		Linux:  {"npm install -g @anthropic-ai/claude-code", "claude login"},
	},
	"codex": {
		Darwin: {"npm install -g @openai/codex", "codex login"},
		Linux:  {"npm install -g @openai/codex", "codex login"},
	},
}

// verify maps a component to the command that confirms it works. It is
// printed after the install lines so that an operator can check the install
// without re-running the whole audit.
var verify = map[string]string{
	// The command is given as bare words, not a quoted string. srt accepts a
	// string only after -c, so `srt "echo ok"` fails with "command not
	// found: echo ok".
	"srt":    "srt echo ok",
	"podman": "podman info",
	"docker": "docker info",
	"claude": "claude --version",
	"codex":  "codex --version",
}

// alternatives maps a driver that cannot start to the text that names the
// other driver.
var alternatives = map[DriverName]string{
	Host:      "Or run in a container instead:  replichai audit … --sandbox container",
	Container: "Or use the host sandbox, which needs no engine:  replichai audit … --sandbox host",
}

// NotReady builds the refusal for one condition, on one platform, for one or
// more components. It is a pure function, so every message this program can
// print is reachable from a test on a machine with none of the components
// installed.
func NotReady(platform Platform, driver DriverName, condition Condition, components ...string) *NotReadyError {
	err := &NotReadyError{
		Driver:      driver,
		Condition:   condition,
		Components:  append([]string(nil), components...),
		Alternative: alternatives[driver],
	}
	if platform == Windows {
		err.Condition = ConditionUnsupported
		err.Remediation = "Windows is not supported. Run replichai inside WSL2, where both\nsandboxes work."
		err.Alternative = ""
		return err
	}
	switch condition {
	case ConditionStopped:
		err.Remediation = stoppedText(platform, components)
	case ConditionImage:
		err.Remediation = imageText(components)
	case ConditionUnsupported:
		err.Remediation = fmt.Sprintf("The %s sandbox does not run on %s.", driver, platform)
		err.Alternative = ""
	default:
		err.Remediation = missingText(platform, components)
	}
	return err
}

func missingText(platform Platform, components []string) string {
	var b strings.Builder
	for index, component := range components {
		if index > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s is not installed. Install it with:\n\n", component)
		for _, line := range installLines(platform, component) {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	if check := verifyLine(components); check != "" {
		fmt.Fprintf(&b, "\nThen confirm with: %s\n", check)
	}
	return strings.TrimRight(b.String(), "\n")
}

func stoppedText(platform Platform, components []string) string {
	var b strings.Builder
	for _, component := range components {
		fmt.Fprintf(&b, "%s is installed but is not running.\n\n", component)
		switch {
		case component == "podman" && platform == Darwin:
			b.WriteString("    podman machine start\n\nIf you have never initialised one:\n\n")
			b.WriteString("    podman machine init --cpus 4 --memory 8192 --disk-size 100\n    podman machine start\n")
		case component == "docker" && platform == Darwin:
			b.WriteString("    open -a Docker\n")
		default:
			fmt.Fprintf(&b, "    systemctl --user start %s\n", component)
		}
	}
	if check := verifyLine(components); check != "" {
		fmt.Fprintf(&b, "\nThen confirm with: %s\n", check)
	}
	return strings.TrimRight(b.String(), "\n")
}

func imageText(components []string) string {
	image := "the harness image"
	if len(components) > 0 {
		image = components[len(components)-1]
	}
	return fmt.Sprintf(`The engine is running, but the harness image is unavailable.

    podman pull %s

The image is public. Behind a proxy, set REPLICHAI_HARNESS_IMAGE to a
mirror holding the same digest.`, image)
}

// installLines looks up the install commands for a component, with a generic
// fallback so that a component with no table row still produces an
// actionable instruction.
func installLines(platform Platform, component string) []string {
	if byPlatform, ok := installs[component]; ok {
		if lines, ok := byPlatform[platform]; ok {
			return lines
		}
		platforms := make([]string, 0, len(byPlatform))
		for known := range byPlatform {
			platforms = append(platforms, string(known))
		}
		sort.Strings(platforms)
		return []string{fmt.Sprintf("# %s is packaged for %s; install it for %s from its own documentation",
			component, strings.Join(platforms, ", "), platform)}
	}
	return []string{fmt.Sprintf("# install %s and put it on PATH", component)}
}

func verifyLine(components []string) string {
	for _, component := range components {
		if line, ok := verify[component]; ok {
			return line
		}
	}
	return ""
}
