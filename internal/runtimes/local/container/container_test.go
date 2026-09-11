// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// engine is a scripted engine. It records every command and answers from a
// table, so that the driver's argv and its refusals can be tested with no
// engine installed.
type engine struct {
	calls   [][]string
	answers map[string]string
	fail    map[string]error
}

func newEngine() *engine {
	return &engine{
		answers: map[string]string{
			"info":          "arm64",
			"image inspect": "pip,npm,apt|python 3.13.2",
			"run":           "abc123",
			"inspect":       "false|0",
			"logs":          "hello from the image",
		},
		fail: map[string]error{},
	}
}

func (e *engine) run(_ context.Context, argv []string, _ io.Reader) ([]byte, error) {
	e.calls = append(e.calls, argv)
	command := strings.Join(argv[1:], " ")
	for key, err := range e.fail {
		if strings.Contains(command, key) {
			return nil, err
		}
	}
	// The longest matching prefix wins, so "run --rm" is not answered by
	// "run".
	best, answer := "", ""
	for key, scripted := range e.answers {
		if strings.HasPrefix(command, key) && len(key) > len(best) {
			best, answer = key, scripted
		}
	}
	if best != "" {
		return []byte(answer), nil
	}
	return nil, fmt.Errorf("unscripted command %v", argv)
}

func (e *engine) lastMatching(prefix string) []string {
	for _, v := range slices.Backward(e.calls) {
		if strings.HasPrefix(strings.Join(v[1:], " "), prefix) {
			return v
		}
	}
	return nil
}

