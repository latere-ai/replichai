// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"errors"
	"strings"
	"testing"
)

// AC8: project membership binds one-way on the same terms as a grant: seeded by
// verified email, bound to the first principal, and a second principal refused.
func TestMembershipSeedBindOneWay(t *testing.T) {
	m := NewMemMembership()
	_ = m.SeedLead(ctx, "proj-1", Principal{Sub: "lead-sub", Email: "lead@example.com"})

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

// TestMembershipLeadOnly checks that only a project's lead may change its
// membership.
func TestMembershipLeadOnly(t *testing.T) {
	m := NewMemMembership()
	_ = m.SeedLead(ctx, "proj-1", Principal{Sub: "lead-sub", Email: "lead@example.com"})
	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	_ = m.Add(ctx, lead, "proj-1", "collab@example.com", RoleCollaborator)
	collab := Principal{Sub: "collab-sub", Email: "collab@example.com"}
	_, _, _ = m.Role(ctx, collab, "proj-1") // bind

	// List returns both members, sorted by email, with roles and binding. Both
	// have bound: the collaborator via Role above, the lead via its Add call.
	members, err := m.List(ctx, "proj-1")
	if err != nil || len(members) != 2 {
		t.Fatalf("List = %+v err=%v; want 2 members", members, err)
	}
	if members[0].Email != "collab@example.com" || !members[0].Bound || members[0].Role != RoleCollaborator {
		t.Errorf("collab member = %+v", members[0])
	}
	if members[1].Email != "lead@example.com" || members[1].Role != RoleLead {
		t.Errorf("lead member = %+v", members[1])
	}

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

// bindingContract asserts the login-time binding contract both stores satisfy.
// It is one function so the in-memory and Postgres implementations cannot
// drift on the invariant a project invitation depends on.
//
// Before the fix this whole contract failed: binding was a side effect of the
// authorization checks, every one of which short-circuits for an owner or a
// public project, so an owner was shown as "pending" permanently and an
// invitee never appeared under "shared with me".
func bindingContract(t *testing.T, m Membership, project string) {
	t.Helper()
	// Subject and addresses are derived from the project so the contract is
	// self-contained: it can run in a store that already holds other rows.
	lead := Principal{Sub: project + "-lead", Email: "Lead-" + project + "@Example.com"}
	collabEmail := "collab-" + project + "@example.com"

	// An owner is bound the moment their project is seeded, with no login,
	// project read, or authorization check in between.
	if err := m.SeedLead(ctx, project, lead); err != nil {
		t.Fatalf("SeedLead: %v", err)
	}
	members, err := m.List(ctx, project)
	if err != nil || len(members) != 1 {
		t.Fatalf("List = %+v err=%v; want the lead", members, err)
	}
	if !members[0].Bound {
		t.Error("the owner must be bound at seed time, not left pending")
	}
	if ids, _ := m.Projects(ctx, lead.Sub); len(ids) != 1 || ids[0] != project {
		t.Errorf("the lead's own project should list, got %v", ids)
	}

	// An invitee is pending until they sign in, and signing in binds them;
	// nothing else has to happen first.
	if err := m.Add(ctx, lead, project, strings.ToUpper(collabEmail), RoleCollaborator); err != nil {
		t.Fatalf("Add: %v", err)
	}
	collab := Principal{Sub: project + "-collab", Email: collabEmail}
	if ids, _ := m.Projects(ctx, collab.Sub); len(ids) != 0 {
		t.Errorf("an invitee must not appear as a member before signing in, got %v", ids)
	}
	n, err := m.BindAll(ctx, collab)
	if err != nil || n != 1 {
		t.Fatalf("BindAll = %d, %v; want 1 membership bound", n, err)
	}
	ids, err := m.Projects(ctx, collab.Sub)
	if err != nil || len(ids) != 1 || ids[0] != project {
		t.Fatalf("after sign-in the invitee's project should list, got %v err=%v", ids, err)
	}
	members, _ = m.List(ctx, project)
	for _, mem := range members {
		if !mem.Bound {
			t.Errorf("%s should be bound after sign-in", mem.Email)
		}
	}

	// Binding is one-way: a second principal presenting a bound email takes
	// nothing, and a repeat sign-in binds nothing new.
	if n, _ := m.BindAll(ctx, Principal{Sub: project + "-imposter", Email: collabEmail}); n != 0 {
		t.Error("a second principal must not bind an already-bound membership")
	}
	if n, _ := m.BindAll(ctx, collab); n != 0 {
		t.Error("a repeat sign-in must bind nothing new")
	}
	if ids, _ := m.Projects(ctx, project+"-imposter"); len(ids) != 0 {
		t.Errorf("the imposter must hold no membership, got %v", ids)
	}

	// An address change at the identity provider must not make one person a
	// member of the same project twice.
	if err := m.Add(ctx, lead, project, "collab-new-"+project+"@example.com", RoleCollaborator); err != nil {
		t.Fatalf("Add second address: %v", err)
	}
	if n, _ := m.BindAll(ctx, Principal{Sub: collab.Sub, Email: "collab-new-" + project + "@example.com"}); n != 0 {
		t.Error("a principal already holding a row in this project must not bind a second one")
	}

	// A sign-in with no verified email binds nothing at all.
	if n, _ := m.BindAll(ctx, Principal{Sub: project + "-nobody"}); n != 0 {
		t.Error("an unverified sign-in must never bind")
	}

	// Removing a bound member releases the binding, so a re-invitation binds
	// again on the next sign-in. Binding a row at seed time made this the common
	// case rather than the rare one: before, a removed member was usually still
	// pending and there was no binding to release.
	if err := m.Remove(ctx, lead, project, collabEmail); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ids, _ := m.Projects(ctx, collab.Sub); len(ids) != 0 {
		t.Errorf("a removed member must hold nothing, got %v", ids)
	}
	for _, mem := range mustList(t, m, project) {
		if mem.Email == collabEmail {
			t.Error("a removed member must not still be listed")
		}
	}
	if err := m.Add(ctx, lead, project, collabEmail, RoleCollaborator); err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if n, _ := m.BindAll(ctx, collab); n != 1 {
		t.Errorf("a re-invited member must bind again on their next sign-in, bound %d", n)
	}
}

// mustList reads a project's members or fails the test.
func mustList(t *testing.T, m Membership, project string) []Member {
	t.Helper()
	members, err := m.List(ctx, project)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return members
}

// TestMemMembershipBindingContract runs the login-time binding contract against
// the in-memory store.
func TestMemMembershipBindingContract(t *testing.T) {
	bindingContract(t, NewMemMembership(), "proj-bind")
}

// AC7: membership admits nobody to the platform. Tier and role are resolved by
// independent stores, and neither can be satisfied by the other.
func TestMembershipAdmitsNobodyToThePlatform(t *testing.T) {
	grants := NewMemStore()
	members := NewMemMembership()

	// A principal is a project lead but has no platform grant.
	_ = members.SeedLead(ctx, "proj-1", Principal{Sub: "lead-sub", Email: "lead@example.com"})
	lead := Principal{Sub: "lead-sub", Email: "lead@example.com"}
	if role, ok, _ := members.Role(ctx, lead, "proj-1"); !ok || role != RoleLead {
		t.Fatal("precondition: lead has a project role")
	}
	// They still reach no operator tier: the gate consults grants, not membership.
	if tier, ok, _ := grants.Resolve(ctx, lead); ok || tier != TierNone {
		t.Errorf("a project role must not admit to the platform, got tier %s/%v", tier, ok)
	}

	// Conversely a granted seed user has no project role they were not given.
	admin := bootstrapAdmin(t, grants)
	_ = grants.Grant(ctx, admin, "seed@example.com", TierUser)
	seed := Principal{Sub: "seed-sub", Email: "seed@example.com"}
	if tier, ok, _ := grants.Resolve(ctx, seed); !ok || tier != TierUser {
		t.Fatal("precondition: seed user is granted")
	}
	if role, ok, _ := members.Role(ctx, seed, "proj-1"); ok || role != RoleNone {
		t.Errorf("a platform grant must not confer a project role, got %s/%v", role, ok)
	}
}
