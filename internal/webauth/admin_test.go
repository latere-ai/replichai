// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

func adminSetup(t *testing.T) (*Admin, httpapi.Identity, httpapi.Identity) {
	t.Helper()
	store := access.NewMemStore()
	_ = store.SeedAdmin(context.Background(), "admin@example.com")
	admin := httpapi.Identity{Sub: "admin-sub", Email: "admin@example.com"}
	_, _, _ = store.Resolve(context.Background(), access.Principal{Sub: admin.Sub, Email: admin.Email})
	_ = store.Grant(context.Background(), access.Principal{Sub: admin.Sub, Email: admin.Email}, "seed@example.com", access.TierUser)
	seed := httpapi.Identity{Sub: "seed-sub", Email: "seed@example.com"}
	_, _, _ = store.Resolve(context.Background(), access.Principal{Sub: seed.Sub, Email: seed.Email})
	return NewAdmin(store, platform.NewMemStore(store), nil), admin, seed
}

func as(id httpapi.Identity, method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r.WithContext(httpapi.WithIdentity(r.Context(), id))
}

func TestAdminGrantRevokeList(t *testing.T) {
	admin, adminID, seedID := adminSetup(t)

	// Grant as admin.
	w := httptest.NewRecorder()
	admin.Grant(w, as(adminID, "POST", "/admin/access", `{"email":"new@example.com","tier":"user"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("grant status = %d, want 200", w.Code)
	}

	// List as admin shows the grants.
	w = httptest.NewRecorder()
	admin.List(w, as(adminID, "GET", "/admin/access", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "new@example.com") {
		t.Errorf("list = %d %s", w.Code, w.Body.String())
	}

	// Revoke as admin.
	r := as(adminID, "DELETE", "/admin/access/new@example.com", "")
	r.SetPathValue("email", "new@example.com")
	w = httptest.NewRecorder()
	admin.Revoke(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("revoke status = %d, want 204", w.Code)
	}

	// A plain user is refused not_admin on every admin op, server-side.
	for _, op := range []func(http.ResponseWriter, *http.Request){admin.List, admin.Grant, admin.Revoke} {
		w = httptest.NewRecorder()
		op(w, as(seedID, "POST", "/admin/access", `{"email":"x@example.com","tier":"user"}`))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_admin") {
			t.Errorf("user admin op = %d %s, want 403 not_admin", w.Code, w.Body.String())
		}
	}
}

func TestAdminBadInput(t *testing.T) {
	admin, adminID, _ := adminSetup(t)

	w := httptest.NewRecorder()
	admin.Grant(w, as(adminID, "POST", "/admin/access", `{"email":"x@example.com","tier":"emperor"}`))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bad_tier") {
		t.Errorf("unknown tier = %d %s, want 400 bad_tier", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	admin.Grant(w, as(adminID, "POST", "/admin/access", `not json`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", w.Code)
	}
}

func TestParseTier(t *testing.T) {
	for s, want := range map[string]access.Tier{"user": access.TierUser, "ADMIN": access.TierAdmin, " Admin ": access.TierAdmin} {
		if got, ok := ParseTier(s); !ok || got != want {
			t.Errorf("ParseTier(%q) = %s/%v", s, got, ok)
		}
	}
	// The removed tiers and the fail-safe "none" are not grantable.
	for _, bad := range []string{"", "none", "seed", "trusted", "root"} {
		if _, ok := ParseTier(bad); ok {
			t.Errorf("ParseTier(%q) should be rejected", bad)
		}
	}
}

// Banning somebody ends the server's use of their stored platform sign-in. The
// grant says who may use ReplicHAI, and the stored sign-in is what lets the
// server act as them on the platform, so revoking one without the other leaves a
// banned person's sessions running as them.
func TestAdminRevokeForgetsTheStoredSignIn(t *testing.T) {
	var forgot []string
	var forgetErr error
	admin, adminID, _ := adminSetup(t)
	admin.forget = func(_ context.Context, subject string) error {
		forgot = append(forgot, subject)
		return forgetErr
	}
	revoke := func(email string) *httptest.ResponseRecorder {
		r := as(adminID, "DELETE", "/admin/access/"+email, "")
		r.SetPathValue("email", email)
		w := httptest.NewRecorder()
		admin.Revoke(w, r)
		return w
	}

	// seed@example.com signed in, so its grant is bound to seed-sub.
	if w := revoke("Seed@Example.com"); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", w.Code)
	}
	if len(forgot) != 1 || forgot[0] != "seed-sub" {
		t.Fatalf("forgotten sign-ins = %v, want [seed-sub]", forgot)
	}

	// An address that never signed in has no stored sign-in to forget.
	forgot = nil
	if w := revoke("never@example.com"); w.Code != http.StatusNoContent {
		t.Fatalf("revoke of an unbound address = %d, want 204", w.Code)
	}
	if len(forgot) != 0 {
		t.Errorf("forgot %v for an address that never signed in", forgot)
	}

	// A store that cannot forget is reported, and a retry forgets again: the
	// revoke is idempotent, so the retry reaches the same subject.
	forgetErr = errors.New("db down")
	if w := revoke("seed@example.com"); w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "signin_not_forgotten") {
		t.Fatalf("revoke with a failing store = %d %s, want 500 signin_not_forgotten", w.Code, w.Body)
	}
	forgetErr = nil
	forgot = nil
	if w := revoke("seed@example.com"); w.Code != http.StatusNoContent || len(forgot) != 1 {
		t.Fatalf("the retry = %d, forgot %v; want 204 and one forget", w.Code, forgot)
	}
}

// An admin revoking themself, or demoting the last admin, is answered 409 with a
// code the console can name, and the grant is left as it was.
func TestAdminRefusesSelfRevokeAndLastAdminDemotion(t *testing.T) {
	admin, adminID, _ := adminSetup(t)

	r := as(adminID, "DELETE", "/admin/access/admin@example.com", "")
	r.SetPathValue("email", "admin@example.com")
	w := httptest.NewRecorder()
	admin.Revoke(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "self_revoke") {
		t.Errorf("self revoke = %d %s, want 409 self_revoke", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	admin.Grant(w, as(adminID, "POST", "/admin/access", `{"email":"admin@example.com","tier":"user"}`))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "last_admin") {
		t.Errorf("demoting the last admin = %d %s, want 409 last_admin", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	admin.List(w, as(adminID, "GET", "/admin/access", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"admin@example.com"`) {
		t.Errorf("the admin is gone after the refusals: %d %s", w.Code, w.Body.String())
	}
}
