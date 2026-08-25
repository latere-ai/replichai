package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox/sandboxtest"
)

// found is a Look that resolves only the named executables, so a preflight can
// be driven on a machine that has none of them.
func found(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

// Every way this machine can be unready produces a refusal naming what to
// install, and the container alternative. None of it needs srt present.
func TestPreflightRefusalsAreActionable(t *testing.T) {
	cases := []struct {
		name     string
		platform sandbox.Platform
		look     func(string) (string, error)
		want     []string
	}{
		{"no srt on macOS", sandbox.Darwin, found(), []string{"srt is not installed", "npm install -g @anthropic-ai/sandbox-runtime", "--sandbox container"}},
		{"no bubblewrap on Linux", sandbox.Linux, found("srt", "rg"), []string{"bubblewrap", "socat", "apt-get"}},
		{"no ripgrep on macOS", sandbox.Darwin, found("srt"), []string{"rg", "brew install ripgrep"}},
		{"no harness", sandbox.Darwin, found("srt", "rg"), []string{"claude", "npm install -g @anthropic-ai/claude-code"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			driver := New(Config{Platform: c.platform, Look: c.look, HarnessBinary: "claude", Home: "/home/x"})
			err := driver.Preflight(context.Background())
			if !errors.Is(err, sandbox.ErrNotReady) {
				t.Fatalf("Preflight = %v", err)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message missing %q:\n%s", want, err)
				}
			}
		})
	}
	// Everything present is ready.
	ready := New(Config{Platform: sandbox.Darwin, Look: found("srt", "rg", "claude"), HarnessBinary: "claude", Home: "/home/x"})
	if err := ready.Preflight(context.Background()); err != nil {
		t.Fatalf("a ready machine was refused: %v", err)
	}
	// Windows is refused whatever is installed: srt marks it alpha, and a
	// boundary that is alpha is not a boundary.
	windows := New(Config{Platform: sandbox.Windows, Look: found("srt", "rg", "claude"), HarnessBinary: "claude"})
	err := windows.Preflight(context.Background())
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "WSL2") {
		t.Fatalf("windows = %v", err)
	}
}

// Every entry in the deny list reaches the settings file. A default that
// widens by accident is how a rule like this fails, so the table is asserted
// rather than trusted.
func TestEveryDeniedPathReachesTheSettings(t *testing.T) {
	home := "/Users/x"
	settings := Render(Profile{
		Home:       home,
		Reads:      []string{"/Users/x/.claude", "/s/workspace"},
		Writes:     []string{"/s/workspace", "/s/tmp"},
		DenyWrites: []string{"/s/session"},
		Network:    sandbox.Network{Mode: sandbox.NetworkNone},
	})
	for _, entry := range AlwaysDenyRead {
		want := strings.Replace(entry, "~", home, 1)
		if !slices.Contains(settings.Filesystem.DenyRead, want) {
			t.Errorf("deny list entry %q did not reach the settings", want)
		}
	}
	// The home directory itself is denied, so anything not named in
	// allowRead is unreadable rather than merely unlisted.
	if !slices.Contains(settings.Filesystem.DenyRead, home) {
		t.Errorf("the home directory is not denied: %v", settings.Filesystem.DenyRead)
	}
	// The harness's own configuration is the one allowance inside it.
	if !slices.Contains(settings.Filesystem.AllowRead, "/Users/x/.claude") {
		t.Errorf("the harness configuration is not readable: %v", settings.Filesystem.AllowRead)
	}
	// The evidence directory is denied for writing even though nothing
	// grants it: a future writable parent would otherwise carry it.
	if !slices.Contains(settings.Filesystem.DenyWrite, "/s/session") {
		t.Errorf("the evidence directory is writable: %+v", settings.Filesystem)
	}
	body, err := settings.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the settings file is not valid JSON: %v", err)
	}
	if _, ok := decoded["filesystem"]; !ok {
		t.Fatalf("the settings file has no filesystem section:\n%s", body)
	}
}

// The three network modes render as three different policies, and none of them
// leaves egress unstated.
func TestNetworkModesRender(t *testing.T) {
	none := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkNone}})
	if len(none.Network.AllowedDomains) != 0 {
		t.Errorf("a closed sandbox allows %v", none.Network.AllowedDomains)
	}
	list := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkAllowlist, Domains: []string{"arxiv.org", "github.com"}}})
	if !slices.Contains(list.Network.AllowedDomains, "arxiv.org") || len(list.Network.AllowedDomains) != 2 {
		t.Errorf("allowlist = %v", list.Network.AllowedDomains)
	}
	open := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkOpen}})
	if len(open.Network.AllowedDomains) != 1 || open.Network.AllowedDomains[0] != "*" {
		t.Errorf("open = %v", open.Network.AllowedDomains)
	}
	// A unix socket is a hole straight through the boundary, and local
	// binding is a service the sandbox is talking to from outside it.
	if open.Network.AllowAllUnixSockets || open.Network.AllowLocalBinding {
		t.Error("the network policy leaves a hole open by default")
	}
}

