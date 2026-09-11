// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Name identifies this runtime in a session reference. The sandbox mechanism
// and the harness are not part of it: both are interchangeable inside one
// runtime, and a name encoding them would have to be migrated whenever either
// changed.
const Name managed.RuntimeName = "local"

// Environment records what a run ran on. The container driver pins the
// toolchain; the host driver does not, so it records the toolchain instead.
// The record is what makes two reproductions comparable: two runs on different
// interpreter minor versions may disagree because of the interpreter, and
// without this file that cannot be distinguished from a disagreement about the
// paper.
//
// It is written by the host, never by the agent, and finalize commits it as
// system-authored evidence beside the transcript.
type Environment struct {
	Sandbox  EnvironmentSandbox `json:"sandbox"`
	Host     EnvironmentHost    `json:"host"`
	Harness  EnvironmentHarness `json:"harness"`
	Model    managed.ModelRef   `json:"model"`
	Packages []EnvironmentPkg   `json:"packages,omitempty"`
	Network  EnvironmentNetwork `json:"network"`
}

// EnvironmentSandbox records which boundary the work ran behind. Every field
// is filled by some driver. An empty field in a disclosure record reads as
// nothing to disclose whether or not that is true, so a field no driver could
// fill is not carried at all.
//
// Version and Image are the container driver's pinned toolchain and image
// reference. They are empty under the host driver, which pins neither and
// records what it ran on in Packages and Host instead. ConfigRead lists the
// paths outside the session that the host driver granted read access to,
// which is the harness's own login, and holds exactly what the run was
// granted.
type EnvironmentSandbox struct {
	Driver      sandbox.DriverName `json:"driver"`
	Version     string             `json:"version,omitempty"`
	Image       string             `json:"image,omitempty"`
	NetworkMode string             `json:"network_mode"`
	ConfigRead  []string           `json:"config_read_allowed,omitempty"`
}

// EnvironmentHost is the machine.
type EnvironmentHost struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Kernel string `json:"kernel,omitempty"`
}

// EnvironmentHarness is which agent produced the work, and which kind of
// credential it used. The credential itself is never recorded.
type EnvironmentHarness struct {
	Name    harness.Name     `json:"name"`
	Version string           `json:"version,omitempty"`
	Auth    harness.AuthKind `json:"auth"`
	Gateway string           `json:"gateway,omitempty"`
}

// EnvironmentPkg is one dependency the run resolved, with the version it
// actually got rather than the one that was asked for.
type EnvironmentPkg struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// EnvironmentNetwork is where the run was allowed to go.
type EnvironmentNetwork struct {
	// Allowed lists the hosts the run was permitted to reach. Under an open
	// policy it is empty, meaning everywhere. Under an allowlist it is the
	// run's entire reach, which bounds what a search that found nothing
	// could have looked at.
	Allowed []string `json:"allowed,omitempty"`
}

// NewEnvironment starts the record with what is knowable before the first
// stage runs. The rest is filled in as the session proceeds.
func NewEnvironment(ctx context.Context, driver sandbox.DriverName, harnessName harness.Name, model managed.ModelRef, gateway harness.Gateway, mode string) Environment {
	return Environment{
		Sandbox: EnvironmentSandbox{Driver: driver, NetworkMode: mode},
		Host:    EnvironmentHost{OS: runtime.GOOS, Arch: runtime.GOARCH, Kernel: kernelVersion(ctx)},
		Harness: EnvironmentHarness{Name: harnessName, Auth: gateway.Auth, Gateway: gateway.Name},
		Model:   model,
	}
}

// kernelVersion is best effort. If the version cannot be read the field is
// left empty rather than failing the run, since it only describes the machine.
func kernelVersion(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
