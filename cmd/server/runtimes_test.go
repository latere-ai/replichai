// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
	"github.com/latere-ai/replichai/internal/signin"
)

func TestTheOfferedRuntimesAreTheDefaultAndTheListed(t *testing.T) {
	catalog, err := builtin.Catalog(builtin.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{
		"":                          "memory",
		"latere, Anthropic-Managed": "memory,latere,anthropic-managed",
		"memory,latere,latere":      "memory,latere",
	} {
		got, err := offeredRuntimes(catalog, raw, "memory")
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		names := make([]string, len(got))
		for i, name := range got {
			names[i] = string(name)
		}
		if strings.Join(names, ",") != want {
			t.Errorf("%q = %v, want %s", raw, names, want)
		}
	}
	for _, raw := range []string{"openai", "local"} {
		if _, err := offeredRuntimes(catalog, raw, "memory"); err == nil {
			t.Errorf("%q was offered", raw)
		}
	}
}

func TestASignInStoreExistsExactlyWhenAPersonPays(t *testing.T) {
	store, minter, err := personSignIns(stores{}, []managed.RuntimeName{"memory", "anthropic-managed"}, false)
	if err != nil || store != nil || minter != nil {
		t.Fatalf("no person-paid runtime: %v %v %v", store, minter, err)
	}
	if store, minter, err := personSignIns(stores{}, []managed.RuntimeName{"latere"}, true); err != nil || store != nil || minter != nil {
		t.Fatalf("a dev server acting as its operator kept sign-ins: %v %v %v", store, minter, err)
	}
	if devLatereCommand(false, "cmd") != "" || devLatereCommand(true, "cmd") != "cmd" {
		t.Fatal("the operator's command reached a server outside dev")
	}
	t.Setenv("REPLICHAI_SIGNIN_KEY", "")
	if _, _, err := personSignIns(stores{}, []managed.RuntimeName{"latere"}, false); err == nil || !strings.Contains(err.Error(), "REPLICHAI_SIGNIN_KEY") {
		t.Fatalf("no key = %v", err)
	}
	t.Setenv("REPLICHAI_SIGNIN_KEY", strings.Repeat("ab", 32))
	// The production client is public, so no client secret is needed: the
	// minter refreshes with the client's id alone.
	for _, secret := range []string{"", "s"} {
		t.Setenv("AUTH_CLIENT_SECRET", secret)
		store, minter, err = personSignIns(stores{}, []managed.RuntimeName{"latere"}, false)
		if err != nil || store == nil || minter == nil || signInOf(minter) == nil {
			t.Fatalf("with the key and client secret %q = %v %v %v", secret, store, minter, err)
		}
	}
	if signInOf(nil) != nil {
		t.Fatal("a source with no minter")
	}
	if strings.Join(loginScopes(true), " ") != "openid email profile offline_access" || slices.Contains(loginScopes(false), "offline_access") {
		t.Fatal("login scopes")
	}
}

// A Latere offer reads its levels and models as the person asking, through
// the sign-in the server kept for them.
func TestALatereOfferActsAsThePersonAsking(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"access-1","refresh_token":"rotated","expires_in":900}`))
		case "/actor-tokens":
			_, _ = w.Write([]byte(`{"actor_token":"as-alice"}`))
		}
	}))
	defer auth.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer as-alice" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/account/effort":
			_, _ = w.Write([]byte(`{"levels":[{"id":"thorough","available":true,"next":{"price":{"per":1000000,"input":"2","output":"10"}},"models":[{"name":"anthropic/claude-sonnet-5.5","charge":"credit","available":true}]},{"id":"sustained","available":false,"models":[]}]}`))
		case "/v1/models/models":
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"openai/gpt-6.1-sol"},"status":{"available":true}}]}`))
		}
	}))
	defer api.Close()
	sealer, _ := signin.NewSealer(strings.Repeat("ab", 32))
	store := signin.NewMemStore(sealer)
	if err := store.Put(context.Background(), "alice", "refresh"); err != nil {
		t.Fatal(err)
	}
	minter := signin.NewMinter(store, signin.Config{Issuer: auth.URL, ClientID: "replichai", ClientSecret: "s", HTTP: auth.Client()})
	catalog, err := builtin.Catalog(builtin.Config{Latere: builtin.LatereConfig{BaseURL: api.URL}})
	if err != nil {
		t.Fatal(err)
	}
	latere, err := catalog.Build(context.Background(), "latere")
	if err != nil {
		t.Fatal(err)
	}
	offers := runtimeOffers([]managed.RuntimeName{"memory", "latere"}, "memory",
		map[managed.RuntimeName]managed.Runtime{"memory": managedmemory.New(), "latere": latere}, minter)
	if len(offers) != 2 || !offers[0].Default || offers[0].Label != "Memory" || offers[0].Efforts != nil || offers[1].Label != "Latere" || !offers[1].PersonPays {
		t.Fatalf("offers = %+v", offers)
	}
	efforts, err := offers[1].Efforts(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(efforts)
	if !strings.Contains(string(encoded), `"tier":"balanced","effort":"thorough","model":"claude-sonnet-5.5","provider":"anthropic","input":2000000,"output":10000000`) ||
		!strings.Contains(string(encoded), `"effort":"sustained","error":"level_unavailable"`) {
		t.Fatalf("efforts = %s", encoded)
	}
	models, err := offers[1].Models(context.Background(), "alice")
	if err != nil || len(models) != 1 || models[0] != "openai/gpt-6.1-sol" {
		t.Fatalf("models = %v, %v", models, err)
	}
	if _, err := offers[1].Models(context.Background(), "bob"); err == nil {
		t.Fatal("a person with no kept sign-in listed models")
	}
	if _, err := offers[1].Efforts(context.Background(), "bob"); err == nil {
		t.Fatal("a person with no kept sign-in read the levels")
	}
	// Under the dev bypass no sign-in is kept, and the runtime acts as the
	// operator through its own configured command.
	devCatalog, err := builtin.Catalog(builtin.Config{Latere: builtin.LatereConfig{BaseURL: api.URL, TokenCommand: "printf as-alice"}})
	if err != nil {
		t.Fatal(err)
	}
	devLatere, err := devCatalog.Build(context.Background(), "latere")
	if err != nil {
		t.Fatal(err)
	}
	dev := runtimeOffers([]managed.RuntimeName{"latere"}, "latere", map[managed.RuntimeName]managed.Runtime{"latere": devLatere}, nil)
	if models, err := dev[0].Models(context.Background(), "dev-operator"); err != nil || len(models) != 1 {
		t.Fatalf("dev models = %v, %v", models, err)
	}
}

