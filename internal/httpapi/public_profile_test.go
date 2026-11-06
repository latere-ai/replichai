// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/profile"
)

// getOpen requests a route with no identity, as a signed-out reader who
// followed a link from a paper would.
func getOpen(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

// TestPublicProfileIsReadableBySignedOutStrangers checks that a public profile
// is readable by anyone, signed in or not, with the same openness as the public
// zone. An attestation whose signer cannot be looked up is worth less than one
// whose signer can.
func TestPublicProfileIsReadableBySignedOutStrangers(t *testing.T) {
	d := testDeps(allowAll)
	owner := Identity{Sub: "owner-sub", Email: "ada@example.org"}
	publishable(t, d, owner.Sub, "ada")
	h := NewRouter(d).Handler()

	w := getOpen(t, h, "/profiles/ada")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a signed-out reader", w.Code)
	}
	var got profile.PublicProfile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Handle != "ada" || got.DisplayName != "Ada Lovelace" {
		t.Errorf("profile = %+v", got)
	}
	// The contact is published for inquiries, so it is rendered in full.
	if got.Contact != "ada@example.org" {
		t.Errorf("contact = %q, want the published address", got.Contact)
	}
	// The account email never appears here. The redacted signer email on an
	// attestation serves that purpose, and it is confined to the attestation.
	if body := w.Body.String(); containsAccountEmail(body) {
		t.Errorf("the page leaks an account address: %s", body)
	}
	// Projects is always an array, never null, so the client renders an empty
	// list rather than crashing on it.
	if got.Projects == nil {
		t.Error("projects = null, want an empty array")
	}
}

// containsAccountEmail reports whether a response carries the redacted or full
// signer address, neither of which belongs on a profile page.
func containsAccountEmail(body string) bool {
	for _, s := range []string{"signed_off_email", "**@", "***@"} {
		if strings.Contains(body, s) {
			return true
		}
	}
	return false
}

// TestPublicProfileListsSignedPublicProjects checks that the page lists the
// public work signed under that name and that a withdrawn project is removed
// from it. The public zone is the single source of truth for what is public,
// and the profile reads from it rather than keeping its own list.
func TestPublicProfileListsSignedPublicProjects(t *testing.T) {
	d := testDeps(allowAll)
	owner := Identity{Sub: "owner-sub", Email: "ada@example.org"}
	publishable(t, d, owner.Sub, "ada")
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Reproducing Study 1")
	publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	h := NewRouter(d).Handler()

	// While the project is private, the page lists nothing.
	if got := publicProfileOf(t, h, "ada"); len(got.Projects) != 0 {
		t.Fatalf("a private project is listed: %+v", got.Projects)
	}

	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", w.Code, w.Body.String())
	}
	got := publicProfileOf(t, h, "ada")
	if len(got.Projects) != 1 || got.Projects[0].ID != p.ID || got.Projects[0].Title != "Reproducing Study 1" {
		t.Fatalf("projects = %+v, want the published one", got.Projects)
	}
	if got.Projects[0].SignedOffAt == nil {
		t.Error("a listed project carries no sign-off date")
	}

	// Withdrawing removes it from the page, because it removes it from the
	// public zone.
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/private", `{"reason":"oops"}`, owner); w.Code != http.StatusOK {
		t.Fatalf("withdraw: %d", w.Code)
	}
	if got := publicProfileOf(t, h, "ada"); len(got.Projects) != 0 {
		t.Errorf("a withdrawn project is still listed: %+v", got.Projects)
	}
}

// TestPublicProfileListsOnlyWhatThisPersonSigned checks that another user's
// public project is not listed as this person's work.
func TestPublicProfileListsOnlyWhatThisPersonSigned(t *testing.T) {
	d := testDeps(allowAll)
	ada := Identity{Sub: "ada-sub", Email: "ada@example.org"}
	bob := Identity{Sub: "bob-sub", Email: "bob@example.org"}
	publishable(t, d, ada.Sub, "ada")
	publishable(t, d, bob.Sub, "bob")
	theirs, _ := d.Projects.Create(context.Background(), bob.Sub, "Bob's work")
	publishInto(t, d.Jobs, bob.Sub, theirs.ID, "r1")
	h := NewRouter(d).Handler()
	if w := doAs(t, h, "POST", "/projects/"+theirs.ID+"/public", signOff, bob); w.Code != http.StatusOK {
		t.Fatalf("publish: %d", w.Code)
	}

	if got := publicProfileOf(t, h, "ada"); len(got.Projects) != 0 {
		t.Errorf("ada's page lists bob's project: %+v", got.Projects)
	}
	if got := publicProfileOf(t, h, "bob"); len(got.Projects) != 1 {
		t.Errorf("bob's page = %+v", got.Projects)
	}
}

