// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package latere

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/contracttest"
)

func TestTheRuntimePassesTheContract(t *testing.T) {
	contracttest.Run(t, func() managed.Runtime { return newFakePlatform(t).runtime(t) })
}

func TestARejectedGradeEndsFailed(t *testing.T) {
	contracttest.RunFailingEvaluation(t, func() managed.Runtime {
		f := newFakePlatform(t)
		f.verdicts = []string{failed}
		return f.runtime(t)
	})
}

func gradedSpec() managed.SessionSpec {
	return managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "balanced"},
		Instructions: "Follow the method.",
		Task:         "Reproduce the result.",
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pypdf"}, {Manager: "pip", Name: "requests"}},
			Network:  managed.NetworkPolicy{Mode: managed.NetworkNone},
		},
		Evaluation: &managed.EvaluationSpec{Rubric: "1. The result matches.", MaxRevisions: 1},
	}
}

// run starts a session and polls it to the end, the way the controller does,
// carrying each checkpointed reference into the next poll.
func run(t *testing.T, r *Runtime, spec managed.SessionSpec) (managed.SessionRef, managed.Status) {
	t.Helper()
	ref, err := r.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for range 20 {
		status, err := r.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		ref = status.Ref
		if status.State.Terminal() {
			return ref, status
		}
	}
	t.Fatal("the session never ended")
	return ref, managed.Status{}
}

func TestARevisionFollowsANeedsRevisionGrade(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{needsRevision, passed}
	_, status := run(t, f.runtime(t), gradedSpec())
	if status.State != managed.StateDone || status.Verdict == nil || !status.Verdict.Passed() {
		t.Fatalf("status = %+v", status)
	}
	if f.graded != 2 {
		t.Fatalf("graded %d times, want 2", f.graded)
	}
}

func TestASpentRevisionBudgetEndsExhausted(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{needsRevision}
	spec := gradedSpec()
	spec.Evaluation.MaxRevisions = 0
	_, status := run(t, f.runtime(t), spec)
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonEvaluationFailed {
		t.Fatalf("status = %+v", status)
	}
	if status.Verdict == nil || status.Verdict.Grade != managed.GradeExhausted {
		t.Fatalf("verdict = %+v", status.Verdict)
	}
	if f.graded != 1 {
		t.Fatalf("graded %d times, want 1: a zero budget grades once and never revises", f.graded)
	}
}

func TestAGraderStartedWrongIsRetriedThenFails(t *testing.T) {
	cases := map[string]func(string) (string, string, []string){
		"another agent": func(task string) (string, string, []string) { return "helper", task, graderTools },
		"another task":  func(string) (string, string, []string) { return graderName, "Grade it kindly.", graderTools },
		"a write tool":  func(task string) (string, string, []string) { return graderName, task, []string{"read", "write"} },
		"no spawn":      func(task string) (string, string, []string) { return "", task, nil },
	}
	for name, spawn := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform(t)
			f.spawn = spawn
			_, status := run(t, f.runtime(t), gradedSpec())
			if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonRuntimeFailed || status.Verdict != nil {
				t.Fatalf("status = %+v", status)
			}
			if got := f.count("POST /v1/agents/sessions/{id}/events"); got != 1+graderAttempts {
				t.Fatalf("sent %d messages, want the task and %d grading requests", got, graderAttempts)
			}
		})
	}
}

func TestAGraderWithNoVerdictFailsTheSession(t *testing.T) {
	f := newFakePlatform(t)
	f.verdicts = []string{"It looks fine to me."}
	_, status := run(t, f.runtime(t), gradedSpec())
	if status.State != managed.StateFailed || status.Error == "" || status.Verdict != nil {
		t.Fatalf("status = %+v", status)
	}
}

