// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// found returns a Look that resolves only the named executables, so that a
// preflight can be tested on a machine that has none of them.
func found(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

// TestPreflightRefusalsAreActionable checks that every way a machine can be
// unready produces a refusal that names what to install and the container
// alternative. None of the cases needs srt installed.
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
	// A machine with everything installed is ready.
	ready := New(Config{Platform: sandbox.Darwin, Look: found("srt", "rg", "claude"), HarnessBinary: "claude", Home: "/home/x"})
	if err := ready.Preflight(context.Background()); err != nil {
		t.Fatalf("a ready machine was refused: %v", err)
	}
	// Windows is refused regardless of what is installed, because srt marks
	// its Windows support as alpha, which is not an acceptable isolation
	// boundary.
	windows := New(Config{Platform: sandbox.Windows, Look: found("srt", "rg", "claude"), HarnessBinary: "claude"})
	err := windows.Preflight(context.Background())
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "WSL2") {
		t.Fatalf("windows = %v", err)
	}
}

// TestEveryDeniedPathReachesTheSettings checks that every entry in the deny
// list reaches the settings file. A rule like this fails when a default widens
// by accident, so the table is asserted rather than trusted.
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
	// allowRead is unreadable rather than only unlisted.
	if !slices.Contains(settings.Filesystem.DenyRead, home) {
		t.Errorf("the home directory is not denied: %v", settings.Filesystem.DenyRead)
	}
	// A .env file holds credentials by convention, and the guide says it is
	// denied. The table is what makes that true.
	if !slices.Contains(settings.Filesystem.DenyRead, "/Users/x/.env") {
		t.Errorf("~/.env is readable: %v", settings.Filesystem.DenyRead)
	}
	// The harness's own configuration is the only allowance inside the home
	// directory.
	if !slices.Contains(settings.Filesystem.AllowRead, "/Users/x/.claude") {
		t.Errorf("the harness configuration is not readable: %v", settings.Filesystem.AllowRead)
	}
	// The evidence directory is denied for writing even though nothing
	// grants it, because a future writable parent would otherwise include
	// it.
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

// TestNetworkModesRender checks that the network modes render as distinct
// policies, and that none of them leaves egress unspecified.
func TestNetworkModesRender(t *testing.T) {
	none := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkNone}})
	if len(none.Network.AllowedDomains) != 0 {
		t.Errorf("a closed sandbox allows %v", none.Network.AllowedDomains)
	}
	list := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkAllowlist, Domains: []string{"arxiv.org", "github.com"}}})
	if !slices.Contains(list.Network.AllowedDomains, "arxiv.org") || len(list.Network.AllowedDomains) != 2 {
		t.Errorf("allowlist = %v", list.Network.AllowedDomains)
	}
	// There is no open mode. srt refuses a wildcard, so a driver that
	// declared one would emit a profile srt rejects, which is what it did
	// until a real run encountered it.
	if New(Config{Platform: sandbox.Darwin}).Capabilities().NetworkModes[sandbox.NetworkOpen] {
		t.Error("the host driver claims it can leave egress unrestricted")
	}
	rendered := Render(Profile{Network: sandbox.Network{Mode: sandbox.NetworkOpen}})
	for _, domain := range rendered.Network.AllowedDomains {
		if strings.Contains(domain, "*") && domain == "*" {
			t.Errorf("a wildcard srt refuses reached the settings: %v", rendered.Network.AllowedDomains)
		}
	}
	// A unix socket bypasses the boundary, and local binding means the
	// sandbox is communicating with a service outside it.
	if list.Network.AllowAllUnixSockets || list.Network.AllowLocalBinding {
		t.Error("the network policy leaves a hole open by default")
	}
}

