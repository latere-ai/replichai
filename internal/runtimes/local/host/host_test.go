// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/hostsandbox"
	"latere.ai/x/pkg/hostsandbox/hostsandboxtest"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
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

// TestPreflightNamesTheHarnessAndTheAlternative checks that the harness this
// program runs is a preflight requirement of its own, that a missing one is
// reported with this program's install rows, and that every refusal names the
// container driver. The shared driver knows none of that: it is what this
// adapter adds.
func TestPreflightNamesTheHarnessAndTheAlternative(t *testing.T) {
	cases := []struct {
		name     string
		platform hostsandbox.Platform
		look     func(string) (string, error)
		want     []string
	}{
		{"no srt on macOS", hostsandbox.Darwin, found(),
			[]string{"srt is not installed", "npm install -g @anthropic-ai/sandbox-runtime", "--sandbox container"}},
		{"no harness", hostsandbox.Darwin, found("srt", "rg"),
			[]string{"claude", "npm install -g @anthropic-ai/claude-code", "claude login", "--sandbox container"}},
		{"no bubblewrap on Linux", hostsandbox.Linux, found("srt", "rg"),
			[]string{"bubblewrap", "socat", "apt-get", "--sandbox container"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			driver := New(Config{Platform: c.platform, Look: c.look, HarnessBinary: "claude", Home: "/home/x"})
			err := driver.Preflight(context.Background())
			if !errors.Is(err, hostsandbox.ErrNotReady) {
				t.Fatalf("Preflight = %v", err)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message missing %q:\n%s", want, err)
				}
			}
		})
	}
	// A machine with everything installed is ready, and a driver composed
	// with no harness requires only what srt needs.
	ready := New(Config{Platform: hostsandbox.Darwin, Look: found("srt", "rg", "claude"), HarnessBinary: "claude", Home: "/home/x"})
	if err := ready.Preflight(context.Background()); err != nil {
		t.Fatalf("a ready machine was refused: %v", err)
	}
	bare := New(Config{Platform: hostsandbox.Darwin, Look: found("srt", "rg"), HarnessBinary: "  ", Home: "/home/x"})
	if err := bare.Preflight(context.Background()); err != nil {
		t.Fatalf("a driver with no harness was refused: %v", err)
	}
	// Windows is refused in this program's words, naming WSL2 rather than
	// the other driver, which is equally unavailable there.
	windows := New(Config{Platform: hostsandbox.Windows, Look: found("srt", "rg", "claude"), HarnessBinary: "claude"})
	err := windows.Preflight(context.Background())
	if !errors.Is(err, hostsandbox.ErrNotReady) || !strings.Contains(err.Error(), "WSL2") {
		t.Fatalf("windows = %v", err)
	}
	// The platform defaults to the one this binary runs on, so a
	// composition root that does not name one still gets a usable driver.
	if New(Config{Home: "/home/x"}).Preflight(context.Background()) == nil && hostsandbox.CurrentPlatform() == hostsandbox.Windows {
		t.Fatal("windows was accepted through the default platform")
	}
}

// TestThisProgramsCredentialIsDeniedToEveryStage checks that the cloud token
// this program keeps on the machine is denied beside the shared deny table,
// whether the policy is inspected through Profile or built by Launch. The
// shared table cannot carry it: it is this program's own credential.
func TestThisProgramsCredentialIsDeniedToEveryStage(t *testing.T) {
	home := "/Users/x"
	driver := New(Config{Platform: hostsandbox.Darwin, Home: home, Look: found("srt", "rg")})
	settings := hostsandbox.Render(driver.Profile(hostsandbox.StageSpec{
		Paths:   []hostsandbox.Path{{Host: "/s/workspace", Access: hostsandbox.ReadWrite}},
		LogPath: "/s/session/raw/worker-0.log",
	}))
	if !slices.Contains(settings.Filesystem.DenyRead, filepath.Join(home, ".config/replichai")) {
		t.Fatalf("the cloud token is readable: %v", settings.Filesystem.DenyRead)
	}
	// The shared table is denied too, so composing the entry did not
	// replace what the shared driver already withholds.
	for _, entry := range hostsandbox.AlwaysDenyRead {
		want := strings.Replace(entry, "~", home, 1)
		if !slices.Contains(settings.Filesystem.DenyRead, want) {
			t.Errorf("shared deny entry %q did not reach the settings", want)
		}
	}
	// A stage spec of the caller's own is not mutated by the grant, so a
	// caller that reuses one stage spec for two launches does not
	// accumulate the entry.
	spec := hostsandbox.StageSpec{Denied: []string{"/Users/x/.claude/CLAUDE.md"}}
	driver.Profile(spec)
	driver.Profile(spec)
	if len(spec.Denied) != 1 {
		t.Fatalf("the caller's spec grew to %v", spec.Denied)
	}
}

