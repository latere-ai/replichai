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
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return k
}

// MintMountToken presents an App JWT (not the org token), scopes the request to
// the one repository, and returns the minted installation token — the sandbox
// mount credential in 006f's two-token split.
func TestMintMountTokenIsScopedAndUsesTheAppJWT(t *testing.T) {
	key := testKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("path = %s", r.URL.Path)
		}
		// The bearer must be a three-part JWT, never the org token.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || len(strings.Split(strings.TrimPrefix(auth, "Bearer "), ".")) != 3 {
			t.Errorf("Authorization is not an App JWT: %q", auth)
		}
		if strings.Contains(auth, "org-token") {
			t.Error("the org token must never be sent to mint a mount token")
		}
		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Repositories) != 1 || body.Repositories[0] != "my-repo" {
			t.Errorf("token not scoped to one repo: %+v", body.Repositories)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "ghs_mounttoken"})
	}))
	defer srv.Close()

	c := New("org-token", WithBaseURL(srv.URL), WithApp("appid", "42", key))
	tok, err := c.MintMountToken(context.Background(), "open-hai", "my-repo")
	if err != nil || tok != "ghs_mounttoken" {
		t.Fatalf("MintMountToken = %q, err %v", tok, err)
	}
}

// A client with no App refuses to mint — it must never hand the org token to the
// sandbox as a fallback.
func TestMintMountTokenRefusesWithoutAnApp(t *testing.T) {
	c := New("org-token")
	if _, err := c.MintMountToken(context.Background(), "open-hai", "r"); err == nil {
		t.Fatal("minting without an App must error, not fall back to the org token")
	}
	if c.HasApp() {
		t.Error("HasApp should be false without WithApp")
	}
}

func TestParseAppKeyRoundTrip(t *testing.T) {
	key := testKey(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	parsed, err := ParseAppKey(pemBytes)
	if err != nil || !parsed.Equal(key) {
		t.Fatalf("ParseAppKey round-trip failed: err=%v", err)
	}
	if _, err := ParseAppKey([]byte("not a pem")); err == nil {
		t.Error("garbage must be rejected")
	}
}
