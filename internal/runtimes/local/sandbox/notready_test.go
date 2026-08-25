package sandbox

import (
	"errors"
	"strings"
	"testing"
)

// Every refusal this program can print is reachable with nothing installed:
// the message is a pure function of platform, driver and condition, so the
// operator-facing text is testable rather than only observable on a broken
// machine.
func TestEveryRefusalNamesItsRemedy(t *testing.T) {
	cases := []struct {
		platform   Platform
		driver     DriverName
		condition  Condition
		components []string
		want       []string
	}{
		{Darwin, Host, ConditionMissing, []string{"srt"},
			[]string{"npm install -g @anthropic-ai/sandbox-runtime", "brew install ripgrep", `srt "echo ok"`, "--sandbox container"}},
		{Linux, Host, ConditionMissing, []string{"bubblewrap", "socat"},
			[]string{"apt-get install -y bubblewrap", "apt-get install -y socat", "--sandbox container"}},
		{Darwin, Host, ConditionMissing, []string{"claude"},
			[]string{"npm install -g @anthropic-ai/claude-code", "claude login"}},
		{Darwin, Container, ConditionMissing, []string{"podman"},
			[]string{"brew install podman", "podman machine init", "podman info", "--sandbox host"}},
		{Linux, Container, ConditionMissing, []string{"podman"},
			[]string{"apt-get install -y podman", "--sandbox host"}},
		{Darwin, Container, ConditionStopped, []string{"podman"},
			[]string{"podman machine start", "never initialised", "podman info"}},
		{Linux, Container, ConditionStopped, []string{"podman"},
			[]string{"systemctl --user start podman"}},
		{Darwin, Container, ConditionImage, []string{"ghcr.io/latere-ai/replichai-harness@sha256:abc"},
			[]string{"podman pull ghcr.io/latere-ai/replichai-harness@sha256:abc", "REPLICHAI_HARNESS_IMAGE"}},
	}
	for _, c := range cases {
		err := NotReady(c.platform, c.driver, c.condition, c.components...)
		if !errors.Is(err, ErrNotReady) {
			t.Fatalf("%s/%s does not unwrap to ErrNotReady", c.platform, c.condition)
		}
		message := err.Error()
		for _, want := range c.want {
			if !strings.Contains(message, want) {
				t.Errorf("%s/%s/%v message missing %q:\n%s", c.platform, c.driver, c.components, want, message)
			}
		}
	}
}

// A component nobody wrote an install row for still gets an actionable line.
// A silent gap in the table would produce a refusal that says nothing, which
// is worse than the exec failure it replaced.
func TestAnUntabulatedComponentStillSaysSomething(t *testing.T) {
	message := NotReady(Linux, Host, ConditionMissing, "some-new-tool").Error()
	if !strings.Contains(message, "some-new-tool") || !strings.Contains(message, "PATH") {
		t.Fatalf("no fallback instruction:\n%s", message)
	}
	// A component packaged for another platform names that, rather than
	// pretending there is nothing to install.
	message = NotReady(Darwin, Host, ConditionMissing, "bubblewrap").Error()
	if !strings.Contains(message, "linux") {
		t.Fatalf("cross-platform fallback lost the platform it is packaged for:\n%s", message)
	}
}

// Windows is refused whatever was asked for, and the refusal names WSL2
// instead of offering the other driver, which is equally unavailable.
func TestWindowsIsRefusedWithAReason(t *testing.T) {
	for _, driver := range []DriverName{Host, Container} {
		err := NotReady(Windows, driver, ConditionMissing, "srt")
		if err.Condition != ConditionUnsupported {
			t.Errorf("%s on windows = %s", driver, err.Condition)
		}
		if message := err.Error(); !strings.Contains(message, "WSL2") || strings.Contains(message, "--sandbox") {
			t.Errorf("windows refusal for %s:\n%s", driver, message)
		}
	}
}

// A handle from one driver must not be looked up by the other: both are opaque
// strings, so nothing but this check stops a pid being read as a container.
func TestHandlesDoNotCrossDrivers(t *testing.T) {
	handle := StageHandle{Driver: Host, ID: "pid:1@2"}
	if err := MatchHandle(Host, handle); err != nil {
		t.Fatalf("matching handle refused: %v", err)
	}
	err := MatchHandle(Container, handle)
	if !errors.Is(err, ErrMismatch) {
		t.Fatalf("cross-driver handle accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "host") || !strings.Contains(err.Error(), "container") {
		t.Errorf("mismatch names only one side: %v", err)
	}
	if err := MatchHandle(Host, StageHandle{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("empty handle accepted: %v", err)
	}
}

// A path with no guest destination is used as-is, which is the host driver's
// normal case; one with a guest is remapped, which is the container's.
func TestPathDestination(t *testing.T) {
	if got := (Path{Host: "/a/b", Access: ReadWrite}).Destination(); got != "/a/b" {
		t.Errorf("unmapped destination = %q", got)
	}
	if got := (Path{Host: "/a/b", Guest: "/workspace", Access: ReadWrite}).Destination(); got != "/workspace" {
		t.Errorf("mapped destination = %q", got)
	}
}

func TestStageStatusSucceededNeedsAFinishedStage(t *testing.T) {
	for _, c := range []struct {
		status StageStatus
		want   bool
	}{
		{StageStatus{Running: true}, false},
		{StageStatus{}, true},
		{StageStatus{ExitCode: 1}, false},
		{StageStatus{Gone: true}, false},
	} {
		if got := c.status.Succeeded(); got != c.want {
			t.Errorf("%+v.Succeeded() = %v", c.status, got)
		}
	}
}
