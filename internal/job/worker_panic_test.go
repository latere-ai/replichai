// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// panickingRunner panics in the named phase, as a runner reaching a nil
// dependency would.
type panickingRunner struct {
	*fakeRunner
	at string
}

func (p *panickingRunner) Execute(ctx context.Context, j *Job, observe func(provider.RunStatus)) error {
	if p.at == "execute" {
		panic("nil engine")
	}
	return p.fakeRunner.Execute(ctx, j, observe)
}

func (p *panickingRunner) Publish(ctx context.Context, j *Job) error {
	if p.at == "publish" {
		panic("nil engine")
	}
	return p.fakeRunner.Publish(ctx, j)
}

// TestARunnerPanicFailsTheJobNotTheProcess checks that a panic inside the
// runner is one job's failure, recorded on its row with the cause, and that
// the worker goes on to the next job. Before this fix the same panic took the
// server down, and a row left in an active phase made the next boot panic
// again.
func TestARunnerPanicFailsTheJobNotTheProcess(t *testing.T) {
	for _, at := range []string{"execute", "publish"} {
		t.Run(at, func(t *testing.T) {
			store := NewMemStore()
			w := NewWorker(store, &panickingRunner{fakeRunner: &fakeRunner{}, at: at})
			j, err := store.Enqueue(context.Background(), Submission{PaperKey: anyKey(), Principal: "p", Project: "proj", Ref: "paper"})
			if err != nil {
				t.Fatal(err)
			}
			drained(t, w)

			got, _ := store.Get(context.Background(), j.ID)
			if got.Phase != PhaseFailed {
				t.Fatalf("phase = %s, want failed", got.Phase)
			}
			if !strings.Contains(got.Err, "runner panicked") || !strings.Contains(got.Err, "nil engine") {
				t.Fatalf("cause = %q, want the panic named", got.Err)
			}
		})
	}
}

// TestAPanicWhileReconcilingIsContained checks that a panic in one of the
// reconciler's rejoin goroutines is contained the same way, so a restart with
// one bad row does not crash-loop.
func TestAPanicWhileReconcilingIsContained(t *testing.T) {
	store := NewMemStore()
	j, _ := store.Enqueue(context.Background(), Submission{PaperKey: anyKey(), Principal: "p", Project: "proj", Ref: "paper"})
	_, _, _ = store.Claim(context.Background(), "old-worker")
	_ = store.Advance(context.Background(), j.ID, PhaseRunning, nil)
	_ = store.Observe(context.Background(), j.ID, provider.RunStatus{RunID: "run-1"})
	_ = store.Advance(context.Background(), j.ID, PhasePublishing, nil)

	w := NewWorker(store, &panickingRunner{fakeRunner: &fakeRunner{}, at: "publish"}, WithWorkerID("old-worker"))
	resumed, _, err := w.Reconcile(context.Background())
	if err != nil || resumed != 1 {
		t.Fatalf("Reconcile: resumed=%d err=%v", resumed, err)
	}
	// The rejoin runs on its own goroutine; wait, bounded, for the row to settle.
	for range 500 {
		got, _ := store.Get(context.Background(), j.ID)
		if got.Phase == PhaseFailed {
			if !strings.Contains(got.Err, "runner panicked") {
				t.Fatalf("cause = %q", got.Err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	got, _ := store.Get(context.Background(), j.ID)
	t.Fatalf("row still in %s after the rejoin panicked", got.Phase)
}