// TestPublicProfileHidesPrivateAndUnknownAlike checks that an unknown handle,
// a private profile, and a profile with no handle all answer the same 404.
// Distinguishing them would let a caller enumerate who holds which name.
func TestPublicProfileHidesPrivateAndUnknownAlike(t *testing.T) {
	d := testDeps(allowAll)
	// A profile with a handle that has not been made public.
	if _, err := d.Profiles.Save(context.Background(), profile.Profile{
		Principal: "shy-sub", DisplayName: "Shy", Handle: "shy",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A public profile with no handle, which cannot be reached by any path.
	if _, err := d.Profiles.Save(context.Background(), profile.Profile{
		Principal: "nameless-sub", DisplayName: "Nameless", Public: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	h := NewRouter(d).Handler()

	for _, handle := range []string{"shy", "nobody", "nameless"} {
		w := getOpen(t, h, "/profiles/"+handle)
		if w.Code != http.StatusNotFound {
			t.Errorf("/profiles/%s = %d, want 404", handle, w.Code)
		}
		if body := w.Body.String(); strings.Contains(body, "Shy") || strings.Contains(body, "Nameless") {
			t.Errorf("/profiles/%s leaked a private profile: %s", handle, body)
		}
	}
}

// TestPublishRequiresAPublicProfile covers 006r: publishing puts a name on a
// result, and the name has to resolve to a page. The request is refused before
// anything is recorded, so a refusal leaves no partial sign-off behind.
func TestPublishRequiresAPublicProfile(t *testing.T) {
	d := testDeps(allowAll)
	owner := Identity{Sub: "owner-sub", Email: "ada@example.org"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Owned")
	publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	h := NewRouter(d).Handler()

	w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner)
	if w.Code != http.StatusConflict {
		t.Fatalf("publish with a private profile = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "profile_not_public") {
		t.Errorf("error = %s, want profile_not_public", w.Body.String())
	}
	// Nothing was recorded.
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.SignedOffAt != nil || got.SignedOffName != "" || got.Visibility == "public" {
		t.Fatalf("a refused publish recorded something: %+v", got)
	}

	// A handle without a public page is not enough either, because the link
	// would not resolve.
	if _, err := d.Profiles.Save(context.Background(), profile.Profile{
		Principal: owner.Sub, DisplayName: "Ada", Contact: "ada@example.org", Handle: "ada",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusConflict {
		t.Errorf("publish with a private profile that has a handle = %d, want 409", w.Code)
	}

	// Making the profile public allows the publish.
	publishable(t, d, owner.Sub, "ada")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Errorf("publish with a public profile = %d (%s)", w.Code, w.Body.String())
	}
}

// TestSavingOneProfileFieldLeavesTheRestAlone checks that PATCH updates only
// the fields sent. A settings form that saves a display name must not make the
// page private as a side effect, which would break every link a published
// attestation makes to it.
func TestSavingOneProfileFieldLeavesTheRestAlone(t *testing.T) {
	d := testDeps(allowAll)
	owner := Identity{Sub: "owner-sub", Email: "ada@example.org"}
	publishable(t, d, owner.Sub, "ada")
	h := NewRouter(d).Handler()

	if w := doAs(t, h, "PATCH", "/me/profile", `{"institution":"LMU Munich"}`, owner); w.Code != http.StatusOK {
		t.Fatalf("patch: %d (%s)", w.Code, w.Body.String())
	}
	got, _ := d.Profiles.Get(context.Background(), owner.Sub)
	if got.Handle != "ada" || !got.Public {
		t.Fatalf("a partial save took the page down: %+v", got)
	}
	if got.Institution != "LMU Munich" || got.DisplayName != "Ada Lovelace" {
		t.Errorf("profile = %+v", got)
	}
	// An explicit false does make the page private.
	if w := doAs(t, h, "PATCH", "/me/profile", `{"public":false}`, owner); w.Code != http.StatusOK {
		t.Fatalf("unpublish: %d", w.Code)
	}
	if got, _ := d.Profiles.Get(context.Background(), owner.Sub); got.Public {
		t.Error("an explicit public:false did not take the page down")
	}
}

// TestHandleErrorsAreDistinct checks that a handle another user holds is a
// conflict and that a malformed or reserved one is a bad field. Each has its
// own code so the settings form can point at the input and name the problem.
func TestHandleErrorsAreDistinct(t *testing.T) {
	d := testDeps(allowAll)
	publishable(t, d, "someone-else", "ada")
	h := NewRouter(d).Handler()
	me := Identity{Sub: "me-sub", Email: "me@example.org"}

	for _, tt := range []struct {
		body   string
		status int
		code   string
	}{
		{`{"handle":"ada"}`, http.StatusConflict, "handle_taken"},
		{`{"handle":"no"}`, http.StatusBadRequest, "invalid_handle"},
		{`{"handle":"Ada Lovelace"}`, http.StatusBadRequest, "invalid_handle"},
		{`{"handle":"admin"}`, http.StatusBadRequest, "handle_reserved"},
	} {
		w := doAs(t, h, "PATCH", "/me/profile", tt.body, me)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) {
			t.Errorf("%s = %d %s, want %d %s", tt.body, w.Code, w.Body.String(), tt.status, tt.code)
		}
	}
	// Nothing was written.
	if got, _ := d.Profiles.Get(context.Background(), me.Sub); got.Handle != "" {
		t.Errorf("a refused handle was written: %+v", got)
	}
}

// publicProfileOf reads a public profile page or fails the test.
func publicProfileOf(t *testing.T, h http.Handler, handle string) profile.PublicProfile {
	t.Helper()
	w := getOpen(t, h, "/profiles/"+handle)
	if w.Code != http.StatusOK {
		t.Fatalf("/profiles/%s = %d (%s)", handle, w.Code, w.Body.String())
	}
	var got profile.PublicProfile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}
