package project

import (
	"context"
	"errors"
	"net/url"
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
	runStoreContract(t, NewPGStore(pool))
}

// TestMigrationBackfillsProjectsFromSessions is 006g AC6: the backfill turns the
// distinct (principal, project_id) pairs already in jobs into projects, reusing
// the slug as the id so sessions' FK already points at them — no re-run, no data
// loss. It replays the real migration order — jobs exist under the job schema
// BEFORE the project migration introduces the table, the backfill, and the
// session FK — since the FK (correctly) forbids a session pointing at a project
// that does not yet exist. Postgres-only.
func TestMigrationBackfillsProjectsFromSessions(t *testing.T) {
	pool := freshDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Only the job schema exists — no projects table, no FK — so three finished
	// CLI sessions seed against three project slugs, as they did before 006g.
	if err := job.Migrate(ctx, pool); err != nil {
		t.Fatalf("job migrate: %v", err)
	}
	js := job.NewPGStore(pool)
	for _, slug := range []string{"paper-a", "paper-b", "paper-c"} {
		j, err := js.Enqueue(ctx, "cli-owner", "ref-"+slug, job.KindAudit, slug, "")
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
		for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
			if err := js.Advance(ctx, j.ID, p, nil); err != nil {
				t.Fatalf("advance %s: %v", slug, err)
			}
		}
	}

	// Now introduce the project migration — it backfills projects from those
	// jobs, then adds the FK; the sessions' project_id already points at the
	// slug-named projects, so the FK holds.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("project migrate: %v", err)
	}

	// Three one-session projects now exist, owned by the CLI principal.
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

// projectTestDB is the dedicated database the project PG tests run in. The
// project migration adds a foreign key to the shared jobs table, which
// internal/job's own contract test (it inserts sessions with dangling
// project_ids) cannot tolerate — and `go test ./...` runs both packages in
// parallel against one server. A private database keeps the two hermetic.
const projectTestDB = "replichai_project_test"

// freshDB creates a clean, empty project test database and returns a pool to it,
// with no migrations applied — so a caller can control the migration order.
func freshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres project tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Terminate stragglers, then drop and recreate — a clean database per run.
	// These cannot run in a transaction, so they go through the pool directly.
	_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, projectTestDB)
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+projectTestDB); err != nil {
		admin.Close()
		t.Fatalf("drop db: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+projectTestDB); err != nil {
		admin.Close()
		t.Fatalf("create db: %v", err)
	}
	admin.Close()

	pool, err := pgxpool.New(ctx, swapDBName(t, base, projectTestDB))
	if err != nil {
		t.Fatalf("connect to %s: %v", projectTestDB, err)
	}
	return pool
}

// testPool returns a clean project test database with both migrations applied.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := freshDB(t)
	ctx := context.Background()
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

// swapDBName rewrites a Postgres URL's database path to name.
func swapDBName(t *testing.T, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}
