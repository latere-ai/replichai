package managed

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type controllerRuntime struct {
	name        RuntimeName
	ref         SessionRef
	statuses    []Status
	pollErrors  []error
	events      []Event
	artifacts   []Artifact
	files       map[string][]byte
	validate    error
	startErr    error
	eventsErr   error
	listErr     error
	cancelErr   error
	releaseErr  error
	calls       []string
	polls       int
	statusPolls int
}

func newControllerRuntime() *controllerRuntime {
	ref := SessionRef{Runtime: "fake", ID: "session-1", Revision: 1, LocatorVersion: 1}
	model := ModelRef{Provider: "fake", ID: "model"}
	return &controllerRuntime{
		name: "fake",
		ref:  ref,
		statuses: []Status{
			{Ref: SessionRef{Runtime: "fake", ID: "session-1", Revision: 2, LocatorVersion: 1}, Model: model, State: StateRunning, Usage: Usage{InputFresh: 1}},
			{Ref: SessionRef{Runtime: "fake", ID: "session-1", Revision: 2, LocatorVersion: 1}, Model: model, State: StateDone, Usage: Usage{InputFresh: 2}},
		},
		events:    []Event{{ID: "event-1", Kind: KindMessage, Content: "done"}},
		artifacts: []Artifact{{ID: "artifact-1", Path: "result.txt"}},
		files:     map[string][]byte{"result.txt": []byte("result")},
	}
}

func (r *controllerRuntime) Name() RuntimeName { return r.name }
func (r *controllerRuntime) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{}, nil
}
func (r *controllerRuntime) Models(context.Context) ([]Model, error) { return nil, nil }
func (r *controllerRuntime) Validate(SessionSpec) error {
	r.calls = append(r.calls, "validate")
	return r.validate
}
func (r *controllerRuntime) Start(context.Context, SessionSpec) (SessionRef, error) {
	r.calls = append(r.calls, "start")
	return r.ref, r.startErr
}
func (r *controllerRuntime) Poll(context.Context, SessionRef) (Status, error) {
	r.calls = append(r.calls, "poll")
	if r.polls < len(r.pollErrors) && r.pollErrors[r.polls] != nil {
		err := r.pollErrors[r.polls]
		r.polls++
		return Status{}, err
	}
	r.polls++
	index := r.statusPolls
	r.statusPolls++
	if index >= len(r.statuses) {
		index = len(r.statuses) - 1
	}
	return r.statuses[index], nil
}
func (r *controllerRuntime) Events(context.Context, SessionRef) ([]Event, error) {
	r.calls = append(r.calls, "events")
	return r.events, r.eventsErr
}
func (r *controllerRuntime) ListArtifacts(context.Context, SessionRef) ([]Artifact, error) {
	r.calls = append(r.calls, "list")
	return r.artifacts, r.listErr
}
func (r *controllerRuntime) FetchArtifact(_ context.Context, _ SessionRef, artifact Artifact) ([]byte, error) {
	r.calls = append(r.calls, "fetch")
	return r.files[artifact.Path], nil
}
func (r *controllerRuntime) Cancel(context.Context, SessionRef) error {
	r.calls = append(r.calls, "cancel")
	return r.cancelErr
}
func (r *controllerRuntime) Release(context.Context, SessionRef) error {
	r.calls = append(r.calls, "release")
	return r.releaseErr
}

type controllerRouter struct {
	runtime Runtime
	newErr  error
	refErr  error
}

func (r controllerRouter) ForNew(SessionSpec) (Runtime, error) { return r.runtime, r.newErr }
func (r controllerRouter) ForRef(SessionRef) (Runtime, error)  { return r.runtime, r.refErr }

func controllerSpec(graded bool) SessionSpec {
	spec := SessionSpec{Model: ModelSelector{Preference: "best"}, Instructions: "instructions", Task: "task"}
	if graded {
		spec.Evaluation = &EvaluationSpec{Rubric: "rubric", MaxRevisions: 1}
	}
	return spec
}

