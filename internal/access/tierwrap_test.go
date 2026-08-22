package access

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openWrapTestPool connects to the throwaway Postgres these tests need, or
// skips. The tier and role columns are SMALLINT with no CHECK, so only a real
// database can hold the values under test here.
func openWrapTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the out-of-range column tests")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"access_grants", "project_members"} {
		if _, err := pool.Exec(ctx, "TRUNCATE "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	return pool
}

// TestPGResolveRefusesATierTheColumnShouldNotHold pins the escalation a
// truncating conversion would allow: 259 kept in a SMALLINT is 3 once it is cut
// down to a uint8, and 3 is TierAdmin. Nothing in this package writes such a
// value, but nothing in the schema forbids one either, so the read must fail
// closed rather than promote whoever owns the row.
func TestPGResolveRefusesATierTheColumnShouldNotHold(t *testing.T) {
	pool := openWrapTestPool(t)
	acc := NewPGAccess(pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO access_grants (email, tier, granted_by, granted_at) VALUES ($1,$2,'sql',now())`,
		"wrap@example.com", int16(259)); err != nil {
		t.Fatalf("seed out-of-range grant: %v", err)
	}

	tier, ok, err := acc.Resolve(ctx, Principal{Sub: "wrap-sub", Email: "wrap@example.com"})
	if ok || tier != TierNone {
		t.Errorf("Resolve on tier 259 = %s/%v, want none/false", tier, ok)
	}
	if err == nil {
		t.Error("Resolve on tier 259 must report the corrupt row, got a nil error")
	}
	// A corrupt row must not have consumed the one-way binding either.
	var principal *string
	if err := pool.QueryRow(ctx,
		`SELECT principal FROM access_grants WHERE email=$1`, "wrap@example.com").Scan(&principal); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if principal != nil {
		t.Errorf("principal bound to a corrupt grant: %q", *principal)
	}
}

// TestPGRoleRefusesARoleTheColumnShouldNotHold is the membership half: 258
// truncates to 2, which is RoleLead — ownership of the project and its
// membership list.
func TestPGRoleRefusesARoleTheColumnShouldNotHold(t *testing.T) {
	pool := openWrapTestPool(t)
	members := NewPGMembership(pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO project_members (project, email, role) VALUES ($1,$2,$3)`,
		"proj-wrap", "wrap@example.com", int16(258)); err != nil {
		t.Fatalf("seed out-of-range member: %v", err)
	}

	role, ok, err := members.Role(ctx, Principal{Sub: "wrap-sub", Email: "wrap@example.com"}, "proj-wrap")
	if ok || role != RoleNone {
		t.Errorf("Role on role 258 = %s/%v, want none/false", role, ok)
	}
	if err == nil {
		t.Error("Role on role 258 must report the corrupt row, got a nil error")
	}
	if _, err := members.List(ctx, "proj-wrap"); err == nil {
		t.Error("List over a corrupt role must report it, got a nil error")
	}
}

// TestDecodeTierAndRole covers the decoders without a database: every declared
// value round-trips, and everything else is refused — including the values that
// a truncating conversion would have turned into authority.
func TestDecodeTierAndRole(t *testing.T) {
	for _, want := range []Tier{TierNone, TierUser, TierAdmin} {
		got, err := decodeTier(int16(want))
		if err != nil || got != want {
			t.Errorf("decodeTier(%d) = %s/%v, want %s/nil", int16(want), got, err, want)
		}
	}
	for _, bad := range []int16{-253, -1, 2, 4, 99, 256, 259, 32767} {
		got, err := decodeTier(bad)
		if err == nil || got != TierNone {
			t.Errorf("decodeTier(%d) = %s/%v, want none and an error", bad, got, err)
		}
	}

	for _, want := range []Role{RoleNone, RoleCollaborator, RoleLead} {
		got, err := decodeRole(int16(want))
		if err != nil || got != want {
			t.Errorf("decodeRole(%d) = %s/%v, want %s/nil", int16(want), got, err, want)
		}
	}
	for _, bad := range []int16{-254, -1, 3, 99, 256, 258, 32767} {
		got, err := decodeRole(bad)
		if err == nil || got != RoleNone {
			t.Errorf("decodeRole(%d) = %s/%v, want none and an error", bad, got, err)
		}
	}
}
