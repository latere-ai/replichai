// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func sealer(t *testing.T) *Sealer {
	t.Helper()
	s, err := NewSealer(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestASealedTokenOpensOnlyForItsPerson(t *testing.T) {
	s := sealer(t)
	sealed, err := s.Seal("alice", "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "refresh-1") {
		t.Fatal("the sealed token holds the token in the clear")
	}
	if plain, err := s.Open("alice", sealed); err != nil || plain != "refresh-1" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
	if _, err := s.Open("bob", sealed); err == nil {
		t.Fatal("alice's token opened as bob's")
	}
	if _, err := s.Open("alice", sealed[:3]); err == nil {
		t.Fatal("a truncated token opened")
	}
	for _, key := range []string{"", "zz", testKey[:62]} {
		if _, err := NewSealer(key); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
}

// runStoreContract holds every Store to the same behavior.
func runStoreContract(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.Rotate(ctx, "alice", func(string) (string, error) { return "x", nil }); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("Rotate before Put = %v", err)
	}
	if err := store.Put(ctx, "alice", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "alice", "r2"); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := store.Rotate(ctx, "alice", func(current string) (string, error) { seen = current; return "r3", nil }); err != nil || seen != "r2" {
		t.Fatalf("Rotate saw %q, %v", seen, err)
	}
	failure := errors.New("Auth is down")
	if err := store.Rotate(ctx, "alice", func(string) (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatalf("a failed rotation = %v", err)
	}
	if err := store.Rotate(ctx, "alice", func(current string) (string, error) { seen = current; return current, nil }); err != nil || seen != "r3" {
		t.Fatalf("a failed rotation changed the token: %q, %v", seen, err)
	}
	// Two rotations at once take turns: each reads what the other stored.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_ = store.Rotate(ctx, "alice", func(current string) (string, error) {
				var n int
				_, _ = fmt.Sscanf(current, "r%d", &n)
				return fmt.Sprintf("r%d", n+1), nil
			})
		})
	}
	wg.Wait()
	if err := store.Rotate(ctx, "alice", func(current string) (string, error) { seen = current; return current, nil }); err != nil || seen != "r11" {
		t.Fatalf("after eight rotations the token is %q, %v", seen, err)
	}
	if err := store.Forget(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := store.Rotate(ctx, "alice", func(string) (string, error) { return "x", nil }); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("Rotate after Forget = %v", err)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	runStoreContract(t, NewMemStore(sealer(t)))
}

const signinTestDB = "replichai_signin_test"

func TestPGStoreSatisfiesTheContract(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres sign-in tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, signinTestDB)
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+signinTestDB); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+signinTestDB); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + signinTestDB
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}
	store := NewPGStore(pool, sealer(t))
	runStoreContract(t, store)
	if err := store.Put(ctx, "carol", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE platform_sign_ins SET sealed = 'garbage' WHERE subject = 'carol'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Rotate(ctx, "carol", func(string) (string, error) { return "x", nil }); err == nil {
		t.Fatal("a row that does not open was rotated")
	}
}

// fakeAuth is Latere Auth's token and actor-token routes. A refresh token is
// spent once: presenting one again fails, as Auth does.
type fakeAuth struct {
	mu        sync.Mutex
	spent     map[string]bool
	next      int
	refreshes int
	mints     int
	refuse    bool
	audience  string
}

