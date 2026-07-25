// Command replichai audits the reproducibility of research papers.
//
// This package owns argument parsing and output. All behavior lives in
// internal/audit, so an HTTP server can drive the same code path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"

	// Adapters register themselves.
	_ "github.com/latere-ai/replichai/internal/provider/anthropic"
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

	case "resolve":
		return runResolve(ctx, args[1:], stdout, stderr)

	case "publish":
		return runPublish(ctx, args[1:], stdout, stderr)

	case "finish":
		return runFinish(ctx, args[1:], stdout, stderr)

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
		doPublish    = fs.Bool("publish", false, "mount a GitHub repo and publish the reproduction to it")
		owner        = fs.String("owner", envOr("REPLICHAI_GITHUB_OWNER", publish.DefaultOrg), "GitHub owner for --publish")
		invite       = fs.String("invite", "", "GitHub login to invite as a collaborator")
		detach       = fs.Bool("detach", false, "with --publish, start the run and return; finish it later")
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

	opts := []audit.Option{
		audit.WithMaxRevisions(*maxRevisions),
		audit.WithPollInterval(*interval),
		audit.WithTimeout(*timeout),
		audit.WithObserver(func(s provider.RunStatus) {
			fmt.Fprintf(stderr, "  %s\n", s.State)
		}),
	}

	// When publishing, create the repository up front and mount it, so the
	// worker commits its reproduction into it as it runs.
	var pub *publisher
	if *doPublish {
		pub, err = newPublisher(ctx, *owner, paper, *timeout)
		if err != nil {
			return err
		}
		opts = append(opts, audit.WithMount(pub.mount))
		fmt.Fprintf(stderr, "  publishing to %s\n", pub.repoFullName())
	}

	auditor := audit.New(p, m, opts...)

	// Detached: the agent commits to the mounted repo on its own, so there is
	// no reason to hold the process open for the whole run. Start it, print how
	// to finish, and return. finish reattaches and finalizes when it is done.
	if *detach {
		if pub == nil {
			fmt.Fprintln(stderr, "--detach needs --publish")
			return errExitQuiet
		}
		handle, err := auditor.Start(ctx, paper)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "session:   %s\n", handle.ID)
		fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", pub.repoFullName())
		fmt.Fprintf(stdout, "finish:    replichai finish %s %q --session %s%s\n",
			pub.repoFullName(), paper, handle.ID, inviteFlag(*invite))
		return nil
	}

	dst := filepath.Join(*out, slugify(paper))
	started := time.Now()
	result, err := auditor.Run(ctx, paper, dst)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "verdict:   %s\n", result.Grade())
	fmt.Fprintf(stdout, "artifacts: %d -> %s\n", len(result.Artifacts), result.ArtifactDir)
	fmt.Fprintf(stdout, "elapsed:   %s\n", time.Since(started).Round(time.Second))

	if pub != nil {
		if err := pub.finalize(ctx, result, *invite, stdout); err != nil {
			return err
		}
	}
	if !result.Passed() {
		return errExitQuiet
	}
	return nil
}

// publisher holds the GitHub state for one --publish run: the created repo, the
// mount handed to the sandbox, and the identity the manifest records.
type publisher struct {
	gh    *github.Client
	owner string
	name  string
	id    paper.Identity
	mount provider.RepoMount
}

func (p *publisher) repoFullName() string { return p.owner + "/" + p.name }

// newPublisher resolves the paper, creates the private repository, and prepares
// the mount. The GitHub token comes from REPLICHAI_GITHUB_TOKEN.
func newPublisher(ctx context.Context, owner, paperRef string, timeout time.Duration) (*publisher, error) {
	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("--publish needs REPLICHAI_GITHUB_TOKEN set")
	}
	id, err := paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: timeout}))
	if err != nil {
		return nil, err
	}
	suffix, err := publish.NewSuffix()
	if err != nil {
		return nil, err
	}
	name := id.Slug + "-" + suffix

	gh := github.New(token)
	repo, err := gh.CreateRepo(ctx, owner, name, true)
	if errors.Is(err, github.ErrRepoExists) {
		repo, err = gh.GetRepo(ctx, owner, name)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare repository: %w", err)
	}
	return &publisher{
		gh:    gh,
		owner: owner,
		name:  name,
		id:    id,
		mount: provider.RepoMount{URL: repo.HTMLURL, Token: token, MountPath: audit.DefaultMountPath},
	}, nil
}

// finalize runs after the audit: verify what was committed, write the manifest,
// and invite the collaborator.
func (p *publisher) finalize(ctx context.Context, result audit.Result, invite string, stdout *os.File) error {
	return finalizePublication(ctx, p.gh, p.owner, p.name, p.id,
		result.MethodDigest, result.Grade(), invite, stdout)
}

