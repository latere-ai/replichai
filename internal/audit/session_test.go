package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/legacy"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/method"
	providermemory "github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/runtimes"
)

func TestSessionAuditorRunsThroughControllerAndRetainsBeforeRelease(t *testing.T) {
	instrument := []byte(`{"version":"1","study":{"type":"observational"}}`)
	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{
		Files:  map[string][]byte{"result.txt": []byte("result"), InstrumentFile: instrument, "__pycache__/x.pyc": []byte("compiled")},
		Events: []managed.Event{{ID: "one", Role: managed.RoleAgent, Kind: managed.KindMessage, Content: "done"}},
	})
	controller := sessionController(t, runtime)
	var started managed.SessionRef
	auditor := NewSessionAuditor(controller, testMethod, WithSessionHooks(managed.Hooks{Started: func(_ context.Context, ref managed.SessionRef) error {
		started = ref
		return nil
	}}))
	destination := t.TempDir()
	result, err := auditor.Run(context.Background(), "paper", destination)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Ref.SameIdentity(started) || !result.Passed() || !result.Released || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if body, err := os.ReadFile(filepath.Join(destination, "result.txt")); err != nil || string(body) != "result" {
		t.Fatalf("retained artifact = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "__pycache__", "x.pyc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compiled artifact was retained: %v", err)
	}
	specs := runtime.Specs()
	if len(specs) != 1 || specs[0].Evaluation == nil || specs[0].Evaluation.Rubric != testMethod.Rubric {
		t.Fatalf("specs = %+v", specs)
	}
	if !strings.Contains(specs[0].Instructions, managedmemory.OutputDir) || strings.Contains(specs[0].Instructions, "{{OUTPUT_DIR}}") {
		t.Fatalf("instructions = %q", specs[0].Instructions)
	}
}

func TestSessionAuditorDetachedStartAndResume(t *testing.T) {
	store := managedmemory.NewStore()
	first := managedmemory.NewWithStore(store, managedmemory.Config{PollsBeforeDone: 2, Files: map[string][]byte{"result.txt": []byte("result")}})
	controller := sessionController(t, first)
	auditor := NewSessionAuditor(controller, testMethod)
	ref, err := auditor.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(first.Specs()) != 1 {
		t.Fatalf("Start did not create exactly one session: %d", len(first.Specs()))
	}
	second := managedmemory.NewWithStore(store, managedmemory.Config{PollsBeforeDone: 2, Files: map[string][]byte{"result.txt": []byte("result")}})
	resumed := NewSessionAuditor(sessionController(t, second), testMethod)
	result, err := resumed.Resume(context.Background(), ref, "paper", t.TempDir())
	if err != nil || result.Ref.ID != ref.ID || !result.Released {
		t.Fatalf("Resume = %+v, %v", result, err)
	}
	if len(second.Specs()) != 1 {
		t.Fatalf("Resume created another session: %d", len(second.Specs()))
	}
}

func TestSessionAuditorMapsRepositoryThroughSecretReference(t *testing.T) {
	backend := providermemory.New()
	runtime, err := legacy.New(legacy.Config{Name: "anthropic-managed", ModelProvider: "anthropic", Provider: backend})
	if err != nil {
		t.Fatalf("legacy runtime: %v", err)
	}
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod, WithSessionMount(SessionMount{
		URL: "https://github.com/open-hai/repro", Token: "scoped-token",
	}))
	ref, err := auditor.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	run := backend.Runs()[0]
	if run.Mount.Token != "scoped-token" || run.Mount.MountPath != DefaultMountPath {
		t.Fatalf("mount = %+v", run.Mount)
	}
	if strings.Contains(string(ref.Locator), "scoped-token") {
		t.Fatalf("session ref contains credential: %s", ref.Locator)
	}
}

func TestSessionAuditorErrorsAreActionable(t *testing.T) {
	runtime := managedmemory.New()
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod)
	if _, err := auditor.Start(context.Background(), " "); !errors.Is(err, method.ErrInvalid) {
		t.Fatalf("empty paper = %v", err)
	}
	badMount := NewSessionAuditor(sessionController(t, managedmemory.New()), testMethod, WithSessionMount(SessionMount{
		URL: "https://example.com/repo", MountPath: "/tmp/out",
	}))
	if _, err := badMount.Start(context.Background(), "paper"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("bad mount = %v", err)
	}
	timed := NewSessionAuditor(sessionController(t, managedmemory.NewWithStore(nil, managedmemory.Config{PollsBeforeDone: 100})), testMethod, WithSessionTimeout(time.Nanosecond))
	if _, err := timed.Run(context.Background(), "paper", t.TempDir()); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("timeout = %v", err)
	}
}

