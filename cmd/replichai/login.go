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
// flow (045i). A terminal cannot receive a browser redirect, so it prints a
// URL and a code and waits.
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
	// Exactly one organisation or the personal context is accepted, never
	// both and never several. `org a b` used to switch silently to the
	// personal context, which is the opposite of what was typed.
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

// openBrowser is best effort. A terminal that cannot open a browser still has
// the printed URL, which is what the device flow relies on.
func openBrowser(url string) {
	if command := browserCommand(runtime.GOOS, url); command != nil {
		_ = command.Start()
	}
}

// browserCommand is the command that opens rawURL on a platform, or nil when
// there is nothing safe to run: an unknown platform, or a URL that is not
// https. The verification URI is the only thing this ever opens, and the
// provider's is always https. Anything else handed to `open` or `xdg-open`,
// such as a file: path or a custom scheme, would run something on the
// operator's machine at the provider's request, and printing the URL is
// enough.
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
