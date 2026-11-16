// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"testing"
	"time"

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