// stubRuntime is a runtime that answers only what binding a session reads:
// its name and whether it takes a writable repository.
type stubRuntime struct {
	managed.Runtime
	name   managed.RuntimeName
	mounts bool
}

func (s stubRuntime) Name() managed.RuntimeName { return s.name }
func (s stubRuntime) Capabilities(context.Context) (managed.Capabilities, error) {
	return managed.Capabilities{RepositoryModes: map[managed.AccessMode]bool{managed.AccessReadWrite: s.mounts}}, nil
}

func TestAJobRunsWhereItChoseAndMountsOnlyWhereItCan(t *testing.T) {
	router, err := runtimes.NewRouter("anthropic-managed",
		stubRuntime{name: "anthropic-managed", mounts: true}, stubRuntime{name: "latere"})
	if err != nil {
		t.Fatal(err)
	}
	var bound []managed.RuntimeName
	e := &engineRunner{router: router, newController: func(r managed.RuntimeRouter) *managed.Controller {
		runtime, _ := r.ForNew(managed.SessionSpec{})
		bound = append(bound, runtime.Name())
		return managed.NewController(r)
	}}
	for _, c := range []struct {
		runtime string
		want    managed.RuntimeName
		mounts  bool
	}{{"", "anthropic-managed", true}, {"latere", "latere", false}} {
		_, mounts, err := e.sessionFor(context.Background(), &job.Job{Runtime: c.runtime})
		if err != nil || mounts != c.mounts || bound[len(bound)-1] != c.want {
			t.Errorf("runtime %q: bound %v mounts %v err %v", c.runtime, bound, mounts, err)
		}
	}
	if _, _, err := e.sessionFor(context.Background(), &job.Job{Runtime: "openai"}); err == nil {
		t.Error("a job bound to a runtime that is not installed")
	}
	if got := modelOf(&job.Job{Model: "openai/gpt-6.1-sol", Tier: "fast"}); got != "openai/gpt-6.1-sol" {
		t.Errorf("an exact model = %q", got)
	}
	if got := modelOf(&job.Job{}); got != "best" {
		t.Errorf("no choice = %q", got)
	}
	plain := &engineRunner{}
	if ctx := plain.actAs(context.Background(), "alice"); ctx != context.Background() {
		t.Error("a runner with no sign-ins changed the context")
	}
}
