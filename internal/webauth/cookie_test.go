package webauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCookieHardening pins the invariant the gosec G124 nolint in session.go
// rests on: HttpOnly and SameSite never depend on the deployment, and only
// Secure and the __Host- prefix follow the plain-HTTP dev escape.
func TestCookieHardening(t *testing.T) {
	for _, secure := range []bool{true, false} {
		w := httptest.NewRecorder()
		setCookie(w, sessionCookie, "sealed", secure, time.Hour)
		got := onlyCookie(t, w)

		if !got.HttpOnly {
			t.Errorf("secure=%v: HttpOnly is off; script-readable session cookie", secure)
		}
		if got.SameSite != http.SameSiteLaxMode {
			t.Errorf("secure=%v: SameSite = %v, want Lax", secure, got.SameSite)
		}
		if got.Secure != secure {
			t.Errorf("secure=%v: Secure = %v", secure, got.Secure)
		}
		if hosted := strings.HasPrefix(got.Name, "__Host-"); hosted != secure {
			t.Errorf("secure=%v: name = %q", secure, got.Name)
		}
	}
}

// TestClearedCookieMatchesTheSetOne checks the expiring cookie carries the same
// name, path, and attributes as the one it replaces: a browser drops a Set-Cookie
// that does not match, which would leave a live session behind after sign-out.
func TestClearedCookieMatchesTheSetOne(t *testing.T) {
	for _, secure := range []bool{true, false} {
		set := httptest.NewRecorder()
		setCookie(set, sessionCookie, "sealed", secure, time.Hour)
		live := onlyCookie(t, set)

		cleared := httptest.NewRecorder()
		clearCookie(cleared, sessionCookie, secure)
		dead := onlyCookie(t, cleared)

		if dead.Name != live.Name || dead.Path != live.Path ||
			dead.HttpOnly != live.HttpOnly || dead.Secure != live.Secure || dead.SameSite != live.SameSite {
			t.Errorf("secure=%v: cleared cookie %+v does not match live cookie %+v", secure, dead, live)
		}
		if dead.MaxAge >= 0 {
			t.Errorf("secure=%v: cleared cookie MaxAge = %d, want negative", secure, dead.MaxAge)
		}
		if dead.Value != "" {
			t.Errorf("secure=%v: cleared cookie still carries a value", secure)
		}
	}
}

// onlyCookie returns the single cookie a recorder captured.
func onlyCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cs))
	}
	return cs[0]
}
