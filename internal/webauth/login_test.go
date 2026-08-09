package webauth

import (
	"context"
	"encoding/json"
	"errors"
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
	return NewLogin(client, codec, acc, "https://replichai.latere.ai", false /* http test */, nil)
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
	if s.Sub != "user-1" || s.Email != "u@example.com" {
		t.Fatalf("session = %+v, want only sub/email/verified", s)
	}
	// The session type has no field any of those claims could land in — checked
	// structurally in TestSessionCarriesNoTier and by the shape of Session here.
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
	if err := l.codec.Open(sealed, &s); err != nil || s.Sub != "u1" {
		t.Errorf("session cookie = %+v, err %v", s, err)
	}
}

// When the ID token carries no email (latere auth), the callback fetches it
// from /userinfo so the whitelist can bind — the fix for a signed-in principal
// stuck at tier none with an empty email.
func TestCallbackFetchesEmailFromUserInfo(t *testing.T) {
	codec, _ := NewCodec(testKey)
	// The ID token yields a sub but no email.
	client := fakeOIDC{identity: oidclogin.Identity{Subject: "uuid-1", Raw: map[string]any{}}}
	userInfo := func(_ context.Context, _ string) (Profile, error) {
		return Profile{Email: "hi@changkun.de"}, nil
	}
	l := NewLogin(client, codec, access.NewMemStore(), "https://replichai.latere.ai", false, userInfo)

	flow, _ := l.codec.Seal(flowState{State: "s", Verifier: "v", Next: "/"})
	r := httptest.NewRequest("GET", "/callback?state=s&code=c", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)

	sealed := findCookie(w, cookieName(sessionCookie, false))
	var s Session
	if err := l.codec.Open(sealed, &s); err != nil {
		t.Fatalf("open session: %v", err)
	}
	if s.Sub != "uuid-1" || s.Email != "hi@changkun.de" {
		t.Errorf("session = %+v, want the userinfo email bound to the sub", s)
	}
}

// Signing in is what turns a project invitation into a membership. Binding used
// to be a side effect of the authorisation checks, every one of which
// short-circuits for an owner or a public project — so an invitee stayed
// unbound, absent from "shared with me" and shown as "pending" forever.
func TestCallbackBindsProjectMemberships(t *testing.T) {
	codec, _ := NewCodec(testKey)
	members := access.NewMemMembership()
	owner := access.Principal{Sub: "owner-sub", Email: "owner@example.com"}
	if err := members.SeedLead(ctx, "proj-1", owner); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := members.Add(ctx, owner, "proj-1", "invitee@example.com", access.RoleCollaborator); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if ids, _ := members.Projects(ctx, "invitee-sub"); len(ids) != 0 {
		t.Fatalf("precondition: the invitee holds nothing before signing in, got %v", ids)
	}

	client := fakeOIDC{identity: oidclogin.Identity{Subject: "invitee-sub", Email: "invitee@example.com"}}
	l := NewLogin(client, codec, access.NewMemStore(), "https://replichai.latere.ai", false, nil).
		BindMemberships(members)
	flow, _ := l.codec.Seal(flowState{State: "s", Verifier: "v", Next: "/"})
	r := httptest.NewRequest("GET", "/callback?state=s&code=c", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want a redirect", w.Code)
	}
	ids, err := members.Projects(ctx, "invitee-sub")
	if err != nil || len(ids) != 1 || ids[0] != "proj-1" {
		t.Fatalf("after sign-in the invitee should hold proj-1, got %v err=%v", ids, err)
	}
	list, _ := members.List(ctx, "proj-1")
	for _, m := range list {
		if !m.Bound {
			t.Errorf("%s should no longer read as pending", m.Email)
		}
	}
}

// 006q: signing in admits you. Before this, a stranger who proved their
// identity was refused by the gate until an admin added them by hand, which
// made every evaluation of the product a support ticket — and made a project
// invitation a promise the platform could not keep.
func TestCallbackAdmitsAnyoneWhoSignsIn(t *testing.T) {
	codec, _ := NewCodec(testKey)
	acc := access.NewMemStore()
	client := fakeOIDC{identity: oidclogin.Identity{Subject: "newcomer", Email: "new@example.com"}}
	l := NewLogin(client, codec, acc, "https://replichai.latere.ai", false, nil)

	p := access.Principal{Sub: "newcomer", Email: "new@example.com"}
	if _, ok, _ := acc.Resolve(ctx, p); ok {
		t.Fatal("precondition: nobody has granted this person anything")
	}

	callback(t, l)

	tier, ok, err := acc.Resolve(ctx, p)
	if err != nil || !ok || tier != access.TierUser {
		t.Fatalf("after signing in: %s/%v/%v, want user", tier, ok, err)
	}
	// The gate is untouched and still resolves per request — the sign-in only
	// created the row it now finds.
	if allowed, reason := Gate(acc).Allow(requestAs(&httpapi.Identity{Sub: p.Sub, Email: p.Email})); !allowed {
		t.Errorf("the gate refused a signed-in principal: %q", reason)
	}
}

