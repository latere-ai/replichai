// Command replichai audits the reproducibility of research papers.
//
// This package owns argument parsing and output. All behavior lives in
// internal/audit, so an HTTP server can drive the same code path.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider"

	// Adapters register themselves.
	_ "github.com/latere-ai/replichai/internal/provider/hosted"
	_ "github.com/latere-ai/replichai/internal/provider/memory"
)

func main() {
	// Ctrl-C cancels the audit, which still releases the run before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errExitQuiet) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}
}

// errExitQuiet signals a non-zero exit whose reason was already reported.
var errExitQuiet = errors.New("exit")

func run(ctx context.Context, args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		usage(stderr)
		return errExitQuiet
	}

	switch args[0] {
	case "providers":
		for _, name := range provider.Available() {
			suffix := ""
			if name == provider.DefaultName {
				suffix = " (default)"
			}
			fmt.Fprintf(stdout, "%s%s\n", name, suffix)
		}
		return nil

	case "audit":
		return runAudit(ctx, args[1:], stdout, stderr)

	case "-h", "--help", "help":
		usage(stdout)
		return nil

	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return errExitQuiet
	}
}

func runAudit(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out          = fs.String("out", "runs", "directory for artifacts")
		providerName = fs.String("provider", provider.DefaultName, "provider to use")
		methodDir    = fs.String("method", "", "path to a method directory")
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "gap between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "how long a run may take")
		maxRevisions = fs.Int("max-revisions", audit.DefaultMaxRevisions, "revision budget")
	)
	// flag stops parsing at the first non-flag argument, so a plain
	// fs.Parse would silently drop every flag in
	// `replichai audit <paper> --provider memory` -- the natural way to type
	// it. Collect positionals and keep parsing what follows each one.
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return errExitQuiet
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "audit takes exactly one paper reference")
		return errExitQuiet
	}
	paper := positional[0]

	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}
	p, err := provider.Get(*providerName)
	if err != nil {
		return err
	}

	auditor := audit.New(p, m,
		audit.WithMaxRevisions(*maxRevisions),
		audit.WithPollInterval(*interval),
		audit.WithTimeout(*timeout),
		audit.WithObserver(func(s provider.RunStatus) {
			fmt.Fprintf(stderr, "  %s\n", s.State)
		}),
	)

	dst := filepath.Join(*out, slugify(paper))
	started := time.Now()
	result, err := auditor.Run(ctx, paper, dst)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "verdict:   %s\n", result.Grade())
	fmt.Fprintf(stdout, "artifacts: %d -> %s\n", len(result.Artifacts), result.ArtifactDir)
	fmt.Fprintf(stdout, "elapsed:   %s\n", time.Since(started).Round(time.Second))
	if !result.Passed() {
		return errExitQuiet
	}
	return nil
}

func usage(w *os.File) {
	fmt.Fprint(w, `replichai - audit the reproducibility of a research paper

usage:
  replichai audit <paper-url> [flags]
  replichai providers

flags for audit:
  -out            directory for artifacts (default "runs")
  -provider       provider to use (default "`+provider.DefaultName+`")
  -method         path to a method directory
  -interval       gap between status checks
  -timeout        how long a run may take
  -max-revisions  revision budget
`)
}

// slugify names a local scratch directory for one paper reference.
//
// Deliberately simple: this is not a published repository name. Repository
// naming is a separate concern with its own identity rules.
func slugify(reference string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(reference)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := b.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 80 {
		slug = strings.Trim(slug[len(slug)-80:], "-")
	}
	if slug == "" {
		return "paper"
	}
	return slug
}
