// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"context"
	"testing"
	"time"
)

// storeContract asserts the invariants both grant stores must hold. It runs
// against the in-memory store here and against Postgres in pgstore_test.go, so
// the two cannot drift on a rule the gate depends on.
//
// Every principal and address is derived from the ns prefix, so the contract is
// self-contained and can run in a store that already holds other rows.
func storeContract(t *testing.T, s interface {
	Access
	SeedGrant(ctx context.Context, email string, tier Tier) error
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

	// Revocation locks its holder out on the next resolve.
	if err := s.Revoke(ctx, admin, userEmail); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if tier, ok, _ := s.Resolve(ctx, user); ok || tier != TierNone {
		t.Errorf("revoked user resolves as %s/%v, want none", tier, ok)
	}
	// Idempotent, and the first revocation is the one that counts: a second must
	// not rewrite when the ban happened.
	revokedAt := revocationOf(t, s, admin, userEmail)
	if err := s.Revoke(ctx, admin, userEmail); err != nil {
		t.Errorf("second Revoke: %v", err)
	}
	if again := revocationOf(t, s, admin, userEmail); !again.Equal(revokedAt) {
		t.Errorf("a second revocation rewrote the timestamp: %v then %v", revokedAt, again)
	}

	// 006q: a ban must survive a re-login. Sign-in self-grants, so a revocation
	// that deleted the row would be undone by the next sign-in, and a ban that
	// lasts one request is a logout.
	if tier, ok, err := s.SelfGrant(ctx, user); err != nil || ok || tier != TierNone {
		t.Errorf("a revoked principal self-granted back in: %s/%v/%v", tier, ok, err)
	}
	if tier, ok, _ := s.Resolve(ctx, user); ok || tier != TierNone {
		t.Errorf("after a re-login the ban did not hold: %s/%v", tier, ok)
	}
	// The person stays visible to the admin, which makes the ban reversible
	// rather than a disappearance.
	if g := grantOf(t, s, admin, userEmail); g == nil || !g.Revoked() {
		t.Error("a revoked grant must remain listed, marked revoked")
	} else if g.RevokedBy != admin.Sub {
		t.Errorf("revoked_by = %q, want the deciding admin", g.RevokedBy)
	}

	// A ban must also survive a restart. ADMIN_EMAILS is replayed through
	// SeedGrant on every boot, so clearing the revocation there would make a
	// ban last until the next deploy, which is the failure a recorded
	// revocation exists to prevent, one restart later.
	if err := s.SeedGrant(ctx, userEmail, TierUser); err != nil {
		t.Fatalf("SeedGrant on a revoked address: %v", err)
	}
	if tier, ok, _ := s.Resolve(ctx, user); ok || tier != TierNone {
		t.Errorf("bootstrap seeding un-revoked a ban: %s/%v", tier, ok)
	}
	if g := grantOf(t, s, admin, userEmail); g == nil || !g.Revoked() {
		t.Error("bootstrap seeding cleared the revocation record")
	}

	// Granting again re-admits: an admin undoing their own decision is a
	// supported operation, not a reason to edit a column by hand.
	if err := s.Grant(ctx, admin, userEmail, TierUser); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if tier, ok, _ := s.Resolve(ctx, user); !ok || tier != TierUser {
		t.Errorf("re-granted user = %s/%v, want user", tier, ok)
	}
	if g := grantOf(t, s, admin, userEmail); g == nil || g.Revoked() {
		t.Error("a re-granted row still reads as revoked")
	}

	// 006q: a stranger admits themselves by signing in, at TierUser and no
	// higher, and a repeat sign-in changes nothing.
	newcomer := Principal{Sub: ns + "-new", Email: ns + "-new@example.com"}
	if tier, ok, err := s.SelfGrant(ctx, newcomer); err != nil || !ok || tier != TierUser {
		t.Fatalf("a new arrival = %s/%v/%v, want user", tier, ok, err)
	}
	if tier, ok, _ := s.Resolve(ctx, newcomer); !ok || tier != TierUser {
		t.Errorf("after signing in, resolve = %s/%v", tier, ok)
	}
	if tier, ok, _ := s.SelfGrant(ctx, newcomer); !ok || tier != TierUser {
		t.Errorf("a repeat sign-in changed the tier: %s/%v", tier, ok)
	}

	// A seeded admin stays an admin through their first login: self-granting
	// must never demote somebody the deployment already trusted.
	if tier, ok, _ := s.SelfGrant(ctx, admin); !ok || tier != TierAdmin {
		t.Errorf("an admin's sign-in = %s/%v, want admin", tier, ok)
	}

	// Nothing to bind means nothing to admit.
	if _, ok, _ := s.SelfGrant(ctx, Principal{Sub: ns + "-nosub"}); ok {
		t.Error("a sign-in with no verified email must not be admitted")
	}
	if _, ok, _ := s.SelfGrant(ctx, Principal{Email: ns + "-nosub@example.com"}); ok {
		t.Error("a sign-in with no subject must not be admitted")
	}

	banContract(t, s, admin, ns)
}

// banContract asserts that a ban holds before its target has ever signed in.
// Revoking an address with no grant used to change nothing, so the person was
// admitted by their first sign-in, and the in-memory store bound a revoked
// grant that had never been claimed. Both stores must refuse them, and an
// admin must still be able to re-admit them.
func banContract(t *testing.T, s Access, admin Principal, ns string) {
	t.Helper()

	// An address that has no grant at all.
	stranger := Principal{Sub: ns + "-ban-stranger", Email: ns + "-ban-stranger@example.com"}
	if err := s.Revoke(ctx, admin, stranger.Email); err != nil {
		t.Fatalf("revoking an address with no grant: %v", err)
	}
	if _, ok, err := s.SelfGrant(ctx, stranger); err != nil || ok {
		t.Errorf("a banned stranger's first sign-in = granted %v, %v; want refused", ok, err)
	}
	if _, ok, _ := s.Resolve(ctx, stranger); ok {
		t.Error("a banned stranger resolves as granted")
	}
	g := grantOf(t, s, admin, stranger.Email)
	if g == nil || !g.Revoked() || g.Principal != "" || g.Tier != TierNone {
		t.Fatalf("the ban is not recorded as a revoked, unbound grant: %+v", g)
	}
	// A second ban is idempotent and does not rewrite when the first happened.
	first := revocationOf(t, s, admin, stranger.Email)
	if err := s.Revoke(ctx, admin, stranger.Email); err != nil {
		t.Fatalf("revoking twice: %v", err)
	}
	if again := revocationOf(t, s, admin, stranger.Email); !again.Equal(first) {
		t.Errorf("a second ban moved the revocation time from %v to %v", first, again)
	}
	// Re-admitting is the admin's deliberate act and works on a banned address.
	if err := s.Grant(ctx, admin, stranger.Email, TierUser); err != nil {
		t.Fatalf("re-admitting a banned address: %v", err)
	}
	if tier, ok, _ := s.SelfGrant(ctx, stranger); !ok || tier != TierUser {
		t.Errorf("a re-admitted address = %s/%v, want user", tier, ok)
	}

	// A grant that was given but never claimed, then revoked.
	unclaimed := Principal{Sub: ns + "-ban-unclaimed", Email: ns + "-ban-unclaimed@example.com"}
	if err := s.Grant(ctx, admin, unclaimed.Email, TierUser); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := s.Revoke(ctx, admin, unclaimed.Email); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, ok, _ := s.Resolve(ctx, unclaimed); ok {
		t.Error("a grant revoked before its first sign-in still binds and admits")
	}
	if g := grantOf(t, s, admin, unclaimed.Email); g == nil || g.Principal != "" {
		t.Errorf("a revoked, unclaimed grant was bound to someone: %+v", g)
	}
}

// grantOf reads one grant off the admin list.
func grantOf(t *testing.T, s Access, admin Principal, email string) *Grant {
	t.Helper()
	grants, err := s.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for i := range grants {
		if grants[i].Email == email {
			return &grants[i]
		}
	}
	return nil
}

// revocationOf reads a grant's revocation timestamp, failing if it has none.
func revocationOf(t *testing.T, s Access, admin Principal, email string) time.Time {
	t.Helper()
	g := grantOf(t, s, admin, email)
	if g == nil || g.RevokedAt == nil {
		t.Fatalf("grant %s carries no revocation: %+v", email, g)
	}
	return *g.RevokedAt
}

// TestMemStoreSatisfiesTheGrantContract runs the grant contract against the
// in-memory store.
func TestMemStoreSatisfiesTheGrantContract(t *testing.T) {
	storeContract(t, NewMemStore(), "mem")
}

// TestTierAndRoleNames checks the names a tier and a role report for logs and
// API responses. The strings are wire values the frontend switches on, so they
// are asserted rather than left to a refactor.
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
