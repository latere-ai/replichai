// Package contracttest contains the black-box checks shared by managed
// runtime adapters.
package contracttest

import (
	"context"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// Factory builds one runtime under test. Run calls it afresh for each case, so
// no state leaks from one case into the next.
type Factory func() managed.Runtime

// Run is the black-box suite every managed runtime adapter has to pass: the
// ungraded and graded lifecycles, that cancelling keeps the evidence readable,
// and that a ref naming another runtime is refused by Poll, Events,
// ListArtifacts, Cancel and Release.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("ungraded lifecycle", func(t *testing.T) {
		runtime := factory()
		ref := start(t, runtime, spec(false))
		status := pollTerminal(t, runtime, ref)
		if status.State != managed.StateDone || status.Verdict != nil {
			t.Fatalf("ungraded terminal status = %+v", status)
		}
		if _, err := runtime.Events(context.Background(), ref); err != nil {
			t.Fatalf("Events before release: %v", err)
		}
		if err := runtime.Release(context.Background(), ref); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if err := runtime.Release(context.Background(), ref); err != nil {
			t.Fatalf("repeated Release: %v", err)
		}
		if _, err := runtime.Events(context.Background(), ref); !errors.Is(err, managed.ErrNotFound) {
			t.Fatalf("Events after release = %v", err)
		}
	})

	t.Run("graded lifecycle", func(t *testing.T) {
		runtime := factory()
		status := pollTerminal(t, runtime, start(t, runtime, spec(true)))
		if status.Verdict == nil || !status.Verdict.Grade.Decided() {
			t.Fatalf("graded terminal status = %+v", status)
		}
	})

	t.Run("cancel retains evidence", func(t *testing.T) {
		runtime := factory()
		ref := start(t, runtime, spec(false))
		if err := runtime.Cancel(context.Background(), ref); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if err := runtime.Cancel(context.Background(), ref); err != nil {
			t.Fatalf("repeated Cancel: %v", err)
		}
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil || status.State != managed.StateFailed || status.TerminalReason != managed.ReasonCancelled {
			t.Fatalf("Poll after cancel = %+v, %v", status, err)
		}
		if _, err := runtime.Events(context.Background(), ref); err != nil {
			t.Fatalf("Events after cancel: %v", err)
		}
	})

	t.Run("runtime mismatch", func(t *testing.T) {
		runtime := factory()
		ref := start(t, runtime, spec(false))
		ref.Runtime = "another-runtime"
		calls := []func() error{
			func() error { _, err := runtime.Poll(context.Background(), ref); return err },
			func() error { _, err := runtime.Events(context.Background(), ref); return err },
			func() error { _, err := runtime.ListArtifacts(context.Background(), ref); return err },
			func() error { return runtime.Cancel(context.Background(), ref) },
			func() error { return runtime.Release(context.Background(), ref) },
		}
		for index, call := range calls {
			if err := call(); !errors.Is(err, managed.ErrRuntimeMismatch) {
				t.Errorf("call %d = %v", index, err)
			}
		}
	})
}

func spec(graded bool) managed.SessionSpec {
	spec := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: "Follow the method.",
		Task:         "Reproduce the result.",
		Sandbox:      managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkNone}},
	}
	if graded {
		spec.Evaluation = &managed.EvaluationSpec{Rubric: "The result matches.", MaxRevisions: 1}
	}
	return spec
}

func start(t *testing.T, runtime managed.Runtime, spec managed.SessionSpec) managed.SessionRef {
	t.Helper()
	if err := runtime.Validate(spec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ref, err := runtime.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		t.Fatalf("invalid start ref: %v", err)
	}
	return ref
}

func pollTerminal(t *testing.T, runtime managed.Runtime, ref managed.SessionRef) managed.Status {
	t.Helper()
	var previous managed.Usage
	for range 20 {
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if !status.Ref.SameIdentity(ref) || !status.Model.Valid() || !status.Usage.MonotonicFrom(previous) {
			t.Fatalf("invalid status = %+v", status)
		}
		previous = status.Usage
		if status.State.Terminal() {
			return status
		}
	}
	t.Fatal("session did not become terminal")
	return managed.Status{}
}
