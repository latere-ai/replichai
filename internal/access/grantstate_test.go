// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"latere.ai/x/pkg/statemachine"
	"latere.ai/x/pkg/statemachine/statemachinetest"
)

// TestGrantStateIsDerivedFromTheRow checks the state a row's columns name.
func TestGrantStateIsDerivedFromTheRow(t *testing.T) {
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
	if !Grants.Can(GrantRevoked, GrantBound) {
		t.Error("a revoked row that kept its principal is re-admitted to bound")
	}
}

// grantWriters is a grant store as the table checks drive it: the store's own
// writers for each state, and a way to put a row in a state directly.
type grantWriters struct {
	access Access
	admin  Principal
	// regrant is Grant, reporting the state the row was in; empty for no row.
	regrant func(email string) (GrantState, error)
	// revoke is Revoke, reporting whether this call recorded the revocation.
	revoke func(email string) (bool, error)
	// insert writes a row in state s with no writer involved.
	insert func(t *testing.T, email, principal string, s GrantState)
}

// driver adapts the writers to statemachinetest. Each state is reached through
// the writer that lands there: Revoke for revoked, Grant for invited (an
// address re-admitted without a principal), and a first sign-in's bind, by a
// fresh principal presenting the email, for bound. A writer that finds the row
// already past the step reports it as refused.
func (w *grantWriters) driver(prefix string) statemachinetest.Driver[GrantState] {
	var n atomic.Int64
	refused := func(to GrantState, why string) error {
		return fmt.Errorf("%w: %s to %s: %s", statemachine.ErrIllegalTransition, "grant", to, why)
	}
	state := func(t *testing.T, email string) GrantState {
		t.Helper()
		gs, err := w.access.GrantsFor(ctx, w.admin, []string{email})
		if err != nil || len(gs) != 1 {
			t.Fatalf("reading %s: %v, %d rows", email, err, len(gs))
		}
		return grantStateOf(gs[0].Principal, gs[0].Revoked())
	}
	return statemachinetest.Driver[GrantState]{
		Enter: func(t *testing.T, s GrantState) string {
			email := fmt.Sprintf("%s-%d@example.com", prefix, n.Add(1))
			w.insert(t, email, "sub-of-"+email, s)
			return email
		},
		Move: func(email string, to GrantState) error {
			switch to {
			case GrantRevoked:
				did, err := w.revoke(email)
				if err == nil && !did {
					return refused(to, "already revoked")
				}
				return err
			case GrantInvited:
				from, err := w.regrant(email)
				if err == nil && from != GrantRevoked {
					return refused(to, "the row was not revoked")
				}
				return err
			case GrantBound:
				gs, err := w.access.GrantsFor(ctx, w.admin, []string{email})
				if err != nil {
					return fmt.Errorf("reading %s: %w", email, err)
				}
				if len(gs) != 1 {
					return fmt.Errorf("reading %s: %d grants, want 1", email, len(gs))
				}
				if gs[0].Revoked() {
					if _, err := w.regrant(email); err != nil {
						return err
					}
				}
				// A second sign-in on the same address is somebody else's.
				p := Principal{Sub: fmt.Sprintf("signin-%d", n.Add(1)), Email: email}
				_, granted, err := w.access.Resolve(ctx, p)
				if err == nil && !granted {
					return refused(to, "the address is not claimable")
				}
				return err
			}
			return refused(to, "no writer")
		},
		State: state,
	}
}

func driveGrantTable(t *testing.T, w *grantWriters, prefix string) {
	t.Helper()
	d := w.driver(prefix)
	statemachinetest.Exhaustive(t, Grants, d)
	statemachinetest.Race(t, Grants, d)
}

func TestMemStoreWritersFollowTheGrantTable(t *testing.T) {
	s := NewMemStore()
	if err := s.SeedAdmin(ctx, "driver-admin@example.com"); err != nil {
		t.Fatal(err)
	}
	admin := Principal{Sub: "driver-admin", Email: "driver-admin@example.com"}
	if _, ok, err := s.Resolve(ctx, admin); err != nil || !ok {
		t.Fatalf("admin resolve: %v %v", ok, err)
	}
	w := &grantWriters{
		access: s, admin: admin,
		regrant: func(email string) (GrantState, error) { return s.grant(email, TierUser, admin.Sub) },
		revoke:  func(email string) (bool, error) { return s.revoke(ctx, admin, email) },
		insert: func(t *testing.T, email, principal string, st GrantState) {
			t.Helper()
			s.mu.Lock()
			defer s.mu.Unlock()
			g := Grant{Email: email, Tier: TierUser, GrantedAt: time.Now()}
			switch st {
			case GrantBound:
				g.Principal = principal
				s.principalEmail[principal] = email
			case GrantRevoked:
				at := time.Now()
				g.Tier, g.RevokedAt = TierNone, &at
			}
			s.grants[email] = g
		},
	}
	driveGrantTable(t, w, "mem")
}

func TestPGStoreWritersFollowTheGrantTable(t *testing.T) {
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
	if err := s.SeedAdmin(ctx, "driver-admin@example.com"); err != nil {
		t.Fatal(err)
	}
	admin := Principal{Sub: "driver-admin", Email: "driver-admin@example.com"}
	if _, ok, err := s.Resolve(ctx, admin); err != nil || !ok {
		t.Fatalf("admin resolve: %v %v", ok, err)
	}
	w := &grantWriters{
		access: s, admin: admin,
		regrant: func(email string) (GrantState, error) { return s.upsert(ctx, email, TierUser, admin.Sub) },
		revoke:  func(email string) (bool, error) { return s.revoke(ctx, admin, email) },
		insert: func(t *testing.T, email, principal string, st GrantState) {
			t.Helper()
			var bound *string
			var revokedAt *time.Time
			tier := TierUser
			switch st {
			case GrantBound:
				bound = &principal
			case GrantRevoked:
				now := time.Now()
				revokedAt, tier = &now, TierNone
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO access_grants (email, tier, principal, granted_by, granted_at, revoked_at, revoked_by)
				 VALUES ($1,$2,$3,'',now(),$4,'')`, email, int16(tier), bound, revokedAt); err != nil {
				t.Fatal(err)
			}
		},
	}
	driveGrantTable(t, w, "pg")
}

// A revoked row that kept its principal is re-admitted to bound, not invited.
func TestRegrantingARevokedBoundRowReturnsItToBound(t *testing.T) {
	s := NewMemStore()
	if err := s.SeedAdmin(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	admin := Principal{Sub: "a", Email: "a@example.com"}
	user := Principal{Sub: "u", Email: "u@example.com"}
	if _, _, err := s.SelfGrant(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, admin, user.Email); err != nil {
		t.Fatal(err)
	}
	from, err := s.grant(user.Email, TierUser, admin.Sub)
	if err != nil || from != GrantRevoked {
		t.Fatalf("regrant = %s, %v", from, err)
	}
	if got := grantOf(t, s, admin, user.Email); got == nil || grantStateOf(got.Principal, got.Revoked()) != GrantBound {
		t.Errorf("after re-admitting: %+v, want bound", got)
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
