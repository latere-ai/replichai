package webauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

func adminSetup(t *testing.T) (*Admin, httpapi.Identity, httpapi.Identity) {
	t.Helper()
	store := access.NewMemStore()
	_ = store.SeedAdmin(context.Background(), "admin@example.com")
	admin := httpapi.Identity{Sub: "admin-sub", Email: "admin@example.com", EmailVerified: true}
	_, _, _ = store.Resolve(context.Background(), access.Principal{Sub: admin.Sub, Email: admin.Email, EmailVerified: true})
	_ = store.Grant(context.Background(), access.Principal{Sub: admin.Sub, Email: admin.Email, EmailVerified: true}, "seed@example.com", access.TierSeed)
	seed := httpapi.Identity{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}
	_, _, _ = store.Resolve(context.Background(), access.Principal{Sub: seed.Sub, Email: seed.Email, EmailVerified: true})
	return NewAdmin(store), admin, seed
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
	admin.Grant(w, as(adminID, "POST", "/admin/access", `{"email":"new@example.com","tier":"trusted"}`))
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

	// A seed user is refused not_trusted on every admin op, server-side.
	for _, op := range []func(http.ResponseWriter, *http.Request){admin.List, admin.Grant, admin.Revoke} {
		w = httptest.NewRecorder()
		op(w, as(seedID, "POST", "/admin/access", `{"email":"x@example.com","tier":"seed"}`))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_trusted") {
			t.Errorf("seed user admin op = %d %s, want 403 not_trusted", w.Code, w.Body.String())
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
	for s, want := range map[string]access.Tier{"seed": access.TierSeed, "TRUSTED": access.TierTrusted, "admin": access.TierAdmin} {
		if got, ok := ParseTier(s); !ok || got != want {
			t.Errorf("ParseTier(%q) = %s/%v", s, got, ok)
		}
	}
	for _, bad := range []string{"", "none", "root"} {
		if _, ok := ParseTier(bad); ok {
			t.Errorf("ParseTier(%q) should be rejected", bad)
		}
	}
}
