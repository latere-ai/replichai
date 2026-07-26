package webauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"latere.ai/x/pkg/oidclogin"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
)

type fakeOIDC struct {
	identity               oidclogin.Identity
	exchangeErr, verifyErr error
}

func (fakeOIDC) AuthCodeURL(state, nonce, verifier string) string {
	return "https://issuer.example/auth?state=" + state + "&nonce=" + nonce
}
func (f fakeOIDC) Exchange(context.Context, string, string) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "at"}, f.exchangeErr
}
func (f fakeOIDC) VerifyIDToken(context.Context, *oauth2.Token, string) (oidclogin.Identity, error) {
	return f.identity, f.verifyErr
}

func newTestLogin(t *testing.T, client OIDCClient, acc access.Access) *Login {
	t.Helper()
	codec, err := NewCodec(testKey)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}
	return NewLogin(client, codec, acc, "https://replichai.latere.ai", false /* http test */)
}

// AC1: only sub and the verified email survive the mapping; roles, name, org,
// and everything else in the token are dropped.
func TestSessionFromIdentityDropsClaims(t *testing.T) {
	id := oidclogin.Identity{
		Subject: "user-1",
		Email:   "u@example.com",
		Name:    "Real Name",
		Roles:   []string{"admin", "superuser"},
		Raw: map[string]any{
			"email_verified":    true,
			"roles":             []any{"admin"},
			"org_id":            "org-9",
			"replichai:operate": true,
		},
	}
	s := sessionFromIdentity(id)
	if s.Sub != "user-1" || s.Email != "u@example.com" || !s.EmailVerified {
		t.Fatalf("session = %+v, want only sub/email/verified", s)
	}
	// The session type has no field any of those claims could land in — checked
	// structurally in TestSessionCarriesNoTier and by the shape of Session here.
}

func TestEmailVerifiedParsing(t *testing.T) {
	cases := map[string]struct {
		raw  map[string]any
		want bool
	}{
		"bool true":      {map[string]any{"email_verified": true}, true},
		"bool false":     {map[string]any{"email_verified": false}, false},
		"string true":    {map[string]any{"email_verified": "true"}, true},
		"string false":   {map[string]any{"email_verified": "false"}, false},
		"missing":        {map[string]any{}, false},
		"unexpected int": {map[string]any{"email_verified": 1}, false},
	}
	for name, tc := range cases {
		if got := emailVerified(tc.raw); got != tc.want {
			t.Errorf("%s: emailVerified = %v, want %v", name, got, tc.want)
		}
	}
}

func TestHandleLoginSetsFlowCookieAndRedirects(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())
	w := httptest.NewRecorder()
	l.HandleLogin(w, httptest.NewRequest("GET", "/login?next=/dash", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if !strings.Contains(w.Header().Get("Location"), "issuer.example/auth?state=") {
		t.Errorf("redirect = %q, want the issuer auth URL", w.Header().Get("Location"))
	}
	if cookie := findCookie(w, cookieName(flowCookie, false)); cookie == "" {
		t.Error("flow cookie was not set")
	}
}

func TestCallbackHappyPathSetsSession(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{identity: oidclogin.Identity{
		Subject: "u1", Email: "u@example.com", Raw: map[string]any{"email_verified": true},
	}}, access.NewMemStore())

	// A valid flow cookie with a known state.
	flow, _ := l.codec.Seal(flowState{State: "st8", Nonce: "n", Verifier: "v", Next: "/dash"})
	r := httptest.NewRequest("GET", "/callback?state=st8&code=abc", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)

	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://replichai.latere.ai/dash" {
		t.Fatalf("callback = %d -> %q, want 302 to /dash", w.Code, w.Header().Get("Location"))
	}
	sealed := findCookie(w, cookieName(sessionCookie, false))
	if sealed == "" {
		t.Fatal("session cookie was not set")
	}
	var s Session
	if err := l.codec.Open(sealed, &s); err != nil || s.Sub != "u1" || !s.EmailVerified {
		t.Errorf("session cookie = %+v, err %v", s, err)
	}
}

func TestCallbackRejections(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())

	t.Run("no flow cookie", func(t *testing.T) {
		w := httptest.NewRecorder()
		l.HandleCallback(w, httptest.NewRequest("GET", "/callback?state=x&code=c", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		flow, _ := l.codec.Seal(flowState{State: "real", Verifier: "v"})
		r := httptest.NewRequest("GET", "/callback?state=forged&code=c", nil)
		r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
		w := httptest.NewRecorder()
		l.HandleCallback(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("a forged state should be 400, got %d", w.Code)
		}
		// The flow cookie is cleared even on rejection (deleted before exchange).
		if c := clearedCookie(w, cookieName(flowCookie, false)); !c {
			t.Error("flow cookie should be cleared on the callback")
		}
	})
}

func TestMiddlewareSetsIdentityFromSession(t *testing.T) {
	l := newTestLogin(t, fakeOIDC{}, access.NewMemStore())
	sealed, _ := l.codec.Seal(Session{Sub: "u1", Email: "u@example.com", EmailVerified: true})

	var got httpapi.Identity
	var ok bool
	h := l.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = httpapi.IdentityFrom(r)
	}))
	r := httptest.NewRequest("GET", "/jobs/x", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(sessionCookie, false), Value: sealed})
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !ok || got.Sub != "u1" || got.Email != "u@example.com" || !got.EmailVerified {
		t.Fatalf("identity = %+v ok=%v", got, ok)
	}

	// A request with no session passes through unauthenticated.
	ok = false
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/jobs/x", nil))
	if ok {
		t.Error("no session should leave the request unauthenticated")
	}
}

// AC: /me is open and reports the resolved tier, so the frontend can render the
// not-invited screen. Ungranted -> tier none, still 200.
func TestMeReportsTier(t *testing.T) {
	store := access.NewMemStore()
	_ = store.SeedAdmin(context.Background(), "admin@example.com")
	me := Me(store)

	call := func(id *httpapi.Identity) map[string]any {
		r := httptest.NewRequest("GET", "/me", nil)
		if id != nil {
			r = r.WithContext(httpapi.WithIdentity(r.Context(), *id))
		}
		w := httptest.NewRecorder()
		me(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("/me status = %d, want 200", w.Code)
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	if out := call(nil); out["authenticated"] != false {
		t.Errorf("unauthenticated /me = %v", out)
	}
	if out := call(&httpapi.Identity{Sub: "nobody", Email: "nobody@example.com", EmailVerified: true}); out["tier"] != "none" {
		t.Errorf("ungranted /me tier = %v, want none", out["tier"])
	}
	if out := call(&httpapi.Identity{Sub: "admin-sub", Email: "admin@example.com", EmailVerified: true}); out["tier"] != "admin" {
		t.Errorf("admin /me tier = %v, want admin", out["tier"])
	}
}

func findCookie(w *httptest.ResponseRecorder, name string) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

func clearedCookie(w *httptest.ResponseRecorder, name string) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge < 0 {
			return true
		}
	}
	return false
}
