// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// closeHookRunner runs onClose inside Close, standing in for the goroutine
// driving the run seeing its session end and writing its failure before the
// cancel path gets to its own write.
type closeHookRunner struct {
	*fakeRunner
	onClose func()
}

func (r closeHookRunner) Close(ctx context.Context, ref managed.SessionRef) error {
	if r.onClose != nil {
		r.onClose()
	}
	return r.fakeRunner.Close(ctx, ref)
}

// claimedRun enqueues a session, claims it for w and records a backend run id,
// so Worker.Cancel has a backend run to close.
func claimedRun(t *testing.T, s Store, w *Worker) Job {
	t.Helper()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, Submission{PaperKey: anyKey(), Principal: "sub", Ref: "ref", Kind: KindAudit})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, err := s.Claim(ctx, w.id); err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if err := s.Observe(WithWorker(ctx, w.id), j.ID, provider.RunStatus{State: provider.StateRunning, RunID: "sesn_cancel_race"}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return j
}

func runCancelRaceContract(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	t.Run("a failure written while the run is closed records the stop", func(t *testing.T) {
		s := newStore(t)
		hook := &closeHookRunner{fakeRunner: &fakeRunner{}}
		w := NewWorker(s, hook)
		j := claimedRun(t, s, w)
		hook.onClose = func() {
			if err := w.fail(j.ID, errors.New("session ended")); err != nil {
				t.Errorf("fail: %v", err)
			}
		}
		if err := w.Cancel(ctx, j.ID); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		got, _ := s.Get(ctx, j.ID)
		if got.Phase != PhaseFailed || got.StopReason != StopCanceled || got.Err != CancelCause {
			t.Errorf("got %s/%s/%q, want failed/canceled/%q", got.Phase, got.StopReason, got.Err, CancelCause)
		}
	})

	t.Run("a failure with no cancel requested is an error", func(t *testing.T) {
		s := newStore(t)
		w := NewWorker(s, &fakeRunner{})
		j := claimedRun(t, s, w)
		if err := w.fail(j.ID, errors.New("boom")); err != nil {
			t.Fatalf("fail: %v", err)
		}
		got, _ := s.Get(ctx, j.ID)
		if got.Phase != PhaseFailed || got.StopReason != StopError || got.Err != "boom" {
			t.Errorf("got %s/%s/%q, want failed/error/boom", got.Phase, got.StopReason, got.Err)
		}
	})

	t.Run("a cancel whose close fails leaves the row active, and its later failure reads as the stop", func(t *testing.T) {
		s := newStore(t)
		w := NewWorker(s, &fakeRunner{closeErr: errors.New("backend down")})
		j := claimedRun(t, s, w)
		if err := w.Cancel(ctx, j.ID); err == nil {
			t.Fatal("Cancel succeeded despite a failed close")
		}
		if got, _ := s.Get(ctx, j.ID); got.Phase.Terminal() {
			t.Fatalf("a failed close made the row %s", got.Phase)
		}
		if err := w.fail(j.ID, errors.New("session ended")); err != nil {
			t.Fatalf("fail: %v", err)
		}
		if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseFailed || got.StopReason != StopCanceled {
			t.Errorf("got %s/%s, want failed/canceled", got.Phase, got.StopReason)
		}
	})

	t.Run("a request on an unknown or finished session", func(t *testing.T) {
		s := newStore(t)
		if err := s.RequestCancel(ctx, "missing"); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown session: %v, want ErrNotFound", err)
		}
		w := NewWorker(s, &fakeRunner{})
		j := claimedRun(t, s, w)
		if err := w.advance(ctx, j.ID, PhaseFailed, func(j *Job) { j.Err = "x" }); err != nil {
			t.Fatalf("advance: %v", err)
		}
		if err := s.RequestCancel(ctx, j.ID); err != nil {
			t.Errorf("finished session: %v", err)
		}
		if got, _ := s.Get(ctx, j.ID); got.StopReason != StopError {
			t.Errorf("stop reason = %s, want error", got.StopReason)
		}
	})
}

func TestMemStoreCancelRace(t *testing.T) {
	runCancelRaceContract(t, func(*testing.T) Store { return NewMemStore() })
}

func TestPGStoreCancelRace(t *testing.T) {
	runCancelRaceContract(t, func(t *testing.T) Store { return NewPGStore(isolatedPG(t)) })
}