func TestSessionAuditorOptionsAndResultHelpers(t *testing.T) {
	runtime := managedmemory.New()
	retained := false
	auditor := NewSessionAuditor(sessionController(t, runtime), testMethod,
		WithSessionModel("fast"), WithSessionMaxRevisions(5), WithoutSessionTimeout(),
		WithSessionHooks(managed.Hooks{Retain: func(context.Context, managed.EvidenceReader) error {
			retained = true
			return nil
		}}),
	)
	result, err := auditor.Run(context.Background(), "paper", t.TempDir())
	if err != nil || !retained || result.Grade() != managed.GradePassed {
		t.Fatalf("Run = %+v, retained=%v, %v", result, retained, err)
	}
	if result.InstrumentOK() || (SessionResult{}).Grade() != managed.GradeUnknown {
		t.Fatal("result helpers misclassified missing instrument/verdict")
	}
	spec := runtime.Specs()[0]
	if spec.Model.Preference != "fast" || spec.Evaluation.MaxRevisions != 5 {
		t.Fatalf("options did not reach spec: %+v", spec)
	}
}

type capabilityRuntime struct {
	managed.Runtime
	capabilities managed.Capabilities
	err          error
}

func (r capabilityRuntime) Capabilities(context.Context) (managed.Capabilities, error) {
	return r.capabilities, r.err
}

func TestSessionAuditorCapabilityAndRetentionFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime managed.Runtime
		dst     func(*testing.T) string
		want    error
	}{
		{"capability read", capabilityRuntime{Runtime: managedmemory.New(), err: errors.New("unavailable")}, func(t *testing.T) string { return t.TempDir() }, nil},
		{"missing output dir", capabilityRuntime{Runtime: managedmemory.New()}, func(t *testing.T) string { return t.TempDir() }, nil},
		{"unsafe artifact", managedmemory.NewWithStore(nil, managedmemory.Config{Files: map[string][]byte{"../escape": []byte("x")}}), func(t *testing.T) string { return t.TempDir() }, ErrUnsafePath},
		{"fetch artifact", managedmemory.NewWithStore(nil, managedmemory.Config{Files: map[string][]byte{"result": []byte("x")}, Failures: map[managedmemory.FailurePoint]error{managedmemory.FailFetch: errors.New("fetch")}}), func(t *testing.T) string { return t.TempDir() }, nil},
		{"destination is file", managedmemory.New(), func(t *testing.T) string {
			name := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return name
		}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			auditor := NewSessionAuditor(sessionController(t, test.runtime), testMethod)
			_, err := auditor.Run(context.Background(), "paper", test.dst(t))
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("Run = %v", err)
			}
		})
	}
}

func sessionController(t *testing.T, runtime managed.Runtime) *managed.Controller {
	t.Helper()
	router, err := runtimes.NewRouter(runtime.Name(), runtime)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return managed.NewController(router, managed.WithWaitPolicy(managed.WaitPolicy{
		InitialDelay: 0, MaxDelay: 0, MaxTemporaryErrors: 2, MaxSilence: time.Millisecond, CleanupTimeout: time.Second,
	}))
}

// A session with no mount is told it has no remote, and told not to push.
// Rubric criterion 9 keys on a remote being configured, so the instructions
// and the rubric are read together by the grader and have to agree about what
// is present — otherwise every local run fails a criterion it cannot satisfy.
func TestTheWorkspaceProtocolSaysThereIsNoRemote(t *testing.T) {
	protocol := workspaceProtocol("/s/workspace")
	for _, want := range []string{
		"There is no remote",
		"nothing to push",
		"Criterion 9 does not apply",
		"not destroyed when this session ends",
		`"git log --oneline -5"`,
		"OUTSIDE /s/workspace",
		`"$TMPDIR"`,
		"EGRESS_BLOCKED",
		"never as \"no artifact",
	} {
		if !strings.Contains(protocol, want) {
			t.Errorf("the workspace protocol is missing %q:\n%s", want, protocol)
		}
	}
	// It must not tell an agent with no remote to push to one. That
	// instruction is what would put a forge credential back in the sandbox.
	if strings.Contains(protocol, "git push") {
		t.Errorf("the workspace protocol asks for a push:\n%s", protocol)
	}
}

// The mount protocol and the workspace protocol are mutually exclusive: a
// session either pushes from inside or is pushed from outside, and being told
// both would be being told to do two different things with one repository.
func TestOnlyOneRepositoryProtocolReachesASession(t *testing.T) {
	mounted := mountProtocol("/workspace/repro")
	workspace := workspaceProtocol("/s/workspace")
	if strings.Contains(mounted, "no remote") {
		t.Error("the mount protocol denies the remote it mounts")
	}
	if strings.Contains(workspace, "origin/main") {
		t.Error("the workspace protocol names a remote branch")
	}
}
