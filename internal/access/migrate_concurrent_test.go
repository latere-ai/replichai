// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A rolling deploy runs two pods at once (maxSurge), so two processes migrate
// the same database concurrently. Before the advisory lock both read "version
// missing", both ran the DDL, and the loser died on "column already exists" —
// which is exactly how the 006q deploy crash-looped its pod once before
// recovering.
//
// This drives the real race: N migrators against one database, from empty.
// Exactly one applies each version and none returns an error.
func TestConcurrentMigratorsDoNotCollide(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the concurrent migration test")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// From empty, so every version is applied under contention rather than
	// skipped by an already-recorded row.
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS access_grants, project_members, forge_links, freeze_overrides, access_migrations CASCADE`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}

	const racers = 4
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

	// Every version recorded exactly once, and the schema is whole.
	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_migrations`).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != len(migrations) {
		t.Errorf("recorded %d versions, want %d", versions, len(migrations))
	}
	var cols int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name='access_grants' AND column_name IN ('revoked_at','revoked_by')`).Scan(&cols); err != nil {
		t.Fatalf("check columns: %v", err)
	}
	if cols != 2 {
		t.Errorf("revoked_at/revoked_by columns present: %d, want 2", cols)
	}
}
