// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/managed"
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
	routes := MountAdmin(mux, RequireTier(acc, access.TierAdmin), NewAdmin(acc, platform.NewMemStore(acc), credit.NewMemStore(acc, nil), WithLatereEnvironments([]string{"base", "gui", "science"})))
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
// non-admin. The test is driven from the enumerated set, so a route added to
// the mount without the guard, or a new admin route mounted elsewhere and not
// listed, fails here rather than shipping open.
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

// 006k AC1: an unauthenticated caller is refused too. Hiding the tab in the UI
// is not the guard.
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

// 022c: the solvency read reports outstanding sold liability, the sum over
// every wallet and pot, so an operator can compare it with the vendor account.
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

// 022d: the people table reports a frozen state, filters to the accounts with a
// negative balance, and an admin unfreezes one by granting the override.
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

// 006k AC2/AC5/AC7, in 011's terms: an admin drives the whole console. They
// read the policy, write it, put credits in somebody's wallet, and see the
// balance in the people table.
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
	if err := json.UnmarshalRead(w.Body, &got); err != nil {
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
	if err := json.UnmarshalRead(w.Body, &got); err != nil {
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
	if err := json.UnmarshalRead(w.Body, &got); err != nil {
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
	if err := json.UnmarshalRead(w.Body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Draft.Model != "claude-haiku-4-5" {
		t.Errorf("an unrelated edit switched drafting off: %+v", got.Draft)
	}

	// A negative policy is a bad request, not a 500.
	if w := do("PUT", "/admin/settings", `{"start_reserve":-1}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("negative reserve = %d, want 400", w.Code)
	}
	// So is a price list that cannot be priced from: a negative rate would
	// credit a project for using a model, and the card in force stays.
	if w := do("PUT", "/admin/settings", `{"start_reserve":8000000,"rates":{"models":[{"model":"claude-opus-5-5","input":-1,"output":25000000}]}}`, admin); w.Code != http.StatusBadRequest {
		t.Errorf("negative rate = %d, want 400", w.Code)
	} else if !strings.Contains(w.Body.String(), "bad_card") {
		t.Errorf("negative rate answered %s, want the bad_card code", w.Body.String())
	}
	// An admin adding a model to the card is what the console now does, and
	// the card comes back with it.
	if w := do("PUT", "/admin/settings", `{"start_reserve":8000000,"rates":{"models":[{"model":"claude-opus-5-5","input":5000000,"output":25000000,"cache_read":500000,"cache_write_5m":6250000,"cache_write_1h":10000000}]}}`, admin); w.Code != http.StatusOK {
		t.Fatalf("a changed card = %d %s", w.Code, w.Body.String())
	}
	w = do("GET", "/admin/settings", "", admin)
	if !strings.Contains(w.Body.String(), `"claude-opus-5-5"`) {
		t.Errorf("the changed card was not stored: %s", w.Body.String())
	}

	// Credits for someone who has never signed in show up in the people table
	// next to their grant. The wallet is keyed by email, which is what lets an
	// admin fund somebody before inviting them.
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
	if err := json.UnmarshalRead(w.Body, &people); err != nil {
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

// TestAnAdminChoosesTheLatereEnvironments checks the console's half of 051:
// the settings show the Latere environments in force and the ones the runtime
// knows, an edit of something else keeps the list, a list naming an
// environment the runtime does not know is refused, and a list without the
// base environment is refused too.
func TestAnAdminChoosesTheLatereEnvironments(t *testing.T) {
	mux, _, admin, _ := mountedAdmin(t)
	do := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/admin/settings", strings.NewReader(body))
		r = r.WithContext(identityCtx(r, admin))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	var view struct {
		Offered []string `json:"latere_environments"`
		Known   []string `json:"known_latere_environments"`
	}
	read := func() {
		t.Helper()
		w := do("GET", "")
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatalf("settings: %v (%s)", err, w.Body)
		}
	}
	read()
	if !slices.Equal(view.Offered, platform.DefaultLatereEnvironments) || !slices.Equal(view.Known, []string{"base", "gui", "science"}) {
		t.Fatalf("offered %v and known %v, want the defaults and the runtime's three", view.Offered, view.Known)
	}
	if w := do("PUT", `{"start_reserve":9000000}`); w.Code != http.StatusOK {
		t.Fatalf("an edit of the reserve = %d", w.Code)
	}
	if read(); !slices.Equal(view.Offered, platform.DefaultLatereEnvironments) {
		t.Fatalf("an edit of the reserve changed the environments to %v", view.Offered)
	}
	if w := do("PUT", `{"start_reserve":9000000,"latere_environments":["base","gui"]}`); w.Code != http.StatusOK {
		t.Fatalf("choosing base and gui = %d %s", w.Code, w.Body)
	}
	if read(); !slices.Equal(view.Offered, []string{"base", "gui"}) {
		t.Fatalf("offered %v, want base and gui", view.Offered)
	}
	for name, body := range map[string]string{
		"an unknown environment": `{"start_reserve":9000000,"latere_environments":["base","fortran"]}`,
		"no base environment":    `{"start_reserve":9000000,"latere_environments":["science"]}`,
	} {
		if w := do("PUT", body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "bad_environments") {
			t.Errorf("%s = %d %s, want 400 bad_environments", name, w.Code, w.Body)
		}
	}
}

// The console shows which rows of the card follow the price list, and a save
// keeps those rows at the list's prices: the next sync would put them back
// within the hour, and a console that read the card before the last sync
// would otherwise write back the prices the list replaced. A row the list
// does not price keeps what the admin gives it. A deployment with no list
// sends no price_list at all, so every row is the admin's.
func TestSettingsShowAndKeepTheRowsThePriceListSets(t *testing.T) {
	ctx := context.Background()
	acc := access.NewMemStore()
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if _, ok, _ := acc.Resolve(ctx, admin); !ok {
		t.Fatal("the admin should resolve granted")
	}
	plat := platform.NewMemStore(acc)
	applied := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	prices := platform.NewPriceList("https://openrouter.ai/api/v1/models", func() time.Time { return applied })
	listed := credit.Rates{
		Model: "claude-opus-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5"},
		Input: 4_000_000, Output: 20_000_000, CacheRead: 400_000, CacheWrite5m: 5_000_000, CacheWrite1h: 8_000_000,
	}
	fetch := func(context.Context) ([]credit.Rates, error) { return []credit.Rates{listed}, nil }
	if _, err := platform.ApplyPriceList(ctx, prices.Recording(plat), fetch); err != nil {
		t.Fatalf("ApplyPriceList: %v", err)
	}
	mux := http.NewServeMux()
	MountAdmin(mux, RequireTier(acc, access.TierAdmin), NewAdmin(acc, plat, nil, WithPriceList(prices)))
	do := func(method, body string) settingsView {
		t.Helper()
		r := httptest.NewRequest(method, "/admin/settings", strings.NewReader(body))
		r = r.WithContext(identityCtx(r, admin))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s /admin/settings = %d %s", method, w.Code, w.Body.String())
		}
		var got settingsView
		if err := json.UnmarshalRead(w.Body, &got); err != nil {
			t.Fatalf("decode settings: %v", err)
		}
		return got
	}

	got := do("GET", "")
	if got.PriceList == nil {
		t.Fatal("the settings name no price list, want the one the card follows")
	}
	if got.PriceList.URL != "https://openrouter.ai/api/v1/models" || !got.PriceList.AppliedAt.Equal(applied) ||
		!slices.Equal(got.PriceList.Models, []managed.ModelRef{listed.Ref}) {
		t.Errorf("the price list reads %+v, want its address, %v and %v", *got.PriceList, applied, listed.Ref)
	}

	// An edit to the listed row is replaced by the list's prices; an edit to
	// a row the list does not price stays.
	body := `{"start_reserve":5000000,"rates":{"models":[` +
		`{"model":"claude-opus-5-5","model_ref":{"provider":"anthropic","id":"claude-opus-5-5"},"input":9000000,"output":25000000,"cache_read":500000,"cache_write_5m":6250000,"cache_write_1h":10000000},` +
		`{"model":"claude-opus-4-1","input":15000000,"output":75000000,"cache_read":1500000,"cache_write_5m":18750000,"cache_write_1h":30000000}]}}`
	got = do("PUT", body)
	opus, ok := got.Rates.PricedRef(listed.Ref)
	if !ok || opus.Input != listed.Input || opus.Output != listed.Output || opus.CacheWrite1h != listed.CacheWrite1h {
		t.Errorf("after a save the listed row prices as (%+v, %v), want the list's prices", opus, ok)
	}
	own, ok := got.Rates.PricedRef(managed.ModelRef{Provider: "anthropic", ID: "claude-opus-4-1"})
	if !ok || own.Input != 15_000_000 || own.Output != 75_000_000 {
		t.Errorf("after a save the admin's row prices as (%+v, %v), want what they gave it", own, ok)
	}
	if len(got.Rates.Models) != 2 {
		t.Errorf("after a save the card has %d rows, want the 2 it was given", len(got.Rates.Models))
	}

	// With no list, the payload names none.
	bare, _, _, _ := mountedAdmin(t)
	r := httptest.NewRequest("GET", "/admin/settings", nil)
	r = r.WithContext(identityCtx(r, admin))
	w := httptest.NewRecorder()
	bare.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "price_list") {
		t.Errorf("a deployment with no price list sent one: %s", w.Body.String())
	}
}
