// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// Signing out must end the session at the identity provider too.
//
// Clearing only this app's cookie does not sign the user out: the provider's
// session survives, so the next /login is answered with a fresh code for the
// same account with no prompt. The app appears to sign out and signs straight
// back in, and the user cannot reach another profile or sign out fully on a
// shared machine.
func TestHandleLogoutEndsTheProviderSession(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore()).
		EndSessionAt("https://auth.latere.ai/logout")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/logout", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(sessionCookie, false), Value: "whatever"})
	l.HandleLogout(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location is not a URL: %v", err)
	}
	if loc.Host != "auth.latere.ai" || loc.Path != "/logout" {
		t.Errorf("Location = %q, want the issuer's end-session endpoint", loc)
	}
	// The redirect has to return here, or signing out leaves the user at the
	// identity provider.
	if got := loc.Query().Get("post_logout_redirect_uri"); got != "https://replichai.latere.ai/" {
		t.Errorf("post_logout_redirect_uri = %q, want the app root", got)
	}
	// The cookie is cleared by being reissued empty and already expired.
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName(sessionCookie, false) && c.Value == "" && c.MaxAge <= 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the session cookie was not cleared; signing out would not sign anybody out")
	}
}

// An issuer that advertises no end-session endpoint still gets a working local
// sign-out, and the cookie is cleared in either case before any redirect.
func TestHandleLogoutWithoutAnIssuerEndpoint(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())
	w := httptest.NewRecorder()
	l.HandleLogout(w, httptest.NewRequest("GET", "/logout", nil))

	if got := w.Header().Get("Location"); got != "https://replichai.latere.ai/" {
		t.Errorf("Location = %q, want the app root", got)
	}
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName(sessionCookie, false) && c.Value == "" && c.MaxAge <= 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the local session survived a sign-out")
	}
}

// A malformed endpoint must not leave the user on a broken URL mid-sign-out.
func TestHandleLogoutFallsBackOnAnUnparseableEndpoint(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore()).EndSessionAt("://not a url")
	w := httptest.NewRecorder()
	l.HandleLogout(w, httptest.NewRequest("GET", "/logout", nil))
	if got := w.Header().Get("Location"); got != "https://replichai.latere.ai/" {
		t.Errorf("Location = %q, want the app root", got)
	}
}

// An endpoint that already carries query parameters keeps them.
func TestHandleLogoutPreservesExistingQuery(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore()).
		EndSessionAt("https://auth.latere.ai/logout?tenant=latere")
	w := httptest.NewRecorder()
	l.HandleLogout(w, httptest.NewRequest("GET", "/logout", nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("tenant") != "latere" {
		t.Errorf("Location = %q, dropped the endpoint's own query", loc)
	}
	if loc.Query().Get("post_logout_redirect_uri") == "" {
		t.Errorf("Location = %q, missing the return URL", loc)
	}
}

// ?next= decides where the login returns to, so it is attacker-reachable. Only
// a same-site relative path is kept: anything else would make the sign-in an
// open redirect usable for phishing.
func TestSafeNextRefusesAnOpenRedirect(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"/projects/p-1", "/projects/p-1"},
		{"/", "/"},
		{"", "/"},
		{"//evil.example.com", "/"},
		{"https://evil.example.com", "/"},
		{"http://evil.example.com/x", "/"},
		{"evil.example.com", "/"},
		{"\\\\evil.example.com", "/"},
	} {
		if got := safeNext(tt.in); got != tt.want {
			t.Errorf("safeNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The login redirect carries the state, and the flow cookie carries the same
// state sealed. The pair is what makes the callback verifiable.
func TestHandleLoginStartsAVerifiableFlow(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())
	w := httptest.NewRecorder()
	l.HandleLogin(w, httptest.NewRequest("GET", "/login?next=/projects/p-1", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect to the issuer", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Location"), "https://issuer.example/auth") {
		t.Errorf("Location = %q", w.Header().Get("Location"))
	}
	sealed := findCookie(w, cookieName(flowCookie, false))
	if sealed == "" {
		t.Fatal("no flow cookie; the callback would have nothing to verify against")
	}
	var fs flowState
	if err := l.codec.Open(sealed, &fs); err != nil {
		t.Fatalf("open flow cookie: %v", err)
	}
	if fs.State == "" || fs.Nonce == "" || fs.Verifier == "" {
		t.Errorf("flow state = %+v, want state, nonce and verifier", fs)
	}
	if fs.Next != "/projects/p-1" {
		t.Errorf("next = %q, want the requested path carried through", fs.Next)
	}
}

// The secure variant of a cookie carries the __Host- prefix, which binds it to
// one host with no path or domain scope. Losing that on a production
// deployment would let a sibling subdomain set the session cookie.
func TestCookieNameIsHostScopedWhenSecure(t *testing.T) {
	if got := cookieName(sessionCookie, true); got != "__Host-replichai-session" {
		t.Errorf("secure cookie name = %q, want the __Host- prefix kept", got)
	}
	// Over plain http the prefix has to go: a browser refuses a __Host- cookie
	// without Secure, so keeping it would break the dev sign-in silently.
	if got := cookieName(sessionCookie, false); got != "replichai-session" {
		t.Errorf("insecure cookie name = %q, want the prefix dropped", got)
	}
	if got := cookieName(flowCookie, false); got != "replichai-flow" {
		t.Errorf("insecure flow cookie name = %q", got)
	}
}

// Discovery is how the login learns where to read an email the ID token does
// not carry. Each failure is distinct, and none may return a usable endpoint.
func TestDiscover(t *testing.T) {
	t.Run("reads the endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/openid-configuration" {
				t.Errorf("discovery path = %q", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"userinfo_endpoint":    "https://issuer.example/userinfo",
				"end_session_endpoint": "https://issuer.example/logout",
			})
		}))
		defer srv.Close()

		got, err := Discover(context.Background(), srv.URL+"/")
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if got.UserInfo != "https://issuer.example/userinfo" {
			t.Errorf("UserInfo = %q", got.UserInfo)
		}
		// Both endpoints come from one document and one request.
		if got.EndSession != "https://issuer.example/logout" {
			t.Errorf("EndSession = %q; sign-out would stay local", got.EndSession)
		}
	})

	t.Run("a non-200 is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if _, err := Discover(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for a 404 discovery document")
		}
	})

	t.Run("malformed JSON is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()
		if _, err := Discover(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for a malformed discovery document")
		}
	})

	t.Run("a document with no endpoint is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{}"))
		}))
		defer srv.Close()
		if _, err := Discover(context.Background(), srv.URL); err == nil {
			t.Error("expected an error when no userinfo_endpoint is advertised")
		}
	})

	t.Run("an unreachable issuer is an error", func(t *testing.T) {
		if _, err := Discover(context.Background(), "http://127.0.0.1:1"); err == nil {
			t.Error("expected an error for an unreachable issuer")
		}
		if _, err := Discover(context.Background(), "http://\x7f-bad-host"); err == nil {
			t.Error("expected an error for an unbuildable request")
		}
	})
}

