// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/cloud"
)

// TestTheRuntimeResolvesInOneOrder checks the resolution order README.md
// documents: "REPLICHAI_RUNTIME selects the managed execution runtime, and
// --runtime overrides it per command." Before this rule existed, only the
// server read the variable; the CLI hard-coded its default, so the documented
// behavior was false for every subcommand.
func TestTheRuntimeResolvesInOneOrder(t *testing.T) {
	env := func(value string) func(string) string {
		return func(key string) string {
			if key == runtimeEnv {
				return value
			}
			return ""
		}
	}
	cases := []struct {
		name, flag, env, fallback, want string
	}{
		{"the flag beats the environment", "memory", "local", "anthropic-managed", "memory"},
		{"the environment beats the default", "", "local", "anthropic-managed", "local"},
		{"nothing set is the subcommand's default", "", "", "anthropic-managed", "anthropic-managed"},
		{"each subcommand keeps its own default", "", "", "local", "local"},
		{"whitespace is unset", " ", " ", "memory", "memory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRuntime(tc.flag, env(tc.env), tc.fallback); got != tc.want {
				t.Fatalf("resolveRuntime(%q, env=%q, %q) = %q, want %q", tc.flag, tc.env, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestTheRuntimeVariableReachesEverySubcommand checks that the variable reaches
// every subcommand that takes a runtime through the same function, so none of
// them can hard-code a default again.
func TestTheRuntimeVariableReachesEverySubcommand(t *testing.T) {
	t.Setenv(runtimeEnv, "memory")

	// audit runs on the memory runtime with no --runtime typed.
	stdout, stderr, err := capture(t, "audit", "https://arxiv.org/abs/1",
		"--method", writeMethod(t), "--out", t.TempDir(), "--interval", "0")
	if err != nil {
		t.Fatalf("audit under %s=memory: %v\n%s", runtimeEnv, err, stderr)
	}
	if !strings.Contains(stdout, "verdict:") {
		t.Fatalf("audit did not run on the memory runtime:\n%s", stdout)
	}

	// providers marks the runtime the environment selected as the default.
	stdout, _, err = capture(t, "providers")
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	if !strings.Contains(stdout, "memory (default)") {
		t.Fatalf("providers does not mark the environment's runtime as the default:\n%s", stdout)
	}

	// The flag still takes precedence per command.
	_, _, err = capture(t, "audit", "paper", "--runtime", "nope", "--method", writeMethod(t))
	if err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("--runtime did not override the environment: %v", err)
	}
}

// With neither LATERE_API_KEY nor LATERE_TOKEN_COMMAND set, the latere
// runtime acts as the operator signed in with `replichai login`: it mints, at
// the Auth that sign-in came from, a token addressed to the platform's API,
// and presents that rather than the stored credential, which the platform
// refuses. Signed out, a call says how to sign in.
func TestTheLatereRuntimeActsWithTheReplichaiLogin(t *testing.T) {
	var audiences []string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actor-tokens" || r.Header.Get("Authorization") != "Bearer stored-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var asked struct {
			Audience string `json:"audience"`
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		audiences = append(audiences, asked.Audience)
		_ = json.NewEncoder(w).Encode(map[string]any{"actor_token": "platform-actor", "expires_in": 300})
	}))
	defer auth.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer platform-actor" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"network":{"mode":"open"}}`))
	}))
	defer api.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_AUTH_URL", auth.URL)
	t.Setenv("LATERE_API_URL", api.URL)
	t.Setenv("LATERE_API_KEY", "")
	t.Setenv("LATERE_TOKEN_COMMAND", "")
	store := cloud.DefaultStore(home)
	if err := store.Save(cloud.Token{AccessToken: "stored-access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	capabilities := func() error {
		catalog, _, err := runtimeCatalog("latere", localOptions{})
		if err != nil {
			return err
		}
		runtime, err := catalog.Build(context.Background(), "latere")
		if err != nil {
			return err
		}
		_, err = runtime.Capabilities(context.Background())
		return err
	}
	if err := capabilities(); err != nil {
		t.Fatalf("signed in: %v", err)
	}
	if len(audiences) != 1 || audiences[0] != "api.latere.ai" {
		t.Fatalf("Auth was asked for %v", audiences)
	}

	if err := os.Remove(store.Path()); err != nil {
		t.Fatal(err)
	}
	err := capabilities()
	if err == nil || !strings.Contains(err.Error(), "replichai login") || !strings.Contains(err.Error(), "LATERE_API_KEY") {
		t.Fatalf("signed out: %v", err)
	}
}
