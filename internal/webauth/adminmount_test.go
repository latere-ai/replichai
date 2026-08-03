package webauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// mountedAdmin wires the real admin surface the way main.go does, and returns
// the mux, the enumerated routes, and the two principals to drive it with.
func mountedAdmin(t *testing.T) (*http.ServeMux, []httpapi.Route, access.Principal, access.Principal) {
	t.Helper()
	ctx := context.Background()
	acc := access.NewMemStore()
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	user := access.Principal{Sub: "user-sub", Email: "user@example.com"}
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if err := acc.SeedGrant(ctx, user.Email, access.TierUser); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	for _, p := range []access.Principal{admin, user} {
		if _, ok, _ := acc.Resolve(ctx, p); !ok {
			t.Fatalf("%s should resolve granted", p.Email)
		}
	}

	mux := http.NewServeMux()
	routes := MountAdmin(mux, RequireTier(acc, access.TierAdmin), NewAdmin(acc, platform.NewMemStore(acc), credit.NewMemStore(acc, nil)))
	return mux, routes, admin, user
}

// identityCtx puts a principal on a request the way the auth middleware does.
func identityCtx(r *http.Request, p access.Principal) context.Context {
	return httpapi.WithIdentity(r.Context(), httpapi.Identity{Sub: p.Sub, Email: p.Email})
}

// concreteFor turns a route pattern into a request path a mux will match.
func concreteFor(pattern string) string {
	return strings.ReplaceAll(strings.ReplaceAll(pattern, "{email}", "someone@example.com"), "{id}", "x")
}

// 006k AC1: every route MountAdmin registers is refused for a signed-in
// non-admin. Driven from the enumerated set, so a route added to the mount
// without the guard — or a new admin route mounted elsewhere and forgotten —
// fails here rather than shipping open.
func TestEveryAdminRouteRefusesANonAdmin(t *testing.T) {
	mux, routes, _, user := mountedAdmin(t)
	if len(routes) < 6 {
		t.Fatalf("mounted %d admin routes; the set looks incomplete", len(routes))
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.Method, concreteFor(rt.Pattern), strings.NewReader(`{}`))
		r = r.WithContext(identityCtx(r, user))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s as a plain user = %d, want 403", rt.Method, rt.Pattern, w.Code)
		}
	}
}

// 006k AC1: an unauthenticated caller is refused too — the tab being hidden is
// not the guard.
func TestEveryAdminRouteRefusesTheUnauthenticated(t *testing.T) {
	mux, routes, _, _ := mountedAdmin(t)
	for _, rt := range routes {
		r := httptest.NewRequest(rt.Method, concreteFor(rt.Pattern), strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusOK || w.Code == http.StatusNoContent {
			t.Errorf("%s %s unauthenticated = %d, want a refusal", rt.Method, rt.Pattern, w.Code)
		}
	}
}

// 006k AC2/AC5/AC7, in 011's terms: an admin drives the whole console — read
// the policy, write it, put credits in somebody's wallet, and see the balance
// in the people table.
func TestAdminDrivesSettingsAndTopups(t *testing.T) {
	mux, _, admin, _ := mountedAdmin(t)

	do := func(method, path, body string, p access.Principal) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(identityCtx(r, p))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	// The console reads the number in force before anyone has set one.
	w := do("GET", "/admin/settings", "", admin)
	var got platform.Settings
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if w.Code != http.StatusOK || got.StartReserve != platform.DefaultStartReserve {
		t.Fatalf("settings = %d %+v, want the seeded default", w.Code, got)
	}

	// Writing it records the admin who did it.
	w = do("PUT", "/admin/settings", `{"start_reserve":9000000}`, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("put settings = %d %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StartReserve != 9*credit.Dollar || got.UpdatedBy != admin.Email {
		t.Errorf("settings after write = %+v", got)
	}

	// A negative policy is a bad request, not a 500.
	if w := do("PUT", "/admin/settings", `{"start_reserve":-1}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("negative reserve = %d, want 400", w.Code)
	}

	// Credits for someone who has never signed in show up in the people table
	// next to their grant — the wallet is keyed by email, which is exactly what
	// lets an admin fund somebody before inviting them.
	if w := do("POST", "/admin/access", `{"email":"newcomer@example.com","tier":"user"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("grant = %d %s", w.Code, w.Body.String())
	}
	if w := do("POST", "/admin/access/newcomer@example.com/credits", `{"amount":5000000}`, admin); w.Code != http.StatusOK {
		t.Fatalf("topup = %d %s", w.Code, w.Body.String())
	}
	// A top-up of nothing is a bad request rather than a silent no-op.
	if w := do("POST", "/admin/access/newcomer@example.com/credits", `{"amount":0}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("zero top-up = %d, want 400", w.Code)
	}
	w = do("GET", "/admin/access", "", admin)
	var people []GrantView
	if err := json.NewDecoder(w.Body).Decode(&people); err != nil {
		t.Fatalf("decode people: %v", err)
	}
	var found *GrantView
	for i := range people {
		if people[i].Email == "newcomer@example.com" {
			found = &people[i]
		}
	}
	if found == nil {
		t.Fatalf("people = %+v, want the newcomer", people)
	}
	if found.Wallet != 5*credit.Dollar || found.Tier != "user" || found.Bound {
		t.Errorf("newcomer = %+v, want a five-dollar wallet on an unbound user grant", *found)
	}
}
