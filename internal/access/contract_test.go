package access

import (
	"context"
	"testing"
)

// storeContract is what both grant stores answer to. It runs against the
// in-memory store here and against Postgres in pgstore_test.go, so the two
// cannot drift on a rule the gate depends on.
//
// Every principal and address is derived from the ns prefix, so the contract is
// self-contained and can run in a store that already holds other rows.
func storeContract(t *testing.T, s interface {
	Access
	SeedGrant(ctx context.Context, email string, tier Tier) error
	EmailOf(ctx context.Context, principalSub string) (string, bool, error)
}, ns string) {
	t.Helper()
	adminEmail := ns + "-admin@example.com"
	admin := Principal{Sub: ns + "-admin", Email: adminEmail}

	if err := s.SeedGrant(ctx, adminEmail, TierAdmin); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	if tier, ok, err := s.Resolve(ctx, admin); err != nil || !ok || tier != TierAdmin {
		t.Fatalf("admin resolve = %s/%v/%v", tier, ok, err)
	}

	// EmailOf prices a payer known only by their subject (006l). Before binding
	// there is nothing to price.
	if email, ok, err := s.EmailOf(ctx, admin.Sub); err != nil || !ok || email != adminEmail {
		t.Errorf("EmailOf(bound) = %q/%v/%v, want the seeded address", email, ok, err)
	}
	if _, ok, err := s.EmailOf(ctx, ns+"-nobody"); err != nil || ok {
		t.Errorf("EmailOf(unknown) = %v/%v, want not-found and no error", ok, err)
	}

	// An admin grants; the grantee resolves and appears in the list.
	userEmail := ns + "-user@example.com"
	if err := s.Grant(ctx, admin, userEmail, TierUser); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	user := Principal{Sub: ns + "-user", Email: userEmail}
	if tier, ok, _ := s.Resolve(ctx, user); !ok || tier != TierUser {
		t.Fatalf("granted user = %s/%v", tier, ok)
	}
	grants, err := s.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var listed *Grant
	for i := range grants {
		if grants[i].Email == userEmail {
			listed = &grants[i]
		}
	}
	if listed == nil {
		t.Fatalf("List did not include %s: %+v", userEmail, grants)
	}
	if listed.Tier != TierUser || listed.Principal != user.Sub {
		t.Errorf("listed grant = %+v, want tier user bound to %s", *listed, user.Sub)
	}
	if listed.GrantedAt.IsZero() {
		t.Error("a listed grant carries no timestamp; the admin console cannot say when access was given")
	}

	// A non-admin drives nothing.
	for _, err := range []error{
		s.Grant(ctx, user, ns+"-x@example.com", TierUser),
		s.Revoke(ctx, user, userEmail),
	} {
		if err == nil {
			t.Error("a plain user must not grant or revoke")
		}
	}
	if _, err := s.List(ctx, user); err == nil {
		t.Error("a plain user must not read the grant list")
	}

	// Revocation locks its holder out on the very next resolve.
	if err := s.Revoke(ctx, admin, userEmail); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if tier, ok, _ := s.Resolve(ctx, user); ok || tier != TierNone {
		t.Errorf("revoked user resolves as %s/%v, want none", tier, ok)
	}
	// Idempotent: revoking again is not an error.
	if err := s.Revoke(ctx, admin, userEmail); err != nil {
		t.Errorf("second Revoke: %v", err)
	}
}

// The in-memory store satisfies the grant contract.
func TestMemStoreSatisfiesTheGrantContract(t *testing.T) {
	storeContract(t, NewMemStore(), "mem")
}

// A tier and a role name themselves for logs and API responses. The strings are
// wire values the frontend switches on, so they are asserted rather than left
// to a refactor.
func TestTierAndRoleNames(t *testing.T) {
	for tier, want := range map[Tier]string{
		TierNone: "none", TierUser: "user", TierAdmin: "admin", Tier(99): "none",
	} {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", tier, got, want)
		}
	}
	for role, want := range map[Role]string{
		RoleNone: "none", RoleCollaborator: "collaborator", RoleLead: "lead", Role(99): "none",
	} {
		if got := role.String(); got != want {
			t.Errorf("Role(%d).String() = %q, want %q", role, got, want)
		}
	}
	// Granted is the gate's own predicate: only User and above pass.
	if TierNone.Granted() || !TierUser.Granted() || !TierAdmin.Granted() {
		t.Error("Granted must admit User and Admin and refuse None")
	}
}
