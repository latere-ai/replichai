// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sandbox is the boundary a local session's work runs inside: the
// driver contract, the stage description a driver executes, and the typed
// not-ready error a driver raises when the machine cannot run it (045a).
//
// Nothing here knows about agents, models, grading or git. A driver starts a
// process under some isolation mechanism, reports whether it is still running,
// and hands back the bytes it wrote — and the runtime above composes those into
// a managed session.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// DriverName identifies one isolation mechanism. It is recorded in a session's
// locator, so a session started under one driver is never observed through
// another: the handles mean different things.
type DriverName string

// The drivers this repository ships. Host is the default — the harness already
// installed on the machine, confined by srt. Container is opt-in, for a pinned
// toolchain.
const (
	Host      DriverName = "host"
	Container DriverName = "container"
)

// Access is how a stage may use one path.
type Access string

// The path access modes. A path not named in a StageSpec is denied outright;
// there is no implicit read.
const (
	ReadOnly  Access = "ro"
	ReadWrite Access = "rw"
)

// Path is one filesystem path a stage may use, given as a host path. A driver
// maps it into whatever the stage sees: the same path under the host driver, a
// mount destination under the container driver.
type Path struct {
	// Host is the absolute path on the machine running the session.
	Host string
	// Guest is where the stage sees it. Empty means the same as Host, which
	// is the host driver's normal case.
	Guest string
	// Access is read-only or read-write. There is no third mode: a path a
	// stage may not touch is left out of the spec entirely.
	Access Access
}

// Destination is where the stage sees this path, which is Guest when the
// driver remaps it and Host when it does not.
func (p Path) Destination() string {
	if strings.TrimSpace(p.Guest) != "" {
		return p.Guest
	}
	return p.Host
}

// Network is a stage's egress policy, mirroring managed.NetworkPolicy without
// importing it: a driver has no business reading a session specification.
type Network struct {
	// Mode is "none", "allowlist" or "open".
	Mode string
	// Domains is the allowlist. It is meaningful only in allowlist mode and
	// a driver rejects it in the other two.
	Domains []string
	// Passthrough are domains reached without the sandbox terminating TLS.
	// They remain subject to the allowlist; what is dropped is the
	// inspection, for clients that will not trust an injected authority.
	Passthrough []string
}

// The network modes a StageSpec can carry.
const (
	NetworkNone      = "none"
	NetworkAllowlist = "allowlist"
	NetworkOpen      = "open"
)

// StageSpec is one unit of work a driver runs: an argv, an environment, the
// paths it may touch, and its egress policy. Everything in it is computed by
// the runtime; a driver interprets it into a Seatbelt profile, a bubblewrap
// invocation, or engine arguments, and adds nothing of its own.
type StageSpec struct {
	// Name is a short, filesystem-safe label such as "worker-0" or
	// "judge-1". It names the stage's log and its container.
	Name string
	// Argv is the command, never a shell fragment.
	Argv []string
	// Env is the environment. Values that are credentials are delivered by
	// the driver through a mechanism that keeps them out of the process
	// table; see EnvFile.
	Env map[string]string
	// EnvFile names the environment keys whose values are credentials.
	// A driver must not pass these on a command line.
	EnvFile []string
	// Workdir is where the stage starts, and must appear in Paths.
	Workdir string
	// Paths is every path the stage may read or write. Anything absent is
	// denied.
	Paths []Path
	// Denied are paths that must stay unreadable even though something in
	// Paths would otherwise cover them. A driver that grants a directory in
	// order to reach one file inside it uses this to cut back the rest.
	Denied []string
	// Network is the egress policy.
	Network Network
	// LogPath is the host file the driver writes the stage's combined
	// output to. It is deliberately not in Paths: the stage must not be
	// able to rewrite its own transcript.
	LogPath string
}

// StageHandle is a driver's durable reference to one launched stage. It is
// opaque to the runtime, which stores it in the session locator and hands it
// back — so it must survive JSON and must carry no credential.
type StageHandle struct {
	// Driver is which driver launched the stage.
	Driver DriverName `json:"driver"`
	// ID is the driver's own handle: a pid and process start time for the
	// host driver, a container name for the container driver.
	ID string `json:"id"`
}

// Valid reports whether the handle names something a driver could look up.
func (h StageHandle) Valid() bool {
	return strings.TrimSpace(string(h.Driver)) != "" && strings.TrimSpace(h.ID) != ""
}

// StageStatus is one observation of a launched stage.
type StageStatus struct {
	// Running reports whether the stage is still alive.
	Running bool
	// ExitCode is meaningful only once Running is false. A stage killed by
	// a signal reports a negative code.
	ExitCode int
	// Gone reports that the driver has no record of the stage at all,
	// which is what a reaped process or a removed container looks like
	// after a restart. Running is false and ExitCode is not meaningful.
	Gone bool
}

// Succeeded reports whether the stage finished and exited zero.
func (s StageStatus) Succeeded() bool { return !s.Running && !s.Gone && s.ExitCode == 0 }

// Capabilities is a driver's half of a runtime's capability declaration. The
// runtime adds what it provides itself — evaluation, artifacts, cancellation —
// and refuses a specification neither half can honour.
type Capabilities struct {
	// PackageManagers are the managers this driver can install with.
	PackageManagers map[string]bool
	// NetworkModes are the egress policies it can actually enforce. A
	// driver declares only what it enforces: there is no best-effort mode
	// in which an allowlist quietly becomes open egress.
	NetworkModes map[string]bool
	// MaxAttachmentBytes bounds a single attachment written into the
	// workspace. Zero means the runtime's own default applies.
	MaxAttachmentBytes int64
}

// Sandbox is one isolation mechanism able to run a stage. The interface is
// small enough to fake, because every driver is held to the same contract
// suite in sandboxtest.
type Sandbox interface {
	// Name is the driver's canonical name, recorded in every handle it
	// issues and demanded back by every call that takes one.
	Name() DriverName

	// Preflight reports whether this machine can run the driver at all. It
	// returns a *NotReadyError carrying remediation when it cannot, so a
	// caller prints instructions rather than a failed exec.
	Preflight(context.Context) error

	// Capabilities is what this driver can enforce and install.
	Capabilities() Capabilities

	// Launch starts one stage detached and returns a handle that survives
	// the calling process.
	Launch(context.Context, StageSpec) (StageHandle, error)

	// Observe reports whether a launched stage is still running, and its
	// exit status once it is not.
	Observe(context.Context, StageHandle) (StageStatus, error)

	// Output streams the stage's raw bytes from an offset. It reads from
	// somewhere the stage itself cannot write.
	Output(context.Context, StageHandle, int64) (io.ReadCloser, error)

	// Stop terminates a running stage with a grace period. Stopping a
	// stage that has already exited is not an error.
	Stop(context.Context, StageHandle) error

	// Discard removes whatever the driver created for the stage. It never
	// touches the session directory: that belongs to the runtime, and the
	// push may not have happened yet.
	Discard(context.Context, StageHandle) error
}

// MatchHandle reports a handle routed to the wrong driver. Handles from two
// drivers are both opaque strings and would otherwise be interchangeable,
// which is how a pid gets looked up as a container name.
func MatchHandle(driver DriverName, handle StageHandle) error {
	if !handle.Valid() {
		return fmt.Errorf("%w: empty stage handle", ErrNotReady)
	}
	if handle.Driver != driver {
		return &MismatchError{Expected: driver, Actual: handle.Driver}
	}
	return nil
}
