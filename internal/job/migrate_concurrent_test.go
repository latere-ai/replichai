// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestConcurrentJobMigratorsDoNotCollide checks that two processes can migrate
// the same database concurrently, as happens when a rolling deploy runs two
// pods at once. The advisory lock serializes the versions, and it has to
// serialize the creation of the migrations table as well.
//
// CREATE TABLE IF NOT EXISTS is not atomic against a concurrent creator: both
// sessions find the table absent, both run the DDL, and the loser fails on a
// duplicate key in pg_type. That is the crash this lock exists to prevent,
// happening one statement before the lock was taken, and it failed CI on a
// commit that touched none of this code.
func TestConcurrentJobMigratorsDoNotCollide(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the concurrent migration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// Start from empty, so every version is applied under contention rather
	// than skipped by an already-recorded row, and so the bootstrap itself
	// races.
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS jobs, schema_migrations CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}

	const racers = 6
	errs := make([]error, racers)
	var wg, start sync.WaitGroup
	start.Add(1)
	for i := range racers {
		wg.Go(func() {
			start.Wait() // release them together
			errs[i] = Migrate(ctx, pool)
		})
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("migrator %d failed: %v", i, err)
		}
	}

	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != len(migrations) {
		t.Errorf("recorded %d versions, want %d", versions, len(migrations))
	}
	// The newest columns are present exactly once, which a half-applied
	// migration would have broken (045k).
	var origin int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name='jobs' AND column_name IN ('origin','external_id')`).Scan(&origin); err != nil {
		t.Fatalf("check columns: %v", err)
	}
	if origin != 2 {
		t.Errorf("origin/external_id columns present: %d, want 2", origin)
	}
}