func found(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func build(t *testing.T, config Config, e *engine) *Sandbox {
	t.Helper()
	if config.Platform == "" {
		config.Platform = sandbox.Darwin
	}
	if config.Look == nil {
		config.Look = found("podman")
	}
	config.Run, config.UID, config.GID = e.run, 501, 20
	driver, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return driver
}

// TestTheThreeEngineFailuresAreDistinguished checks that the three common
// failures produce three different messages, each naming the host sandbox as
// the alternative.
func TestTheThreeEngineFailuresAreDistinguished(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		e := newEngine()
		_, err := New(context.Background(), Config{Platform: sandbox.Darwin, Look: found(), Run: e.run})
		if !errors.Is(err, sandbox.ErrNotReady) {
			t.Fatalf("New = %v", err)
		}
		for _, want := range []string{"brew install podman", "podman machine init", "--sandbox host"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q:\n%s", want, err)
			}
		}
	})
	t.Run("installed but not running", func(t *testing.T) {
		e := newEngine()
		e.fail["info"] = errors.New("cannot connect to podman socket")
		_, err := New(context.Background(), Config{Platform: sandbox.Darwin, Look: found("podman"), Run: e.run})
		if !errors.Is(err, sandbox.ErrNotReady) {
			t.Fatalf("New = %v", err)
		}
		for _, want := range []string{"podman machine start", "never initialised", "cannot connect"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q:\n%s", want, err)
			}
		}
	})
	t.Run("image unavailable", func(t *testing.T) {
		e := newEngine()
		e.fail["image inspect"] = errors.New("no such image")
		e.fail["pull"] = errors.New("network unreachable")
		_, err := New(context.Background(), Config{Platform: sandbox.Darwin, Look: found("podman"), Run: e.run})
		if !errors.Is(err, sandbox.ErrNotReady) {
			t.Fatalf("New = %v", err)
		}
		for _, want := range []string{"podman pull", "REPLICHAI_HARNESS_IMAGE"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q:\n%s", want, err)
			}
		}
	})
	t.Run("an absent image is pulled once and then read", func(t *testing.T) {
		e := newEngine()
		calls := 0
		e.answers["image inspect"] = "pip|python 3.13.2"
		original := e.run
		driver, err := New(context.Background(), Config{
			Platform: sandbox.Darwin, Look: found("podman"),
			Run: func(ctx context.Context, argv []string, stdin io.Reader) ([]byte, error) {
				if strings.HasPrefix(strings.Join(argv[1:], " "), "image inspect") {
					calls++
					if calls == 1 {
						return nil, errors.New("no such image")
					}
				}
				if strings.HasPrefix(strings.Join(argv[1:], " "), "pull") {
					return []byte("pulled"), nil
				}
				return original(ctx, argv, stdin)
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if !driver.Capabilities().PackageManagers["pip"] {
			t.Fatal("the pulled image's declaration was not read")
		}
	})
}

// TestANamedEngineDoesNotFallBack checks that a named engine that cannot run
// is an error rather than a silent fallback to the other engine. An operator
// who named an engine gets that engine or an error message.
func TestANamedEngineDoesNotFallBack(t *testing.T) {
	e := newEngine()
	_, err := New(context.Background(), Config{
		Platform: sandbox.Darwin, Engine: Docker, Look: found("podman"), Run: e.run,
	})
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("New = %v", err)
	}
	// When no engine is named, the two are tried in order.
	driver := build(t, Config{}, newEngine())
	if driver.Engine() != Podman {
		t.Fatalf("engine = %q", driver.Engine())
	}
}

// TestCapabilitiesComeFromTheImage checks that capabilities come from the
// image's label, and that an image which declares nothing fails construction
// rather than being given a default.
func TestCapabilitiesComeFromTheImage(t *testing.T) {
	driver := build(t, Config{}, newEngine())
	capabilities := driver.Capabilities()
	for _, manager := range []string{"pip", "npm", "apt"} {
		if !capabilities.PackageManagers[manager] {
			t.Errorf("the image's %q is missing: %v", manager, capabilities.PackageManagers)
		}
	}
	if driver.Toolchain() != "python 3.13.2" {
		t.Errorf("toolchain = %q", driver.Toolchain())
	}
	// The allowlist mode is not declared. Neither engine filters egress by
	// domain, and declaring a mode this driver cannot enforce would make the
	// capability declaration, which specifications are validated against,
	// incorrect.
	if capabilities.NetworkModes[sandbox.NetworkAllowlist] {
		t.Error("the container driver claims to enforce a domain allowlist")
	}
	if !capabilities.NetworkModes[sandbox.NetworkNone] || !capabilities.NetworkModes[sandbox.NetworkOpen] {
		t.Errorf("network modes = %v", capabilities.NetworkModes)
	}

	e := newEngine()
	e.answers["image inspect"] = "|"
	_, err := New(context.Background(), Config{Platform: sandbox.Darwin, Look: found("podman"), Run: e.run})
	if err == nil || !strings.Contains(err.Error(), ManagersLabel) {
		t.Fatalf("an image declaring nothing was accepted: %v", err)
	}
}

func stage() sandbox.StageSpec {
	return sandbox.StageSpec{
		Name:    "worker-0",
		Argv:    []string{"claude", "--print", "--", "reproduce it"},
		Workdir: "/s/workspace",
		Env:     map[string]string{"ANTHROPIC_API_KEY": "sk-secret", "TMPDIR": "/tmp-session"},
		EnvFile: []string{"ANTHROPIC_API_KEY"},
		Paths: []sandbox.Path{
			{Host: "/s/workspace", Guest: "/workspace", Access: sandbox.ReadWrite},
			{Host: "/s/tmp", Guest: "/tmp-session", Access: sandbox.ReadWrite},
		},
		Network: sandbox.Network{Mode: sandbox.NetworkOpen},
		LogPath: "/s/session/raw/worker-0.log",
	}
}

// TestLaunchArgvKeepsCredentialsAndEvidenceOut checks that a credential never
// reaches the command line, that the evidence directory is never mounted, and
// that ownership is mapped so the operator can push what was produced.
func TestLaunchArgvKeepsCredentialsAndEvidenceOut(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	if _, err := driver.Launch(context.Background(), stage()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	argv := e.lastMatching("run ")
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "sk-secret") {
		t.Fatalf("the credential is in the command line: %s", joined)
	}
	if !strings.Contains(joined, "--env-file") {
		t.Fatalf("the credential did not go through an environment file: %s", joined)
	}
	if !strings.Contains(joined, "--env TMPDIR=/tmp-session") {
		t.Errorf("an ordinary variable did not reach the stage: %s", joined)
	}
	for _, mount := range argv {
		if strings.HasPrefix(mount, "/s/session") || strings.Contains(mount, "/s/session:") {
			t.Fatalf("the evidence directory was mounted: %s", mount)
		}
	}
	if !strings.Contains(joined, "--volume /s/workspace:/workspace:rw") {
		t.Errorf("the workspace mount is wrong: %s", joined)
	}
	if !strings.Contains(joined, "--userns=keep-id") {
		t.Errorf("podman ownership is not mapped: %s", joined)
	}
	if !strings.Contains(joined, "--workdir /workspace") {
		t.Errorf("the working directory was not mapped into the container: %s", joined)
	}
	if !strings.Contains(joined, "--log-driver=json-file") {
		t.Errorf("log retention is inherited rather than pinned: %s", joined)
	}
}

// TestDockerMapsOwnershipByUID checks that Docker maps ownership by uid,
// because it has no keep-id option.
func TestDockerMapsOwnershipByUID(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{Engine: Docker, Look: found("docker")}, e)
	if _, err := driver.Launch(context.Background(), stage()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	joined := strings.Join(e.lastMatching("run "), " ")
	if !strings.Contains(joined, "--user 501:20") {
		t.Fatalf("docker ownership = %s", joined)
	}
}

// TestTheJudgesWorkspaceIsMountedReadOnly checks that a judge's read-only
// workspace reaches the engine as a read-only mount.
func TestTheJudgesWorkspaceIsMountedReadOnly(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	spec := stage()
	spec.Name = "judge-1"
	spec.Paths = []sandbox.Path{
		{Host: "/s/workspace", Guest: "/workspace", Access: sandbox.ReadOnly},
		{Host: "/s/grade", Guest: "/grade", Access: sandbox.ReadWrite},
	}
	spec.Workdir = "/s/grade"
	if _, err := driver.Launch(context.Background(), spec); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	joined := strings.Join(e.lastMatching("run "), " ")
	if !strings.Contains(joined, "/s/workspace:/workspace:ro") {
		t.Fatalf("the judge can write what it grades: %s", joined)
	}
	if !strings.Contains(joined, "/s/grade:/grade:rw") {
		t.Fatalf("the judge cannot write its verdict: %s", joined)
	}
}

// TestALostContainerIsGoneNotFinished checks that a container the engine no
// longer holds is reported as gone, not finished. Reporting an unknown exit as
// success is the defect recorded in 044.
func TestALostContainerIsGoneNotFinished(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	handle, err := driver.Launch(context.Background(), stage())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	e.answers["inspect"] = "true|0"
	if status, _ := driver.Observe(context.Background(), handle); !status.Running {
		t.Fatalf("a running container = %+v", status)
	}
	e.answers["inspect"] = "false|7"
	status, err := driver.Observe(context.Background(), handle)
	if err != nil || status.Running || status.ExitCode != 7 {
		t.Fatalf("an exited container = %+v, %v", status, err)
	}
	e.fail["inspect"] = errors.New("no such container")
	status, err = driver.Observe(context.Background(), handle)
	if err != nil || !status.Gone || status.Succeeded() {
		t.Fatalf("a lost container = %+v, %v", status, err)
	}
}

// TestOutputReadsFromAnOffset checks that Output reads from an offset, so
// that a poll appends rather than re-reading the whole log.
func TestOutputReadsFromAnOffset(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	handle, _ := driver.Launch(context.Background(), stage())
	stream, err := driver.Output(context.Background(), handle, 6)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	defer func() { _ = stream.Close() }()
	body, _ := io.ReadAll(stream)
	if string(body) != "from the image" {
		t.Fatalf("output from offset = %q", body)
	}
	stream, _ = driver.Output(context.Background(), handle, 9999)
	body, _ = io.ReadAll(stream)
	if len(body) != 0 {
		t.Fatalf("an offset past the end returned %q", body)
	}
}

// TestWindowsIsRefused checks that Windows is refused with a message naming
// WSL2 rather than the other driver, which is equally unavailable there.
func TestWindowsIsRefused(t *testing.T) {
	_, err := New(context.Background(), Config{Platform: sandbox.Windows, Look: found("podman"), Run: newEngine().run})
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "WSL2") {
		t.Fatalf("New = %v", err)
	}
}