func (a *fakeAuth) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		id, secret, _ := r.BasicAuth()
		token := r.FormValue("refresh_token")
		if id != "replichai" || secret != "client-secret" || a.refuse || a.spent[token] || r.FormValue("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.spent[token] = true
		a.next++
		a.refreshes++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("access-%d", a.next), "refresh_token": fmt.Sprintf("rotated-%d", a.next), "expires_in": 900})
	case "/actor-tokens":
		var body struct {
			Audience string `json:"audience"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		a.audience = body.Audience
		a.mints++
		_ = json.NewEncoder(w).Encode(map[string]string{"actor_token": "actor-for-" + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")})
	}
}

func newMinter(t *testing.T, store Store) (*Minter, *fakeAuth) {
	t.Helper()
	auth := &fakeAuth{spent: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(auth.serve))
	t.Cleanup(server.Close)
	return NewMinter(store, Config{Issuer: server.URL + "/", ClientID: "replichai", ClientSecret: "client-secret", HTTP: server.Client()}), auth
}

func TestAPlatformTokenIsMintedFromTheKeptSignIn(t *testing.T) {
	store := NewMemStore(sealer(t))
	if err := store.Put(context.Background(), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	minter, auth := newMinter(t, store)
	for range 3 {
		bearer, err := minter.TokenFunc("alice")(context.Background())
		if err != nil || bearer != "actor-for-access-1" {
			t.Fatalf("Token = %q, %v", bearer, err)
		}
	}
	if auth.refreshes != 1 || auth.mints != 1 || auth.audience != PlatformAudience {
		t.Fatalf("refreshes=%d mints=%d audience=%q", auth.refreshes, auth.mints, auth.audience)
	}
	// Past the actor token's keep, a new one is minted from the same access
	// token; past the access token's life, the sign-in is refreshed with the
	// token Auth rotated it to.
	clock := time.Now()
	minter.now = func() time.Time { return clock.Add(actorKeep + time.Second) }
	if _, err := minter.Token(context.Background(), "alice"); err != nil || auth.refreshes != 1 || auth.mints != 2 {
		t.Fatalf("refreshes=%d mints=%d err=%v", auth.refreshes, auth.mints, err)
	}
	minter.now = func() time.Time { return clock.Add(15 * time.Minute) }
	bearer, err := minter.Token(context.Background(), "alice")
	if err != nil || bearer != "actor-for-access-2" || auth.refreshes != 2 {
		t.Fatalf("Token = %q, %v after %d refreshes", bearer, err, auth.refreshes)
	}
}

// Two servers refreshing one person's sign-in at once must never present a
// spent refresh token, which would end the person's sign-in at Auth.
func TestTwoServersNeverSpendOneRefreshTokenTwice(t *testing.T) {
	store := NewMemStore(sealer(t))
	if err := store.Put(context.Background(), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	first, auth := newMinter(t, store)
	second := NewMinter(store, first.config)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			minter := first
			if i%2 == 1 {
				minter = second
			}
			if _, err := minter.Token(context.Background(), "alice"); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Token: %v", err)
	}
	if auth.refreshes != 2 {
		t.Fatalf("refreshed %d times, want once per server", auth.refreshes)
	}
}

func TestARefusedSignInIsForgotten(t *testing.T) {
	store := NewMemStore(sealer(t))
	if err := store.Put(context.Background(), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	minter, auth := newMinter(t, store)
	auth.refuse = true
	if _, err := minter.Token(context.Background(), "alice"); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("Token = %v", err)
	}
	if _, err := minter.Token(context.Background(), "alice"); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("a forgotten sign-in = %v", err)
	}
	if _, err := minter.Token(context.Background(), "nobody"); !errors.Is(err, ErrNoSignIn) {
		t.Fatalf("no sign-in = %v", err)
	}
}

func TestAuthFailuresAreReported(t *testing.T) {
	store := NewMemStore(sealer(t))
	_ = store.Put(context.Background(), "alice", "initial")
	cases := map[string]http.HandlerFunc{
		"down": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"no access token": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"refresh_token":"r"}`))
		},
		"garbled": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{`)) },
		"no actor token": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				_, _ = w.Write([]byte(`{"access_token":"a"}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		},
		"mint refused": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				_, _ = w.Write([]byte(`{"access_token":"a"}`))
				return
			}
			w.WriteHeader(http.StatusForbidden)
		},
	}
	for name, handler := range cases {
		server := httptest.NewServer(handler)
		minter := NewMinter(store, Config{Issuer: server.URL, ClientID: "c", ClientSecret: "s", HTTP: server.Client()})
		if _, err := minter.Token(context.Background(), "alice"); err == nil || errors.Is(err, ErrNoSignIn) {
			t.Errorf("%s: %v", name, err)
		}
		server.Close()
	}
	unreachable := NewMinter(store, Config{Issuer: "http://127.0.0.1:1", HTTP: http.DefaultClient})
	if _, err := unreachable.Token(context.Background(), "alice"); err == nil {
		t.Error("an unreachable Auth gave a token")
	}
}

// A store that cannot reach its database, a sealed token under another key,
// and an Auth address that is not one each fail rather than act.
func TestFailuresAreReportedNotActedOn(t *testing.T) {
	ctx := context.Background()
	if base := os.Getenv("TEST_DATABASE_URL"); base != "" {
		pool, err := pgxpool.New(ctx, base)
		if err != nil {
			t.Fatal(err)
		}
		pool.Close()
		if err := Migrate(ctx, pool); err == nil {
			t.Error("a migration over a closed pool succeeded")
		}
		closed := NewPGStore(pool, sealer(t))
		if err := closed.Put(ctx, "alice", "r"); err == nil {
			t.Error("a Put over a closed pool succeeded")
		}
		if err := closed.Rotate(ctx, "alice", func(string) (string, error) { return "x", nil }); err == nil {
			t.Error("a Rotate over a closed pool succeeded")
		}
		if err := closed.Forget(ctx, "alice"); err == nil {
			t.Error("a Forget over a closed pool succeeded")
		}
	}
	store := NewMemStore(sealer(t))
	if err := store.Put(ctx, "alice", "r"); err != nil {
		t.Fatal(err)
	}
	other, err := NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	store.sealer = other
	if err := store.Rotate(ctx, "alice", func(string) (string, error) { return "x", nil }); err == nil {
		t.Error("a token sealed under another key opened")
	}
	minter := NewMinter(NewMemStore(sealer(t)), Config{Issuer: "http://bad\x7f", HTTP: http.DefaultClient})
	_ = minter.store.Put(ctx, "alice", "r")
	if _, err := minter.Token(ctx, "alice"); err == nil {
		t.Error("an Auth address that is not one gave a token")
	}
}