// finalizePublication verifies the committed tree, writes replication.json, and
// invites the collaborator. It reads the repository from GitHub, so it works
// whether the run finished in-process or is picked up afterwards by finish.
func finalizePublication(ctx context.Context, gh *github.Client, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, invite string, stdout *os.File) error {
	repoFull := owner + "/" + name
	head, err := gh.HeadCommit(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("read head commit: %w", err)
	}
	tree, err := gh.TreePaths(ctx, owner, name, head)
	if err != nil {
		return fmt.Errorf("read committed tree: %w", err)
	}
	leaked := publish.VerifyTree(tree)

	manifest := publish.ManifestFor(id, repoFull, methodDigest, verdict, time.Now(), len(leaked) == 0)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := gh.PutFile(ctx, owner, name, publish.ManifestFile,
		"chore: reproduction manifest", append(raw, '\n')); err != nil {
		return fmt.Errorf("commit manifest: %w", err)
	}

	fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", repoFull)
	fmt.Fprintf(stdout, "commit:    %s\n", head)
	if len(leaked) == 0 {
		fmt.Fprintln(stdout, "committed: clean (only worker-written files)")
	} else {
		fmt.Fprintf(stdout, "committed: %d third-party path(s) leaked — repo kept private:\n", len(leaked))
		for _, path := range leaked {
			fmt.Fprintf(stdout, "  - %s\n", path)
		}
	}
	if invite != "" {
		if err := gh.Invite(ctx, owner, name, invite); err != nil {
			return fmt.Errorf("invite %s: %w", invite, err)
		}
		fmt.Fprintf(stdout, "invited:   %s\n", invite)
	}
	return nil
}

// runFinish reattaches to a still-running or finished audit session, waits for
// it to settle, then finalizes the publication of an already-created repository.
// This is the recovery path when `audit --publish` times out while the agent
// keeps committing to the mounted repo.
func runFinish(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("finish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		session      = fs.String("session", "", "session id to reattach to and wait for")
		providerName = fs.String("provider", provider.DefaultName, "provider to poll the session with")
		methodDir    = fs.String("method", "", "the method directory the audit used")
		invite       = fs.String("invite", "", "GitHub login to invite as a collaborator")
		verdictFlag  = fs.String("verdict", "", "override the recorded verdict when no session is polled")
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "gap between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "how long to wait for the session")
	)
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
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "finish takes <owner>/<repo> and a paper reference")
		return errExitQuiet
	}
	owner, name, ok := strings.Cut(positional[0], "/")
	if !ok {
		fmt.Fprintln(stderr, "the repository must be <owner>/<repo>")
		return errExitQuiet
	}
	paperRef := positional[1]

	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("finish needs REPLICHAI_GITHUB_TOKEN set")
	}
	id, err := paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: 30 * time.Second}))
	if err != nil {
		return err
	}
	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}

	verdict := provider.GradeUnknown
	if *session != "" {
		p, err := provider.Get(*providerName)
		if err != nil {
			return err
		}
		status, err := waitForSession(ctx, p, *session, *interval, *timeout, stderr)
		if err != nil {
			return err
		}
		if status.Verdict != nil {
			verdict = status.Verdict.Grade
		}
	} else if *verdictFlag != "" {
		_ = verdict.UnmarshalJSON([]byte(`"` + *verdictFlag + `"`))
	}

	return finalizePublication(ctx, github.New(token), owner, name, id, m.Digest(), verdict, *invite, stdout)
}

// waitForSession polls a session by id until it settles or the deadline passes.
func waitForSession(ctx context.Context, p provider.Provider, session string, interval, timeout time.Duration, stderr *os.File) (provider.RunStatus, error) {
	h := provider.RunHandle{ID: session}
	deadline := time.Now().Add(timeout)
	for {
		status, err := p.Poll(ctx, h)
		if err != nil {
			return provider.RunStatus{}, fmt.Errorf("poll %s: %w", session, err)
		}
		fmt.Fprintf(stderr, "  %s\n", status.State)
		if status.State.Terminal() {
			return status, nil
		}
		if !time.Now().Before(deadline) {
			return status, fmt.Errorf("%w: session %s (last state: %s)", audit.ErrTimedOut, session, status.State)
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// inviteFlag renders the --invite portion of the suggested finish command.
func inviteFlag(login string) string {
	if login == "" {
		return ""
	}
	return " --invite " + login
}

// envOr returns the environment value for key, or fallback when it is unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runResolve(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 30*time.Second, "network timeout for a single fetch")
	if err := fs.Parse(args); err != nil {
		return errExitQuiet
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "resolve takes exactly one paper reference")
		return errExitQuiet
	}

	id, err := paper.Resolve(ctx, fs.Arg(0), paper.WithFetcher(httpFetcher{timeout: *timeout}))
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "identity:  %s\n", id)
	fmt.Fprintf(stdout, "scheme:    %s\n", id.Scheme)
	fmt.Fprintf(stdout, "key:       %s\n", id.Key)
	fmt.Fprintf(stdout, "slug:      %s\n", id.Slug)
	if id.Title != "" {
		fmt.Fprintf(stdout, "title:     %s\n", id.Title)
	}
	for _, src := range id.Sources {
		fmt.Fprintf(stdout, "source:    %s\n", src)
	}
	return nil
}

