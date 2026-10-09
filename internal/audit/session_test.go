// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	if instrumentOK(result.InstrumentPresent, result.InstrumentViolations) || (SessionResult{}).Grade() != managed.GradeUnknown {
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

// TestTheWorkspaceProtocolSaysThereIsNoRemote checks that a session with no
// mount is told it has no remote and is not told to push. Rubric criterion 9
// depends on a remote being configured, and the grader reads the instructions
// and the rubric together, so they have to agree about what is present.
// Otherwise every local run fails a criterion it cannot satisfy.
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
		// The refusal is recorded with the result the findings schema has for
		// it, so the system can read a row an agent would otherwise word freely.
		`the result "not_fetched"`,
		`"$VIRTUAL_ENV"`,
		"do not create a virtualenv inside",
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

// TestEveryRepositoryProtocolNamesEveryDeliverable checks that each repository
// protocol asks for every file the method requires. The lists were once
// written by hand in each protocol, and the mount protocol omitted
// verdict.json. Deriving them from method.Deliverables prevents the omission,
// and this test checks that it does.
func TestEveryRepositoryProtocolNamesEveryDeliverable(t *testing.T) {
	for name, protocol := range map[string]string{
		"mount":     mountProtocol("/workspace/repro"),
		"workspace": workspaceProtocol("/s/workspace"),
	} {
		for _, file := range method.Deliverables() {
			if !strings.Contains(protocol, file) {
				t.Errorf("the %s protocol does not ask for %s", name, file)
			}
		}
		if !strings.Contains(protocol, method.SourceDir+"/") {
			t.Errorf("the %s protocol does not ask for %s/", name, method.SourceDir)
		}
	}
}

// TestOnlyOneRepositoryProtocolReachesASession checks that the mount protocol
// and the workspace protocol are mutually exclusive. A session either pushes
// from inside the sandbox or is pushed from outside it, and receiving both
// texts would instruct two different things for one repository.
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

// TestTheNetworkPolicyFollowsWhatTheRuntimeCanEnforce checks that the artifact
// search asks for the egress policy the runtime can enforce. A host sandbox
// accepts only an allowlist and refuses a wildcard, so a specification that
// demands open egress would be refused and the local audit could not start.
func TestTheNetworkPolicyFollowsWhatTheRuntimeCanEnforce(t *testing.T) {
	open := networkFor(managed.Capabilities{NetworkModes: map[managed.NetworkMode]bool{
		managed.NetworkOpen: true, managed.NetworkAllowlist: true, managed.NetworkNone: true,
	}})
	if open.Mode != managed.NetworkOpen || len(open.AllowedDomains) != 0 {
		t.Fatalf("a runtime that can offer open egress was given %+v", open)
	}

	bounded := networkFor(managed.Capabilities{NetworkModes: map[managed.NetworkMode]bool{
		managed.NetworkAllowlist: true, managed.NetworkNone: true,
	}})
	if bounded.Mode != managed.NetworkAllowlist {
		t.Fatalf("a runtime that cannot was given %q", bounded.Mode)
	}
	// The allowlist is ArtifactDomains, and it has to include the places an
	// artifact is published: a preprint server, a publisher, a code host, a
	// data repository. Otherwise the search cannot find anything.
	joined := strings.Join(bounded.AllowedDomains, " ")
	for _, want := range []string{"arxiv.org", "doi.org", "github.com", "*.github.io", "zenodo.org", "osf.io", "pypi.org"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the hunt cannot reach %s", want)
		}
	}

	// A runtime that can enforce neither gets a closed sandbox rather than
	// a policy it would refuse.
	closed := networkFor(managed.Capabilities{NetworkModes: map[managed.NetworkMode]bool{managed.NetworkNone: true}})
	if closed.Mode != managed.NetworkNone || len(closed.AllowedDomains) != 0 {
		t.Fatalf("closed = %+v", closed)
	}

	// Every domain has a form srt accepts: a hostname or a single-label
	// wildcard, never a bare wildcard or a top-level one. srt refuses those,
	// which would fail the whole run at launch.
	for _, domain := range ArtifactDomains() {
		if domain == "*" || strings.HasPrefix(domain, "*.") && strings.Count(domain, ".") < 2 {
			t.Errorf("%q is too broad for the sandbox to accept", domain)
		}
		if strings.ContainsAny(domain, "/: ") {
			t.Errorf("%q is not a bare hostname", domain)
		}
	}
}