// A seeded admin must not be demoted by their own first login.
func TestCallbackDoesNotDemoteASeededAdmin(t *testing.T) {
	codec, _ := NewCodec(testKey)
	acc := access.NewMemStore()
	if err := acc.SeedGrant(ctx, "boss@example.com", access.TierAdmin); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := fakeOIDC{identity: oidclogin.Identity{Subject: "boss", Email: "boss@example.com"}}
	l := NewLogin(client, codec, acc, "https://replichai.latere.ai", false, nil)

	callback(t, l)

	tier, ok, _ := acc.Resolve(ctx, access.Principal{Sub: "boss", Email: "boss@example.com"})
	if !ok || tier != access.TierAdmin {
		t.Fatalf("admin after signing in = %s/%v, want admin", tier, ok)
	}
}

// A ban has to survive a re-login, or it is a logout.
func TestCallbackDoesNotReadmitARevokedPrincipal(t *testing.T) {
	codec, _ := NewCodec(testKey)
	acc := access.NewMemStore()
	admin := access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	if err := acc.SeedGrant(ctx, admin.Email, access.TierAdmin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, _, err := acc.Resolve(ctx, admin); err != nil {
		t.Fatalf("bind admin: %v", err)
	}
	if err := acc.Grant(ctx, admin, "banned@example.com", access.TierUser); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := acc.Revoke(ctx, admin, "banned@example.com"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	client := fakeOIDC{identity: oidclogin.Identity{Subject: "banned-sub", Email: "banned@example.com"}}
	l := NewLogin(client, codec, acc, "https://replichai.latere.ai", false, nil)
	callback(t, l)

	p := access.Principal{Sub: "banned-sub", Email: "banned@example.com"}
	if tier, ok, _ := acc.Resolve(ctx, p); ok || tier != access.TierNone {
		t.Fatalf("a revoked address signed itself back in: %s/%v", tier, ok)
	}
	if allowed, _ := Gate(acc).Allow(requestAs(&httpapi.Identity{Sub: p.Sub, Email: p.Email})); allowed {
		t.Error("the gate admitted a revoked principal")
	}
}

// An access store that is down costs the admission, never the sign-in: the
// session is still established and the next request retries the grant.
func TestCallbackSurvivesAnAdmissionFailure(t *testing.T) {
	codec, _ := NewCodec(testKey)
	client := fakeOIDC{identity: oidclogin.Identity{Subject: "u1", Email: "u@example.com"}}
	l := NewLogin(client, codec, failingAccess{Access: access.NewMemStore()}, "https://replichai.latere.ai", false, nil)

	w := callback(t, l)
	if findCookie(w, cookieName(sessionCookie, false)) == "" {
		t.Error("the session cookie must still be set when the grant store is down")
	}
}

// failingAccess fails only the admission.
type failingAccess struct{ access.Access }

func (failingAccess) SelfGrant(context.Context, access.Principal) (access.Tier, bool, error) {
	return access.TierNone, false, errors.New("grant store is down")
}

// callback drives one complete OIDC callback and asserts it redirected.
func callback(t *testing.T, l *Login) *httptest.ResponseRecorder {
	t.Helper()
	flow, _ := l.codec.Seal(flowState{State: "s", Verifier: "v", Next: "/"})
	r := httptest.NewRequest("GET", "/callback?state=s&code=c", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want a redirect", w.Code)
	}
	return w
}

// A membership store that is down costs a collaborator one sign-in, never the
// sign-in itself.
func TestCallbackSurvivesABindFailure(t *testing.T) {
	codec, _ := NewCodec(testKey)
	client := fakeOIDC{identity: oidclogin.Identity{Subject: "u1", Email: "u@example.com"}}
	l := NewLogin(client, codec, access.NewMemStore(), "https://replichai.latere.ai", false, nil).
		BindMemberships(failingMembership{})
	flow, _ := l.codec.Seal(flowState{State: "s", Verifier: "v", Next: "/"})
	r := httptest.NewRequest("GET", "/callback?state=s&code=c", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(flowCookie, false), Value: flow})
	w := httptest.NewRecorder()
	l.HandleCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want the sign-in to complete anyway", w.Code)
	}
	if findCookie(w, cookieName(sessionCookie, false)) == "" {
		t.Error("the session cookie must still be set")
	}
}

// failingMembership fails only the bind; nothing else in the callback path
// touches it.
type failingMembership struct{ access.Membership }

func (failingMembership) BindAll(context.Context, access.Principal) (int, error) {
	return 0, errors.New("membership store is down")
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
	sealed, _ := l.codec.Seal(Session{Sub: "u1", Email: "u@example.com"})

	var got httpapi.Identity
	var ok bool
	h := l.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = httpapi.IdentityFrom(r)
	}))
	r := httptest.NewRequest("GET", "/jobs/x", nil)
	r.AddCookie(&http.Cookie{Name: cookieName(sessionCookie, false), Value: sealed})
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !ok || got.Sub != "u1" || got.Email != "u@example.com" {
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
	me := Me(store, nil, nil)

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
	if out := call(&httpapi.Identity{Sub: "nobody", Email: "nobody@example.com"}); out["tier"] != "none" {
		t.Errorf("ungranted /me tier = %v, want none", out["tier"])
	}
	if out := call(&httpapi.Identity{Sub: "admin-sub", Email: "admin@example.com"}); out["tier"] != "admin" {
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
