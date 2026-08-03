package platform

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
)

var ctx = context.Background()

var (
	admin = access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	user  = access.Principal{Sub: "user-sub", Email: "user@example.com"}
)

// newAuthority builds an access store with one admin and one plain user bound,
// so the platform store's tier re-check has something real to resolve against.
func newAuthority(t *testing.T) *access.MemStore {
	t.Helper()
	acc := access.NewMemStore()
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if err := acc.SeedGrant(ctx, user.Email, access.TierUser); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	for _, p := range []access.Principal{admin, user} {
		if _, ok, _ := acc.Resolve(ctx, p); !ok {
			t.Fatalf("%s should resolve granted", p.Email)
		}
	}
	return acc
}

// contract is every invariant a Store must satisfy, run against both
// implementations so the two cannot drift.
func contract(t *testing.T, s Store) {
	t.Helper()

	// AC3: the seeded value is the compiled-in conservative default, and it is
	// not unlimited.
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.DefaultSessionQuota != DefaultSessionQuota {
		t.Errorf("seeded default = %d, want %d", got.DefaultSessionQuota, DefaultSessionQuota)
	}

	// AC1: writes are admin-only, refused in the store itself.
	if err := s.SetSettings(ctx, user, Settings{DefaultSessionQuota: 999}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("SetSettings by a user: want ErrNotAdmin, got %v", err)
	}
	if err := s.SetTopup(ctx, user, "x@example.com", 5); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("SetTopup by a user: want ErrNotAdmin, got %v", err)
	}
	if _, err := s.Topups(ctx, user); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("Topups by a user: want ErrNotAdmin, got %v", err)
	}

	// AC2: an admin's write persists, recording who made it.
	if err := s.SetSettings(ctx, admin, Settings{DefaultSessionQuota: 7}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	got, err = s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.DefaultSessionQuota != 7 {
		t.Errorf("default = %d, want 7", got.DefaultSessionQuota)
	}
	if got.UpdatedBy != admin.Email {
		t.Errorf("updated_by = %q, want %q", got.UpdatedBy, admin.Email)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at should be stamped")
	}

	// A negative quota means "owes sessions" and is refused.
	if err := s.SetSettings(ctx, admin, Settings{DefaultSessionQuota: -1}); !errors.Is(err, ErrNegative) {
		t.Errorf("negative default: want ErrNegative, got %v", err)
	}
	if err := s.SetTopup(ctx, admin, "x@example.com", -1); !errors.Is(err, ErrNegative) {
		t.Errorf("negative topup: want ErrNegative, got %v", err)
	}
	if err := s.SetTopup(ctx, admin, "  ", 5); !errors.Is(err, ErrNoEmail) {
		t.Errorf("topup with no email: want ErrNoEmail, got %v", err)
	}

	// AC5/AC6: a top-up composes with the default, is keyed by (normalized)
	// email so it can be set before that person has ever signed in, and clears
	// at zero.
	if err := s.SetTopup(ctx, admin, "  Newcomer@Example.com ", 4); err != nil {
		t.Fatalf("SetTopup: %v", err)
	}
	newcomer := access.Principal{Sub: "newcomer-sub", Email: "newcomer@example.com"}
	if n, unlimited, err := s.Allowance(ctx, newcomer); err != nil || unlimited || n != 11 {
		t.Errorf("allowance = %d/%v (err %v), want 11 (7 default + 4 top-up), capped", n, unlimited, err)
	}
	tops, err := s.Topups(ctx, admin)
	if err != nil {
		t.Fatalf("Topups: %v", err)
	}
	if len(tops) != 1 || tops[0].Email != "newcomer@example.com" || tops[0].Extra != 4 || tops[0].GrantedBy != admin.Email {
		t.Errorf("topups = %+v", tops)
	}
	if err := s.SetTopup(ctx, admin, "newcomer@example.com", 0); err != nil {
		t.Fatalf("clear topup: %v", err)
	}
	if tops, _ := s.Topups(ctx, admin); len(tops) != 0 {
		t.Errorf("topups after clear = %+v, want empty", tops)
	}

	// AC6: someone with no top-up gets the plain default; an admin is unlimited
	// and owns the whole platform budget.
	if n, unlimited, err := s.Allowance(ctx, user); err != nil || unlimited || n != 7 {
		t.Errorf("user allowance = %d/%v (err %v), want 7, capped", n, unlimited, err)
	}
	if _, unlimited, err := s.Allowance(ctx, admin); err != nil || !unlimited {
		t.Errorf("admin allowance unlimited = %v (err %v), want true", unlimited, err)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	contract(t, NewMemStore(newAuthority(t)))
}

// AC3: the compiled-in default is conservative and finite — it is both the
// value a fresh store starts at and the value a missing Postgres row falls back
// to, so the two cannot drift into "unlimited by accident".
//
// Since 006q it is zero. Sign-in is open, so this number is no longer a second
// lock behind an admin's guest list; it is the only one, and a stranger who
// signs in must arrive with nothing to spend. A negative value would be worse
// than useless (it would read as "owes sessions"), and any positive value would
// hand every arrival free runs on the platform's credential.
func TestFreshStoreStartsAtTheConservativeDefault(t *testing.T) {
	if DefaultSessionQuota != 0 {
		t.Fatalf("DefaultSessionQuota = %d; with sign-in open a new arrival starts at 0", DefaultSessionQuota)
	}
	got, err := NewMemStore(nil).Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.DefaultSessionQuota != DefaultSessionQuota {
		t.Errorf("fresh store default = %d, want the compiled-in %d", got.DefaultSessionQuota, DefaultSessionQuota)
	}
	// The seeded migration writes that same constant, so a fresh database and a
	// fresh in-memory store agree.
	if n, _, err := NewMemStore(nil).Allowance(ctx, user); err != nil || n != DefaultSessionQuota {
		t.Errorf("allowance on a fresh store = %d (err %v), want %d", n, err, DefaultSessionQuota)
	}
}

// AC1-AC6 against a real Postgres, including AC4's single-row constraint.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres platform contract")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := access.Migrate(ctx, pool); err != nil {
		t.Fatalf("access migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"quota_topups", "access_grants"} {
		if _, err := pool.Exec(ctx, "TRUNCATE "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	// The settings row is restored to the seeded default rather than truncated,
	// so the contract starts where a fresh migration leaves it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at)
		VALUES (1, $1, 'bootstrap', now())
		ON CONFLICT (id) DO UPDATE SET default_session_quota = EXCLUDED.default_session_quota,
		                               updated_by = 'bootstrap', updated_at = now()`,
		DefaultSessionQuota); err != nil {
		t.Fatalf("reset settings: %v", err)
	}

	acc := access.NewPGAccess(pool)
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if err := acc.SeedGrant(ctx, user.Email, access.TierUser); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	for _, p := range []access.Principal{admin, user} {
		if _, ok, _ := acc.Resolve(ctx, p); !ok {
			t.Fatalf("%s should resolve granted", p.Email)
		}
	}

	contract(t, NewPGStore(pool, acc))

	// AC4: a second policy row cannot exist.
	if _, err := pool.Exec(ctx,
		`INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at) VALUES (2, 1, 'x', now())`,
	); err == nil {
		t.Error("a second platform_settings row must violate the check constraint")
	}

	// AC3: the fallback holds when the row is gone.
	if _, err := pool.Exec(ctx, `DELETE FROM platform_settings`); err != nil {
		t.Fatalf("delete settings: %v", err)
	}
	s := NewPGStore(pool, acc)
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings with no row: %v", err)
	}
	if got.DefaultSessionQuota != DefaultSessionQuota {
		t.Errorf("missing row = %d, want the conservative default %d", got.DefaultSessionQuota, DefaultSessionQuota)
	}
	if n, unlimited, _ := s.Allowance(ctx, user); unlimited || n != DefaultSessionQuota {
		t.Errorf("allowance with no row = %d/%v, want %d and capped — never unlimited", n, unlimited, DefaultSessionQuota)
	}
}
