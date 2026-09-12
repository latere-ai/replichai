// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package container is the opt-in sandbox driver. It runs a stage in an OCI
// container, Podman by default with Docker also supported, which gives a
// toolchain pinned identically on every machine (045c).
//
// It is the second driver, not the default. Pinning is what CI and
// cross-operator comparison need. A first local run does not need it, and it
// costs an image, a virtual machine on macOS, and a raw API key instead of the
// login the operator already has.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/pkg/hostsandbox"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// DefaultImage is the harness image, pinned by tag here and by digest in a
// deployment. It holds the toolchain and both harnesses.
const DefaultImage = "ghcr.io/latere-ai/replichai-harness:latest"

// Labels the image declares about itself. Capabilities are read from the image
// rather than repeated in this code, so that a custom image with a different
// toolchain declares its own, and a specification naming a manager the image
// lacks is refused rather than failing mid-run.
const (
	ManagersLabel  = "org.replichai.managers"
	HarnessesLabel = "org.replichai.harnesses"
	ToolchainLabel = "org.replichai.toolchain"
)

// Engine is a container engine.
type Engine string

// The engines this driver drives.
const (
	Podman Engine = "podman"
	Docker Engine = "docker"
)

// Runner executes an engine command. It is an interface so that the driver
// can be tested without an engine, and so that both engines go through one
// function.
type Runner func(ctx context.Context, argv []string, stdin io.Reader) ([]byte, error)

// Config is the configuration a composition root passes to this driver.
type Config struct {
	// Engine names the engine to use. Empty selects podman, then docker.
	// A named engine that is unusable is an error, never a fallback. An
	// operator who named an engine gets that engine or an error message.
	Engine Engine
	// Image is the harness image.
	Image string
	// Platform is the operating system. It is a parameter so that refusals
	// can be tested on one machine.
	Platform hostsandbox.Platform
	// UID and GID are the ownership a stage writes with, so that the
	// operator can read and push what the run produced.
	UID, GID int
	// Look resolves an executable, defaulting to exec.LookPath.
	Look func(string) (string, error)
	// Run executes an engine command, defaulting to the real one.
	Run Runner
}

// Sandbox is the container driver.
type Sandbox struct {
	config       Config
	engine       Engine
	capabilities hostsandbox.Capabilities
	toolchain    string
}

// New probes the machine and the image, and returns a driver only when both
// respond. Probing at construction lets an operator learn that their
// environment is wrong before a paper is fetched or a repository created.
func New(ctx context.Context, config Config) (*Sandbox, error) {
	if config.Platform == "" {
		config.Platform = hostsandbox.CurrentPlatform()
	}
	if config.Image == "" {
		config.Image = DefaultImage
	}
	if config.Look == nil {
		config.Look = exec.LookPath
	}
	if config.Run == nil {
		config.Run = realRunner
	}
	if config.UID == 0 && config.GID == 0 {
		config.UID, config.GID = os.Getuid(), os.Getgid()
	}
	if config.Platform == hostsandbox.Windows {
		return nil, sandbox.NotReady(config.Platform, sandbox.Container, hostsandbox.ConditionUnsupported, "podman")
	}
	driver := &Sandbox{config: config}
	engine, err := driver.selectEngine(ctx)
	if err != nil {
		return nil, err
	}
	driver.engine = engine
	if err := driver.readImage(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

// Name is the driver's canonical name.
func (s *Sandbox) Name() hostsandbox.DriverName { return sandbox.Container }

// Engine is the engine this driver selected.
func (s *Sandbox) Engine() Engine { return s.engine }

// Toolchain is the toolchain the image declares. It is recorded in the
// environment record so that two runs on different machines can be compared.
func (s *Sandbox) Toolchain() string { return s.toolchain }

// Image is the image reference the stage runs from. It is recorded in the
// environment record because under this driver the image is everything that
// was pinned, and a reader comparing two reproductions needs to know they ran
// the same one.
func (s *Sandbox) Image() string { return s.config.Image }

// selectEngine picks the engine and checks that it works. A named engine that
// cannot run is an error rather than a silent fallback to the other one.
func (s *Sandbox) selectEngine(ctx context.Context) (Engine, error) {
	if named := Engine(strings.TrimSpace(string(s.config.Engine))); named != "" {
		return named, s.probe(ctx, named)
	}
	var first error
	for _, candidate := range []Engine{Podman, Docker} {
		err := s.probe(ctx, candidate)
		if err == nil {
			return candidate, nil
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		first = sandbox.NotReady(s.config.Platform, sandbox.Container, hostsandbox.ConditionMissing, string(Podman))
	}
	return "", first
}

// probe distinguishes the three common failures, because each needs a
// different instruction: not installed, installed but not running, and
// running but unable to supply the image.
func (s *Sandbox) probe(ctx context.Context, engine Engine) error {
	if _, err := s.config.Look(string(engine)); err != nil {
		return sandbox.NotReady(s.config.Platform, sandbox.Container, hostsandbox.ConditionMissing, string(engine))
	}
	if _, err := s.config.Run(ctx, []string{string(engine), "info", "--format", "{{.Host.Arch}}"}, nil); err != nil {
		notReady := sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionStopped, string(engine))
		notReady.Detail = err.Error()
		return notReady
	}
	return nil
}

// readImage reads what the image declares about itself, pulling it first if
// it is absent.
func (s *Sandbox) readImage(ctx context.Context) error {
	format := fmt.Sprintf(`{{index .Config.Labels "%s"}}|{{index .Config.Labels "%s"}}`, ManagersLabel, ToolchainLabel)
	output, err := s.config.Run(ctx, []string{string(s.engine), "image", "inspect", "--format", format, s.config.Image}, nil)
	if err != nil {
		if _, pullErr := s.config.Run(ctx, []string{string(s.engine), "pull", s.config.Image}, nil); pullErr != nil {
			notReady := sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionImage, s.config.Image)
			notReady.Detail = pullErr.Error()
			return notReady
		}
		output, err = s.config.Run(ctx, []string{string(s.engine), "image", "inspect", "--format", format, s.config.Image}, nil)
		if err != nil {
			notReady := sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionImage, s.config.Image)
			notReady.Detail = err.Error()
			return notReady
		}
	}
	managers, toolchain, _ := strings.Cut(strings.TrimSpace(string(output)), "|")
	declared := splitList(managers)
	if len(declared) == 0 {
		return fmt.Errorf("image %s declares no %s label, so nothing is known about what it can install",
			s.config.Image, ManagersLabel)
	}
	s.toolchain = strings.TrimSpace(toolchain)
	s.capabilities = hostsandbox.Capabilities{
		PackageManagers: declared,
		// Neither engine filters egress by domain natively. Doing so
		// requires a proxy the container is forced through, which is what
		// srt already provides on the host. Declaring a mode this driver
		// cannot enforce would make the capability declaration, which
		// specifications are validated against, incorrect.
		NetworkModes:       map[string]bool{hostsandbox.NetworkNone: true, hostsandbox.NetworkOpen: true},
		MaxAttachmentBytes: 64 << 20,
	}
	return nil
}

