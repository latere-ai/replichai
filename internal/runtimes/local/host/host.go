// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package host is the default sandbox driver: the harness already installed
// on the machine, confined by srt to the session's directories and an
// allow-only network (045b). The driver is latere.ai/x/pkg/hostsandbox; this
// package composes it with what is replichai's. The harness binary is
// required at preflight and its directory granted, this program's own
// credential is denied beside the shared deny table, every refusal names the
// container alternative and this program's install rows, and the capability
// declaration carries the package managers and the attachment cap the runtime
// reads, which srt itself knows nothing about.
//
// The driver starts in milliseconds, needs no image and no virtual machine,
// and reuses the harness's own login rather than requiring a raw API key. It
// does not provide a pinned toolchain; the container driver does. The
// environment record is what makes an unpinned run reproducible.
package host

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"time"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Config is the configuration a composition root passes to this driver.
type Config struct {
	// Platform is the operating system. It is a parameter rather than a call
	// to runtime.GOOS so that every refusal can be tested on one machine.
	Platform hostsandbox.Platform
	// Home is the operator's home directory. The whole directory is denied.
	Home string
	// HarnessBinary is the executable the stage runs. Preflight checks for
	// it so that a missing harness is reported with install instructions
	// rather than as an exec failure after the stage has started.
	HarnessBinary string
	// StopGrace is how long Stop waits after SIGTERM before it sends SIGKILL
	// to the stage's process group. Zero selects the shared default.
	StopGrace time.Duration
	// Look resolves an executable. It defaults to exec.LookPath.
	Look func(string) (string, error)
	// Lookup reads one variable of the process environment; the composition
	// root passes os.LookupEnv. A stage inherits only the variables that
	// hostsandbox.Environment admits through it.
	Lookup func(string) (string, bool)
}

// DenyRead is the credential this program keeps on the machine: the cloud
// token (045i). It is denied to every stage beside the shared deny table.
const DenyRead = "~/.config/replichai"

// MaxAttachmentBytes bounds a single attachment written into the workspace.
const MaxAttachmentBytes = 64 << 20

// Sandbox is the srt driver composed for this program.
type Sandbox struct{ *hostsandbox.Driver }

// New composes the driver.
func New(config Config) *Sandbox {
	if config.Platform == "" {
		config.Platform = hostsandbox.CurrentPlatform()
	}
	var requires []string
	if binary := strings.TrimSpace(config.HarnessBinary); binary != "" {
		requires = []string{binary}
	}
	return &Sandbox{Driver: hostsandbox.New(hostsandbox.Config{
		Platform:    config.Platform,
		Home:        config.Home,
		Requires:    requires,
		Remedies:    sandbox.Remedies(),
		Alternative: sandbox.Alternative(config.Platform, hostsandbox.Host),
		StopGrace:   config.StopGrace,
		Look:        config.Look,
		Lookup:      config.Lookup,
	})}
}

// CarriesHarnessLogin reports whether a stage under this driver can reach the
// credential the harness on this machine is already signed in with. It can: a
// stage runs on the operator's own filesystem, and the runtime grants read
// access to exactly the paths the harness names for its login.
func (s *Sandbox) CarriesHarnessLogin() bool { return true }

// Capabilities adds what the runtime reads beyond the egress policies srt
// enforces: pip and npm, which install into the session's own environment,
// and the attachment cap. apt is absent because it needs a privileged root
// this driver does not have.
func (s *Sandbox) Capabilities() hostsandbox.Capabilities {
	capabilities := s.Driver.Capabilities()
	capabilities.PackageManagers = map[string]bool{"pip": true, "npm": true}
	capabilities.MaxAttachmentBytes = MaxAttachmentBytes
	return capabilities
}

// Profile is the access policy a stage runs under, with this program's own
// credential denied.
func (s *Sandbox) Profile(spec hostsandbox.StageSpec) hostsandbox.Profile {
	return s.Driver.Profile(withDenied(spec))
}

// Launch starts the stage with this program's own credential denied.
func (s *Sandbox) Launch(ctx context.Context, spec hostsandbox.StageSpec) (hostsandbox.StageHandle, error) {
	return s.Driver.Launch(ctx, withDenied(spec))
}

func withDenied(spec hostsandbox.StageSpec) hostsandbox.StageSpec {
	spec.Denied = append(slices.Clone(spec.Denied), DenyRead)
	return spec
}

// Version reports the version of the harness on this machine, so that a run
// records what produced it. It runs outside the sandbox because it is this
// program's query about its own dependency, not the agent's work.
func (s *Sandbox) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no version command")
	}
	//nolint:gosec // the argv is the harness's own version command from its adapter
	output, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
