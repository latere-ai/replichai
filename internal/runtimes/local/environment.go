package local

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Name identifies this runtime in a session reference. The sandbox mechanism
// and the harness are not part of it: both are interchangeable inside one
// runtime, and a name encoding them would have to be migrated whenever either
// changed.
const Name managed.RuntimeName = "local"

// Environment is what a run ran on, recorded because the host sandbox does not
// pin it. The container driver pins the toolchain; this driver records it, and
// the record is what makes two reproductions comparable — two that disagree on
// different interpreter minors disagree about the interpreter, and without
// this file nobody can tell that from a disagreement about the paper.
//
// It is written by the host, never by the agent, and finalize commits it as
// system-authored evidence beside the transcript.
type Environment struct {
	Sandbox   EnvironmentSandbox `json:"sandbox"`
	Host      EnvironmentHost    `json:"host"`
	Harness   EnvironmentHarness `json:"harness"`
	Model     managed.ModelRef   `json:"model"`
	Toolchain map[string]string  `json:"toolchain,omitempty"`
	Packages  []EnvironmentPkg   `json:"packages,omitempty"`
	Network   EnvironmentNetwork `json:"network"`
}

// EnvironmentSandbox is which boundary the work ran behind. ConfigRead records
// the one read allowance the host driver grants outside the session — the
// harness's own configuration — so it is a disclosed property of a local run
// rather than a surprise.
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

// EnvironmentNetwork is where the run went. Under an open policy the driver's
// proxy log is the source, so an artifact hunt is defensible by being visible.
type EnvironmentNetwork struct {
	DomainsReached []string `json:"domains_reached,omitempty"`
}

// NewEnvironment starts the record with what is knowable before the first
// stage runs. The rest is filled in as the session proceeds.
func NewEnvironment(driver sandbox.DriverName, harnessName harness.Name, model managed.ModelRef, gateway harness.Gateway, mode string) Environment {
	return Environment{
		Sandbox: EnvironmentSandbox{Driver: driver, NetworkMode: mode},
		Host:    EnvironmentHost{OS: runtime.GOOS, Arch: runtime.GOARCH, Kernel: kernelVersion()},
		Harness: EnvironmentHarness{Name: harnessName, Auth: gateway.Auth, Gateway: gateway.Name},
		Model:   model,
	}
}

// kernelVersion is best effort: an unavailable one leaves the field empty
// rather than failing a run over a description of the machine.
func kernelVersion() string {
	output, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
