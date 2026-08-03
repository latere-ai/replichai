package access

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPGStoresSatisfyTheContract runs the seed-and-bind invariants against a
// real Postgres, skipping when TEST_DATABASE_URL is not set. The Postgres
// binding is a row lock, not a mutex, so the FOR UPDATE path is exercised here.
func TestPGStoresSatisfyTheContract(t *testing.T) {
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
	for _, tbl := range []string{"access_grants", "project_members", "forge_links"} {
		if _, err := pool.Exec(ctx, "TRUNCATE "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}

	acc := NewPGAccess(pool)
	if err := acc.SeedAdmin(ctx, "admin@example.com"); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	admin := Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if tier, ok, _ := acc.Resolve(ctx, admin); !ok || tier != TierAdmin {
		t.Fatalf("admin resolve = %s/%v", tier, ok)
	}

	if err := acc.Grant(ctx, admin, "seed@example.com", TierUser); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	if tier, ok, _ := acc.Resolve(ctx, seed); !ok || tier != TierUser {
		t.Fatalf("seed bind = %s/%v", tier, ok)
	}
	// One-way: a second principal on the bound email is refused.
	if _, ok, _ := acc.Resolve(ctx, Principal{Sub: "imposter", Email: "seed@example.com"}); ok {
		t.Error("a second principal on a bound email must be refused")
	}
	// Admin-only.
	if err := acc.Grant(ctx, seed, "x@example.com", TierUser); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("grant by seed user: want ErrNotAdmin, got %v", err)
	}
	// Revoke locks out.
	if err := acc.Revoke(ctx, admin, "seed@example.com"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, ok, _ := acc.Resolve(ctx, seed); ok {
		t.Error("revoked grant must lock out")
	}
	// Link round-trip.
	if err := acc.LinkAccount(ctx, "sub", "github", "octocat"); err != nil {
		t.Fatalf("LinkAccount: %v", err)
	}
	if login, ok, _ := acc.LinkedAccount(ctx, "sub", "github"); !ok || login != "octocat" {
		t.Errorf("linked = %q/%v", login, ok)
	}

	// Membership binds and is lead-gated.
	mem := NewPGMembership(pool)
	if err := mem.SeedLead(ctx, "proj-1", Principal{Sub: "lead-sub", Email: "lead@example.com"}); err != nil {
		t.Fatalf("SeedLead: %v", err)
	}
	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	if role, ok, _ := mem.Role(ctx, lead, "proj-1"); !ok || role != RoleLead {
		t.Fatalf("lead role = %s/%v", role, ok)
	}
	if err := mem.Add(ctx, lead, "proj-1", "collab@example.com", RoleCollaborator); err != nil {
		t.Fatalf("Add: %v", err)
	}
	collab := Principal{Sub: "collab-sub", Email: "collab@example.com"}
	if role, ok, _ := mem.Role(ctx, collab, "proj-1"); !ok || role != RoleCollaborator {
		t.Errorf("collab role = %s/%v", role, ok)
	}
	if err := mem.Add(ctx, collab, "proj-1", "x@example.com", RoleCollaborator); !errors.Is(err, ErrNotLead) {
		t.Errorf("add by collaborator: want ErrNotLead, got %v", err)
	}

	// The same login-time binding contract the in-memory store satisfies, in a
	// project of its own so it does not read the rows above.
	bindingContract(t, mem, "proj-bind")
}
