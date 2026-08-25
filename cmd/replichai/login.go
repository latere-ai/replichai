package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	authURL := fs.String("auth-url", envOr("REPLICHAI_AUTH_URL", cloud.DefaultAuthURL), "identity provider")
	noBrowser := fs.Bool("no-browser", false, "print the URL instead of opening it")
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}
	session := cloud.New(homeDir(), *authURL)
	token, err := session.Login(ctx, func(prompt cloud.Prompt) {
		fmt.Fprintf(stdout, "Open %s and enter code:  %s\n", prompt.VerificationURI, prompt.UserCode)
		if !*noBrowser {
			openBrowser(prompt.VerificationURI)
		}
		fmt.Fprintln(stderr, "Waiting for approval…")
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "signed in as %s\n", identityOf(token))
	return nil
}

// runWhoami prints the stored identity without a network call, so it answers
// the same when the provider is unreachable.
func runWhoami(_ context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}
	token, err := cloud.New(homeDir(), "").Store.Load()
	if errors.Is(err, cloud.ErrSignedOut) {
		fmt.Fprintln(stderr, "not signed in; run `replichai login`")
		return errExitQuiet
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s\n", identityOf(token))
	context := token.Org
	if context == "" {
		context = "personal"
	}
	fmt.Fprintf(stdout, "context: %s\n", context)
	return nil
}

// runLogout removes the stored credential.
func runLogout(_ context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}
	if err := cloud.New(homeDir(), "").Logout(); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "signed out")
	return nil
}

// runOrg shows or switches the organisation a project belongs to. Switching
// goes through the refresh grant, so it needs no second device login.
func runOrg(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("org", flag.ContinueOnError)
	fs.SetOutput(stderr)
	personal := fs.Bool("personal", false, "switch to the personal context")
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}
	session := cloud.New(homeDir(), envOr("REPLICHAI_AUTH_URL", cloud.DefaultAuthURL))
	if fs.NArg() == 0 && !*personal {
		token, err := session.Store.Load()
		if err != nil {
			return err
		}
		if token.Org == "" {
			fmt.Fprintln(stdout, "personal")
		} else {
			fmt.Fprintln(stdout, token.Org)
		}
		return nil
	}
	org := ""
	if fs.NArg() == 1 && !*personal {
		org = fs.Arg(0)
	}
	if _, err := session.SwitchOrg(ctx, org); err != nil {
		return err
	}
	if org == "" {
		fmt.Fprintln(stdout, "switched to the personal context")
	} else {
		fmt.Fprintf(stdout, "switched to %s\n", org)
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
	return "an unnamed principal"
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
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		//nolint:gosec,noctx // the URL is the identity provider's own verification URI
		command = exec.Command("open", url)
	case "linux":
		//nolint:gosec,noctx // the URL is the identity provider's own verification URI
		command = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = command.Start()
}