// TestStopAndDiscardAreIdempotent checks that stopping and discarding are
// idempotent, and that neither touches the session directory, which belongs
// to the runtime and may not have been pushed yet.
func TestStopAndDiscardAreIdempotent(t *testing.T) {
	e := newEngine()
	e.answers["stop"] = ""
	e.answers["rm"] = ""
	driver := build(t, Config{}, e)
	handle, err := driver.Launch(context.Background(), stage())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for range 2 {
		if err := driver.Stop(context.Background(), handle); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := driver.Discard(context.Background(), handle); err != nil {
			t.Fatalf("Discard: %v", err)
		}
	}
	// Even when the engine fails both commands, the driver reports success.
	// The controller cancels without knowing the state, and a container that
	// is already gone is the outcome it wanted.
	e.fail["stop"] = errors.New("no such container")
	e.fail["rm"] = errors.New("no such container")
	if err := driver.Stop(context.Background(), handle); err != nil {
		t.Fatalf("Stop on a gone container: %v", err)
	}
	if err := driver.Discard(context.Background(), handle); err != nil {
		t.Fatalf("Discard on a gone container: %v", err)
	}
	// A handle from the other driver is refused by both.
	other := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:1@2"}
	if err := driver.Stop(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
		t.Errorf("Stop = %v", err)
	}
	if err := driver.Discard(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
		t.Errorf("Discard = %v", err)
	}
}