func TestAPollReplayedAfterACrashSendsNothingTwice(t *testing.T) {
	f := newFakePlatform(t)
	r := f.runtime(t)
	ref, err := r.Start(context.Background(), gradedSpec())
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Poll(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	// The process dies before the controller checkpoints first.Ref, and a
	// fresh one polls the reference it had.
	again, err := f.runtime(t).Poll(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Ref.Locator) != string(first.Ref.Locator) {
		t.Fatalf("a replayed poll moved differently:\n%s\n%s", first.Ref.Locator, again.Ref.Locator)
	}
	if f.graded != 1 {
		t.Fatalf("graded %d times, want 1", f.graded)
	}
}

func TestStartRefusesWhatThePlatformCannotDo(t *testing.T) {
	cases := map[string]func(*managed.SessionSpec){
		"a package the image lacks": func(s *managed.SessionSpec) { s.Sandbox.Packages = []managed.Package{{Manager: "pip", Name: "torch"}} },
		"a pinned version": func(s *managed.SessionSpec) {
			s.Sandbox.Packages = []managed.Package{{Manager: "pip", Name: "numpy", Version: "1.0"}}
		},
		"bootstrap": func(s *managed.SessionSpec) { s.Sandbox.Bootstrap = [][]string{{"make"}} },
		"an allowlist": func(s *managed.SessionSpec) {
			s.Sandbox.Network = managed.NetworkPolicy{Mode: managed.NetworkAllowlist, AllowedDomains: []string{"example.org"}}
		},
		"an attachment": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceAttachment, Attachment: &managed.AttachmentResource{Path: "a.txt", Content: []byte("a"), SHA256: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}}
		},
		"an input": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceInput, Input: &managed.InputResource{Path: "a.txt", Content: []byte("a"), SHA256: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"}}}
		},
		"a repository": func(s *managed.SessionSpec) {
			s.Resources = []managed.Resource{{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{URL: "https://github.com/o/r", Destination: "repo", Access: managed.AccessReadWrite, CredentialRef: "forge"}}}
		},
		"a findings file":                          func(s *managed.SessionSpec) { s.Evaluation.Findings = "verdict.json" },
		"an environment the runtime does not know": func(s *managed.SessionSpec) { s.Sandbox.Environment = "fortran" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform(t)
			spec := gradedSpec()
			edit(&spec)
			if _, err := f.runtime(t).Start(context.Background(), spec); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("Start = %v, want unsupported", err)
			}
			if f.count("PUT /v1/agents/agents/{id}") != 0 || f.count("POST /v1/agents/sessions") != 0 {
				t.Fatal("something was written on the platform before the refusal")
			}
		})
	}
}

func TestStartRefusesANetworkThePreferenceDoesNotGive(t *testing.T) {
	f := newFakePlatform(t)
	f.preference = "listed"
	_, err := f.runtime(t).Start(context.Background(), gradedSpec())
	var unsupported *managed.UnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Field != "sandbox.network.mode" {
		t.Fatalf("Start = %v", err)
	}
	if f.count("POST /v1/agents/sessions") != 0 {
		t.Fatal("a session was created")
	}
}

func TestAFailedTaskSendDeletesTheSession(t *testing.T) {
	f := newFakePlatform(t)
	f.fail["POST /v1/agents/sessions/ses_0001/events"] = 400
	if _, err := f.runtime(t).Start(context.Background(), gradedSpec()); err == nil {
		t.Fatal("Start succeeded")
	}
	if !f.sessions["ses_0001"].deleted {
		t.Fatal("the session was left behind")
	}
}

func TestCapabilitiesDeclareThePreferencesNetwork(t *testing.T) {
	for preference, want := range map[string]managed.NetworkMode{"open": managed.NetworkOpen, "none": managed.NetworkNone, "listed": ""} {
		f := newFakePlatform(t)
		f.preference = preference
		r := f.runtime(t)
		capabilities, err := r.Capabilities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want == "" && len(capabilities.NetworkModes) != 0 || want != "" && (len(capabilities.NetworkModes) != 1 || !capabilities.NetworkModes[want]) {
			t.Fatalf("%s: network modes = %v", preference, capabilities.NetworkModes)
		}
		if capabilities.OutputDir != OutputDir || !capabilities.Evaluation || capabilities.FindingsRounds || capabilities.InputDir != "" {
			t.Fatalf("capabilities = %+v", capabilities)
		}
	}
}

