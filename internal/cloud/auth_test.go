// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// jwt builds an unsigned token whose payload the CLI reads to print who is
// signed in. The CLI is not a resource server; the API verifies for real.
func jwt(subject, email string) string {
	payload, _ := json.Marshal(map[string]string{"sub": subject, "email": email})
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}

// provider is a scripted auth server: the device endpoints, the token
// endpoint, and the actor-token mint.
type provider struct {
	approved   bool
	refreshErr string
	refreshNew string
	noActor    bool
	tokenCalls int
	lastForm   string
}

func (p *provider) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"device_code": "dev-1", "user_code": "WXYZ-1234",
			"verification_uri": "https://auth.example/device", "expires_in": 300, "interval": 1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		p.tokenCalls++
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		p.lastForm = string(body)
		if strings.Contains(p.lastForm, "grant_type=refresh_token") {
			if p.refreshErr != "" {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]string{"error": p.refreshErr})
				return
			}
			writeJSON(w, map[string]any{
				"access_token": jwt("user-1", "changkun@latere.ai"), "token_type": "Bearer",
				"refresh_token": p.refreshNew, "expires_in": 3600,
			})
			return
		}
		if !p.approved {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "authorization_pending"})
			return
		}
		writeJSON(w, map[string]any{
			"access_token": jwt("user-1", "changkun@latere.ai"), "token_type": "Bearer",
			"refresh_token": "refresh-1", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/actor-tokens", func(w http.ResponseWriter, r *http.Request) {
		if p.noActor {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("audience mismatch"))
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]string{"actor_token": "actor-1"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func session(t *testing.T, p *provider) *Session {
	t.Helper()
	server := p.serve(t)
	return &Session{AuthURL: server.URL, Store: Store{Dir: t.TempDir()}, HTTP: server.Client(), Now: time.Now}
}

// A device login writes a private credential file naming who signed in.
func TestLoginWritesAPrivateCredential(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	var prompt Prompt
	token, err := s.Login(context.Background(), func(shown Prompt) { prompt = shown })
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if prompt.UserCode != "WXYZ-1234" || prompt.VerificationURI == "" {
		t.Fatalf("prompt = %+v", prompt)
	}
	if token.Subject != "user-1" || token.Email != "changkun@latere.ai" {
		t.Fatalf("token = %+v", token)
	}
	info, err := os.Stat(s.Store.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credential file mode = %o", perm)
	}
	// It is replichai's own file, not the latere CLI's: two programs
	// sharing one would share a refresh that can narrow scopes.
	if strings.Contains(s.Store.Path(), "/latere/") {
		t.Fatalf("the credential landed in another CLI's directory: %s", s.Store.Path())
	}
}

// An auth outage is not a sign-out. Only an explicit rejection of the
// credential clears it.
func TestAnOutageIsNotASignOut(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Expire the stored credential so every read attempts a refresh.
	stored, _ := s.Store.Load()
	stored.Expiry = time.Now().Add(-time.Hour)
	if err := s.Store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p.refreshErr = "temporarily_unavailable"
	if _, err := s.Current(context.Background()); err == nil || errors.Is(err, ErrSignedOut) {
		t.Fatalf("a server error read as a sign-out: %v", err)
	}
	if _, err := s.Store.Load(); err != nil {
		t.Fatalf("a server error cleared the credential: %v", err)
	}

	// A network failure likewise.
	s.AuthURL = "http://127.0.0.1:1"
	if _, err := s.Current(context.Background()); errors.Is(err, ErrSignedOut) {
		t.Fatalf("a network failure read as a sign-out: %v", err)
	}
	if _, err := s.Store.Load(); err != nil {
		t.Fatalf("a network failure cleared the credential: %v", err)
	}
}

// A credential the provider rejects outright is cleared, and the message says
// what to do.
func TestARejectedCredentialIsCleared(t *testing.T) {
	p := &provider{approved: true, refreshErr: "invalid_grant"}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	stored, _ := s.Store.Load()
	stored.Expiry = time.Now().Add(-time.Hour)
	_ = s.Store.Save(stored)

	_, err := s.Current(context.Background())
	if !errors.Is(err, ErrSignedOut) || !strings.Contains(err.Error(), "replichai login") {
		t.Fatalf("Current = %v", err)
	}
	if _, err := s.Store.Load(); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("the rejected credential survived: %v", err)
	}
}

// A refresh response that omits a new refresh token keeps the old one. This is
// ordinary OAuth behaviour and a common way a working client signs itself out
// after a day.
func TestARefreshWithoutANewTokenKeepsTheOldOne(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	stored, _ := s.Store.Load()
	stored.Expiry = time.Now().Add(-time.Hour)
	_ = s.Store.Save(stored)

	refreshed, err := s.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if refreshed.RefreshToken != "refresh-1" {
		t.Fatalf("the refresh token was lost: %+v", refreshed)
	}
}

// The API is reached with a short-lived actor token where auth mints one, and
// with the stored credential where it does not — and the fallback is reported
// rather than silent.
func TestTheBearerPrefersAnActorToken(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	bearer, minted, err := s.Bearer(context.Background(), time.Minute)
	if err != nil || !minted || bearer != "actor-1" {
		t.Fatalf("Bearer = %q, %v, %v", bearer, minted, err)
	}

	p.noActor = true
	bearer, minted, err = s.Bearer(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if minted {
		t.Fatal("a refused mint was reported as minted")
	}
	if !strings.HasPrefix(bearer, "x.") {
		t.Fatalf("the fallback did not present the stored credential: %q", bearer)
	}
}

// Switching organisation goes through the refresh grant, so it needs no second
// device login, and the context is stored beside the credential.
func TestSwitchingOrganisationNeedsNoSecondLogin(t *testing.T) {
	p := &provider{approved: true, refreshNew: "refresh-2"}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	before := p.tokenCalls
	switched, err := s.SwitchOrg(context.Background(), "3fa85f64-5717-4562-b3fc-2c963f66afa6")
	if err != nil {
		t.Fatalf("SwitchOrg: %v", err)
	}
	if switched.Org != "3fa85f64-5717-4562-b3fc-2c963f66afa6" {
		t.Fatalf("org = %q", switched.Org)
	}
	if p.tokenCalls != before+1 || !strings.Contains(p.lastForm, "organization_id=") {
		t.Fatalf("the switch did not use the refresh grant: %d calls, form %q", p.tokenCalls-before, p.lastForm)
	}
	stored, _ := s.Store.Load()
	if stored.Org != switched.Org {
		t.Fatalf("the context was not stored: %+v", stored)
	}
	// Switching back to the personal context is the same grant with no org.
	back, err := s.SwitchOrg(context.Background(), "")
	if err != nil || back.Org != "" {
		t.Fatalf("SwitchOrg(personal) = %+v, %v", back, err)
	}
}

// Signing out removes the credential, and signing out twice is not a failure.
func TestLogoutIsIdempotent(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	for range 2 {
		if err := s.Logout(); err != nil {
			t.Fatalf("Logout: %v", err)
		}
	}
	if _, err := s.Current(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Current after logout = %v", err)
	}
}

// The requested scopes are identity only: what a person may do is decided by
// the deployment's own table, never read out of a token.
func TestScopesAreIdentityOnly(t *testing.T) {
	for _, scope := range Scopes {
		if strings.Contains(scope, ":") {
			t.Errorf("scope %q asks for a permission; authorisation is not the token's", scope)
		}
	}
	if fmt.Sprint(Scopes) != "[openid email profile offline_access]" {
		t.Fatalf("scopes = %v", Scopes)
	}
}

// A store with no credential answers "not signed in" rather than an I/O
// error: that is the fact a caller acts on.
func TestAnEmptyStoreIsSignedOut(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if _, err := store.Load(); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Load = %v", err)
	}
	// A file holding no access token is the same thing.
	if err := store.Save(Token{Issuer: "https://auth.example"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("a credential-less file = %v", err)
	}
	// So is a credential with no refresh token that has expired: there is
	// nothing left to refresh with.
	if err := store.Save(Token{AccessToken: "a", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	session := &Session{AuthURL: "https://auth.example", Store: store, HTTP: http.DefaultClient}
	if _, err := session.Current(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Current = %v", err)
	}
}

// A credential still in date is used as it stands, with no call to the
// provider at all — so an ordinary command works with the network down.
func TestAValidCredentialNeedsNoNetwork(t *testing.T) {
	p := &provider{approved: true}
	s := session(t, p)
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	before := p.tokenCalls
	s.AuthURL = "http://127.0.0.1:1"
	token, err := s.Current(context.Background())
	if err != nil || token.AccessToken == "" {
		t.Fatalf("Current = %+v, %v", token, err)
	}
	if p.tokenCalls != before {
		t.Fatalf("a valid credential was refreshed anyway")
	}
}

// Expiry has a minute of margin, so a call does not start with a credential
// that dies in flight.
func TestExpiryHasMargin(t *testing.T) {
	now := time.Now()
	if (Token{Expiry: now.Add(2 * time.Minute)}).Expired(now) {
		t.Error("a credential with two minutes left was called expired")
	}
	if !(Token{Expiry: now.Add(30 * time.Second)}).Expired(now) {
		t.Error("a credential dying in thirty seconds was called usable")
	}
	// A credential with no stated expiry is not expired: the provider did
	// not say, and guessing would sign a person out for nothing.
	if (Token{}).Expired(now) {
		t.Error("a credential with no expiry was called expired")
	}
}
