package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeApp is a minimal GitHub App backend: it resolves one installation, mints
// installation tokens on demand (numbered, with a settable expiry), and serves a
// couple of REST endpoints that record which credential authenticated them — so
// a test can prove App calls carry the JWT and REST calls carry the minted
// installation token, never the other way round.
type fakeApp struct {
	mints       int       // installation-token mints
	resolves    int       // installation lookups
	tokenExp    time.Time // expiry the mint endpoint reports
	lastMinted  string    // most recent minted token
	restBearers []string  // Authorization bearers seen on REST endpoints
}

func (f *fakeApp) handler() http.Handler {
	mux := http.NewServeMux()
	// App-authenticated (JWT) endpoints.
	mux.HandleFunc("GET /orgs/{owner}/installation", func(w http.ResponseWriter, r *http.Request) {
		if !isJWT(bearer(r)) {
			http.Error(w, "installation lookup must use the App JWT", http.StatusUnauthorized)
			return
		}
		f.resolves++
		writeJSON(w, http.StatusOK, map[string]any{"id": 42})
	})
	mux.HandleFunc("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if !isJWT(bearer(r)) {
			http.Error(w, "minting must use the App JWT", http.StatusUnauthorized)
			return
		}
		f.mints++
		f.lastMinted = "ghs_inst_" + itoa(f.mints)
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":      f.lastMinted,
			"expires_at": f.tokenExp.Format(time.RFC3339),
		})
	})
	// REST endpoints — these must be reached with the installation token.
	mux.HandleFunc("POST /orgs/{owner}/repos", func(w http.ResponseWriter, r *http.Request) {
		f.restBearers = append(f.restBearers, bearer(r))
		writeJSON(w, http.StatusCreated, map[string]any{"name": "r", "html_url": "u", "private": true})
	})
	mux.HandleFunc("GET /orgs/{owner}/repos", func(w http.ResponseWriter, r *http.Request) {
		f.restBearers = append(f.restBearers, bearer(r))
		writeJSON(w, http.StatusOK, []map[string]any{})
	})
	return mux
}

// AC3: the App client mints, caches, and refreshes an installation token; every
// REST call carries that token, never the App JWT; and a call after expiry
// re-mints rather than reusing a stale token.
func TestAppClientMintsAndRefreshes(t *testing.T) {
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	fa := &fakeApp{tokenExp: base.Add(time.Hour)}
	srv := httptest.NewServer(fa.handler())
	defer srv.Close()

	c := appClientForTest(t, srv.URL, base)

	// First call mints a token and uses it.
	if _, err := c.CreateRepo(context.Background(), "open-hai", "r", true); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	// A second call inside the token's life reuses it — no re-mint.
	if _, err := c.ListRepos(context.Background(), "open-hai"); err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if fa.mints != 1 {
		t.Errorf("mints = %d after two in-life calls, want 1 (cached)", fa.mints)
	}
	if fa.resolves != 1 {
		t.Errorf("installation resolves = %d, want 1 (cached)", fa.resolves)
	}

	// Advance past the token's expiry; the next call re-mints.
	c.app.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := c.ListRepos(context.Background(), "open-hai"); err != nil {
		t.Fatalf("ListRepos after expiry: %v", err)
	}
	if fa.mints != 2 {
		t.Errorf("mints = %d after expiry, want 2 (re-minted)", fa.mints)
	}

	// Every REST call carried an installation token (ghs_...), never the JWT.
	for i, b := range fa.restBearers {
		if !strings.HasPrefix(b, "ghs_inst_") {
			t.Errorf("REST call %d carried %q, want an installation token", i, b)
		}
	}
}

// AC4: with no App configured the client authenticates with the static PAT and
// touches none of the App endpoints, so a deploy before the App exists is safe.
func TestPATClientNeverMints(t *testing.T) {
	fa := &fakeApp{tokenExp: time.Now().Add(time.Hour)}
	srv := httptest.NewServer(fa.handler())
	defer srv.Close()

	c := New("ghp_static_pat", WithBaseURL(srv.URL))
	if _, err := c.CreateRepo(context.Background(), "open-hai", "r", true); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if fa.mints != 0 || fa.resolves != 0 {
		t.Errorf("a PAT client minted (mints=%d resolves=%d), want none", fa.mints, fa.resolves)
	}
	if len(fa.restBearers) != 1 || fa.restBearers[0] != "ghp_static_pat" {
		t.Errorf("REST bearers = %v, want [ghp_static_pat]", fa.restBearers)
	}
}

func TestParseRSAKeyRejectsGarbage(t *testing.T) {
	if _, err := parseRSAPrivateKey([]byte("not a pem")); err == nil {
		t.Error("parseRSAPrivateKey accepted non-PEM input")
	}
}

// appClientForTest builds an App-backed client pointed at a fake server, with a
// fixed clock and a generated key.
func appClientForTest(t *testing.T, baseURL string, clock time.Time) *Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	c, err := NewApp(123, pemBytes, "open-hai", WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	c.app.now = func() time.Time { return clock }
	return c
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// isJWT reports whether a bearer looks like a signed JWT (three base64 segments)
// rather than an installation token (ghs_...).
func isJWT(b string) bool {
	return strings.Count(b, ".") == 2 && !strings.HasPrefix(b, "ghs_")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