// TestASessionRunsOnTheEnvironmentItNames checks that a session naming an
// environment the deployment offers runs on that environment's image, and
// that a package is judged against what that image holds: scipy is in the
// science image and not in the base one.
func TestASessionRunsOnTheEnvironmentItNames(t *testing.T) {
	f := newFakePlatform(t)
	r, err := New(Config{BaseURL: f.server.URL, HTTP: f.server.Client(), Environments: func(context.Context) ([]string, error) { return []string{"base", "science"}, nil },
		Tokens: TokenFunc(func(ctx context.Context) (string, error) { return "bearer-1", nil })})
	if err != nil {
		t.Fatal(err)
	}
	r.sleep = func(context.Context, time.Duration) error { return nil }
	capabilities, err := r.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Environments["base"] || !capabilities.Environments["science"] || capabilities.Environments["gui"] {
		t.Fatalf("offered environments = %v, want base and science", capabilities.Environments)
	}

	spec := gradedSpec()
	spec.Sandbox.Packages = append(spec.Sandbox.Packages, managed.Package{Manager: "pip", Name: "scipy"}, managed.Package{Manager: "cran", Name: "lme4"})
	if err := r.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("scipy and lme4 on the base environment: Validate = %v, want unsupported", err)
	}
	spec.Sandbox.Environment = "science"
	if _, err := r.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start on science: %v", err)
	}
	var images []string
	for _, versions := range f.agents {
		for _, raw := range versions {
			var manifest agentManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Spec.Machine.Image != "" {
				images = append(images, manifest.Spec.Machine.Image)
			}
		}
	}
	if len(images) != 1 || images[0] != "science" {
		t.Fatalf("the agent's machine images = %v, want [science]", images)
	}
}

// TestTheEnvironmentsOfferedAreReadEachTime checks that the environments a
// deployment offers are read when a session asks, so an admin's change applies
// to the next one; that a failed read offers the base environment alone; that
// a name the runtime does not know is ignored and the base one is always
// offered; and that a runtime with nothing to read offers every environment it
// knows, as an operator's own machine does.
func TestTheEnvironmentsOfferedAreReadEachTime(t *testing.T) {
	f := newFakePlatform(t)
	list, failure := []string{"base"}, error(nil)
	r, err := New(Config{BaseURL: f.server.URL, HTTP: f.server.Client(),
		Environments: func(context.Context) ([]string, error) { return list, failure },
		Tokens:       TokenFunc(func(ctx context.Context) (string, error) { return "bearer-1", nil })})
	if err != nil {
		t.Fatal(err)
	}
	offered := func() map[string]bool {
		t.Helper()
		capabilities, err := r.Capabilities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return capabilities.Environments
	}
	if got := offered(); got["science"] || !got["base"] {
		t.Fatalf("offered %v, want base alone", got)
	}
	list = []string{"science", "fortran"}
	if got := offered(); !got["science"] || !got["base"] || got["fortran"] {
		t.Fatalf("after the admin's change, offered %v, want base and science", got)
	}
	failure = errors.New("settings unavailable")
	if got := offered(); got["science"] || !got["base"] {
		t.Fatalf("on a failed read, offered %v, want base alone", got)
	}

	all, err := New(Config{BaseURL: f.server.URL, HTTP: f.server.Client(),
		Tokens: TokenFunc(func(ctx context.Context) (string, error) { return "bearer-1", nil })})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := all.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name := range KnownEnvironments {
		if !capabilities.Environments[name] {
			t.Errorf("a runtime with nothing to read does not offer %s", name)
		}
	}
}
