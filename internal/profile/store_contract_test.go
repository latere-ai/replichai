package profile

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// runStoreContract asserts the invariants every profile Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// A principal who has never saved one gets the zero profile, not an error —
	// the settings screen and the publish wizard render on first use.
	got, err := s.Get(ctx, "sub-1")
	if err != nil {
		t.Fatalf("Get before any save: %v", err)
	}
	if got.DisplayName != "" || got.Contact != "" || got.Principal != "sub-1" {
		t.Fatalf("empty profile = %+v", got)
	}

	// A save with no principal is refused: a profile is always somebody's.
	if _, err := s.Save(ctx, Profile{Principal: "  ", DisplayName: "Ada"}); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("blank principal: want ErrNoPrincipal, got %v", err)
	}

	// Save normalizes and reads back.
	saved, err := s.Save(ctx, Profile{
		Principal:   "sub-1",
		DisplayName: "  Ada Lovelace ",
		Institution: " LMU Munich ",
		Contact:     " Ada@Example.ORG ",
		ORCID:       "0000-0002-1825-0097",
		Scholar:     "https://scholar.google.com/citations?user=abc",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.DisplayName != "Ada Lovelace" || saved.Institution != "LMU Munich" || saved.Contact != "ada@example.org" {
		t.Fatalf("saved profile not normalized: %+v", saved)
	}
	if saved.UpdatedAt.IsZero() {
		t.Errorf("saved profile has no UpdatedAt")
	}
	got, err = s.Get(ctx, "sub-1")
	if err != nil || got.Contact != "ada@example.org" || got.ORCID != "0000-0002-1825-0097" {
		t.Fatalf("Get after Save = %+v err=%v", got, err)
	}

	// Save replaces rather than merges, so clearing a field really clears it.
	if _, err := s.Save(ctx, Profile{Principal: "sub-1", DisplayName: "Ada"}); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, _ = s.Get(ctx, "sub-1")
	if got.Institution != "" || got.ORCID != "" || got.Scholar != "" || got.Contact != "" {
		t.Fatalf("second Save did not replace: %+v", got)
	}

	// An invalid identifier never reaches the table, whichever field it is in.
	for _, tc := range []struct {
		name string
		p    Profile
		want error
	}{
		{"contact", Profile{Principal: "sub-2", Contact: "not-an-email"}, ErrInvalidContact},
		{"orcid", Profile{Principal: "sub-2", ORCID: "1234"}, ErrInvalidORCID},
		{"scholar", Profile{Principal: "sub-2", Scholar: "https://example.com/me"}, ErrInvalidScholar},
	} {
		if _, err := s.Save(ctx, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("Save with bad %s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	if got, _ := s.Get(ctx, "sub-2"); got.Contact != "" || got.ORCID != "" || got.Scholar != "" {
		t.Errorf("a refused Save wrote something: %+v", got)
	}

	// Profiles are per-principal.
	if got, _ := s.Get(ctx, "sub-3"); got.DisplayName != "" {
		t.Errorf("another principal sees sub-1's profile: %+v", got)
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

// profileTestDB is the dedicated database the profile PG tests run in, so
// `go test ./...` running every package against one server stays hermetic.
const profileTestDB = "replichai_profile_test"

// testPool creates a clean profile test database, migrated, and returns a pool.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres profile tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, profileTestDB)
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+profileTestDB); err != nil {
		admin.Close()
		t.Fatalf("drop db: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+profileTestDB); err != nil {
		admin.Close()
		t.Fatalf("create db: %v", err)
	}
	admin.Close()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + profileTestDB
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect to %s: %v", profileTestDB, err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("profile migrate: %v", err)
	}
	return pool
}
