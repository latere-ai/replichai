// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeNotifier records the notifications the platform tried to send, and can
// be made to fail.
type fakeNotifier struct {
	mu     sync.Mutex
	sent   []sentInvitation
	frozen []string // Addresses told their account is frozen (022d).
	err    error
	calls  int
}

type sentInvitation struct{ to, projectID, projectName, inviter string }

func (f *fakeNotifier) ProjectInvitation(_ context.Context, to, projectID, projectName, inviter string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentInvitation{to, projectID, projectName, inviter})
	return nil
}

func (f *fakeNotifier) AccountFrozen(_ context.Context, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.frozen = append(f.frozen, to)
	return nil
}

func (f *fakeNotifier) frozenSends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.frozen...)
}

func (f *fakeNotifier) last() (sentInvitation, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return sentInvitation{}, f.calls
	}
	return f.sent[len(f.sent)-1], f.calls
}

// TestInvitingAMemberSendsAnInvitation checks that inviting somebody sends
// them an invitation. Before 006p the membership row was the whole feature,
// and the person named had no way to learn that it existed.
func TestInvitingAMemberSendsAnInvitation(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com", Name: "Ada Lovelace"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Reproducing Study 1")

	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("add member: status %d, want 204", w.Code)
	}
	got, calls := notifier.last()
	if calls != 1 {
		t.Fatalf("notifier called %d time(s), want exactly 1", calls)
	}
	if got.to != "collab@x.com" || got.projectID != p.ID || got.projectName != "Reproducing Study 1" {
		t.Errorf("invitation = %+v", got)
	}
	// The inviter is named from the session, so the reader learns who added
	// them rather than an opaque subject.
	if got.inviter != "Ada Lovelace" {
		t.Errorf("inviter = %q, want the session's display name", got.inviter)
	}
}

// TestInvitationNamesTheInviterByAddressWhenNameless checks that when the
// identity provider supplies no display name, the address is used instead.
func TestInvitationNamesTheInviterByAddressWhenNameless(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner)
	got, _ := notifier.last()
	if got.inviter != "owner@x.com" {
		t.Errorf("inviter = %q, want the owner's address", got.inviter)
	}
}

// TestAFailedInvitationStillAddsTheMember checks that a mail provider that is
// down does not make sharing fail for the owner. The membership is what
// persists.
func TestAFailedInvitationStillAddsTheMember(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{err: errors.New("provider is down")}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("add member: status %d, want 204 despite the send failing", w.Code)
	}
	members, _ := d.Members.List(context.Background(), p.ID)
	found := false
	for _, m := range members {
		if m.Email == "collab@x.com" {
			found = true
		}
	}
	if !found {
		t.Error("the membership must survive a failed send")
	}
}

// TestInvitationIsNotRepeatedForAnExistingMember checks that re-inviting an
// existing member sends nothing, and that re-inviting somebody who was removed
// is a new invitation and sends one.
func TestInvitationIsNotRepeatedForAnExistingMember(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner)
	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"COLLAB@x.com"}`, owner)
	if _, calls := notifier.last(); calls != 1 {
		t.Errorf("notifier called %d times, want 1 — re-inviting an existing member says nothing", calls)
	}

	doAs(t, h, "DELETE", "/projects/"+p.ID+"/members/collab@x.com", "", owner)
	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner)
	if _, calls := notifier.last(); calls != 2 {
		t.Errorf("notifier called %d times, want 2 — re-inviting a removed member is a real invitation", calls)
	}
}

// TestNoInvitationWhenTheAddIsRefused checks that a refused invite sends
// nothing, because nobody was added.
func TestNoInvitationWhenTheAddIsRefused(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	stranger := Identity{Sub: "stranger", Email: "s@x.com"}
	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"x@x.com"}`, stranger)
	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"  "}`, owner)
	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `not json`, owner)
	if _, calls := notifier.last(); calls != 0 {
		t.Errorf("notifier called %d times, want 0 — nobody was added", calls)
	}
}

// TestAddingAMemberWorksWithNoNotifier checks that a deployment with no mail
// configured, and so no notifier, still adds members.
func TestAddingAMemberWorksWithNoNotifier(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("add member with no notifier: status %d, want 204", w.Code)
	}
}

// TestInvitationGoesToTheAddressInvited checks that the invitation goes to
// the address invited, normalized the way membership normalizes it, and never
// to a second address from the body.
func TestInvitationGoesToTheAddressInvited(t *testing.T) {
	d := testDeps(allowAll)
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"Collab@X.com","role":"lead"}`, owner)
	got, _ := notifier.last()
	if !strings.EqualFold(got.to, "collab@x.com") {
		t.Errorf("invitation sent to %q", got.to)
	}
}