// seedAdmin bootstraps a platform admin and returns their identity.
func seedAdmin(t *testing.T, store *access.MemStore) httpapi.Identity {
	t.Helper()
	id := httpapi.Identity{Sub: "admin-sub", Email: "admin@example.com"}
	if err := store.SeedGrant(context.Background(), id.Email, access.TierAdmin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, ok, _ := store.Resolve(context.Background(), access.Principal{Sub: id.Sub, Email: id.Email}); !ok {
		t.Fatal("precondition: the admin grant must bind")
	}
	return id
}

// failingPlatform is a policy store that is down.
type failingPlatform struct {
	platform.Store
	err error
}

// Settings mirrors the Store contract on failure: the conservative default is
// returned together with the error, never a zero value. A fake that returned
// zero here would make this test pass against a handler that shows a reserve
// of zero, which is the one answer that admits a session on an empty pot.
func (f failingPlatform) Settings(ctx context.Context) (platform.Settings, error) {
	if f.err != nil {
		return platform.Settings{StartReserve: platform.DefaultStartReserve}, f.err
	}
	return f.Store.Settings(ctx)
}

func (f failingPlatform) SetSettings(ctx context.Context, by access.Principal, s platform.Settings) error {
	if f.err != nil {
		return f.err
	}
	return f.Store.SetSettings(ctx, by, s)
}

// A policy write that did not happen must not be reported as done.
func TestAdminReportsPlatformWriteFailures(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	plat := failingPlatform{Store: platform.NewMemStore(acc), err: errors.New("policy store is down")}
	a := NewAdmin(acc, plat, failingLedger{err: errors.New("ledger is down")})

	for _, tt := range []struct {
		name   string
		call   func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
	}{
		{"write settings", a.SetSettings, "PUT", "/admin/platform", `{"start_reserve":5000000}`},
		{"top up a wallet", a.Topup, "POST", "/admin/access/x@y.com/credits", `{"amount":3000000}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.call(w, as(adminID, tt.method, tt.path, tt.body))
			if w.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", w.Code)
			}
		})
	}
}

// A policy read is the opposite case by design: the store is fail-closed, so a
// failed read still yields the conservative default. The console shows the
// number in force rather than an error, and the number it shows is never
// "unlimited".
func TestAdminShowsTheEnforcedDefaultWhenTheReadFails(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	plat := failingPlatform{Store: platform.NewMemStore(acc), err: errors.New("policy store is down")}
	a := NewAdmin(acc, plat, nil)

	w := httptest.NewRecorder()
	a.Settings(w, as(adminID, "GET", "/admin/platform", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the fail-closed default", w.Code)
	}
	var got platform.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StartReserve != platform.DefaultStartReserve {
		t.Errorf("reserve = %d, want the conservative default %d", got.StartReserve, platform.DefaultStartReserve)
	}
}

// With no platform store or ledger wired, those routes answer 503 rather than
// reporting that the deployment has no policy and no money.
func TestAdminWithoutAPlatformStore(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	a := NewAdmin(acc, nil, nil)

	for _, call := range []func(http.ResponseWriter, *http.Request){a.Settings, a.SetSettings, a.Topup} {
		w := httptest.NewRecorder()
		call(w, as(adminID, "PUT", "/admin/platform", "{}"))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503 when no store is wired", w.Code)
		}
	}
}

// A malformed admin body is a 400, never a partial write.
func TestAdminRefusesMalformedBodies(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	a := NewAdmin(acc, platform.NewMemStore(acc), credit.NewMemStore(acc, nil))

	for _, call := range []func(http.ResponseWriter, *http.Request){a.SetSettings, a.Topup, a.Grant} {
		w := httptest.NewRecorder()
		call(w, as(adminID, "PUT", "/admin/x", "not json"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for a malformed body", w.Code)
		}
	}
}

// failingLedger is a ledger that is down, so a top-up that did not happen is
// never reported as done.
type failingLedger struct {
	credit.Store
	err error
}

func (f failingLedger) Topup(context.Context, access.Principal, string, int64) error { return f.err }
func (f failingLedger) Balance(context.Context, credit.Holder) (int64, error)        { return 0, f.err }