func instantController(runtime Runtime) *Controller {
	clock := time.Unix(100, 0)
	return NewController(controllerRouter{runtime: runtime}, WithWaitPolicy(WaitPolicy{
		InitialDelay: 1, MaxDelay: 4, MaxTemporaryErrors: 3, MaxSilence: time.Minute, CleanupTimeout: time.Second,
	}), withControllerClock(func() time.Time { return clock }, func(context.Context, time.Duration) error {
		clock = clock.Add(time.Second)
		return nil
	}))
}

func TestControllerExecuteOrdersCheckpointEvidenceAndRelease(t *testing.T) {
	runtime := newControllerRuntime()
	var hooks []string
	controller := instantController(runtime)
	result, err := controller.Execute(context.Background(), controllerSpec(false), Hooks{
		Started: func(_ context.Context, ref SessionRef) error {
			hooks = append(hooks, fmt.Sprintf("started:%d", ref.Revision))
			return nil
		},
		Checkpoint: func(_ context.Context, previous, next SessionRef) error {
			hooks = append(hooks, fmt.Sprintf("checkpoint:%d:%d", previous.Revision, next.Revision))
			return nil
		},
		Status: func(_ context.Context, status Status) error {
			hooks = append(hooks, "status:"+status.State.String())
			return nil
		},
		Retain: func(ctx context.Context, evidence EvidenceReader) error {
			hooks = append(hooks, "retain")
			body, err := evidence.Fetch(ctx, evidence.Artifacts[0])
			if err != nil || string(body) != "result" {
				t.Fatalf("Fetch = %q, %v", body, err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Released || result.Status.State != StateDone || result.Ref.Revision != 2 {
		t.Fatalf("result = %+v", result)
	}
	wantHooks := []string{"started:1", "status:pending", "checkpoint:1:2", "status:running", "status:done", "retain"}
	if !reflect.DeepEqual(hooks, wantHooks) {
		t.Fatalf("hooks = %v, want %v", hooks, wantHooks)
	}
	wantCalls := []string{"validate", "start", "poll", "poll", "events", "list", "fetch", "release"}
	if !reflect.DeepEqual(runtime.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", runtime.calls, wantCalls)
	}
}

func TestControllerRetriesTemporaryPollErrors(t *testing.T) {
	runtime := newControllerRuntime()
	runtime.pollErrors = []error{
		&TemporaryError{Err: errors.New("one")},
		&TemporaryError{Err: errors.New("two"), After: 3 * time.Second},
	}
	var sleeps []time.Duration
	now := time.Unix(0, 0)
	controller := NewController(controllerRouter{runtime: runtime}, WithWaitPolicy(WaitPolicy{
		InitialDelay: time.Second, MaxDelay: 2 * time.Second, MaxTemporaryErrors: 4, MaxSilence: time.Minute, CleanupTimeout: time.Second,
	}), withControllerClock(func() time.Time { return now }, func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		now = now.Add(delay)
		return nil
	}))
	if _, err := controller.Execute(context.Background(), controllerSpec(false), Hooks{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(sleeps) < 3 || sleeps[0] != time.Second || sleeps[1] != 3*time.Second {
		t.Fatalf("sleeps = %v", sleeps)
	}
}

func TestControllerExhaustedTemporaryErrorsCancelsThenRetainsThenReleases(t *testing.T) {
	runtime := newControllerRuntime()
	temporary := &TemporaryError{Err: errors.New("offline")}
	runtime.pollErrors = []error{temporary, temporary, temporary}
	controller := instantController(runtime)
	_, err := controller.Execute(context.Background(), controllerSpec(false), Hooks{Retain: func(context.Context, EvidenceReader) error { return nil }})
	if !errors.Is(err, temporary) {
		t.Fatalf("Execute error = %v", err)
	}
	cancel := sliceIndex(runtime.calls, "cancel")
	events := sliceIndex(runtime.calls, "events")
	release := sliceIndex(runtime.calls, "release")
	if cancel < 0 || events <= cancel+1 || runtime.calls[cancel+1] != "poll" || release <= events {
		t.Fatalf("cleanup calls = %v", runtime.calls)
	}
}

func TestRetentionFailurePreservesRuntimeEvidence(t *testing.T) {
	runtime := newControllerRuntime()
	retention := errors.New("disk full")
	result, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{
		Retain: func(context.Context, EvidenceReader) error { return retention },
	})
	if !errors.Is(err, retention) || result.Released {
		t.Fatalf("result/error = %+v, %v", result, err)
	}
	if slicesContain(runtime.calls, "release") {
		t.Fatalf("release called after retention failure: %v", runtime.calls)
	}
}

func TestReleaseFailureReturnsResultAndCleanupError(t *testing.T) {
	runtime := newControllerRuntime()
	runtime.releaseErr = errors.New("archive failed")
	result, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{})
	var cleanup *CleanupError
	if !errors.As(err, &cleanup) || result.Status.State != StateDone || result.Released {
		t.Fatalf("result/error = %+v, %v", result, err)
	}
}

func TestStartedAndCheckpointFailuresAbort(t *testing.T) {
	for _, test := range []struct {
		name  string
		hooks Hooks
	}{
		{"started", Hooks{Started: func(context.Context, SessionRef) error { return errors.New("persist") }}},
		{"checkpoint", Hooks{Checkpoint: func(context.Context, SessionRef, SessionRef) error { return errors.New("cas") }}},
		{"status", Hooks{Status: func(_ context.Context, status Status) error {
			if status.State == StateRunning {
				return errors.New("observe")
			}
			return nil
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newControllerRuntime()
			if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), test.hooks); err == nil {
				t.Fatal("failure was ignored")
			}
			if !slicesContain(runtime.calls, "cancel") || !slicesContain(runtime.calls, "release") {
				t.Fatalf("calls = %v", runtime.calls)
			}
		})
	}
}

func TestControllerValidationAndRoutingFailuresDoNotStart(t *testing.T) {
	runtime := newControllerRuntime()
	runtime.validate = errors.New("unsupported")
	if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{}); err == nil {
		t.Fatal("validation failure ignored")
	}
	if slicesContain(runtime.calls, "start") {
		t.Fatalf("start called after validation failure: %v", runtime.calls)
	}
	controller := NewController(controllerRouter{runtime: runtime, newErr: errors.New("no route")})
	if _, err := controller.Execute(context.Background(), controllerSpec(false), Hooks{}); err == nil {
		t.Fatal("route failure ignored")
	}
	runtime.validate = nil
	runtime.startErr = errors.New("create failed")
	if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{}); err == nil {
		t.Fatal("start failure ignored")
	}
}

func TestControllerRejectsRuntimeAndStatusContractViolations(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*controllerRuntime)
	}{
		{"start runtime", func(r *controllerRuntime) { r.ref.Runtime = "other" }},
		{"status identity", func(r *controllerRuntime) { r.statuses[0].Ref.ID = "other" }},
		{"locator without revision", func(r *controllerRuntime) {
			r.statuses[1].Ref.Locator = []byte(`{"stage":2}`)
		}},
		{"invalid state", func(r *controllerRuntime) { r.statuses[0].State = StateUnknown }},
		{"state regression", func(r *controllerRuntime) { r.statuses[0].State = StateRunning; r.statuses[1].State = StatePending }},
		{"usage regression", func(r *controllerRuntime) { r.statuses[0].Usage.InputFresh = 3; r.statuses[1].Usage.InputFresh = 2 }},
		{"model change", func(r *controllerRuntime) { r.statuses[1].Model.ID = "other" }},
		{"ungraded verdict", func(r *controllerRuntime) { r.statuses[1].Verdict = &Verdict{Grade: GradePassed} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newControllerRuntime()
			test.alter(runtime)
			_, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{})
			if err == nil || (!errors.Is(err, ErrContract) && test.name != "start runtime") {
				t.Fatalf("Execute = %v", err)
			}
		})
	}
}

