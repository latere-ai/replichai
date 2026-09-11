// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// projectFloor is the backstop as the composition root wires it: the projection
// is the project's balance less what every in-flight session in it has accrued,
// compared against a floor.
func projectFloor(s Store, ledger credit.Store, floor int64) Budget {
	return func(ctx context.Context, j Job) (bool, string, error) {
		if j.ProjectID == "" {
			return false, "", nil
		}
		balance, err := ledger.Balance(ctx, credit.Pot(j.ProjectID))
		if err != nil {
			return false, "", err
		}
		accrued, err := s.Accrued(ctx, j.ProjectID)
		if err != nil {
			return false, "", err
		}
		if balance-accrued <= -floor {
			return true, "over the floor", nil
		}
		return false, "", nil
	}
}

// TestTheFloorIsProjectWideNotPerSession checks that the floor is a property
// of the project, not of the session that happened to observe. Accrued spend
// does not reach the ledger until a session settles, so a check subtracting
// only the observing session's own cost would let each of N concurrent runs
// stay above the floor while together they exceeded it by far.
//
// The test fails against a per-session projection: with a floor of $10 and
// three sessions at $8 each, a per-session check passes every one of them.
func TestTheFloorIsProjectWideNotPerSession(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 6*credit.Dollar)

	// Three sessions, each having spent $8: individually under a $10 floor
	// against a $6 balance, jointly $18 over it.
	burn := provider.RunStatus{
		Model: "cheap",
		Usage: provider.Usage{InputTokens: 8_000_000},
	}
	var ids []string
	for _, ref := range []string{"a", "b", "c"} {
		j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		if err := s.Observe(ctx, j.ID, burn); err != nil {
			t.Fatalf("observe: %v", err)
		}
		ids = append(ids, j.ID)
	}

	const floor = 10 * credit.Dollar
	budget := projectFloor(s, ledger, floor)

	// The project-wide projection sees all of it and stops the observed session.
	first, _ := s.Get(ctx, ids[0])
	stop, _, err := budget(ctx, first)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	if !stop {
		t.Fatal("the projection cleared a project $18 past its floor")
	}

	// The per-session projection this replaces would not have: each session on
	// its own is $2 short of the floor.
	perSession := 6*credit.Dollar - first.Cost
	if perSession <= -floor {
		t.Fatalf("the fixture does not discriminate: per-session projection %d is already past the floor", perSession)
	}
}

// TestAStoppedSessionRecordsOnlyThatItFailed checks that a session the
// backstop stops records that it did not complete and nothing about why. The
// reason names the limit, and the limit must not leak.
func TestAStoppedSessionRecordsOnlyThatItFailed(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	store := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 6*credit.Dollar)

	j, err := store.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.Observe(ctx, j.ID, provider.RunStatus{
		Model: "cheap", Usage: provider.Usage{InputTokens: 30_000_000},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	w := NewWorker(store, nil, WithBudget(projectFloor(store, ledger, 10*credit.Dollar)))
	w.watch(ctx, j.ID)

	got, err := store.Get(ctx, j.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Phase != PhaseFailed {
		t.Fatalf("phase = %s, want failed", got.Phase)
	}
	if got.Err != GenericFailure {
		t.Errorf("recorded cause = %q, want the generic one", got.Err)
	}
	if got.Err == CancelCause {
		t.Error("a stopped run reads as a deliberate cancel; the owner did not stop it")
	}
	// It settled: the tokens it used are charged and its reserve is released.
	bal, _ := ledger.Balance(ctx, credit.Pot("p"))
	avail, _ := ledger.Available(ctx, credit.Pot("p"))
	if bal != avail {
		t.Errorf("balance %d != available %d — a stopped run left its hold open", bal, avail)
	}
	if bal >= 6*credit.Dollar {
		t.Errorf("balance = %d; a stopped run must still pay for what it burned", bal)
	}
}

// TestABrokenBackstopStopsNothing checks that a backstop that cannot read its
// inputs does not stop anything. A run stopped because of a transient database
// fault would turn that fault into lost work, which is worse than the
// overspend it was guarding against.
func TestABrokenBackstopStopsNothing(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	store := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 6*credit.Dollar)

	j, err := store.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	w := NewWorker(store, nil, WithBudget(func(context.Context, Job) (bool, string, error) {
		return false, "", errors.New("postgres is not answering")
	}))
	w.watch(ctx, j.ID)

	got, _ := store.Get(ctx, j.ID)
	if got.Phase.Terminal() {
		t.Errorf("phase = %s; a backstop that could not read its inputs killed a healthy run", got.Phase)
	}
}