// TestVersionIsReadFromTheImage checks that the harness version is read from
// the image, so that a run records what the image holds rather than what is
// installed on the operator's machine.
func TestVersionIsReadFromTheImage(t *testing.T) {
	e := newEngine()
	e.answers["run --rm"] = "2.1.4 (Claude Code)\n"
	driver := build(t, Config{}, e)
	version, err := driver.Version(context.Background(), []string{"claude", "--version"})
	if err != nil || version != "2.1.4 (Claude Code)" {
		t.Fatalf("Version = %q, %v", version, err)
	}
	if _, err := driver.Version(context.Background(), nil); err == nil {
		t.Fatal("an empty version command was accepted")
	}
	joined := strings.Join(e.lastMatching("run --rm"), " ")
	if !strings.Contains(joined, DefaultImage) {
		t.Errorf("the version was not read from the image: %s", joined)
	}
}

// TestLaunchRefusesAnEmptyStageAndAStoppedEngine checks that a stage with no
// command is refused, and that a machine whose engine stopped between
// construction and launch is refused with instructions.
func TestLaunchRefusesAnEmptyStageAndAStoppedEngine(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	empty := stage()
	empty.Argv = nil
	if _, err := driver.Launch(context.Background(), empty); err == nil {
		t.Fatal("a stage with no command was launched")
	}
	e.fail["info"] = errors.New("cannot connect to podman socket")
	if _, err := driver.Launch(context.Background(), stage()); !errors.Is(err, sandbox.ErrNotReady) {
		t.Fatalf("Launch on a stopped engine = %v", err)
	}
}

// TestAStageMissingItsCredentialIsRefused checks that a stage naming a
// credential it does not carry is refused rather than started without it. A
// run that fails after it has started for lack of a key is worse than one
// that never starts.
func TestAStageMissingItsCredentialIsRefused(t *testing.T) {
	e := newEngine()
	driver := build(t, Config{}, e)
	spec := stage()
	spec.Env = map[string]string{"TMPDIR": "/tmp-session"}
	if _, err := driver.Launch(context.Background(), spec); err == nil {
		t.Fatal("a stage naming a credential it does not carry was launched")
	}
	// A closed network reaches the engine as a closed network.
	spec = stage()
	spec.Network = sandbox.Network{Mode: sandbox.NetworkNone}
	if _, err := driver.Launch(context.Background(), spec); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !strings.Contains(strings.Join(e.lastMatching("run "), " "), "--network none") {
		t.Error("a closed network did not reach the engine")
	}
}
