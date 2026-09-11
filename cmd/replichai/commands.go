// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

// command is one subcommand: its name, the synopsis of its arguments, a
// one-line summary, and the function that runs it. Dispatch and the usage
// text are both derived from the one list, so help cannot name a subcommand
// dispatch does not know, or omit one it does. A subcommand's flags are
// printed by its own flag set via `replichai <command> -h`, so they cannot
// drift from their definitions either.
type command struct {
	name     string
	synopsis string
	summary  string
	run      func(ctx context.Context, args []string, stdout, stderr *os.File) error
}

// commands is every subcommand, in the order usage lists them. It is a
// function rather than a variable because the run functions it names build
// their help from the registry, and a package-level variable that refers to
// functions referring back to it is an initialisation cycle.
func commands() []command {
	return []command{
		{"audit", "<paper-ref> [flags]", "audit one paper and write its reproduction", runAudit},
		{"run", "<project-file> [flags]", "audit every paper in a project file in parallel and write a report", runRun},
		{"resolve", "<paper-ref> [flags]", "show the identity and slug a reference resolves to", runResolve},
		{"publish", "<artifact-dir> <paper-ref> [flags]", "publish an audit's artifacts to a filesystem forge", runPublish},
		{"finish", "<owner>/<repo> <paper-ref> [flags]", "reattach to a detached audit and finish publishing its repository", runFinish},
		{"login", "[flags]", "sign in at auth.latere.ai with a device code", runLogin},
		{"whoami", "", "show who is signed in and the current context", runWhoami},
		{"logout", "", "remove the stored credential", runLogout},
		{"org", "[<org-uuid> | --personal]", "show or switch the organisation context", runOrg},
		{"sessions", "ls | prune [flags]", "list or remove the session directories left by local runs", runSessions},
		{"project", "create <title> | ls | sessions <id> | rm-session <id>...", "manage projects in the app", runProject},
		{"push", "<project-file> --project <id>", "add a local run's sessions to a project in the app", runPush},
		{"providers", "", "list the installed runtimes", runProviders},
		{"rates", "[flags]", "check that the rate card names the models a runtime resolves to", runRates},
	}
}

// lookup finds a subcommand by name.
func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// errHelp reports that a subcommand printed its help, which is a clean exit
// rather than a failure.
var errHelp = errors.New("help")

func run(ctx context.Context, args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		usage(stderr)
		return errExitQuiet
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return nil
	}
	c, ok := lookup(args[0])
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return errExitQuiet
	}
	err := c.run(ctx, args[1:], stdout, stderr)
	if errors.Is(err, errHelp) {
		return nil
	}
	return err
}

// usage is generated from the registry, so every subcommand appears with the
// synopsis and summary its own entry declares.
func usage(w *os.File) {
	_, _ = fmt.Fprint(w, "replichai - audit the reproducibility of a research paper\n\nusage:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands() {
		line := "replichai " + c.name
		if c.synopsis != "" {
			line += " " + c.synopsis
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", line, c.summary)
	}
	_, _ = fmt.Fprintf(tw, "  replichai help\tshow this text\n")
	_ = tw.Flush()
	_, _ = fmt.Fprint(w, `
a paper-ref is a DOI, an arXiv URL or bare ID, a publisher URL, or a PDF URL.
`+"`replichai <command> -h`"+` lists the flags a command takes, with their defaults.
`)
}

// flagSet is a subcommand's flag set. Its -h prints the subcommand's usage
// line from the registry followed by the flags it defines, so the help text
// matches what the command accepts.
func flagSet(name string, stderr *os.File) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		if c, ok := lookup(name); ok {
			_, _ = fmt.Fprintf(stderr, "usage: replichai %s %s\n    %s\n", c.name, c.synopsis, c.summary)
		}
		defined := false
		fs.VisitAll(func(*flag.Flag) { defined = true })
		if defined {
			_, _ = fmt.Fprintln(stderr, "\nflags:")
			fs.PrintDefaults()
		}
	}
	return fs
}

// parseFlags parses args against fs and returns the positional arguments
// wherever they appeared. The flag package stops at the first non-flag
// argument, and `replichai audit <paper> --runtime memory` is the natural way
// to type a command. -h prints the subcommand's help and returns errHelp,
// which is not a failure. A malformed flag has already been reported by the
// flag set and returns errExitQuiet.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, errExitQuiet
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// noPositionals refuses arguments a subcommand does not take rather than
// ignoring them, because `replichai whoami foo` is most likely a typo.
func noPositionals(name string, positional []string, stderr *os.File) error {
	if len(positional) == 0 {
		return nil
	}
	_, _ = fmt.Fprintf(stderr, "%s takes no arguments, got %q\n", name, positional)
	return errExitQuiet
}

// runProviders lists the installed runtimes and marks the one a bare command
// would use.
func runProviders(_ context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("providers", stderr)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noPositionals("providers", positional, stderr); err != nil {
		return err
	}
	// The listing covers everything installed, so it is built from the
	// compiled-in default. The marker follows what the environment selects.
	selected := resolveRuntime("", os.Getenv, defaultCLIRuntime)
	catalog, _, err := runtimeCatalog(defaultCLIRuntime, localOptions{})
	if err != nil {
		return err
	}
	for _, name := range catalog.Names() {
		suffix := ""
		if string(name) == selected {
			suffix = " (default)"
		}
		_, _ = fmt.Fprintf(stdout, "%s%s\n", name, suffix)
	}
	return nil
}