// CarriesHarnessLogin returns false. A container has neither the operator's
// configuration directory nor their credential store, which is the cost of
// the pinned toolchain. A session under this driver is supplied a credential
// instead.
func (s *Sandbox) CarriesHarnessLogin() bool { return false }

// Preflight re-checks the engine, because a machine can be stopped between
// construction and a launch.
func (s *Sandbox) Preflight(ctx context.Context) error { return s.probe(ctx, s.engine) }

// Capabilities reports what the image declares and what the engine enforces.
func (s *Sandbox) Capabilities() hostsandbox.Capabilities { return s.capabilities }

// Launch starts one stage as a detached container. Ownership is mapped so
// that the operator can read and push what the run produced. A rootless
// container writing the workspace as root would leave a directory the push
// cannot use, and that failure would appear later and elsewhere.
func (s *Sandbox) Launch(ctx context.Context, spec hostsandbox.StageSpec) (hostsandbox.StageHandle, error) {
	if err := s.Preflight(ctx); err != nil {
		return hostsandbox.StageHandle{}, err
	}
	if len(spec.Argv) == 0 {
		return hostsandbox.StageHandle{}, errors.New("a stage needs a command")
	}
	name := containerName(spec.Name)
	argv := []string{string(s.engine), "run", "--detach", "--name", name,
		// Log retention is pinned rather than inherited from the host's
		// default, so that a transcript's availability does not depend on
		// how the engine was configured.
		"--log-driver=json-file", "--log-opt", "max-size=0",
		"--workdir", guestOf(spec, spec.Workdir)}
	argv = append(argv, s.ownership()...)
	for _, path := range spec.Paths {
		mode := "ro"
		if path.Access == hostsandbox.ReadWrite {
			mode = "rw"
		}
		argv = append(argv, "--volume", fmt.Sprintf("%s:%s:%s", path.Host, path.Destination(), mode))
	}
	if spec.Network.Mode == hostsandbox.NetworkNone {
		argv = append(argv, "--network", "none")
	}
	// Credentials go through an environment file rather than --env, because
	// engine arguments are visible in the process table to every user on
	// the machine.
	envFile, err := s.writeEnvFile(spec)
	if err != nil {
		return hostsandbox.StageHandle{}, err
	}
	if envFile != "" {
		defer func() { _ = os.Remove(envFile) }()
		argv = append(argv, "--env-file", envFile)
	}
	for name, value := range spec.Env {
		if contains(spec.EnvFile, name) {
			continue
		}
		argv = append(argv, "--env", name+"="+value)
	}
	argv = append(argv, s.config.Image)
	argv = append(argv, spec.Argv...)

	if _, err := s.config.Run(ctx, argv, nil); err != nil {
		return hostsandbox.StageHandle{}, fmt.Errorf("start stage %s: %w", spec.Name, err)
	}
	return hostsandbox.StageHandle{Driver: sandbox.Container, ID: name}, nil
}