// TestTheDeniedCredentialSurvivesALaunch checks that the denial reaches the
// settings file a launched stage actually runs under, not only the profile a
// test can ask for.
func TestTheDeniedCredentialSurvivesALaunch(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	logDir := filepath.Join(dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// /bin/echo stands in for srt: what is under test is the policy the
	// driver writes beside the log, not srt's enforcement of it.
	driver := New(Config{Platform: hostsandbox.CurrentPlatform(), Home: home, Lookup: os.LookupEnv,
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	spec := hostsandbox.StageSpec{
		Name: "worker-0", Argv: []string{"unused"}, Workdir: dir,
		Network: hostsandbox.Network{Mode: hostsandbox.NetworkNone},
		LogPath: filepath.Join(logDir, "worker-0.log"),
	}
	if _, err := driver.Launch(context.Background(), spec); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(logDir, "worker-0.srt.json"))
	if err != nil {
		t.Fatalf("read the stage's settings: %v", err)
	}
	if !strings.Contains(string(body), filepath.Join(home, ".config/replichai")) {
		t.Fatalf("the cloud token is readable by the launched stage:\n%s", body)
	}
}

// TestCapabilitiesCarryWhatTheRuntimeReads checks the half of the declaration
// srt knows nothing about: the package managers a stage installs with, and
// the attachment cap. apt is absent because it needs a privileged root this
// driver does not have.
func TestCapabilitiesCarryWhatTheRuntimeReads(t *testing.T) {
	capabilities := New(Config{Platform: hostsandbox.Darwin}).Capabilities()
	if !capabilities.PackageManagers["pip"] || !capabilities.PackageManagers["npm"] {
		t.Errorf("package managers = %v", capabilities.PackageManagers)
	}
	if capabilities.PackageManagers["apt"] {
		t.Error("the host driver claims it can install system packages")
	}
	if capabilities.MaxAttachmentBytes != MaxAttachmentBytes {
		t.Errorf("attachment cap = %d", capabilities.MaxAttachmentBytes)
	}
	// The egress declaration stays the shared driver's. There is no open
	// mode: srt refuses a wildcard.
	if !capabilities.NetworkModes[hostsandbox.NetworkNone] || !capabilities.NetworkModes[hostsandbox.NetworkAllowlist] {
		t.Errorf("network modes = %v", capabilities.NetworkModes)
	}
	if capabilities.NetworkModes[hostsandbox.NetworkOpen] {
		t.Error("the host driver claims it can leave egress unrestricted")
	}
}

// TestVersionRunsTheHarnessVersionCommand checks that the version is read by
// running the harness's own version command outside the sandbox. It is this
// program's query about its own dependency, not the agent's work.
func TestVersionRunsTheHarnessVersionCommand(t *testing.T) {
	driver := New(Config{Platform: hostsandbox.CurrentPlatform(), Home: t.TempDir(), Lookup: os.LookupEnv,
		Look: func(string) (string, error) { return "/bin/echo", nil }})
	version, err := driver.Version(context.Background(), []string{"echo", "2.1.4"})
	if err != nil || version != "2.1.4" {
		t.Fatalf("Version = %q, %v", version, err)
	}
	if _, err := driver.Version(context.Background(), nil); err == nil {
		t.Fatal("an empty version command was accepted")
	}
	if _, err := driver.Version(context.Background(), []string{"/nonexistent/harness", "--version"}); err == nil {
		t.Fatal("a harness that cannot run reported a version")
	}
}

// TestTheLoginGrantIsTiedToTheSessionThatNeedsIt checks that the operator's
// login is reachable under this driver, because a stage runs on the
// operator's own filesystem, and that the paths it needs are granted per
// session rather than always. A session that carries its own credential gets
// none of them, and the keychain is never in the profile unless the run uses
// the login that lives there.
func TestTheLoginGrantIsTiedToTheSessionThatNeedsIt(t *testing.T) {
	if !New(Config{Platform: hostsandbox.Darwin}).CarriesHarnessLogin() {
		t.Error("the host sandbox refuses a login on the operator's own machine")
	}
	driver := New(Config{Platform: hostsandbox.Darwin, Home: "/Users/x"})
	// A stage the runtime did not add login paths to gets nothing extra.
	// The driver grants what the StageSpec carries and no more.
	settings := hostsandbox.Render(driver.Profile(hostsandbox.StageSpec{
		Paths: []hostsandbox.Path{{Host: "/s/workspace", Access: hostsandbox.ReadWrite}},
	}))
	for _, path := range append(settings.Filesystem.AllowRead, settings.Filesystem.AllowWrite...) {
		if strings.Contains(path, "Keychain") {
			t.Fatalf("a session that does not use the login still opened the keychain: %v", path)
		}
	}
	// A stage that does use the login carries it as a read-only path, and
	// its denials reach the profile: the keychain is readable, and the
	// operator's own standing instructions are not.
	withLogin := hostsandbox.Render(driver.Profile(hostsandbox.StageSpec{
		Paths: []hostsandbox.Path{
			{Host: "/s/workspace", Access: hostsandbox.ReadWrite},
			{Host: "/Users/x/Library/Keychains", Access: hostsandbox.ReadOnly},
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

// TestTheAlternativeNamesTheOtherDriver checks that the refusal an operator
// reads names the driver this program ships as the fallback, in this
// program's own command line.
func TestTheAlternativeNamesTheOtherDriver(t *testing.T) {
	if got := sandbox.Alternative(hostsandbox.Darwin, hostsandbox.Host); !strings.Contains(got, "--sandbox container") {
		t.Fatalf("the host driver's alternative = %q", got)
	}
}

// TestTheDriverMeetsTheSandboxContract runs the shared contract suite against
// this driver where srt is installed, and reports why it was skipped where it
// is not.
func TestTheDriverMeetsTheSandboxContract(t *testing.T) {
	if err := New(Config{Home: os.Getenv("HOME")}).Preflight(context.Background()); err != nil {
		t.Skipf("the host sandbox is not installed here, so its contract is unchecked:\n%v", err)
	}
	removeStraySockets(t)
	hostsandboxtest.Run(t, func(t *testing.T) *hostsandboxtest.Subject {
		return &hostsandboxtest.Subject{
			// The environment comes from the composition root, here as
			// elsewhere. A stage with no PATH resolves nothing, not even
			// the sandbox.
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

// removeStraySockets deletes the multiplexing sockets srt opens under the
// temporary root and does not always remove when a stage is killed. They
// cannot be moved out of the way: a unix socket path is limited to 104 bytes
// on macOS, and a temporary root named after a test exhausts it before srt
// can bind. Only the sockets that appear while this test runs are removed.
func removeStraySockets(t *testing.T) {
	t.Helper()
	pattern := filepath.Join(os.TempDir(), "srt-mux-*.sock")
	before, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("list the temporary root: %v", err)
	}
	t.Cleanup(func() {
		after, err := filepath.Glob(pattern)
		if err != nil {
			t.Errorf("list the temporary root: %v", err)
			return
		}
		for _, path := range after {
			if slices.Contains(before, path) {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove %s: %v", path, err)
			}
		}
	})
}
