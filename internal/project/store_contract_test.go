package project

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/job"
)

// runStoreContract asserts the invariants every project Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// A blank owner is refused.
	if _, err := s.Create(ctx, "  ", "t"); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("blank owner: want ErrNoOwner, got %v", err)
	}

	// Create and read back — private by default.
	p, err := s.Create(ctx, "owner", "Attention Is All You Need")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Visibility != VisibilityPrivate || p.Owner != "owner" || p.CreatedAt.IsZero() {
		t.Fatalf("created project = %+v", p)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil || got.Title != "Attention Is All You Need" {
		t.Fatalf("Get = %+v, err %v", got, err)
	}

	// A missing project is ErrNotFound.
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing Get: want ErrNotFound, got %v", err)
	}

	// ListByOwner is scoped and newest-first.
	second, _ := s.Create(ctx, "owner", "Second")
	_, _ = s.Create(ctx, "stranger", "Theirs")
	mine, err := s.ListByOwner(ctx, "owner")
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(mine) != 2 || mine[0].ID != second.ID || mine[1].ID != p.ID {
		t.Fatalf("ListByOwner = %+v; want [second, first], no stranger", mine)
	}

	// Delete removes it, idempotently.
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	runStoreContract(t, NewMemStore())
}

// TestPGStoreSatisfiesTheContract runs the same contract against a real
// Postgres, skipping when TEST_DATABASE_URL is not set.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `TRUNCATE projects CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runStoreContract(t, NewPGStore(pool))
}

// TestMigrationBackfillsProjectsFromSessions is 006g AC6: the backfill turns the
// distinct (principal, project_id) pairs already in jobs into projects, reusing
// the slug as the id so sessions' FK already points at them — no re-run, no
// data loss. Postgres-only, since the backfill is SQL over the jobs table.
func TestMigrationBackfillsProjectsFromSessions(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Start from a clean jobs table with three sessions the CLI backfilled:
	// two principals, three project slugs, all terminal.
	if _, err := pool.Exec(ctx, `TRUNCATE projects CASCADE; TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	js := job.NewPGStore(pool)
	for _, slug := range []string{"paper-a", "paper-b", "paper-c"} {
		j, err := js.Enqueue(ctx, "cli-owner", "ref-"+slug, job.KindAudit, slug, "")
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
		// Drive it to done so it looks like a finished CLI reproduction.
		for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
			if err := js.Advance(ctx, j.ID, p, nil); err != nil {
				t.Fatalf("advance %s: %v", slug, err)
			}
		}
	}

	// Drop the constraint the app's earlier migration may have added, then re-run
	// the backfill from scratch, so this test owns the outcome deterministically.
	if _, err := pool.Exec(ctx, `ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_project_fk`); err != nil {
		t.Fatalf("drop fk: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM project_migrations WHERE version = 1`); err != nil {
		t.Fatalf("reset migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS projects CASCADE`); err != nil {
		t.Fatalf("drop projects: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Three one-session projects now exist, owned by the CLI principal, each
	// session's FK already pointing at its slug-named project.
	ps, err := NewPGStore(pool).ListByOwner(ctx, "cli-owner")
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("backfilled %d projects, want 3", len(ps))
	}
	for _, p := range ps {
		sessions, err := js.ListByProject(ctx, p.ID)
		if err != nil {
			t.Fatalf("ListByProject %s: %v", p.ID, err)
		}
		if len(sessions) != 1 {
			t.Errorf("project %s has %d sessions, want 1", p.ID, len(sessions))
		}
	}
}

// testPool opens the test database or skips.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres project tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := job.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("job migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("project migrate: %v", err)
	}
	return pool
}
