// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGrantTransitionsAreWellFormed checks the declared table: every state is a
// key, every target is a state, nothing loops on itself, and the derived state
// of a row is one the table names.
func TestGrantTransitionsAreWellFormed(t *testing.T) {
	for from, tos := range grantTransitions {
		for _, to := range tos {
			if to == from {
				t.Errorf("%s lists itself", from)
			}
			if _, ok := grantTransitions[to]; !ok {
				t.Errorf("%s -> %s: target is not a state", from, to)
			}
		}
	}
	for _, s := range grantInitial {
		if _, ok := grantTransitions[s]; !ok {
			t.Errorf("initial state %s is not a state", s)
		}
	}
	cases := []struct {
		principal string
		revoked   bool
		want      GrantState
	}{
		{"", false, GrantInvited},
		{"sub", false, GrantBound},
		{"", true, GrantRevoked},
		{"sub", true, GrantRevoked},
	}
	for _, c := range cases {
		if got := grantStateOf(c.principal, c.revoked); got != c.want {
			t.Errorf("grantStateOf(%q, %v) = %s, want %s", c.principal, c.revoked, got, c.want)
		}
	}
}

// TestCheckGrantStep walks every ordered pair of states: a pair is allowed
// exactly when the table lists it, and a pair of one state is no change.
func TestCheckGrantStep(t *testing.T) {
	states := []GrantState{GrantInvited, GrantBound, GrantRevoked}
	for _, from := range states {
		for _, to := range states {
			err := checkGrantStep(from, to)
			want := from == to || slices.Contains(grantTransitions[from], to)
			if (err == nil) != want {
				t.Errorf("%s -> %s: err = %v, want allowed %v", from, to, err, want)
			}
			if err != nil && !errors.Is(err, ErrIllegalGrantTransition) {
				t.Errorf("%s -> %s: %v does not wrap ErrIllegalGrantTransition", from, to, err)
			}
		}
	}
	if err := checkGrantCreate(GrantBound); !errors.Is(err, ErrIllegalGrantTransition) {
		t.Errorf("a new row starting bound = %v, want illegal", err)
	}
}

// adminGuardContract asserts, on a store whose only active admin is a, that an
// admin cannot revoke themself and that the last admin cannot be demoted.
func adminGuardContract(t *testing.T, s Access, a Principal) {
	t.Helper()
	if err := s.Revoke(ctx, a, a.Email); !errors.Is(err, ErrSelfRevoke) {
		t.Fatalf("an admin revoking themself = %v, want ErrSelfRevoke", err)
	}
	if err := s.Revoke(ctx, a, "  "+a.Email+" "); !errors.Is(err, ErrSelfRevoke) {
		t.Errorf("self revoke with a differently typed address = %v, want ErrSelfRevoke", err)
	}
	if err := s.Grant(ctx, a, a.Email, TierUser); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the last admin = %v, want ErrLastAdmin", err)
	}
	if tier, ok, err := s.Resolve(ctx, a); err != nil || !ok || tier != TierAdmin {
		t.Fatalf("after the refusals the admin = %s/%v/%v, want admin", tier, ok, err)
	}
	// With a second admin the first may step down, and the second is then last.
	b := Principal{Sub: a.Sub + "-second", Email: "second-" + a.Email}
	if err := s.Grant(ctx, a, b.Email, TierAdmin); err != nil {
		t.Fatalf("granting a second admin: %v", err)
	}
	if _, ok, _ := s.Resolve(ctx, b); !ok {
		t.Fatal("the second admin does not resolve")
	}
	if err := s.Grant(ctx, a, a.Email, TierUser); err != nil {
		t.Fatalf("demoting one of two admins: %v", err)
	}
	if err := s.Grant(ctx, b, b.Email, TierUser); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the remaining admin = %v, want ErrLastAdmin", err)
	}
}

func TestMemStoreGuardsTheLastAdmin(t *testing.T) {
	s := NewMemStore()
	if err := s.SeedAdmin(ctx, "only@example.com"); err != nil {
		t.Fatal(err)
	}
	adminGuardContract(t, s, Principal{Sub: "only-sub", Email: "only@example.com"})
}

func TestPGStoreGuardsTheLastAdmin(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres access contract")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE access_grants"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	s := NewPGAccess(pool)
	if err := s.SeedAdmin(ctx, "only@example.com"); err != nil {
		t.Fatal(err)
	}
	adminGuardContract(t, s, Principal{Sub: "only-sub", Email: "only@example.com"})
}