func TestGradedSessionRequiresDecidedVerdict(t *testing.T) {
	runtime := newControllerRuntime()
	if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(true), Hooks{}); !errors.Is(err, ErrContract) {
		t.Fatalf("missing verdict = %v", err)
	}
	runtime = newControllerRuntime()
	runtime.statuses[1].Verdict = &Verdict{Grade: GradePassed}
	if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(true), Hooks{}); err != nil {
		t.Fatalf("decided verdict: %v", err)
	}
}

func TestResumeAndCancelRouteByReference(t *testing.T) {
	runtime := newControllerRuntime()
	controller := instantController(runtime)
	if _, err := controller.Resume(context.Background(), runtime.ref, Hooks{}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	runtime = newControllerRuntime()
	runtime.statuses[1].State = StateFailed
	runtime.statuses[1].TerminalReason = ReasonCancelled
	controller = instantController(runtime)
	result, err := controller.Cancel(context.Background(), runtime.ref, Hooks{})
	if !errors.Is(err, context.Canceled) || !result.Released || result.Status.TerminalReason != ReasonCancelled {
		t.Fatalf("Cancel = %+v, %v", result, err)
	}
	wantOrder := []string{"cancel", "poll", "poll", "events", "list", "release"}
	if !reflect.DeepEqual(runtime.calls, wantOrder) {
		t.Fatalf("Cancel calls = %v, want %v", runtime.calls, wantOrder)
	}
	missing := NewController(controllerRouter{refErr: errors.New("missing adapter")})
	if _, err := missing.Resume(context.Background(), runtime.ref, Hooks{}); err == nil {
		t.Fatal("Resume route error ignored")
	}
	if _, err := missing.Cancel(context.Background(), runtime.ref, Hooks{}); err == nil {
		t.Fatal("Cancel route error ignored")
	}
}

func TestEvidenceReadFailuresPreventRelease(t *testing.T) {
	for _, field := range []string{"events", "artifacts"} {
		t.Run(field, func(t *testing.T) {
			runtime := newControllerRuntime()
			if field == "events" {
				runtime.eventsErr = errors.New("events unavailable")
			} else {
				runtime.listErr = errors.New("artifacts unavailable")
			}
			if _, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{}); err == nil {
				t.Fatal("evidence failure ignored")
			}
			if slicesContain(runtime.calls, "release") {
				t.Fatalf("released after evidence failure: %v", runtime.calls)
			}
		})
	}
}

func TestAbortJoinsCancelFailureAndToleratesNotFound(t *testing.T) {
	for _, test := range []struct {
		name      string
		cancelErr error
		wantText  string
	}{
		{"failure", errors.New("cancel failed"), "cancel failed"},
		{"not found", &NotFoundError{Runtime: "fake", ID: "session-1"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newControllerRuntime()
			runtime.cancelErr = test.cancelErr
			runtime.pollErrors = []error{errors.New("poll failed")}
			_, err := instantController(runtime).Execute(context.Background(), controllerSpec(false), Hooks{})
			if err == nil || (test.wantText != "" && !strings.Contains(err.Error(), test.wantText)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSleepContextAndBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("zero sleep = %v", err)
	}
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("zero sleep background = %v", err)
	}
	if got := nextDelay(2, 3); got != 3 || nextDelay(0, 3) != 0 || nextDelay(2, 0) != 2 {
		t.Fatalf("backoff values wrong: %v", got)
	}
}

func slicesContain(values []string, target string) bool {
	return sliceIndex(values, target) >= 0
}

func sliceIndex(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
