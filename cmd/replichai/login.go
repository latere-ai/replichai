// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/latere-ai/replichai/internal/cloud"
)

// runLogin signs the terminal in against auth.latere.ai with the device-code
// flow: a terminal cannot run a browser redirect, so it prints a URL and a
// code and waits (045i).
func runLogin(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("login", stderr)
	authURL := fs.String("auth-url", envOr("REPLICHAI_AUTH_URL", cloud.DefaultAuthURL), "identity provider URL")
	noBrowser := fs.Bool("no-browser", false, "print the URL instead of opening it")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noPositionals("login", positional, stderr); err != nil {
		return err
	}
	session := cloud.New(homeDir(), *authURL)
	token, err := session.Login(ctx, func(prompt cloud.Prompt) {
		_, _ = fmt.Fprintf(stdout, "Open %s and enter code:  %s\n", prompt.VerificationURI, prompt.UserCode)
		if !*noBrowser {
			openBrowser(prompt.VerificationURI)
		}
		_, _ = fmt.Fprintln(stderr, "Waiting for approval…")
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "signed in as %s\n", identityOf(token))
	return nil
}

// runWhoami prints the stored identity without a network call, so it answers
// the same when the provider is unreachable.
func runWhoami(_ context.Context, args []string, stdout, stderr *os.File) error {
	positional, err := parseFlags(flagSet("whoami", stderr), args)
	if err != nil {
		return err
	}
	if err := noPositionals("whoami", positional, stderr); err != nil {
		return err
	}
	token, err := cloud.New(homeDir(), "").Store.Load()
	if errors.Is(err, cloud.ErrSignedOut) {
		_, _ = fmt.Fprintln(stderr, "not signed in; run `replichai login`")
		return errExitQuiet
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", identityOf(token))
	context := token.Org
	if context == "" {
		context = "personal"
	}
	_, _ = fmt.Fprintf(stdout, "context: %s\n", context)
	return nil
}

// runLogout removes the stored credential.
func runLogout(_ context.Context, args []string, stdout, stderr *os.File) error {
	positional, err := parseFlags(flagSet("logout", stderr), args)
	if err != nil {
		return err
	}
	if err := noPositionals("logout", positional, stderr); err != nil {
		return err
	}
	if err := cloud.New(homeDir(), "").Logout(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "signed out")
	return nil
}

// runOrg shows or switches the organisation a project belongs to. Switching
// goes through the refresh grant, so it needs no second device login.
func runOrg(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("org", stderr)
	personal := fs.Bool("personal", false, "switch to the personal context")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	// One organisation, or the personal context, never both and never
	// several: `org a b` used to switch silently to the personal context,
	// which is the opposite of what was typed.
	if len(positional) > 1 || (len(positional) == 1 && *personal) {
		_, _ = fmt.Fprintln(stderr, "org takes one organisation id, or --personal")
		return errExitQuiet
	}
	session := cloud.New(homeDir(), envOr("REPLICHAI_AUTH_URL", cloud.DefaultAuthURL))
	if len(positional) == 0 && !*personal {
		token, err := session.Store.Load()
		if err != nil {
			return err
		}
		if token.Org == "" {
			_, _ = fmt.Fprintln(stdout, "personal")
		} else {
			_, _ = fmt.Fprintln(stdout, token.Org)
		}
		return nil
	}
	org := ""
	if len(positional) == 1 {
		org = positional[0]
	}
	if _, err := session.SwitchOrg(ctx, org); err != nil {
		return err
	}
	if org == "" {
		_, _ = fmt.Fprintln(stdout, "switched to the personal context")
	} else {
		_, _ = fmt.Fprintf(stdout, "switched to %s\n", org)
	}
	return nil
}

func identityOf(token cloud.Token) string {
	if strings.TrimSpace(token.Email) != "" {
		return token.Email
	}
	if strings.TrimSpace(token.Subject) != "" {
		return token.Subject
	}
	return "an unnamed account"
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// openBrowser is best effort: a terminal that cannot open one still prints the
// URL, which is the whole point of the device flow.
func openBrowser(url string) {
	if command := browserCommand(runtime.GOOS, url); command != nil {
		_ = command.Start()
	}
}

// browserCommand is the command that opens rawURL on a platform, or nil where
// there is nothing safe to run: an unknown platform, or a URL that is not
// https. The verification URI is the only thing this ever opens and the
// provider's is always https; anything else handed to `open` or `xdg-open` —
// a file: path, a custom scheme — runs something on the operator's machine at
// the provider's say-so, and printing the URL is enough.
func browserCommand(goos, rawURL string) *exec.Cmd {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil
	}
	switch goos {
	case "darwin":
		//nolint:gosec,noctx // the URL is the identity provider's own https verification URI
		return exec.Command("open", rawURL)
	case "linux":
		//nolint:gosec,noctx // the URL is the identity provider's own https verification URI
		return exec.Command("xdg-open", rawURL)
	default:
		return nil
	}
}