func runPublish(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out       = fs.String("out", "published", "filesystem forge root")
		live      = fs.Bool("live", false, "write the repository instead of previewing")
		verdict   = fs.String("verdict", "passed", "the run's grade recorded in the manifest")
		methodDir = fs.String("method", "", "path to a method directory")
		timeout   = fs.Duration("timeout", 30*time.Second, "network timeout for identity resolution")
	)
	// flag stops parsing at the first non-flag argument, so collect positionals
	// and keep parsing the flags that follow each — the same shape runAudit uses.
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
	if len(positional) != 2 {
		fmt.Fprintln(stderr, "publish takes an artifact directory and a paper reference")
		return errExitQuiet
	}
	dir, ref := positional[0], positional[1]

	id, err := paper.Resolve(ctx, ref, paper.WithFetcher(httpFetcher{timeout: *timeout}))
	if err != nil {
		return err
	}
	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}
	var grade provider.Grade
	if err := grade.UnmarshalJSON([]byte(`"` + *verdict + `"`)); err != nil {
		return fmt.Errorf("invalid -verdict %q", *verdict)
	}
	artifacts, err := collectArtifacts(dir)
	if err != nil {
		return err
	}

	suffix, err := publish.NewSuffix()
	if err != nil {
		return err
	}
	result := audit.Result{
		ArtifactDir:  dir,
		MethodDigest: m.Digest(),
		Status:       provider.RunStatus{Verdict: &provider.Verdict{Grade: grade}},
		Artifacts:    artifacts,
	}
	plan, err := publish.PlanPublication(result, id, suffix)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "repo:        %s\n", plan.Manifest.Reproduction.Repo)
	fmt.Fprintf(stdout, "verdict:     %s\n", plan.Manifest.Reproduction.Verdict)
	fmt.Fprintf(stdout, "included:    %d files\n", len(plan.Include))
	fmt.Fprintf(stdout, "quarantined: %d files\n", len(plan.Quarantine))
	for _, q := range plan.Quarantine {
		fmt.Fprintf(stdout, "  - %s — %s\n", q.Path, q.Reason)
	}

	forge := publish.NewFSForge(*out, publish.DefaultOrg)
	var opts []publish.PublishOption
	if *live {
		opts = append(opts, publish.Live())
	}
	pub, err := publish.Publish(ctx, plan, forge, opts...)
	if err != nil {
		return err
	}
	if pub.Preview {
		fmt.Fprintln(stdout, "preview only — pass -live to write the repository")
	} else {
		fmt.Fprintf(stdout, "written:     %s\n", filepath.Join(*out, plan.RepoName))
	}
	return nil
}

// collectArtifacts lists every file under dir as an artifact, its path relative
// to dir and forward-slashed, so the publish gate can classify it.
func collectArtifacts(dir string) ([]provider.Artifact, error) {
	var arts []provider.Artifact
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		arts = append(arts, provider.Artifact{Path: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading artifact directory %q: %w", dir, err)
	}
	return arts, nil
}

// httpFetcher is the resolver's network seam at the CLI edge: an ordinary HTTP
// GET. The internal/paper package stays network-free and testable; the real
// transport lives here.
type httpFetcher struct{ timeout time.Duration }

func (h httpFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "replichai")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func usage(w *os.File) {
	fmt.Fprint(w, `replichai - audit the reproducibility of a research paper

usage:
  replichai audit <paper-ref> [flags]
  replichai resolve <paper-ref>
  replichai publish <artifact-dir> <paper-ref> [flags]
  replichai finish <owner>/<repo> <paper-ref> --session <id> [flags]
  replichai providers

a paper-ref is a DOI, an arXiv URL or bare ID, a publisher URL, or a PDF URL

flags for audit:
  -out            directory for artifacts (default "runs")
  -provider       provider to use (default "`+provider.DefaultName+`")
  -method         path to a method directory
  -interval       gap between status checks
  -timeout        how long a run may take
  -max-revisions  revision budget
  -publish        mount a GitHub repo and publish the reproduction to it
  -owner          GitHub owner for -publish (default "`+publish.DefaultOrg+`")
  -invite         GitHub login to invite as a collaborator
  -detach         with -publish, start the run and return; finish it later

flags for finish:
  -session        session id to reattach to and wait for
  -invite         GitHub login to invite as a collaborator
  -method         the method directory the audit used
  -timeout        how long to wait for the session

flags for resolve:
  -timeout        network timeout for a single fetch (default 30s)

flags for publish:
  -out            filesystem forge root (default "published")
  -live           write the repository instead of previewing
  -verdict        the run's grade recorded in the manifest (default "passed")
  -method         path to a method directory
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
