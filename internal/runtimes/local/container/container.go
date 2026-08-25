// Package container is the opt-in sandbox driver: an OCI container, Podman by
// default and Docker supported, giving a toolchain pinned identically on every
// machine (045c).
//
// It is the second driver, not the first. Pinning is what CI and cross-operator
// comparison need; it is not what a first local run needs, and it costs an
// image, a virtual machine on macOS, and a raw API key instead of the login the
// operator already has.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// DefaultImage is the harness image, pinned by tag here and by digest in a
// deployment. It holds the toolchain and both harnesses.
const DefaultImage = "ghcr.io/latere-ai/replichai-harness:latest"

// Labels the image declares about itself. Capabilities are read from the image
// rather than repeated in this code, so a custom image with a different
// toolchain declares its own and a specification naming a manager it lacks is
// refused rather than failing mid-run.
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

// Runner executes an engine command. It is a seam so the driver is testable
// without an engine, and so both engines go through one place.
type Runner func(ctx context.Context, argv []string, stdin io.Reader) ([]byte, error)

// Config is what a composition root hands this driver.
type Config struct {
	// Engine names the engine to use. Empty selects podman, then docker.
	// A named engine that is unusable is an error, never a fallback: an
	// operator who named one gets that one or a message.
	Engine Engine
	// Image is the harness image.
	Image string
	// Platform is the operating system, a parameter so refusals are
	// testable on one machine.
	Platform sandbox.Platform
	// UID and GID are the ownership a stage writes with, so the operator
	// can read and push what the run produced.
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
	capabilities sandbox.Capabilities
	toolchain    string
}

// New probes the machine and the image, and returns a driver only once both
// answer. Probing at construction is what lets an operator learn their
// environment is wrong before a paper is fetched or a repository created.
func New(ctx context.Context, config Config) (*Sandbox, error) {
	if config.Platform == "" {
		config.Platform = sandbox.CurrentPlatform()
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
	if config.Platform == sandbox.Windows {
		return nil, sandbox.NotReady(config.Platform, sandbox.Container, sandbox.ConditionUnsupported, "podman")
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
func (s *Sandbox) Name() sandbox.DriverName { return sandbox.Container }

// Engine is the engine this driver settled on.
func (s *Sandbox) Engine() Engine { return s.engine }

// Toolchain is what the image says it holds, recorded in the environment
// record so two runs on different machines can be compared.
func (s *Sandbox) Toolchain() string { return s.toolchain }

// selectEngine picks the engine and proves it works. A named engine that
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
		first = sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionMissing, string(Podman))
	}
	return "", first
}

// probe distinguishes the three ordinary failures, because they need three
// different sentences: not installed, installed but not running, and running
// but unable to supply the image.
func (s *Sandbox) probe(ctx context.Context, engine Engine) error {
	if _, err := s.config.Look(string(engine)); err != nil {
		return sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionMissing, string(engine))
	}
	if _, err := s.config.Run(ctx, []string{string(engine), "info", "--format", "{{.Host.Arch}}"}, nil); err != nil {
		notReady := sandbox.NotReady(s.config.Platform, sandbox.Container, sandbox.ConditionStopped, string(engine))
		notReady.Detail = err.Error()
		return notReady
	}
	return nil
}

// readImage reads what the image declares about itself, pulling it first where
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
	s.capabilities = sandbox.Capabilities{
		PackageManagers: declared,
		// Neither engine filters egress by domain natively; doing it
		// properly needs a proxy the container is forced through, which
		// is what srt already provides on the host. Declaring a mode this
		// driver cannot enforce would be a lie a specification is
		// validated against.
		NetworkModes:       map[string]bool{sandbox.NetworkNone: true, sandbox.NetworkOpen: true},
		MaxAttachmentBytes: 64 << 20,
	}
	return nil
}

// Preflight re-checks the engine, because a machine can be stopped between
// construction and a launch.
func (s *Sandbox) Preflight(ctx context.Context) error { return s.probe(ctx, s.engine) }

// Capabilities is what the image declares plus what the engine enforces.
func (s *Sandbox) Capabilities() sandbox.Capabilities { return s.capabilities }

