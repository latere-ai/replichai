package access

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

var ctx = context.Background()

// bootstrapAdmin seeds an admin and returns its principal, bound.
func bootstrapAdmin(t *testing.T, s *MemStore) Principal {
	t.Helper()
	_ = s.SeedAdmin(ctx, "admin@example.com")
	admin := Principal{Sub: "admin-sub", Email: "admin@example.com", EmailVerified: true}
	if tier, ok, _ := s.Resolve(ctx, admin); !ok || tier != TierAdmin {
		t.Fatalf("admin should resolve as admin, got %s/%v", tier, ok)
	}
	return admin
}

// AC1: only sub and a verified email may feed authorisation. Asserted on the
// type, so a later change cannot add a role, scope, or org claim that a login
// could smuggle into a grant decision.
func TestPrincipalCarriesOnlyIdentity(t *testing.T) {
	allowed := map[string]bool{"Sub": true, "Email": true, "EmailVerified": true}
	ty := reflect.TypeOf(Principal{})
	for i := 0; i < ty.NumField(); i++ {
		if !allowed[ty.Field(i).Name] {
			t.Errorf("Principal has field %q — no IDP role/scope/org may reach an authorisation decision", ty.Field(i).Name)
		}
	}
}

// The fail-closed default: an empty table grants nobody.
func TestEmptyTableGrantsNobody(t *testing.T) {
	s := NewMemStore()
	if tier, ok, _ := s.Resolve(ctx, Principal{Sub: "x", Email: "x@y.com", EmailVerified: true}); ok || tier != TierNone {
		t.Errorf("an empty table must admit nobody, got %s/%v", tier, ok)
	}
}

// AC3: a grant seeded by verified email admits on first sign-in and binds to the
// principal; a second principal presenting that email is refused; revoking locks
// the holder out on the next request.
func TestSeedBindAndRevoke(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)

	if err := s.Grant(ctx, admin, "Seed@Example.com", TierSeed); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// First login binds the grant and admits.
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}
	if tier, ok, _ := s.Resolve(ctx, seed); !ok || tier != TierSeed {
		t.Fatalf("seed should be admitted at first login, got %s/%v", tier, ok)
	}

	// A second principal presenting the same email is refused, not admitted onto
	// the grant.
	imposter := Principal{Sub: "imposter-sub", Email: "seed@example.com", EmailVerified: true}
	if _, ok, _ := s.Resolve(ctx, imposter); ok {
		t.Error("a second principal on a bound email must be refused")
	}

	// Revoking locks the original holder out on the next request.
	if err := s.Revoke(ctx, admin, "seed@example.com"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, ok, _ := s.Resolve(ctx, seed); ok {
		t.Error("a revoked grant must lock its holder out on the next request")
	}
}

// An unverified email never seeds a grant.
func TestUnverifiedEmailNeverBinds(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)
	_ = s.Grant(ctx, admin, "seed@example.com", TierSeed)

	unverified := Principal{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: false}
	if _, ok, _ := s.Resolve(ctx, unverified); ok {
		t.Error("an unverified email must never bind a grant")
	}
}

// AC4: only a platform admin may grant, revoke, or list.
func TestAdminOnlyOperations(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)
	_ = s.Grant(ctx, admin, "seed@example.com", TierSeed)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}
	_, _, _ = s.Resolve(ctx, seed) // bind

	for _, op := range []struct {
		name string
		call func(Principal) error
	}{
		{"grant", func(p Principal) error { return s.Grant(ctx, p, "new@example.com", TierSeed) }},
		{"revoke", func(p Principal) error { return s.Revoke(ctx, p, "seed@example.com") }},
		{"list", func(p Principal) error { _, err := s.List(ctx, p); return err }},
	} {
		if err := op.call(seed); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s by a seed user should be ErrNotAdmin, got %v", op.name, err)
		}
		ungranted := Principal{Sub: "nobody", Email: "nobody@example.com", EmailVerified: true}
		if err := op.call(ungranted); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s by an ungranted principal should be ErrNotAdmin, got %v", op.name, err)
		}
		if err := op.call(admin); err != nil {
			t.Errorf("%s by an admin should succeed, got %v", op.name, err)
		}
	}
}

// Grant is a promote when the email already has a grant, preserving its binding.
func TestGrantPromotesAndPreservesBinding(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)
	_ = s.Grant(ctx, admin, "seed@example.com", TierSeed)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}
	_, _, _ = s.Resolve(ctx, seed) // bind at seed

	if err := s.Grant(ctx, admin, "seed@example.com", TierTrusted); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The same principal now resolves at the higher tier — binding preserved.
	if tier, ok, _ := s.Resolve(ctx, seed); !ok || tier != TierTrusted {
		t.Errorf("promote should keep the binding and raise the tier, got %s/%v", tier, ok)
	}
}

func TestLinkedAccountRoundTrip(t *testing.T) {
	s := NewMemStore()
	if _, ok, _ := s.LinkedAccount(ctx, "sub", "github"); ok {
		t.Error("no link should exist initially")
	}
	if err := s.LinkAccount(ctx, "sub", "github", "octocat"); err != nil {
		t.Fatalf("LinkAccount: %v", err)
	}
	if login, ok, _ := s.LinkedAccount(ctx, "sub", "github"); !ok || login != "octocat" {
		t.Errorf("linked account = %q/%v, want octocat", login, ok)
	}
}

func TestTierGrantedAndString(t *testing.T) {
	if TierNone.Granted() {
		t.Error("TierNone must not be granted")
	}
	for _, tr := range []Tier{TierSeed, TierTrusted, TierAdmin} {
		if !tr.Granted() {
			t.Errorf("%s should be granted", tr)
		}
	}
	if TierAdmin.String() != "admin" || TierNone.String() != "none" {
		t.Error("tier strings are wrong")
	}
}
