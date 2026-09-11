// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	admin := Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if tier, ok, _ := s.Resolve(ctx, admin); !ok || tier != TierAdmin {
		t.Fatalf("admin should resolve as admin, got %s/%v", tier, ok)
	}
	return admin
}

// TestPrincipalCarriesOnlyIdentity checks AC1: only sub and a verified email
// may feed authorisation. It is asserted on the type, so a later change cannot
// add a role, scope, or org claim that a login could pass into a grant
// decision.
func TestPrincipalCarriesOnlyIdentity(t *testing.T) {
	allowed := map[string]bool{"Sub": true, "Email": true}
	ty := reflect.TypeFor[Principal]()
	for field := range ty.Fields() {
		if !allowed[field.Name] {
			t.Errorf("Principal has field %q — no IDP role/scope/org may reach an authorisation decision", field.Name)
		}
	}
}

// TestEmptyTableGrantsNobody checks the fail-closed default: an empty table
// grants nobody.
func TestEmptyTableGrantsNobody(t *testing.T) {
	s := NewMemStore()
	if tier, ok, _ := s.Resolve(ctx, Principal{Sub: "x", Email: "x@y.com"}); ok || tier != TierNone {
		t.Errorf("an empty table must admit nobody, got %s/%v", tier, ok)
	}
}

// AC3: a grant seeded by verified email admits on first sign-in and binds to the
// principal; a second principal presenting that email is refused; revoking locks
// the holder out on the next request.
func TestSeedBindAndRevoke(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)

	if err := s.Grant(ctx, admin, "Seed@Example.com", TierUser); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// First login binds the grant and admits.
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	if tier, ok, _ := s.Resolve(ctx, seed); !ok || tier != TierUser {
		t.Fatalf("seed should be admitted at first login, got %s/%v", tier, ok)
	}

	// A second principal presenting the same email is refused, not admitted onto
	// the grant.
	imposter := Principal{Sub: "imposter-sub", Email: "seed@example.com"}
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

// AC4: only a platform admin may grant, revoke, or list.
func TestAdminOnlyOperations(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)
	_ = s.Grant(ctx, admin, "seed@example.com", TierUser)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	_, _, _ = s.Resolve(ctx, seed) // bind

	for _, op := range []struct {
		name string
		call func(Principal) error
	}{
		{"grant", func(p Principal) error { return s.Grant(ctx, p, "new@example.com", TierUser) }},
		{"revoke", func(p Principal) error { return s.Revoke(ctx, p, "seed@example.com") }},
		{"list", func(p Principal) error { _, err := s.List(ctx, p); return err }},
	} {
		if err := op.call(seed); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s by a seed user should be ErrNotAdmin, got %v", op.name, err)
		}
		ungranted := Principal{Sub: "nobody", Email: "nobody@example.com"}
		if err := op.call(ungranted); !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s by an ungranted principal should be ErrNotAdmin, got %v", op.name, err)
		}
		if err := op.call(admin); err != nil {
			t.Errorf("%s by an admin should succeed, got %v", op.name, err)
		}
	}
}

// TestGrantPromotesAndPreservesBinding checks that Grant on an email that
// already has a grant promotes it and preserves its binding.
func TestGrantPromotesAndPreservesBinding(t *testing.T) {
	s := NewMemStore()
	admin := bootstrapAdmin(t, s)
	_ = s.Grant(ctx, admin, "seed@example.com", TierUser)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	_, _, _ = s.Resolve(ctx, seed) // bind at seed

	if err := s.Grant(ctx, admin, "seed@example.com", TierAdmin); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The same principal now resolves at the higher tier, so the binding is
	// preserved.
	if tier, ok, _ := s.Resolve(ctx, seed); !ok || tier != TierAdmin {
		t.Errorf("promote should keep the binding and raise the tier, got %s/%v", tier, ok)
	}
}

func TestTierGrantedAndString(t *testing.T) {
	if TierNone.Granted() {
		t.Error("TierNone must not be granted")
	}
	for _, tr := range []Tier{TierUser, TierAdmin} {
		if !tr.Granted() {
			t.Errorf("%s should be granted", tr)
		}
	}
	if TierUser.String() != "user" || TierAdmin.String() != "admin" || TierNone.String() != "none" {
		t.Error("tier strings are wrong")
	}
}
