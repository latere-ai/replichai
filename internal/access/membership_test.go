package access

import (
	"errors"
	"testing"
)

// AC8: project membership binds one-way on the same terms as a grant — seeded by
// verified email, bound to the first principal, a second principal refused.
func TestMembershipSeedBindOneWay(t *testing.T) {
	m := NewMemMembership()
	_ = m.SeedLead(ctx, "proj-1", "lead@example.com")

	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	if role, ok, _ := m.Role(ctx, lead, "proj-1"); !ok || role != RoleLead {
		t.Fatalf("lead should bind to RoleLead, got %s/%v", role, ok)
	}

	// The lead invites a collaborator by email.
	if err := m.Add(ctx, lead, "proj-1", "collab@example.com", RoleCollaborator); err != nil {
		t.Fatalf("Add: %v", err)
	}
	collab := Principal{Sub: "collab-sub", Email: "collab@example.com"}
	if role, ok, _ := m.Role(ctx, collab, "proj-1"); !ok || role != RoleCollaborator {
		t.Fatalf("collaborator should bind, got %s/%v", role, ok)
	}
	// A second principal on the bound email is refused.
	imposter := Principal{Sub: "imposter", Email: "collab@example.com"}
	if _, ok, _ := m.Role(ctx, imposter, "proj-1"); ok {
		t.Error("a second principal on a bound membership must be refused")
	}
	// An unverified email never binds.
	if _, ok, _ := m.Role(ctx, Principal{Sub: "x", Email: "collab@example.com"}, "proj-1"); ok {
		t.Error("an unverified email must never bind a membership")
	}
}

// Only a project's lead may change its membership.
func TestMembershipLeadOnly(t *testing.T) {
	m := NewMemMembership()
	_ = m.SeedLead(ctx, "proj-1", "lead@example.com")
	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	_ = m.Add(ctx, lead, "proj-1", "collab@example.com", RoleCollaborator)
	collab := Principal{Sub: "collab-sub", Email: "collab@example.com"}
	_, _, _ = m.Role(ctx, collab, "proj-1") // bind

	// A collaborator cannot add or remove.
	if err := m.Add(ctx, collab, "proj-1", "other@example.com", RoleCollaborator); !errors.Is(err, ErrNotLead) {
		t.Errorf("add by a collaborator should be ErrNotLead, got %v", err)
	}
	if err := m.Remove(ctx, collab, "proj-1", "collab@example.com"); !errors.Is(err, ErrNotLead) {
		t.Errorf("remove by a collaborator should be ErrNotLead, got %v", err)
	}
	// A stranger to the project cannot either.
	stranger := Principal{Sub: "s", Email: "s@example.com"}
	if err := m.Add(ctx, stranger, "proj-1", "x@example.com", RoleLead); !errors.Is(err, ErrNotLead) {
		t.Errorf("add by a stranger should be ErrNotLead, got %v", err)
	}
	// The lead can remove.
	if err := m.Remove(ctx, lead, "proj-1", "collab@example.com"); err != nil {
		t.Errorf("remove by the lead should succeed, got %v", err)
	}
}

// AC7: membership admits nobody to the platform. Tier and role are resolved by
// independent stores, and neither can be satisfied by the other.
func TestMembershipAdmitsNobodyToThePlatform(t *testing.T) {
	grants := NewMemStore()
	members := NewMemMembership()

	// A principal is a project lead but has no platform grant.
	_ = members.SeedLead(ctx, "proj-1", "lead@example.com")
	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	if role, ok, _ := members.Role(ctx, lead, "proj-1"); !ok || role != RoleLead {
		t.Fatal("precondition: lead has a project role")
	}
	// They still reach no operator tier — the gate consults grants, not membership.
	if tier, ok, _ := grants.Resolve(ctx, lead); ok || tier != TierNone {
		t.Errorf("a project role must not admit to the platform, got tier %s/%v", tier, ok)
	}

	// Conversely a granted seed user has no project role they were not given.
	admin := bootstrapAdmin(t, grants)
	_ = grants.Grant(ctx, admin, "seed@example.com", TierSeed)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	if tier, ok, _ := grants.Resolve(ctx, seed); !ok || tier != TierSeed {
		t.Fatal("precondition: seed user is granted")
	}
	if role, ok, _ := members.Role(ctx, seed, "proj-1"); ok || role != RoleNone {
		t.Errorf("a platform grant must not confer a project role, got %s/%v", role, ok)
	}
}
