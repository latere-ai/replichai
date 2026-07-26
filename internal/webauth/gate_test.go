package webauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
)

var ctx = context.Background()

// requestAs builds a request carrying an authenticated identity, or none.
func requestAs(id *httpapi.Identity) *http.Request {
	r := httptest.NewRequest("GET", "/jobs/x", nil)
	if id != nil {
		r = r.WithContext(httpapi.WithIdentity(r.Context(), *id))
	}
	return r
}

// AC2/AC3/AC4 at the gate: fail-closed for the unauthenticated and the
// ungranted, admits a granted principal, and re-resolves per request so a revoke
// locks out immediately.
func TestGateFailsClosedAndReResolves(t *testing.T) {
	store := access.NewMemStore()
	gate := Gate(store)

	// No identity -> not_authenticated.
	if ok, reason := gate.Allow(requestAs(nil)); ok || reason != "not_authenticated" {
		t.Errorf("unauthenticated: ok=%v reason=%q", ok, reason)
	}

	// Authenticated but ungranted -> not_invited (fail-closed on empty table).
	seed := httpapi.Identity{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}
	if ok, reason := gate.Allow(requestAs(&seed)); ok || reason != "not_invited" {
		t.Errorf("ungranted: ok=%v reason=%q", ok, reason)
	}

	// Grant the email; first request binds and admits.
	_ = store.SeedAdmin(ctx, "admin@example.com")
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com", EmailVerified: true}
	if err := store.Grant(ctx, admin, "seed@example.com", access.TierSeed); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if ok, _ := gate.Allow(requestAs(&seed)); !ok {
		t.Error("a granted principal should be admitted")
	}

	// Revoke; the very next request is locked out — no session caches the tier.
	if err := store.Revoke(ctx, admin, "seed@example.com"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if ok, reason := gate.Allow(requestAs(&seed)); ok || reason != "not_invited" {
		t.Errorf("after revoke: ok=%v reason=%q, want locked out", ok, reason)
	}
}

// AC4: the tier guard refuses a granted-but-insufficient principal server-side,
// and admits one at or above the required tier.
func TestRequireTier(t *testing.T) {
	store := access.NewMemStore()
	_ = store.SeedAdmin(ctx, "admin@example.com")
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com", EmailVerified: true}
	_ = store.Grant(ctx, admin, "seed@example.com", access.TierSeed)
	_ = store.Grant(ctx, admin, "trusted@example.com", access.TierTrusted)

	guard := RequireTier(store, access.TierTrusted)
	handler := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	call := func(id httpapi.Identity) int {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, requestAs(&id))
		return w.Code
	}

	// A seed user is refused not_trusted even reaching the route directly.
	if code := call(httpapi.Identity{Sub: "seed-sub", Email: "seed@example.com", EmailVerified: true}); code != http.StatusForbidden {
		t.Errorf("seed user at a trusted route: %d, want 403", code)
	}
	// A trusted user passes.
	if code := call(httpapi.Identity{Sub: "trusted-sub", Email: "trusted@example.com", EmailVerified: true}); code != http.StatusOK {
		t.Errorf("trusted user at a trusted route: %d, want 200", code)
	}
	// An ungranted principal is refused before the tier check.
	if code := call(httpapi.Identity{Sub: "nobody", Email: "nobody@example.com", EmailVerified: true}); code != http.StatusForbidden {
		t.Errorf("ungranted at a trusted route: %d, want 403", code)
	}
	// No identity at all is refused.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, requestAs(nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("unauthenticated at a trusted route: %d, want 403", w.Code)
	}
}

// AC2 (route coverage): every operator route the router serves is behind the
// gate. There is no API to enumerate a ServeMux's patterns, so this asserts the
// known operator routes appear in GatedRoutes — a route added without the gated
// helper would be absent here and fail the test.
func TestOperatorRoutesAreGated(t *testing.T) {
	rt := httpapi.NewRouter(httpapi.Deps{Jobs: job.NewMemStore(), Reproductions: nil, Gate: Gate(access.NewMemStore())})
	gated := map[string]bool{}
	for _, route := range rt.GatedRoutes() {
		gated[route.Method+" "+route.Pattern] = true
	}
	for _, must := range []string{
		"POST /papers",
		"GET /jobs/{id}",
		"GET /jobs/{id}/events",
		"GET /reproductions",
		"GET /reproductions/{id}",
		"GET /reproductions/{id}/transcript",
	} {
		if !gated[must] {
			t.Errorf("operator route %q is not behind the gate", must)
		}
	}
	// The health check is deliberately open, not gated.
	for _, route := range rt.GatedRoutes() {
		if strings.Contains(route.Pattern, "healthz") {
			t.Error("healthz must not be gated")
		}
	}
}
