// Package sandboxtest is the contract suite every sandbox driver is held to.
// The runtime above the seam assumes four things of any driver: a stage that
// outlives the process that launched it, an exit status recoverable after that
// process is gone, output readable from an offset out of somewhere the stage
// cannot write, and a handle that does not answer to another driver.
package sandboxtest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Subject is one driver under test, with the argv shapes it can run. The
// commands differ per driver — one runs on the host's shell, another inside an
// image — so the suite asks for them rather than assuming.
type Subject struct {
	// Driver is the implementation under test.
	Driver sandbox.Sandbox
	// Print builds argv that writes text to stdout and exits with code.
	Print func(text string, code int) []string
	// Wait builds argv that stays alive for at least the given duration.
	Wait func(d time.Duration) []string
	// Dir is a writable directory the stage may use.
	Dir string
}

// Run drives one driver through the contract.
func Run(t *testing.T, newSubject func(t *testing.T) *Subject) {
	t.Helper()

	t.Run("a stage runs, exits, and reports its status", func(t *testing.T) {
		subject := newSubject(t)
		spec := subject.spec(t, "worker-0", subject.Print("hello sandbox", 0))
		handle, err := subject.Driver.Launch(context.Background(), spec)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		status := waitFor(t, subject.Driver, handle)
		if !status.Succeeded() {
			t.Fatalf("status = %+v", status)
		}
		if body := readAll(t, subject.Driver, handle, 0); !contains(body, "hello sandbox") {
			t.Fatalf("output = %q", body)
		}
	})

	t.Run("a non-zero exit is reported, not swallowed", func(t *testing.T) {
		subject := newSubject(t)
		handle, err := subject.Driver.Launch(context.Background(),
			subject.spec(t, "worker-0", subject.Print("failing", 3)))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		status := waitFor(t, subject.Driver, handle)
		if status.Succeeded() || status.ExitCode != 3 {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("output is readable from an offset", func(t *testing.T) {
		subject := newSubject(t)
		handle, err := subject.Driver.Launch(context.Background(),
			subject.spec(t, "worker-0", subject.Print("abcdefgh", 0)))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		waitFor(t, subject.Driver, handle)
		whole := readAll(t, subject.Driver, handle, 0)
		if len(whole) < 4 {
			t.Fatalf("output = %q", whole)
		}
		tail := readAll(t, subject.Driver, handle, 4)
		if string(whole[4:]) != string(tail) {
			t.Fatalf("reading from an offset diverged:\nwhole %q\ntail  %q", whole, tail)
		}
	})

	t.Run("a running stage can be stopped", func(t *testing.T) {
		subject := newSubject(t)
		handle, err := subject.Driver.Launch(context.Background(),
			subject.spec(t, "worker-0", subject.Wait(30*time.Second)))
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		if err := subject.Driver.Stop(context.Background(), handle); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		status := waitFor(t, subject.Driver, handle)
		if status.Running {
			t.Fatalf("a stopped stage is still running: %+v", status)
		}
		// Stopping twice, and stopping something already finished, are
		// both fine: the controller cancels without knowing the state.
		if err := subject.Driver.Stop(context.Background(), handle); err != nil {
			t.Fatalf("repeated Stop: %v", err)
		}
	})

	t.Run("discard is idempotent and leaves the session directory", func(t *testing.T) {
		subject := newSubject(t)
		spec := subject.spec(t, "worker-0", subject.Print("done", 0))
		handle, err := subject.Driver.Launch(context.Background(), spec)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		waitFor(t, subject.Driver, handle)
		for range 2 {
			if err := subject.Driver.Discard(context.Background(), handle); err != nil {
				t.Fatalf("Discard: %v", err)
			}
		}
		if _, err := os.Stat(spec.LogPath); err != nil {
			t.Fatalf("discard removed the stage log, which is the runtime's: %v", err)
		}
	})

	t.Run("a handle from another driver is refused", func(t *testing.T) {
		subject := newSubject(t)
		other := sandbox.StageHandle{Driver: "not-this-one", ID: "x"}
		if _, err := subject.Driver.Observe(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
			t.Errorf("Observe = %v", err)
		}
		if _, err := subject.Driver.Output(context.Background(), other, 0); !errors.Is(err, sandbox.ErrMismatch) {
			t.Errorf("Output = %v", err)
		}
		if err := subject.Driver.Stop(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
			t.Errorf("Stop = %v", err)
		}
		if err := subject.Driver.Discard(context.Background(), other); !errors.Is(err, sandbox.ErrMismatch) {
			t.Errorf("Discard = %v", err)
		}
	})

	t.Run("the declaration is honest about what it enforces", func(t *testing.T) {
		subject := newSubject(t)
		capabilities := subject.Driver.Capabilities()
		if len(capabilities.NetworkModes) == 0 {
			t.Fatal("a driver that declares no network mode can run nothing")
		}
		if !capabilities.NetworkModes[sandbox.NetworkNone] {
			t.Error("every driver must be able to close the network")
		}
		if len(capabilities.PackageManagers) == 0 {
			t.Error("a driver that declares no package manager cannot run the method")
		}
	})
}

// spec builds a stage whose log lives where the runtime would put it: inside
// the session's evidence directory, which no stage may write.
func (s *Subject) spec(t *testing.T, name string, argv []string) sandbox.StageSpec {
	t.Helper()
	logDir := filepath.Join(s.Dir, "session", "raw")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	workspace := filepath.Join(s.Dir, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return sandbox.StageSpec{
		Name:    name,
		Argv:    argv,
		Workdir: workspace,
		Paths:   []sandbox.Path{{Host: workspace, Guest: "/workspace", Access: sandbox.ReadWrite}},
		Network: sandbox.Network{Mode: sandbox.NetworkNone},
		LogPath: filepath.Join(logDir, name+".log"),
	}
}

func waitFor(t *testing.T, driver sandbox.Sandbox, handle sandbox.StageHandle) sandbox.StageStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := driver.Observe(context.Background(), handle)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !status.Running {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the stage never finished")
	return sandbox.StageStatus{}
}

func readAll(t *testing.T, driver sandbox.Sandbox, handle sandbox.StageHandle, offset int64) []byte {
	t.Helper()
	stream, err := driver.Output(context.Background(), handle, offset)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	defer func() { _ = stream.Close() }()
	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return body
}

func contains(body []byte, want string) bool {
	return len(body) > 0 && len(want) > 0 && indexOf(string(body), want) >= 0
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