// The stage's own paths become its access, and the judge's read-only workspace
// stays read-only.
func TestTheProfileIsBuiltFromTheStagePaths(t *testing.T) {
	driver := New(Config{Platform: sandbox.Darwin, Home: "/Users/x", Look: found("srt", "rg"), ConfigDirs: []string{"/Users/x/.claude"}})
	spec := sandbox.StageSpec{
		Name: "judge-1",
		Paths: []sandbox.Path{
			{Host: "/s/workspace", Guest: "/workspace", Access: sandbox.ReadOnly},
			{Host: "/s/grade", Guest: "/grade", Access: sandbox.ReadWrite},
		},
		Network: sandbox.Network{Mode: sandbox.NetworkOpen},
		LogPath: "/s/session/raw/judge-1.log",
	}
	settings := Render(driver.profile(spec))
	if slices.Contains(settings.Filesystem.AllowWrite, "/s/workspace") {
		t.Fatalf("the judge can write what it grades: %v", settings.Filesystem.AllowWrite)
	}
	if !slices.Contains(settings.Filesystem.AllowRead, "/s/workspace") {
		t.Fatalf("the judge cannot read what it grades: %v", settings.Filesystem.AllowRead)
	}
	if !slices.Contains(settings.Filesystem.AllowWrite, "/s/grade") {
		t.Fatalf("the judge cannot write its verdict: %v", settings.Filesystem.AllowWrite)
	}
	if !slices.Contains(settings.Filesystem.DenyWrite, "/s/session") {
		t.Fatalf("the transcript is writable: %v", settings.Filesystem.DenyWrite)
	}
}

// A credential is never rendered into a command line: it reaches the stage
// through the launcher's environment, where the process table does not show it.
func TestCredentialsDoNotReachTheCommandLine(t *testing.T) {
	driver := New(Config{Platform: sandbox.Darwin, Home: t.TempDir(), Look: found("srt", "rg")})
	spec := sandbox.StageSpec{
		Name:    "worker-0",
		Argv:    []string{"claude", "--print", "--", "reproduce it"},
		Env:     map[string]string{"ANTHROPIC_API_KEY": "sk-secret", "TMPDIR": "/s/tmp"},
		EnvFile: []string{"ANTHROPIC_API_KEY"},
	}
	for _, entry := range driver.environment(spec) {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") && strings.Contains(entry, "sk-secret") {
			// The environment is the intended channel; the command line
			// is not.
			goto commandline
		}
	}
	t.Fatal("the credential did not reach the stage at all")
commandline:
	joined := strings.Join(spec.Argv, " ")
	if strings.Contains(joined, "sk-secret") {
		t.Fatalf("the credential is in the command line: %s", joined)
	}
}

// srt is not on every machine, so the contract suite runs where it is and says
// why it did not where it is not.
func TestHostDriverMeetsTheSandboxContract(t *testing.T) {
	driver := New(Config{Home: os.Getenv("HOME")})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Skipf("the host sandbox is not installed here, so its contract is unchecked:\n%v", err)
	}
	sandboxtest.Run(t, func(t *testing.T) *sandboxtest.Subject {
		return &sandboxtest.Subject{
			Driver: New(Config{Home: os.Getenv("HOME")}),
			Dir:    t.TempDir(),
			Print: func(text string, code int) []string {
				return []string{"/bin/sh", "-c", fmt.Sprintf("printf %%s %q; exit %d", text, code)}
			},
			Wait: func(d time.Duration) []string {
				return []string{"/bin/sh", "-c", fmt.Sprintf("sleep %d", int(d.Seconds()))}
			},
		}
	})
}

// A stage outlives the process that launched it, and its exit status is
// recoverable afterwards: a reaped child tells nobody anything, so the wrapper
// records it. This runs without srt, because it is the driver's own bookkeeping
// rather than its isolation.
func TestAStageOutlivesItsLauncher(t *testing.T) {
	// srt stands in as /bin/echo and its dependencies as anything on PATH:
	// what is under test here is the driver's own bookkeeping — a detached
	// stage and a recoverable exit status — not srt's isolation.
	stub := func(name string) (string, error) {
		if name == "srt" {
			return "/bin/echo", nil
		}
		return "/bin/echo", nil
	}
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Look: stub})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	dir := t.TempDir()
	logDir := filepath.Join(dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spec := sandbox.StageSpec{
		Name:    "worker-0",
		Argv:    []string{"ignored-because-srt-is-echo"},
		Workdir: dir,
		Network: sandbox.Network{Mode: sandbox.NetworkNone},
		LogPath: filepath.Join(logDir, "worker-0.log"),
	}
	handle, err := driver.Launch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !strings.HasPrefix(handle.ID, "pid:") || handle.Driver != sandbox.Host {
		t.Fatalf("handle = %+v", handle)
	}
	deadline := time.Now().Add(10 * time.Second)
	var status sandbox.StageStatus
	for time.Now().Before(deadline) {
		status, err = driver.Observe(context.Background(), handle)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !status.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.Running || status.Gone {
		t.Fatalf("status = %+v", status)
	}
	if status.ExitCode != 0 {
		t.Fatalf("exit = %d", status.ExitCode)
	}
	// The status survives being asked again from a driver that never saw
	// the launch, which is what a fresh process does.
	fresh := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Look: driver.config.Look})
	again, err := fresh.Observe(context.Background(), handle)
	if err != nil || again.Running || again.ExitCode != 0 {
		t.Fatalf("a fresh driver lost the exit status: %+v, %v", again, err)
	}
	// A handle naming a pid that never was is gone, not running: an
	// unknowable exit must not read as success.
	lost := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:999999:worker-9:" + spec.LogPath}
	if got, err := fresh.Observe(context.Background(), lost); err != nil || !got.Gone {
		t.Fatalf("a lost stage = %+v, %v", got, err)
	}
}
