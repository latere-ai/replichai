package job

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runMoveSessionsContract asserts what carrying a paper's sessions to another
// project must do (035). The mover is passed in rather than read off Store: it
// runs inside the project store's transaction where there is one, so the two
// implementations take different arguments and cannot share a method on the
// interface.
func runMoveSessionsContract(t *testing.T, s Store, move func(from, to, key string) error) {
	t.Helper()
	ctx := context.Background()
	const (
		from = "pFrom"
		to   = "pTo"
		key  = "doi:10.1145/3544548.3581433"
	)

	// Two runs of the paper being moved, one of them thrown away; one run of a
	// different paper in the same project; and one run of the same paper that
	// somebody else already has in the destination.
	//
	// Each run of the moving paper is finished before the next is enqueued: the
	// guard forbids two live runs of one paper, and terminal is the only state a
	// move is allowed in anyway.
	thrownAway, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "r1", Kind: KindAudit, Project: from, PaperKey: key})
	if err != nil {
		t.Fatalf("enqueue thrown away: %v", err)
	}
	if err := s.Advance(ctx, thrownAway.ID, PhaseFailed, func(j *Job) { j.Err, j.Status = "over", burned }); err != nil {
		t.Fatalf("advance thrown away: %v", err)
	}
	if err := s.Delete(ctx, thrownAway.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	moving, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "r2", Kind: KindAudit, Project: from, PaperKey: key})
	if err != nil {
		t.Fatalf("enqueue moving: %v", err)
	}
	if err := s.Advance(ctx, moving.ID, PhaseFailed, func(j *Job) { j.Err = "over" }); err != nil {
		t.Fatalf("advance moving: %v", err)
	}
	sibling, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "r3", Kind: KindAudit, Project: from, PaperKey: "arxiv:2401.00001"})
	if err != nil {
		t.Fatalf("enqueue sibling: %v", err)
	}

	before, err := s.Get(ctx, moving.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	if err := move(from, to, key); err != nil {
		t.Fatalf("move: %v", err)
	}

	// The paper's live session is under the destination now, and the sibling
	// paper's is not.
	if got, _ := s.ListByProject(ctx, to); len(got) != 1 || got[0].ID != moving.ID {
		t.Errorf("destination holds %s; want only %s", first3(got), moving.ID)
	}
	if got, _ := s.ListByProject(ctx, from); len(got) != 1 || got[0].ID != sibling.ID {
		t.Errorf("source holds %s; want only %s — the other paper's run stays", first3(got), sibling.ID)
	}

	// The deleted one moved too, even though no list will say so: SpendSince is
	// the read that still counts it, and a charge left under a project that no
	// longer holds the paper it paid for is a total nobody can explain. Only the
	// thrown-away run burned anything, so the whole of its cost appearing under
	// the destination is the assertion that it travelled.
	spend, err := s.SpendSince(ctx, []string{from, to}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if spend[to] != burnedCost {
		t.Errorf("destination spend = %d; want %d — the deleted session did not move", spend[to], burnedCost)
	}
	if spend[from] != 0 {
		t.Errorf("source spend = %d; want 0", spend[from])
	}

	// Nothing else on the row was rewritten. updated_at especially: SpendSince
	// windows on it, so a bump here would drag old sessions into the 30-day
	// figure of both projects at once.
	after, err := s.Get(ctx, moving.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at %v -> %v; want it untouched", before.UpdatedAt, after.UpdatedAt)
	}
	if after.ProjectID != to {
		t.Errorf("project = %q; want %q", after.ProjectID, to)
	}
	if after.PaperKey != before.PaperKey || after.Ref != before.Ref || after.Phase != before.Phase ||
		after.RepoName != before.RepoName || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("the move rewrote the session: %+v", after)
	}

	// A paper with no sessions is not an error.
	if err := move(from, to, "doi:10.0000/never-run"); err != nil {
		t.Errorf("moving a paper nobody has run: %v", err)
	}
}

func TestMemStoreMovesAPapersSessions(t *testing.T) {
	s := NewMemStore(WithRates(fixedCard(testCard)))
	runMoveSessionsContract(t, s, func(from, to, key string) error {
		return s.MovePaperSessions(context.Background(), from, to, key)
	})
}

// TestPGStoreMovesAPapersSessions runs the same contract through a real
// transaction, which is where the mover actually lives.
func TestPGStoreMovesAPapersSessions(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres session mover")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s := NewPGStore(pool, WithPGRates(fixedCard(testCard)))
	runMoveSessionsContract(t, s, func(from, to, key string) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return s.MovePaperSessionsTx(ctx, tx, from, to, key)
		})
	})
}

// TestAFailedMoveRollsBack is the reason the mover takes the caller's
// transaction rather than the pool: a paper that fails to move must leave its
// sessions where they were, not half of them somewhere else.
func TestAFailedMoveRollsBack(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres session mover")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s := NewPGStore(pool)
	j, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "r", Kind: KindAudit, Project: "pA", PaperKey: "arxiv:2401.00001"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	boom := errors.New("the paper row would not move")
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := s.MovePaperSessionsTx(ctx, tx, "pA", "pB", "arxiv:2401.00001"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("BeginFunc = %v; want the caller's error", err)
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ProjectID != "pA" {
		t.Errorf("project = %q after a rolled-back move; want pA", got.ProjectID)
	}
}
