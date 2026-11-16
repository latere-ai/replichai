// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"latere.ai/x/pkg/statemachine/statemachinetest"

	"github.com/latere-ai/replichai/internal/job"
)

// activeStrings is the job package's active phases as the project store takes
// them.
func activeStrings() []string {
	var out []string
	for _, p := range job.ActivePhases() {
		out = append(out, string(p))
	}
	return out
}

// runPublicationContract asserts the publication machine through a store's own
// writers: the history keeps every publication and withdrawal, a signer's
// handle is kept with the sign-off, publish and withdraw refuse out-of-order
// calls, a trashed project is neither published nor withdrawn, and restore
// leaves only the trash.
func runPublicationContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	p, err := s.Create(ctx, "pub-owner", "History")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SetPrivate(ctx, p.ID, Withdrawal{By: "pub-owner"}); !errors.Is(err, ErrNotPublic) {
		t.Errorf("withdrawing a private project = %v, want ErrNotPublic", err)
	}
	if err := s.Restore(ctx, p.ID); !errors.Is(err, ErrNotTrashed) {
		t.Errorf("restoring a private project = %v, want ErrNotTrashed", err)
	}

	first := SignOff{By: "pub-owner", AttestationVersion: "v1", Name: "Ada", Contact: "a@b.c", Handle: "ada"}
	if err := s.SetPublic(ctx, p.ID, first); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got, _ := s.Get(ctx, p.ID); got.SignedOffHandle != "ada" {
		t.Errorf("the signer's handle was not kept with the sign-off: %+v", got)
	}
	if err := s.SetPublic(ctx, p.ID, first); !errors.Is(err, ErrAlreadyPublic) {
		t.Errorf("publishing a public project = %v, want ErrAlreadyPublic", err)
	}
	if err := s.SetPrivate(ctx, p.ID, Withdrawal{By: "pub-owner", Reason: "typo"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	second := SignOff{By: "pub-owner", AttestationVersion: "v2", Name: "Ada L", Contact: "a@b.c", Handle: "ada2"}
	if err := s.SetPublic(ctx, p.ID, second); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if err := s.SetPrivate(ctx, p.ID, Withdrawal{By: "pub-owner", Reason: "again"}); err != nil {
		t.Fatalf("second withdraw: %v", err)
	}

	hist, err := s.Publications(ctx, p.ID)
	if err != nil {
		t.Fatalf("Publications: %v", err)
	}
	want := []struct {
		kind PublicationKind
		text string
	}{
		{PublicationPublished, "v1"}, {PublicationWithdrawn, "typo"},
		{PublicationPublished, "v2"}, {PublicationWithdrawn, "again"},
	}
	if len(hist) != len(want) {
		t.Fatalf("history holds %d entries, want %d: %+v", len(hist), len(want), hist)
	}
	for i, w := range want {
		got := hist[i].AttestationVersion + hist[i].Reason
		if hist[i].Kind != w.kind || got != w.text {
			t.Errorf("history[%d] = %s %q, want %s %q", i, hist[i].Kind, got, w.kind, w.text)
		}
	}
	if hist[0].Handle != "ada" || hist[2].Handle != "ada2" || hist[0].Name != "Ada" {
		t.Errorf("the history lost a signer snapshot: %+v", hist)
	}
	if _, err := s.Publications(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Publications of a missing project = %v, want ErrNotFound", err)
	}

	// A trashed project is neither published nor withdrawn.
	if err := s.Trash(ctx, p.ID); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if err := s.SetPublic(ctx, p.ID, second); !errors.Is(err, ErrTrashed) {
		t.Errorf("publishing a trashed project = %v, want ErrTrashed", err)
	}
	if err := s.SetPrivate(ctx, p.ID, Withdrawal{By: "pub-owner"}); !errors.Is(err, ErrTrashed) {
		t.Errorf("withdrawing a trashed project = %v, want ErrTrashed", err)
	}
	if err := s.Restore(ctx, p.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if hist, _ := s.Publications(ctx, p.ID); len(hist) != len(want) {
		t.Errorf("refused calls added history: %+v", hist)
	}
}

func TestMemStoreKeepsPublicationHistory(t *testing.T) {
	runPublicationContract(t, NewMemStore())
}

func TestPGStoreKeepsPublicationHistory(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	runPublicationContract(t, NewPGStore(pool))
}

// TestPGStoreRefusesPublishAndTrashWithASessionInFlight seeds a running session
// and checks both writes are refused with ErrSessionsActive, then allowed once
// the session finished. Postgres-only: the check is a read under the project's
// row lock.
func TestPGStoreRefusesPublishAndTrashWithASessionInFlight(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	s := NewPGStore(pool, WithActivePhases(activeStrings()))
	p, err := s.Create(ctx, "busy-owner", "Busy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	seedSession(t, pool, legacySession{id: "busy-job", project: p.ID, principal: "busy-owner", ref: "r", key: "doi:1", phase: string(job.PhaseRunning), created: time.Now()})

	if err := s.SetPublic(ctx, p.ID, SignOff{By: "busy-owner", AttestationVersion: "v1"}); !errors.Is(err, ErrSessionsActive) {
		t.Errorf("publish with a running session = %v, want ErrSessionsActive", err)
	}
	if err := s.Trash(ctx, p.ID); !errors.Is(err, ErrSessionsActive) {
		t.Errorf("trash with a running session = %v, want ErrSessionsActive", err)
	}
	if got, _ := s.Get(ctx, p.ID); got.Visibility != VisibilityPrivate || got.Trashed() {
		t.Errorf("a refused write changed the project: %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET phase=$2 WHERE id=$1`, "busy-job", string(job.PhaseDone)); err != nil {
		t.Fatalf("finish the session: %v", err)
	}
	if err := s.SetPublic(ctx, p.ID, SignOff{By: "busy-owner", AttestationVersion: "v1"}); err != nil {
		t.Errorf("publish after the session finished: %v", err)
	}
}

// TestMemStoreRefusesPublishAndTrashWithASessionInFlight is the same contract
// against the in-memory store with a supplied read.
func TestMemStoreRefusesPublishAndTrashWithASessionInFlight(t *testing.T) {
	ctx := context.Background()
	busy := true
	s := NewMemStore(WithActiveSessions(func(context.Context, string) (bool, error) { return busy, nil }))
	p, _ := s.Create(ctx, "busy-owner", "Busy")
	if err := s.SetPublic(ctx, p.ID, SignOff{By: "busy-owner", AttestationVersion: "v1"}); !errors.Is(err, ErrSessionsActive) {
		t.Errorf("publish = %v, want ErrSessionsActive", err)
	}
	if err := s.Trash(ctx, p.ID); !errors.Is(err, ErrSessionsActive) {
		t.Errorf("trash = %v, want ErrSessionsActive", err)
	}
	busy = false
	if err := s.Trash(ctx, p.ID); err != nil {
		t.Errorf("trash after the session finished: %v", err)
	}
}

// runPaperMissContract asserts a lookup that found nothing is recorded on the
// paper, that the sweep's wait is read from it, and that a missing paper is not
// an error.
func runPaperMissContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	p, err := s.Create(ctx, "miss-owner", "Misses")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.AddPaper(ctx, p.ID, Paper{Key: "doi:10.1/miss", Ref: "doi:10.1/miss", AddedBy: "miss-owner"}); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	papers, _ := s.ListPapers(ctx, p.ID)
	if papers[0].DetailsMissedAt != nil || papers[0].RecentlyMissed(time.Now()) {
		t.Fatalf("a paper nobody looked up carries a miss: %+v", papers[0])
	}
	if err := s.RecordPaperMiss(ctx, p.ID, "doi:10.1/miss"); err != nil {
		t.Fatalf("RecordPaperMiss: %v", err)
	}
	papers, _ = s.ListPapers(ctx, p.ID)
	if papers[0].DetailsMissedAt == nil || !papers[0].RecentlyMissed(time.Now()) {
		t.Errorf("the miss was not recorded: %+v", papers[0])
	}
	if papers[0].RecentlyMissed(time.Now().Add(PaperRetryWait + time.Hour)) {
		t.Error("a miss still holds after the wait")
	}
	if err := s.RecordPaperMiss(ctx, p.ID, "doi:10.1/other"); err != nil {
		t.Errorf("a miss on a paper the project does not hold: %v", err)
	}
	if err := s.RecordPaperMiss(ctx, "nope", "doi:10.1/miss"); err != nil {
		t.Errorf("a miss on a missing project: %v", err)
	}
}

func TestMemStoreRecordsAPaperMiss(t *testing.T) { runPaperMissContract(t, NewMemStore()) }

func TestPGStoreRecordsAPaperMiss(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	runPaperMissContract(t, NewPGStore(pool))
}

// publicationDriver reaches a store's own writers for statemachinetest. Enter
// puts a project in a state, Move calls the writer that takes it there.
func publicationDriver(s Store, enter func(t *testing.T, st State) string) statemachinetest.Driver[State] {
	ctx := context.Background()
	return statemachinetest.Driver[State]{
		Enter: enter,
		Move: func(id string, to State) error {
			switch to {
			case StatePublic:
				return s.SetPublic(ctx, id, SignOff{By: "owner", AttestationVersion: "v1"})
			case StateTrashed:
				return s.Trash(ctx, id)
			}
			// Both withdrawing and restoring land in private. The writer is the
			// one for the state the project is in when the move starts.
			p, err := s.Get(ctx, id)
			if err != nil {
				return err
			}
			if p.State() == StateTrashed {
				return s.Restore(ctx, id)
			}
			return s.SetPrivate(ctx, id, Withdrawal{By: "owner"})
		},
		State: func(t *testing.T, id string) State {
			p, err := s.Get(ctx, id)
			if err != nil {
				t.Fatalf("read %s: %v", id, err)
			}
			return p.State()
		},
	}
}

// TestPGStorePublicationWritersFollowTheTable drives SetPublic, SetPrivate,
// Trash and Restore through every pair of states and every pair of racing steps
// against Postgres. Enter inserts the row in the state directly.
func TestPGStorePublicationWritersFollowTheTable(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	s := NewPGStore(pool)
	n := 0
	enter := func(t *testing.T, st State) string {
		t.Helper()
		n++
		id := fmt.Sprintf("table-%d", n)
		visibility, deleted := VisibilityPrivate, any(nil)
		switch st {
		case StatePublic:
			visibility = VisibilityPublic
		case StateTrashed:
			deleted = time.Now()
		}
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO projects (id, owner, title, visibility, created_at, deleted_at)
			VALUES ($1, 'owner', 't', $2, now(), $3)`, id, visibility, deleted); err != nil {
			t.Fatalf("insert a %s project: %v", st, err)
		}
		return id
	}
	d := publicationDriver(s, enter)
	statemachinetest.Exhaustive(t, Publications, d)
	statemachinetest.Race(t, Publications, d)
}

// TestMemStorePublicationWritersFollowTheTable is the same check against the
// in-memory store, whose rows Enter writes directly.
func TestMemStorePublicationWritersFollowTheTable(t *testing.T) {
	s := NewMemStore()
	n := 0
	enter := func(t *testing.T, st State) string {
		t.Helper()
		n++
		id := fmt.Sprintf("table-%d", n)
		p := Project{ID: id, Owner: "owner", Title: "t", Visibility: VisibilityPrivate, CreatedAt: time.Now()}
		switch st {
		case StatePublic:
			p.Visibility = VisibilityPublic
		case StateTrashed:
			now := time.Now()
			p.DeletedAt = &now
		}
		s.mu.Lock()
		s.projects[id] = p
		s.order = append(s.order, id)
		s.mu.Unlock()
		return id
	}
	d := publicationDriver(s, enter)
	statemachinetest.Exhaustive(t, Publications, d)
	statemachinetest.Race(t, Publications, d)
}

// runTrashWithdrawingContract asserts the one-step delete through a store's own
// writers. busy switches whether a session is in flight; it is called with the
// store's project id so the Postgres store can seed a real row.
func runTrashWithdrawingContract(t *testing.T, s Store, busy func(t *testing.T, id string, on bool)) {
	t.Helper()
	ctx := context.Background()
	sign := SignOff{By: "del-owner", AttestationVersion: "v1", Handle: "ada"}
	withdrawals := func(id string) (n int) {
		hist, err := s.Publications(ctx, id)
		if err != nil {
			t.Fatalf("Publications: %v", err)
		}
		for _, h := range hist {
			if h.Kind == PublicationWithdrawn {
				n++
			}
		}
		return n
	}

	// A session in flight refuses the delete and leaves the project public.
	pub, err := s.Create(ctx, "del-owner", "Public")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetPublic(ctx, pub.ID, sign); err != nil {
		t.Fatalf("publish: %v", err)
	}
	busy(t, pub.ID, true)
	if err := s.TrashWithdrawing(ctx, pub.ID, Withdrawal{By: "del-owner", Reason: "gone"}); !errors.Is(err, ErrSessionsActive) {
		t.Fatalf("delete with a session in flight = %v, want ErrSessionsActive", err)
	}
	got, _ := s.Get(ctx, pub.ID)
	if got.Visibility != VisibilityPublic || got.Trashed() || got.WithdrawnAt != nil {
		t.Errorf("a refused delete changed the project: %+v", got)
	}
	if n := withdrawals(pub.ID); n != 0 {
		t.Errorf("a refused delete recorded %d withdrawals", n)
	}

	// Without one it withdraws once and trashes.
	busy(t, pub.ID, false)
	if err := s.TrashWithdrawing(ctx, pub.ID, Withdrawal{By: "del-owner", Reason: "gone"}); err != nil {
		t.Fatalf("delete a public project: %v", err)
	}
	got, _ = s.Get(ctx, pub.ID)
	if got.Visibility != VisibilityPrivate || !got.Trashed() || got.WithdrawnAt == nil || got.WithdrawnBy != "del-owner" || got.WithdrawnReason != "gone" || got.SignedOffBy == "" {
		t.Errorf("deleted public project = %+v", got)
	}
	if n := withdrawals(pub.ID); n != 1 {
		t.Errorf("withdrawals recorded = %d, want 1", n)
	}
	if err := s.TrashWithdrawing(ctx, pub.ID, Withdrawal{By: "del-owner"}); !errors.Is(err, ErrTrashed) {
		t.Errorf("deleting a trashed project = %v, want ErrTrashed", err)
	}
	if n := withdrawals(pub.ID); n != 1 {
		t.Errorf("a second delete recorded a withdrawal: %d", n)
	}

	// A private project is trashed with no withdrawal.
	priv, _ := s.Create(ctx, "del-owner", "Private")
	if err := s.TrashWithdrawing(ctx, priv.ID, Withdrawal{By: "del-owner"}); err != nil {
		t.Fatalf("delete a private project: %v", err)
	}
	if got, _ := s.Get(ctx, priv.ID); !got.Trashed() || got.WithdrawnAt != nil {
		t.Errorf("deleted private project = %+v", got)
	}
	if n := withdrawals(priv.ID); n != 0 {
		t.Errorf("a private delete recorded %d withdrawals", n)
	}
	if err := s.TrashWithdrawing(ctx, "no-such-project", Withdrawal{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a missing project = %v, want ErrNotFound", err)
	}
}

func TestMemStoreTrashWithdrawing(t *testing.T) {
	var on bool
	s := NewMemStore(WithActiveSessions(func(context.Context, string) (bool, error) { return on, nil }))
	runTrashWithdrawingContract(t, s, func(_ *testing.T, _ string, v bool) { on = v })
}

func TestPGStoreTrashWithdrawing(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	s := NewPGStore(pool, WithActivePhases(activeStrings()))
	runTrashWithdrawingContract(t, s, func(t *testing.T, id string, on bool) {
		t.Helper()
		if on {
			seedSession(t, pool, legacySession{id: "del-job-" + id, project: id, principal: "del-owner", ref: "r", key: "doi:1", phase: string(job.PhaseRunning), created: time.Now()})
			return
		}
		if _, err := pool.Exec(ctx, `UPDATE jobs SET phase=$2 WHERE project_id=$1`, id, string(job.PhaseDone)); err != nil {
			t.Fatalf("finish the session: %v", err)
		}
	})
}