// TestTheProfileIsBuiltFromTheStagePaths checks that the stage's paths
// determine its access, and that the judge's read-only workspace stays
// read-only.
func TestTheProfileIsBuiltFromTheStagePaths(t *testing.T) {
	driver := New(Config{Platform: sandbox.Darwin, Home: "/Users/x", Look: found("srt", "rg")})
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

// TestCredentialsDoNotReachTheCommandLine checks that a credential is never
// rendered into a command line. It reaches the stage through the launcher's
// environment, which the process table does not show.
func TestCredentialsDoNotReachTheCommandLine(t *testing.T) {
	driver := New(Config{Platform: sandbox.Darwin, Home: t.TempDir(), Look: found("srt", "rg")})
	spec := sandbox.StageSpec{
		Name:    "worker-0",
		Argv:    []string{"claude", "--print", "--", "reproduce it"},
		Env:     map[string]string{"ANTHROPIC_API_KEY": "sk-secret", "TMPDIR": "/s/tmp"},
		EnvFile: []string{"ANTHROPIC_API_KEY"},
	}
	for _, entry := range driver.environment(sandbox.HostEnvironment(os.LookupEnv), spec) {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") && strings.Contains(entry, "sk-secret") {
			// The environment is the intended channel. The command line
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

// TestStopKillsAStageThatIgnoresSIGTERM checks that a stage which ignores
// SIGTERM is killed with SIGKILL, together with its process group, when the
// grace period elapses, so that nothing is left running when Stop returns.
// The stage appends to a marker file while it runs, so its death is observed
// directly rather than inferred from the leader alone.
func TestStopKillsAStageThatIgnoresSIGTERM(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "srt")
	// The shim stands in for srt: it drops `--settings <file> --` and execs
	// the command.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nshift 3\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	grace := 400 * time.Millisecond
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Lookup: os.LookupEnv, StopGrace: grace,
		Look: func(name string) (string, error) {
			if name == "srt" {
				return shim, nil
			}
			return "/bin/echo", nil
		}})
	dir := t.TempDir()
	logDir := filepath.Join(dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(dir, "ticks")
	spec := sandbox.StageSpec{
		Name: "worker-0", Workdir: dir,
		// The stage ignores SIGTERM and keeps appending to the marker, so a
		// Stop that only signalled and returned would leave it running.
		Argv:    []string{"/bin/sh", "-c", `trap "" TERM; while :; do echo tick >> "$MARK"; sleep 0.05; done`},
		Env:     map[string]string{"MARK": marker},
		Network: sandbox.Network{Mode: sandbox.NetworkNone},
		LogPath: filepath.Join(logDir, "worker-0.log"),
	}
	handle, err := driver.Launch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// Wait until the stage has started writing the marker.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the stage never started ticking: %v", err)
	}

	start := time.Now()
	if err := driver.Stop(context.Background(), handle); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed < grace {
		t.Errorf("Stop returned in %v, before the %v grace: it did not wait for the term to take", elapsed, grace)
	}
	// The stage is gone once the marker stops growing after the group is
	// killed.
	size := fileSize(t, marker)
	time.Sleep(300 * time.Millisecond)
	if grown := fileSize(t, marker); grown != size {
		t.Fatalf("the stage kept running after Stop: marker grew from %d to %d", size, grown)
	}
	status, err := driver.Observe(context.Background(), handle)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if status.Running {
		t.Fatalf("the stage is still running after Stop: %+v", status)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// TestHostDriverMeetsTheSandboxContract runs the contract suite where srt is
// installed, and reports why it was skipped where it is not.
func TestHostDriverMeetsTheSandboxContract(t *testing.T) {
	driver := New(Config{Home: os.Getenv("HOME")})
	if err := driver.Preflight(context.Background()); err != nil {
		t.Skipf("the host sandbox is not installed here, so its contract is unchecked:\n%v", err)
	}
	sandboxtest.Run(t, func(t *testing.T) *sandboxtest.Subject {
		return &sandboxtest.Subject{
			// The environment comes from the composition root, here as
			// elsewhere. A stage with no PATH resolves nothing, not even the
			// sandbox.
			Driver: New(Config{Home: os.Getenv("HOME"), Lookup: os.LookupEnv}),
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

// TestAStageOutlivesItsLauncher checks that a stage outlives the process that
// launched it and that its exit status can be recovered afterwards. The exit
// status of a reaped child is lost, so the wrapper records it. The test runs
// without srt, because it exercises the driver's bookkeeping rather than its
// isolation.
func TestAStageOutlivesItsLauncher(t *testing.T) {
	// /bin/echo stands in for srt and for its dependencies. What is under
	// test is the driver's bookkeeping, a detached stage and a recoverable
	// exit status, not srt's isolation.
	stub := func(name string) (string, error) {
		if name == "srt" {
			return "/bin/echo", nil
		}
		return "/bin/echo", nil
	}
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Look: stub, Lookup: os.LookupEnv})
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
	// The status is renamed into place rather than written directly to the
	// path it is read from. Written in place, it could be observed empty,
	// with the process exited and the file present but not yet written. That
	// would read as a stage the driver lost, and fail a session that
	// finished.
	if !strings.Contains(readScript(t, spec), "mv -f") {
		t.Fatalf("the stage's status is not written atomically:\n%s", readScript(t, spec))
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
	// The status can be read by a driver that never saw the launch, which is
	// what happens after a process restart.
	fresh := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Look: driver.config.Look})
	again, err := fresh.Observe(context.Background(), handle)
	if err != nil || again.Running || again.ExitCode != 0 {
		t.Fatalf("a fresh driver lost the exit status: %+v, %v", again, err)
	}
	// A handle naming a pid that never existed is reported as gone, not
	// running. An unknown exit must not be reported as success.
	lost := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:999999:worker-9:" + spec.LogPath}
	if got, err := fresh.Observe(context.Background(), lost); err != nil || !got.Gone {
		t.Fatalf("a lost stage = %+v, %v", got, err)
	}
}

// readScript returns the wrapper script the driver would run for a stage, so
// that a test can check how it records its exit without racing the stage.
func readScript(t *testing.T, spec sandbox.StageSpec) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(spec.LogPath))
	if err != nil {
		t.Fatalf("read stage directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	// The settings file is written beside the log, which shows that the
	// stage's profile was built and not only that the stage was launched.
	if !slices.Contains(names, spec.Name+".srt.json") {
		t.Fatalf("no sandbox profile was written: %v", names)
	}
	return wrapperScript(spec.Name, spec.Argv, filepath.Join(filepath.Dir(spec.LogPath), spec.Name+".status"))
}

// TestStopAndDiscardTolerateAGoneStage checks that stopping and discarding a
// stage that has already finished, or one this driver never launched, both
// succeed. The controller cancels and releases without knowing the stage's
// state.
func TestStopAndDiscardTolerateAGoneStage(t *testing.T) {
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Lookup: os.LookupEnv,
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	dir := t.TempDir()
	handle := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:999999:worker-9:" + filepath.Join(dir, "worker-9.log")}
	for range 2 {
		if err := driver.Stop(context.Background(), handle); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := driver.Discard(context.Background(), handle); err != nil {
			t.Fatalf("Discard: %v", err)
		}
	}
	// A handle from the other driver is refused rather than acted on. Both
	// drivers' handles are opaque strings.
	other := sandbox.StageHandle{Driver: sandbox.Container, ID: "replichai-x"}
	if err := driver.Stop(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
		t.Errorf("Stop = %v", err)
	}
	if _, err := driver.Output(context.Background(), other, 0); !errors.Is(err, sandbox.ErrMismatch) {
		t.Errorf("Output = %v", err)
	}
	// A malformed handle is refused rather than parsed into an unintended
	// pid.
	malformed := sandbox.StageHandle{Driver: sandbox.Host, ID: "not-a-handle"}
	if _, err := driver.Observe(context.Background(), malformed); !errors.Is(err, sandbox.ErrNotReady) {
		t.Errorf("Observe = %v", err)
	}
}

// TestOutputReadsTheStageLogFromAnOffset checks that the stage's output is
// readable from an offset, from a file the stage cannot write.
func TestOutputReadsTheStageLogFromAnOffset(t *testing.T) {
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Lookup: os.LookupEnv,
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	dir := t.TempDir()
	logPath := filepath.Join(dir, "worker-0.log")
	if err := os.WriteFile(logPath, []byte("hello sandbox"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	handle := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:1:worker-0:" + logPath}
	stream, err := driver.Output(context.Background(), handle, 6)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	defer func() { _ = stream.Close() }()
	body := make([]byte, 32)
	n, _ := stream.Read(body)
	if string(body[:n]) != "sandbox" {
		t.Fatalf("output from offset = %q", body[:n])
	}
	// A stage that has written nothing yet reads empty rather than failing,
	// because the caller polls.
	absent := sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:1:worker-1:" + filepath.Join(dir, "absent.log")}
	stream, err = driver.Output(context.Background(), absent, 0)
	if err != nil {
		t.Fatalf("Output on an absent log: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if n, _ := stream.Read(body); n != 0 {
		t.Fatalf("an absent log returned %q", body[:n])
	}
}

// TestLaunchRefusesAnEmptyStageAndAnUnreadyMachine checks that a stage with no
// command is refused, and so is a launch on a machine whose sandbox went
// missing between construction and launch.
func TestLaunchRefusesAnEmptyStageAndAnUnreadyMachine(t *testing.T) {
	ready := func(string) (string, error) { return "/bin/echo", nil }
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Look: ready, Lookup: os.LookupEnv})
	dir := t.TempDir()
	spec := sandbox.StageSpec{Name: "worker-0", Workdir: dir, LogPath: filepath.Join(dir, "worker-0.log")}
	if _, err := driver.Launch(context.Background(), spec); err == nil {
		t.Fatal("a stage with no command was launched")
	}
	missing := New(Config{Platform: sandbox.Darwin, Home: t.TempDir(), Look: found()})
	spec.Argv = []string{"claude"}
	if _, err := missing.Launch(context.Background(), spec); !errors.Is(err, sandbox.ErrNotReady) {
		t.Fatalf("Launch on an unready machine = %v", err)
	}
}

// TestVersionRunsTheHarnessVersionCommand checks that the version is read by
// running the harness's own version command outside the sandbox. It is this
// program's query about its own dependency, not the agent's work.
func TestVersionRunsTheHarnessVersionCommand(t *testing.T) {
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(), Lookup: os.LookupEnv,
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	version, err := driver.Version(context.Background(), []string{"echo", "2.1.4"})
	if err != nil || version != "2.1.4" {
		t.Fatalf("Version = %q, %v", version, err)
	}
	if _, err := driver.Version(context.Background(), nil); err == nil {
		t.Fatal("an empty version command was accepted")
	}
}

// TestAStageReadsTheNullDevice checks that a stage reads from the null device
// and never from this process's stdin. A harness that reads an inherited stdin
// waits on it, and a detached stage whose stdin is a pipe that nobody closes
// waits forever.
func TestAStageReadsTheNullDevice(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "srt")
	// The shim stands in for srt: it ignores the settings flag and reports
	// what it read from stdin.
	script := "#!/bin/sh\nshift 2\nprintf 'stdin:[%s]' \"$(cat)\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(),
		Lookup: os.LookupEnv,
		Look: func(name string) (string, error) {
			if name == "srt" {
				return shim, nil
			}
			return "/bin/echo", nil
		}})
	dir := t.TempDir()
	logDir := filepath.Join(dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spec := sandbox.StageSpec{
		Name: "worker-0", Argv: []string{"unused"}, Workdir: dir,
		Network: sandbox.Network{Mode: sandbox.NetworkNone},
		LogPath: filepath.Join(logDir, "worker-0.log"),
	}
	handle, err := driver.Launch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := driver.Observe(context.Background(), handle)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !status.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, err := os.ReadFile(spec.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(body), "stdin:[]") {
		t.Fatalf("the stage did not read an empty stdin: %q", body)
	}
}

// TestTheCommandIsSeparatedFromTheSandboxsOwnFlags checks that the command is
// separated from srt's own options. srt parses options anywhere in its
// arguments unless they are separated, so a harness argv containing -d, -s,
// -c, -V or -h would have them consumed by srt: without the separator,
// `srt --settings f echo -d -s -V` fails with an srt settings error rather
// than running echo. Current harnesses use long flags only, but that is not a
// property to rely on.
func TestTheCommandIsSeparatedFromTheSandboxsOwnFlags(t *testing.T) {
	argv := []string{"claude", "--print", "-d", "-s", "-c", "--", "reproduce it"}
	script := wrapperScript("worker-0", append([]string{"/opt/srt", "--settings", "/s/p.json", "--"}, argv...),
		"/s/session/raw/worker-0.status")
	separator := strings.Index(script, "'--'")
	first := strings.Index(script, "'claude'")
	if separator < 0 || first < 0 || separator > first {
		t.Fatalf("the command is not separated from the sandbox's own flags:\n%s", script)
	}
	// Every element of the command survives, in order.
	previous := separator
	for _, arg := range argv {
		at := strings.Index(script[previous:], "'"+arg+"'")
		if at < 0 {
			t.Fatalf("%q did not reach the command line:\n%s", arg, script)
		}
		previous += at
	}
}

// TestAStageWithNoEnvironmentIsRefused checks that a stage with no PATH is
// refused. Such a stage resolves nothing, not even the sandbox, whose own
// shebang needs node. It used to fail with exit code 127 and an empty log,
// which is the kind of unexplained failure this driver must not produce.
func TestAStageWithNoEnvironmentIsRefused(t *testing.T) {
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(),
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	dir := t.TempDir()
	_, err := driver.Launch(context.Background(), sandbox.StageSpec{
		Name: "worker-0", Argv: []string{"/bin/sh", "-c", "true"}, Workdir: dir,
		LogPath: filepath.Join(dir, "worker-0.log"),
	})
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("Launch with no environment = %v", err)
	}
	// A driver whose environment has no PATH is refused in the same way.
	driver = New(Config{Platform: sandbox.CurrentPlatform(), Home: t.TempDir(),
		Lookup: func(name string) (string, bool) { return "/x", name == "HOME" },
		Look:   func(string) (string, error) { return "/bin/echo", nil }})
	if _, err := driver.Launch(context.Background(), sandbox.StageSpec{
		Name: "worker-0", Argv: []string{"/bin/sh"}, Workdir: dir,
		LogPath: filepath.Join(dir, "worker-0.log"),
	}); !errors.Is(err, sandbox.ErrNotReady) {
		t.Fatalf("Launch with a PATH-less environment = %v", err)
	}
}

// TestAStageInheritsOnlyTheAllowlistedEnvironment checks that a stage
// inherits the allowlisted variables and nothing else. The operator's shell
// may export an ANTHROPIC_API_KEY, a GITHUB_TOKEN or an AWS secret, and an
// agent running with its permission prompts bypassed must see none of them.
// PATH, without which nothing runs, and the HOME and TERM the driver sets must
// all arrive. The test runs the real driver with a shim in place of srt that
// executes the command, so the assertion is about the environment a stage
// receives in practice.
func TestAStageInheritsOnlyTheAllowlistedEnvironment(t *testing.T) {
	t.Setenv("REPLICHAI_TEST_CANARY", "leaked-into-the-sandbox")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-leaked")
	shim := filepath.Join(t.TempDir(), "srt")
	// The shim stands in for srt: it drops `--settings <file> --` and runs
	// the command.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nshift 3\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	home := t.TempDir()
	driver := New(Config{Platform: sandbox.CurrentPlatform(), Home: home, Lookup: os.LookupEnv,
		Look: func(name string) (string, error) {
			if name == "srt" {
				return shim, nil
			}
			return "/bin/echo", nil
		}})
	dir := t.TempDir()
	logDir := filepath.Join(dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spec := sandbox.StageSpec{
		Name: "worker-0", Argv: []string{"/usr/bin/env"}, Workdir: dir,
		Env:     map[string]string{"REPLICHAI_WS": dir},
		Network: sandbox.Network{Mode: sandbox.NetworkNone},
		LogPath: filepath.Join(logDir, "worker-0.log"),
	}
	handle, err := driver.Launch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := driver.Observe(context.Background(), handle)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !status.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	body, err := os.ReadFile(spec.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	seen := string(body)
	for _, leaked := range []string{"REPLICHAI_TEST_CANARY", "ANTHROPIC_API_KEY"} {
		if strings.Contains(seen, leaked) {
			t.Errorf("%s reached the stage:\n%s", leaked, seen)
		}
	}
	for _, want := range []string{"PATH=", "HOME=" + home, "TERM=dumb", "REPLICHAI_WS=" + dir} {
		if !strings.Contains(seen, want) {
			t.Errorf("the stage did not get %s:\n%s", want, seen)
		}
	}
}

// TestTheLoginGrantIsTiedToTheSessionThatNeedsIt checks that the operator's
// login is reachable under this driver, because a stage runs on the operator's
// own filesystem, and that the paths it needs are granted per session rather
// than always. A session that carries its own credential gets none of them,
// and the keychain is never in the profile unless the run uses the login that
// lives there.
func TestTheLoginGrantIsTiedToTheSessionThatNeedsIt(t *testing.T) {
	if !New(Config{Platform: sandbox.Darwin}).CarriesHarnessLogin() {
		t.Error("the host sandbox refuses a login on the operator's own machine")
	}
	// A stage the runtime did not add login paths to gets nothing extra. The
	// driver grants what the StageSpec carries and no more.
	settings := Render(New(Config{Platform: sandbox.Darwin, Home: "/Users/x"}).profile(sandbox.StageSpec{
		Paths: []sandbox.Path{{Host: "/s/workspace", Access: sandbox.ReadWrite}},
	}))
	for _, path := range append(settings.Filesystem.AllowRead, settings.Filesystem.AllowWrite...) {
		if strings.Contains(path, "Keychain") {
			t.Fatalf("a session that does not use the login still opened the keychain: %v", path)
		}
	}
	// A stage that does use the login carries it as a read-only path, and
	// its denials reach the profile: the keychain is readable, and the
	// operator's own standing instructions are not.
	withLogin := Render(New(Config{Platform: sandbox.Darwin, Home: "/Users/x"}).profile(sandbox.StageSpec{
		Paths: []sandbox.Path{
			{Host: "/s/workspace", Access: sandbox.ReadWrite},
			{Host: "/Users/x/Library/Keychains", Access: sandbox.ReadOnly},
		},
		Denied: []string{"/Users/x/.claude/CLAUDE.md", "/Users/x/.claude/plugins"},
	}))
	if !slices.Contains(withLogin.Filesystem.AllowRead, "/Users/x/Library/Keychains") {
		t.Fatalf("the login is unreachable: %v", withLogin.Filesystem.AllowRead)
	}
	if slices.Contains(withLogin.Filesystem.AllowWrite, "/Users/x/Library/Keychains") {
		t.Fatalf("the keychain is writable: %v", withLogin.Filesystem.AllowWrite)
	}
	for _, want := range []string{"/Users/x/.claude/CLAUDE.md", "/Users/x/.claude/plugins"} {
		if !slices.Contains(withLogin.Filesystem.DenyRead, want) {
			t.Errorf("%s reaches the run: %v", want, withLogin.Filesystem.DenyRead)
		}
	}
}
