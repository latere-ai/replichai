// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identityProvider is a scripted stand-in for auth.latere.ai: the device
// endpoints, a token endpoint that approves at once, and the actor-token mint.
// It records the forms it was sent so a test can see what the terminal asked
// for.
type identityProvider struct {
	forms []string
}

func (p *identityProvider) serve(t *testing.T) *httptest.Server {
	t.Helper()
	token := func(subject, email string) string {
		payload, _ := json.Marshal(map[string]string{"sub": subject, "email": email})
		return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]any{
			"device_code": "dev-1", "user_code": "WXYZ-1234",
			// The interval is one second because the OAuth library treats a
			// zero interval as five.
			"verification_uri": "https://auth.example/device", "expires_in": 300, "interval": 1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.forms = append(p.forms, r.PostForm.Encode())
		write(w, http.StatusOK, map[string]any{
			"access_token": token("user-1", "operator@example.test"), "token_type": "Bearer",
			"refresh_token": "refresh-1", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/actor-tokens", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]string{"actor_token": "actor-1"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// signedInHome is a home directory with the terminal signed in against the
// scripted provider, and the environment pointed at both.
func signedInHome(t *testing.T) (home string, provider *identityProvider) {
	t.Helper()
	provider = &identityProvider{}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_AUTH_URL", provider.serve(t).URL)
	if _, stderr, err := capture(t, "login", "--no-browser"); err != nil {
		t.Fatalf("login: %v\n%s", err, stderr)
	}
	return home, provider
}

// TestLoginWhoamiAndLogoutFromTheTerminal covers the whole sign-in from the
// terminal: the prompt names the code, the credential is written with private
// permissions under the operator's configuration directory, and whoami answers
// from it without a network call.
func TestLoginWhoamiAndLogoutFromTheTerminal(t *testing.T) {
	provider := &identityProvider{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("REPLICHAI_AUTH_URL", provider.serve(t).URL)

	stdout, stderr, err := capture(t, "login", "--no-browser")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, stderr)
	}
	for _, want := range []string{"https://auth.example/device", "WXYZ-1234", "signed in as operator@example.test"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("login did not print %q:\n%s", want, stdout)
		}
	}
	credential := filepath.Join(home, ".config", "replichai", "auth.json")
	info, err := os.Stat(credential)
	if err != nil {
		t.Fatalf("the credential was not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the credential is %v, want private", info.Mode().Perm())
	}

	// whoami reads the file and names the principal and the context.
	stdout, _, err = capture(t, "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(stdout, "operator@example.test") || !strings.Contains(stdout, "context: personal") {
		t.Fatalf("whoami printed %q", stdout)
	}

	// logout removes the credential, and whoami then reports that and exits
	// non-zero.
	if stdout, _, err = capture(t, "logout"); err != nil || !strings.Contains(stdout, "signed out") {
		t.Fatalf("logout = %q, %v", stdout, err)
	}
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatalf("the credential survived logout: %v", err)
	}
	_, stderr, err = capture(t, "whoami")
	if !errors.Is(err, errExitQuiet) || !strings.Contains(stderr, "not signed in") {
		t.Fatalf("whoami after logout = %v, %q", err, stderr)
	}
}

// TestOrgShowsAndSwitchesTheContext checks that switching organisation goes
// through the refresh grant and is reflected by whoami, and that switching back
// is the same grant with no organisation.
func TestOrgShowsAndSwitchesTheContext(t *testing.T) {
	_, provider := signedInHome(t)

	stdout, _, err := capture(t, "org")
	if err != nil || strings.TrimSpace(stdout) != "personal" {
		t.Fatalf("org = %q, %v", stdout, err)
	}
	const org = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	if stdout, _, err = capture(t, "org", org); err != nil || !strings.Contains(stdout, "switched to "+org) {
		t.Fatalf("org %s = %q, %v", org, stdout, err)
	}
	if last := provider.forms[len(provider.forms)-1]; !strings.Contains(last, "organization_id="+org) {
		t.Fatalf("the switch did not name the organisation in the grant: %q", last)
	}
	if stdout, _, err = capture(t, "whoami"); err != nil || !strings.Contains(stdout, "context: "+org) {
		t.Fatalf("whoami after the switch = %q, %v", stdout, err)
	}
	if stdout, _, err = capture(t, "org", "--personal"); err != nil || !strings.Contains(stdout, "personal context") {
		t.Fatalf("org --personal = %q, %v", stdout, err)
	}
	if stdout, _, err = capture(t, "org"); err != nil || strings.TrimSpace(stdout) != "personal" {
		t.Fatalf("org after switching back = %q, %v", stdout, err)
	}
}

// TestOrgRefusesMoreThanOneChoice checks that `org a b` and `org a --personal`
// are refused rather than read as a switch to the personal context, which is
// the opposite of what was typed.
func TestOrgRefusesMoreThanOneChoice(t *testing.T) {
	_, provider := signedInHome(t)
	before := len(provider.forms)
	for _, args := range [][]string{{"org", "a", "b"}, {"org", "a", "--personal"}} {
		_, stderr, err := capture(t, args...)
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("%v: err = %v, want a refusal", args, err)
		}
		if !strings.Contains(stderr, "one organisation") {
			t.Errorf("%v: stderr %q does not explain", args, stderr)
		}
	}
	if len(provider.forms) != before {
		t.Fatalf("a refused switch still reached the provider: %v", provider.forms[before:])
	}
}

// TestOnlyAnHTTPSVerificationURIIsOpened checks that the terminal opens only
// the provider's https verification URI. Anything else it is handed is printed
// and never run.
func TestOnlyAnHTTPSVerificationURIIsOpened(t *testing.T) {
	for _, hostile := range []string{
		"http://auth.example/device", "file:///etc/passwd", "javascript:alert(1)",
		"ftp://x", "https://", "not a url", "",
	} {
		if command := browserCommand("darwin", hostile); command != nil {
			t.Errorf("browserCommand(darwin, %q) would run %v", hostile, command.Args)
		}
		if command := browserCommand("linux", hostile); command != nil {
			t.Errorf("browserCommand(linux, %q) would run %v", hostile, command.Args)
		}
	}
	const uri = "https://auth.latere.ai/device?user_code=WXYZ-1234"
	if command := browserCommand("darwin", uri); command == nil || command.Args[0] != "open" || command.Args[1] != uri {
		t.Errorf("browserCommand(darwin, %q) = %v", uri, command)
	}
	if command := browserCommand("linux", uri); command == nil || command.Args[0] != "xdg-open" || command.Args[1] != uri {
		t.Errorf("browserCommand(linux, %q) = %v", uri, command)
	}
	if browserCommand("plan9", uri) != nil {
		t.Error("an unknown platform has no browser command")
	}
}
