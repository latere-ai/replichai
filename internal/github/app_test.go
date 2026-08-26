package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
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
	mints       int              // installation-token mints
	resolves    int              // installation lookups
	tokenExp    time.Time        // expiry the mint endpoint reports
	lastMinted  string           // most recent minted token
	restBearers []string         // Authorization bearers seen on REST endpoints
	scopes      []map[string]any // the body of each mint, so a scope is asserted
	revoked     []string         // tokens presented to DELETE /installation/token
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
		var scope map[string]any
		_ = json.NewDecoder(r.Body).Decode(&scope)
		f.scopes = append(f.scopes, scope)
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
	mux.HandleFunc("DELETE /installation/token", func(w http.ResponseWriter, r *http.Request) {
		presented := bearer(r)
		f.revoked = append(f.revoked, presented)
		if presented != f.lastMinted {
			// A token already revoked or expired is not a live credential.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.lastMinted = ""
		w.WriteHeader(http.StatusNoContent)
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

// 045l AC1–AC3. The minted credential can write to one repository and to
// nothing else, it is not the client's own installation token, and revoking it
// is authenticated by the token itself.
func TestMintRepoTokenIsScopedToOneRepositoryAndRevocable(t *testing.T) {
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fa := &fakeApp{tokenExp: base.Add(time.Hour)}
	srv := httptest.NewServer(fa.handler())
	defer srv.Close()
	c := appClientForTest(t, srv.URL, base)

	// The client's own broad token, so the minted one can be told from it.
	server, err := c.app.serverToken(context.Background())
	if err != nil {
		t.Fatalf("serverToken: %v", err)
	}

	minted, err := c.MintRepoToken(context.Background(), "a-paper-a3f9")
	if err != nil {
		t.Fatalf("MintRepoToken: %v", err)
	}
	if minted.Value == "" || minted.Value == server {
		t.Fatalf("minted %q, want a value distinct from the server token %q", minted.Value, server)
	}
	if !minted.Expires.Equal(fa.tokenExp) {
		t.Errorf("expires = %v, want %v", minted.Expires, fa.tokenExp)
	}

	scope := fa.scopes[len(fa.scopes)-1]
	repositories, _ := scope["repositories"].([]any)
	if len(repositories) != 1 || repositories[0] != "a-paper-a3f9" {
		t.Errorf("repositories = %v, want exactly the one repository", scope["repositories"])
	}
	permissions, _ := scope["permissions"].(map[string]any)
	if len(permissions) != 1 || permissions["contents"] != "write" {
		t.Errorf("permissions = %v, want only contents:write", scope["permissions"])
	}
	if len(scope) != 2 {
		t.Errorf("the mint asked for %v, want repositories and permissions and nothing else", scope)
	}

	if err := c.RevokeToken(context.Background(), minted.Value); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if len(fa.revoked) != 1 || fa.revoked[0] != minted.Value {
		t.Errorf("revoked %v, want the minted token to authenticate its own revocation", fa.revoked)
	}
	// Revoking again is the outcome already reached, not a failure.
	if err := c.RevokeToken(context.Background(), minted.Value); err != nil {
		t.Errorf("revoking an already-revoked token = %v, want nil", err)
	}
}

// A deployment with no App cannot mint, and says so rather than handing back
// the credential it does have.
func TestAPATClientCannotMint(t *testing.T) {
	c := New("ghp_static_pat", WithBaseURL("http://127.0.0.1:1"))
	minted, err := c.MintRepoToken(context.Background(), "a-paper-a3f9")
	if !errors.Is(err, ErrMintingUnavailable) {
		t.Fatalf("MintRepoToken = %v, want ErrMintingUnavailable", err)
	}
	if minted.Value != "" {
		t.Errorf("a client that cannot mint returned %q", minted.Value)
	}
}
