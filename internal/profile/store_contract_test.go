// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// runStoreContract checks the invariants every profile Store must hold, so the
// in-memory and Postgres implementations cannot diverge. It expects an empty
// store.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// A principal who has never saved a profile gets the zero profile, not an
	// error, so the settings screen and the publish wizard render on first use.
	got, err := s.Get(ctx, "sub-1")
	if err != nil {
		t.Fatalf("Get before any save: %v", err)
	}
	if got.DisplayName != "" || got.Contact != "" || got.Principal != "sub-1" {
		t.Fatalf("empty profile = %+v", got)
	}

	// A save with no principal is refused. A profile always belongs to a
	// principal.
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

	runHandleContract(t, s)
}

// runHandleContract checks the public-profile half (006r): a handle is
// unique, a page exists only when its owner enables it, and neither rule can
// be bypassed by a read.
func runHandleContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// Claiming a handle does not create a page. The profile stays private,
	// and a private profile cannot be read.
	if _, err := s.Save(ctx, Profile{Principal: "ada-sub", DisplayName: "Ada", Handle: "ada"}); err != nil {
		t.Fatalf("claim a handle: %v", err)
	}
	if _, err := s.ByHandle(ctx, "ada"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a private profile is readable by handle: %v", err)
	}
	if _, ok, err := s.PrincipalHandle(ctx, "ada-sub"); err != nil || ok {
		t.Errorf("a private profile reported a public handle: %v/%v", ok, err)
	}

	// Setting Public creates the page and the link a sign-off can make to it.
	saved, err := s.Save(ctx, Profile{Principal: "ada-sub", DisplayName: "Ada", Contact: "ada@example.org", Handle: "ada", Public: true})
	if err != nil {
		t.Fatalf("publish a profile: %v", err)
	}
	if saved.Handle != "ada" || !saved.Public {
		t.Fatalf("saved profile = %+v", saved)
	}
	got, err := s.ByHandle(ctx, "ada")
	if err != nil || got.Principal != "ada-sub" {
		t.Fatalf("ByHandle = %+v, %v", got, err)
	}
	// Case and surrounding whitespace do not distinguish handles.
	if got, err := s.ByHandle(ctx, "  ADA "); err != nil || got.Principal != "ada-sub" {
		t.Errorf("ByHandle is case- or space-sensitive: %+v, %v", got, err)
	}
	if handle, ok, err := s.PrincipalHandle(ctx, "ada-sub"); err != nil || !ok || handle != "ada" {
		t.Errorf("PrincipalHandle = %q/%v/%v", handle, ok, err)
	}

	// Nobody else may take it.
	if _, err := s.Save(ctx, Profile{Principal: "other-sub", Handle: "ada"}); !errors.Is(err, ErrHandleTaken) {
		t.Errorf("a second principal took a claimed handle: %v", err)
	}
	// Re-saving one's own handle is not a collision.
	if _, err := s.Save(ctx, Profile{Principal: "ada-sub", DisplayName: "Ada L.", Handle: "ada", Public: true}); err != nil {
		t.Errorf("re-saving your own handle: %v", err)
	}
	// Changing a handle frees the old one.
	if _, err := s.Save(ctx, Profile{Principal: "ada-sub", Handle: "ada-l", Public: true}); err != nil {
		t.Fatalf("change a handle: %v", err)
	}
	if _, err := s.ByHandle(ctx, "ada"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the freed handle still resolves: %v", err)
	}
	if _, err := s.Save(ctx, Profile{Principal: "other-sub", Handle: "ada", Public: true}); err != nil {
		t.Errorf("the freed handle could not be claimed: %v", err)
	}

	// Going private removes the page and the link. The profile and the handle
	// are kept, so the change is reversible.
	if _, err := s.Save(ctx, Profile{Principal: "ada-sub", DisplayName: "Ada", Handle: "ada-l"}); err != nil {
		t.Fatalf("go private: %v", err)
	}
	if _, err := s.ByHandle(ctx, "ada-l"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a profile turned private is still readable: %v", err)
	}
	if _, ok, _ := s.PrincipalHandle(ctx, "ada-sub"); ok {
		t.Error("a private profile still offers a link target")
	}
	if back, _ := s.Get(ctx, "ada-sub"); back.Handle != "ada-l" || back.DisplayName != "Ada" {
		t.Errorf("going private lost the profile: %+v", back)
	}

	// Many principals may have no handle at all: the uniqueness rule constrains
	// only the people who chose one.
	for _, sub := range []string{"nohandle-1", "nohandle-2", "nohandle-3"} {
		if _, err := s.Save(ctx, Profile{Principal: sub, DisplayName: "Anon"}); err != nil {
			t.Fatalf("save with no handle (%s): %v", sub, err)
		}
	}

	// An unknown handle and an empty one both return ErrNotFound.
	for _, handle := range []string{"nobody", "", "   "} {
		if _, err := s.ByHandle(ctx, handle); !errors.Is(err, ErrNotFound) {
			t.Errorf("ByHandle(%q) = %v, want not-found", handle, err)
		}
	}
	// A principal with no profile at all has no handle and no error.
	if _, ok, err := s.PrincipalHandle(ctx, "stranger"); err != nil || ok {
		t.Errorf("PrincipalHandle(stranger) = %v/%v", ok, err)
	}

	// A malformed or reserved handle never reaches the table.
	for _, bad := range []string{"ab", "Ada Lovelace", "ada_lovelace", "ada!", strings.Repeat("a", 40)} {
		if _, err := s.Save(ctx, Profile{Principal: "bad-sub", Handle: bad}); err == nil {
			t.Errorf("handle %q was accepted", bad)
		}
	}
	if _, err := s.Save(ctx, Profile{Principal: "bad-sub", Handle: "admin"}); !errors.Is(err, ErrHandleReserved) {
		t.Errorf("a reserved handle was accepted: %v", err)
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

// profileTestDB is the dedicated database the profile Postgres tests run in,
// so the packages `go test ./...` runs against one server do not share tables.
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
