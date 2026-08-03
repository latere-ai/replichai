package webauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/platform"
)

// Signing out clears the session and returns to the app root — the mirror of
// the login, and the only way a shared machine stops being signed in.
func TestHandleLogoutClearsTheSession(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/logout", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(sessionCookie, false), Value: "whatever"})
	l.HandleLogout(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", w.Code)
	}
	if got := w.Header().Get("Location"); got != "https://replichai.latere.ai/" {
		t.Errorf("Location = %q, want the app root", got)
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

// ?next= decides where the login returns to, so it is attacker-reachable. Only
// a same-site relative path survives: anything else would make the sign-in an
// open redirect somebody could hang a phishing page off.
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
// state sealed — the pair is what makes the callback verifiable.
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
func TestDiscoverUserInfo(t *testing.T) {
	t.Run("reads the endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/openid-configuration" {
				t.Errorf("discovery path = %q", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"userinfo_endpoint": "https://issuer.example/userinfo"})
		}))
		defer srv.Close()

		got, err := DiscoverUserInfo(context.Background(), srv.URL+"/")
		if err != nil || got != "https://issuer.example/userinfo" {
			t.Fatalf("DiscoverUserInfo = %q, %v", got, err)
		}
	})

	t.Run("a non-200 is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if _, err := DiscoverUserInfo(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for a 404 discovery document")
		}
	})

	t.Run("malformed JSON is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()
		if _, err := DiscoverUserInfo(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for a malformed discovery document")
		}
	})

	t.Run("a document with no endpoint is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{}"))
		}))
		defer srv.Close()
		if _, err := DiscoverUserInfo(context.Background(), srv.URL); err == nil {
			t.Error("expected an error when no userinfo_endpoint is advertised")
		}
	})

	t.Run("an unreachable issuer is an error", func(t *testing.T) {
		if _, err := DiscoverUserInfo(context.Background(), "http://127.0.0.1:1"); err == nil {
			t.Error("expected an error for an unreachable issuer")
		}
		if _, err := DiscoverUserInfo(context.Background(), "http://\x7f-bad-host"); err == nil {
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
// returned *alongside* the error, never a zero value and never unlimited. A
// fake that returned zero here would make this test pass against a handler that
// shows nobody any quota at all.
func (f failingPlatform) Settings(ctx context.Context) (platform.Settings, error) {
	if f.err != nil {
		return platform.Settings{DefaultSessionQuota: platform.DefaultSessionQuota}, f.err
	}
	return f.Store.Settings(ctx)
}

func (f failingPlatform) SetSettings(ctx context.Context, by access.Principal, s platform.Settings) error {
	if f.err != nil {
		return f.err
	}
	return f.Store.SetSettings(ctx, by, s)
}

func (f failingPlatform) SetTopup(ctx context.Context, by access.Principal, email string, extra int) error {
	if f.err != nil {
		return f.err
	}
	return f.Store.SetTopup(ctx, by, email, extra)
}

// A policy *write* that did not happen must not be reported as done.
func TestAdminReportsPlatformWriteFailures(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	plat := failingPlatform{Store: platform.NewMemStore(acc), err: errors.New("policy store is down")}
	a := NewAdmin(acc, plat, nil)

	for _, tt := range []struct {
		name   string
		call   func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
	}{
		{"write settings", a.SetSettings, "PUT", "/admin/platform", `{"default_session_quota":5}`},
		{"set a top-up", a.SetTopup, "PUT", "/admin/topups/x@y.com", `{"extra":3}`},
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

// A policy *read* is the opposite case, and deliberately so: the store is
// fail-closed, so a failed read still yields the conservative default. The
// console shows the number that is actually in force rather than an error, and
// the number it shows is never "unlimited".
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
	if got.DefaultSessionQuota != platform.DefaultSessionQuota {
		t.Errorf("quota = %d, want the conservative default %d", got.DefaultSessionQuota, platform.DefaultSessionQuota)
	}
}

// With no platform store wired at all, the policy routes say so rather than
// pretending the deployment has no quota.
func TestAdminWithoutAPlatformStore(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	a := NewAdmin(acc, nil, nil)

	for _, call := range []func(http.ResponseWriter, *http.Request){a.Settings, a.SetSettings, a.SetTopup} {
		w := httptest.NewRecorder()
		call(w, as(adminID, "PUT", "/admin/platform", "{}"))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503 when no platform store is wired", w.Code)
		}
	}
}

// A malformed admin body is a 400, never a partial write.
func TestAdminRefusesMalformedBodies(t *testing.T) {
	acc := access.NewMemStore()
	adminID := seedAdmin(t, acc)
	a := NewAdmin(acc, platform.NewMemStore(acc), nil)

	for _, call := range []func(http.ResponseWriter, *http.Request){a.SetSettings, a.SetTopup, a.Grant} {
		w := httptest.NewRecorder()
		call(w, as(adminID, "PUT", "/admin/x", "not json"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for a malformed body", w.Code)
		}
	}
}