// Launch starts one stage as a detached container. Ownership is mapped so the
// operator can read and push what the run produced: a rootless container
// writing the workspace as root leaves a directory the push cannot use, and
// that failure would surface later and somewhere else.
func (s *Sandbox) Launch(ctx context.Context, spec sandbox.StageSpec) (sandbox.StageHandle, error) {
	if err := s.Preflight(ctx); err != nil {
		return sandbox.StageHandle{}, err
	}
	if len(spec.Argv) == 0 {
		return sandbox.StageHandle{}, errors.New("a stage needs a command")
	}
	name := containerName(spec.Name)
	argv := []string{string(s.engine), "run", "--detach", "--name", name,
		// Retention is pinned rather than inherited from the host's
		// default, so a transcript's availability does not depend on how
		// somebody configured their engine.
		"--log-driver=json-file", "--log-opt", "max-size=0",
		"--workdir", guestOf(spec, spec.Workdir)}
	argv = append(argv, s.ownership()...)
	for _, path := range spec.Paths {
		mode := "ro"
		if path.Access == sandbox.ReadWrite {
			mode = "rw"
		}
		argv = append(argv, "--volume", fmt.Sprintf("%s:%s:%s", path.Host, path.Destination(), mode))
	}
	if spec.Network.Mode == sandbox.NetworkNone {
		argv = append(argv, "--network", "none")
	}
	// Credentials go through an environment file rather than --env, because
	// engine arguments are visible in the process table to every user on
	// the machine.
	envFile, err := s.writeEnvFile(spec)
	if err != nil {
		return sandbox.StageHandle{}, err
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
		return sandbox.StageHandle{}, fmt.Errorf("start stage %s: %w", spec.Name, err)
	}
	return sandbox.StageHandle{Driver: sandbox.Container, ID: name}, nil
}

// ownership is the uid mapping, which differs by engine: rootless Podman maps
// the container user onto the invoking one, and Docker is told the uid.
func (s *Sandbox) ownership() []string {
	if s.engine == Podman {
		return []string{"--userns=keep-id"}
	}
	return []string{"--user", fmt.Sprintf("%d:%d", s.config.UID, s.config.GID)}
}

// writeEnvFile puts the stage's credentials in a private file, which is
// removed as soon as the container is running. `inspect` still shows the value
// to whoever can talk to the engine, which is the same person who owns the
// credential — stated rather than papered over.
func (s *Sandbox) writeEnvFile(spec sandbox.StageSpec) (string, error) {
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
// is gone, not finished: an unknowable exit must not read as success.
func (s *Sandbox) Observe(ctx context.Context, handle sandbox.StageHandle) (sandbox.StageStatus, error) {
	if err := sandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return sandbox.StageStatus{}, err
	}
	output, err := s.config.Run(ctx, []string{string(s.engine), "inspect", "--format",
		"{{.State.Running}}|{{.State.ExitCode}}", handle.ID}, nil)
	if err != nil {
		return sandbox.StageStatus{Gone: true}, nil
	}
	running, code, _ := strings.Cut(strings.TrimSpace(string(output)), "|")
	if strings.EqualFold(running, "true") {
		return sandbox.StageStatus{Running: true}, nil
	}
	exit, err := strconv.Atoi(strings.TrimSpace(code))
	if err != nil {
		return sandbox.StageStatus{Gone: true}, nil
	}
	return sandbox.StageStatus{ExitCode: exit}, nil
}

// Output reads the container's log from an offset. The log is written by the
// engine, outside the container's filesystem, so a stage cannot rewrite its
// own transcript — the same guarantee the host driver gets from a denied path.
func (s *Sandbox) Output(ctx context.Context, handle sandbox.StageHandle, offset int64) (io.ReadCloser, error) {
	if err := sandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return nil, err
	}
	body, err := s.config.Run(ctx, []string{string(s.engine), "logs", handle.ID}, nil)
	if err != nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	if offset >= int64(len(body)) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return io.NopCloser(bytes.NewReader(body[offset:])), nil
}

// Stop ends a running container. Stopping one that has already exited is not
// an error: the controller cancels without knowing the state.
func (s *Sandbox) Stop(ctx context.Context, handle sandbox.StageHandle) error {
	if err := sandbox.MatchHandle(sandbox.Container, handle); err != nil {
		return err
	}
	_, _ = s.config.Run(ctx, []string{string(s.engine), "stop", "--time", "10", handle.ID}, nil)
	return nil
}

// Discard removes the container. It never touches the session directory: that
// belongs to the runtime, and the push may not have happened yet.
func (s *Sandbox) Discard(ctx context.Context, handle sandbox.StageHandle) error {
	if err := sandbox.MatchHandle(sandbox.Container, handle); err != nil {
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

// guestOf maps a host path to what the stage sees, so a working directory
// stated as a host path reaches the container as its mount destination.
func guestOf(spec sandbox.StageSpec, host string) string {
	for _, path := range spec.Paths {
		if path.Host == host {
			return path.Destination()
		}
	}
	return host
}

func splitList(value string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func realRunner(ctx context.Context, argv []string, stdin io.Reader) ([]byte, error) {
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