// ownership returns the uid mapping arguments, which differ by engine.
// Rootless Podman maps the container user onto the invoking user, and Docker
// is given the uid explicitly.
func (s *Sandbox) ownership() []string {
	if s.engine == Podman {
		return []string{"--userns=keep-id"}
	}
	return []string{"--user", fmt.Sprintf("%d:%d", s.config.UID, s.config.GID)}
}

// writeEnvFile writes the stage's credentials to a private file, which is
// removed as soon as the container is running. Note that `inspect` still
// shows the value to anyone who can talk to the engine, which is the same
// person who owns the credential.
func (s *Sandbox) writeEnvFile(spec hostsandbox.StageSpec) (string, error) {
	if len(spec.EnvFile) == 0 {
		return "", nil
	}
	var body bytes.Buffer
	for _, name := range spec.EnvFile {
		value, ok := spec.Env[name]
		if !ok {
			return "", fmt.Errorf("stage %s names credential %s and does not carry it", spec.Name, name)
		}
		fmt.Fprintf(&body, "%s=%s\n", name, value)
	}
	file, err := os.CreateTemp("", "replichai-env-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := file.Write(body.Bytes()); err != nil {
		return "", err
	}
	return file.Name(), nil
}

// Observe reads the container's state. A container the engine no longer holds
// is reported as gone, not finished, because an unknown exit must not be
// reported as success.
func (s *Sandbox) Observe(ctx context.Context, handle hostsandbox.StageHandle) (hostsandbox.StageStatus, error) {
	if err := hostsandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return hostsandbox.StageStatus{}, err
	}
	output, err := s.config.Run(ctx, []string{string(s.engine), "inspect", "--format",
		"{{.State.Running}}|{{.State.ExitCode}}", handle.ID}, nil)
	if err != nil {
		// A container the engine no longer holds is gone. That is an
		// observation rather than a failure to observe, and gone is not
		// finished, so it is never reported as success.
		//nolint:nilerr // a missing container is an observation
		return hostsandbox.StageStatus{Gone: true}, nil
	}
	running, code, _ := strings.Cut(strings.TrimSpace(string(output)), "|")
	if strings.EqualFold(running, "true") {
		return hostsandbox.StageStatus{Running: true}, nil
	}
	exit, err := strconv.Atoi(strings.TrimSpace(code))
	if err != nil {
		// An exit code the engine did not report is unknown, and an unknown
		// exit must not be reported as success.
		//nolint:nilerr // an unreadable exit code is gone, not zero
		return hostsandbox.StageStatus{Gone: true}, nil
	}
	return hostsandbox.StageStatus{ExitCode: exit}, nil
}

// Output reads the container's log from an offset. The engine writes the log
// outside the container's filesystem, so a stage cannot rewrite its own
// transcript. This is the same guarantee the host driver gets from a denied
// path.
func (s *Sandbox) Output(ctx context.Context, handle hostsandbox.StageHandle, offset int64) (io.ReadCloser, error) {
	if err := hostsandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return nil, err
	}
	body, err := s.config.Run(ctx, []string{string(s.engine), "logs", handle.ID}, nil)
	if err != nil {
		// A container with no log yet, or one already removed, yields an
		// empty read rather than an error. The caller polls, and an error
		// here would fail a session over an empty transcript.
		//nolint:nilerr // an unreadable log is an empty read, not a failure
		return io.NopCloser(strings.NewReader("")), nil
	}
	if offset >= int64(len(body)) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return io.NopCloser(bytes.NewReader(body[offset:])), nil
}

// Stop ends a running container. Stopping one that has already exited is not
// an error, because the controller cancels without knowing the state.
func (s *Sandbox) Stop(ctx context.Context, handle hostsandbox.StageHandle) error {
	if err := hostsandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return err
	}
	_, _ = s.config.Run(ctx, []string{string(s.engine), "stop", "--time", "10", handle.ID}, nil)
	return nil
}

// Discard removes the container. It never touches the session directory,
// which belongs to the runtime and may not have been pushed yet.
func (s *Sandbox) Discard(ctx context.Context, handle hostsandbox.StageHandle) error {
	if err := hostsandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return err
	}
	_, _ = s.config.Run(ctx, []string{string(s.engine), "rm", "--force", handle.ID}, nil)
	return nil
}

// Version reports the harness version the image holds.
func (s *Sandbox) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no version command")
	}
	full := append([]string{string(s.engine), "run", "--rm", s.config.Image}, argv...)
	output, err := s.config.Run(ctx, full, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func containerName(stage string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, stage)
	return "replichai-" + safe
}

// guestOf maps a host path to the path the stage sees, so that a working
// directory given as a host path reaches the container as its mount
// destination.
func guestOf(spec hostsandbox.StageSpec, host string) string {
	for _, path := range spec.Paths {
		if path.Host == host {
			return path.Destination()
		}
	}
	return host
}

func splitList(value string) map[string]bool {
	out := make(map[string]bool)
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func realRunner(ctx context.Context, argv []string, stdin io.Reader) ([]byte, error) {
	//nolint:gosec // the argv is this package's own engine command, built from
	// a fixed verb table and a StageSpec the runtime computed; nothing in it
	// comes from a model or from a paper.
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdin = stdin
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}
