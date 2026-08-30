// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// 022c: the solvency read reports outstanding sold-liability — the fold over
// every wallet and pot — so an operator can watch it against the vendor account.
func TestSolvencyReportsOutstandingLiability(t *testing.T) {
	ctx := context.Background()
	acc := access.NewMemStore()
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	ledger := credit.NewMemStore(nil, nil)
	if err := ledger.TopupPaid(ctx, "u1@x.io", 18_700_000, "pi_1", "stripe:pi_1"); err != nil {
		t.Fatalf("TopupPaid: %v", err)
	}
	if err := ledger.TopupPaid(ctx, "u2@x.io", 5_000_000, "pi_2", "stripe:pi_2"); err != nil {
		t.Fatalf("TopupPaid: %v", err)
	}

	mux := http.NewServeMux()
	MountAdmin(mux, RequireTier(acc, access.TierAdmin), NewAdmin(acc, platform.NewMemStore(acc), ledger))

	r := httptest.NewRequest("GET", "/admin/solvency", nil)
	r = r.WithContext(identityCtx(r, admin))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("solvency = %d, want 200 (%s)", w.Code, w.Body)
	}
	var got struct {
		Outstanding int64 `json:"outstanding_micro"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Outstanding != 23_700_000 {
		t.Fatalf("outstanding = %d (%v), want the sum of the two top-ups 23700000", got.Outstanding, err)
	}
}

// 022d: the people table reports a frozen state, filters to the accounts that
// owe, and an admin unfreezes one — granting the override that thaws it.
func TestAdminFreezeListAndUnfreeze(t *testing.T) {
	ctx := context.Background()
	acc := access.NewMemStore()
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	for _, e := range []string{"debtor@example.com", "solvent@example.com"} {
		if err := acc.Grant(ctx, admin, e, access.TierUser); err != nil {
			t.Fatalf("Grant %s: %v", e, err)
		}
	}
	ledger := credit.NewMemStore(nil, nil)
	// Drive the debtor to -$2.
	_ = ledger.TopupPaid(ctx, "debtor@example.com", 5*credit.Dollar, "pi", "a")
	_ = ledger.RefundPaid(ctx, "debtor@example.com", 7*credit.Dollar, "re", "a")

	mux := http.NewServeMux()
	MountAdmin(mux, RequireTier(acc, access.TierAdmin), NewAdmin(acc, platform.NewMemStore(acc), ledger, WithOverrides(acc)))

	list := func(path string) map[string]GrantView {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(identityCtx(r, admin))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d (%s)", path, w.Code, w.Body)
		}
		var rows []GrantView
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		by := map[string]GrantView{}
		for _, v := range rows {
			by[v.Email] = v
		}
		return by
	}

	// The debtor reads frozen; the solvent account does not.
	all := list("/admin/access")
	if !all["debtor@example.com"].Frozen {
		t.Fatalf("debtor should read frozen: %+v", all["debtor@example.com"])
	}
	if all["solvent@example.com"].Frozen {
		t.Fatalf("solvent account should not read frozen")
	}

	// The negative filter returns only the account that owes.
	neg := list("/admin/access?filter=negative")
	if len(neg) != 1 {
		t.Fatalf("filter=negative returned %d rows, want just the debtor", len(neg))
	}

	// Unfreezing grants an override, and the debtor stops reading frozen.
	r := httptest.NewRequest("POST", "/admin/access/debtor@example.com/unfreeze", nil)
	r = r.WithContext(identityCtx(r, admin))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("unfreeze = %d (%s)", w.Code, w.Body)
	}
	if list("/admin/access")["debtor@example.com"].Frozen {
		t.Fatalf("after unfreeze the debtor should not read frozen")
	}
	if active, _ := acc.OverrideActive(ctx, "debtor@example.com"); !active {
		t.Fatalf("unfreeze should have recorded an override")
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

	// The drafting model is written by the same request, and survives it (039).
	// It did not, at first: the handler decoded an explicit struct and had no
	// field for it, so the console's Save appeared to work and changed nothing.
	w = do("PUT", "/admin/settings", `{"start_reserve":9000000,"draft":{"provider":"anthropic","model":"claude-haiku-4-5"}}`, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("put drafting settings = %d %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Draft.Provider != "anthropic" || got.Draft.Model != "claude-haiku-4-5" {
		t.Errorf("drafting config after write = %+v", got.Draft)
	}
	// And an edit that does not mention it leaves it alone, the way the spread
	// and the floor are left alone.
	if w := do("PUT", "/admin/settings", `{"start_reserve":8000000}`, admin); w.Code != http.StatusOK {
		t.Fatalf("put settings = %d", w.Code)
	}
	w = do("GET", "/admin/settings", "", admin)
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Draft.Model != "claude-haiku-4-5" {
		t.Errorf("an unrelated edit switched drafting off: %+v", got.Draft)
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
