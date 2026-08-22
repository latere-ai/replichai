// Command replichai audits the reproducibility of research papers.
//
// This package owns argument parsing and output. All behavior lives in
// internal/audit, so an HTTP server can drive the same code path.
package main

import (
	"context"
	"encoding/base64"
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
	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/registry"
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
	case "rates":
		return runRates(ctx, args[1:], stdout, stderr)

	case "providers":
		catalog, _, err := runtimeCatalog("anthropic")
		if err != nil {
			return err
		}
		for _, name := range catalog.Names() {
			suffix := ""
			if name == "anthropic-managed" {
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
		providerName = fs.String("provider", defaultCLIRuntime, "managed runtime to use (deprecated name; use --runtime in new automation)")
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
	controller, _, err := controllerFor(ctx, *providerName, *interval)
	if err != nil {
		return err
	}

	hooks := managed.Hooks{Status: func(_ context.Context, status managed.Status) error {
		fmt.Fprintf(stderr, "  %s\n", status.State)
		return nil
	}}
	opts := []audit.SessionOption{
		audit.WithSessionMaxRevisions(*maxRevisions),
		audit.WithSessionTimeout(*timeout),
		audit.WithSessionHooks(hooks),
	}

	// When publishing, create the repository up front and mount it, so the
	// worker commits its reproduction into it as it runs.
	var pub *publisher
	if *doPublish {
		pub, err = newPublisher(ctx, *owner, paper, m.Digest(), *timeout, stderr)
		if err != nil {
			return err
		}
		opts = append(opts, audit.WithSessionMount(audit.SessionMount{
			URL: pub.mount.URL, Token: pub.mount.Token, MountPath: pub.mount.MountPath, ReadOnly: pub.mount.ReadOnly,
		}))
		hooks.Retain = func(ctx context.Context, evidence managed.EvidenceReader) error {
			grade := provider.GradeUnknown
			if evidence.Status.Verdict != nil {
				grade = provider.Grade(evidence.Status.Verdict.Grade)
			}
			return finalizePublication(ctx, pub.gh, pub.owner, pub.name, pub.id, m.Digest(), grade, providerEventsFromManaged(evidence.Events), *invite, stdout)
		}
		opts = append(opts, audit.WithSessionHooks(hooks))
		fmt.Fprintf(stderr, "  publishing to %s\n", pub.repoFullName())
	}

	auditor := audit.NewSessionAuditor(controller, m, opts...)

	// Detached: the agent commits to the mounted repo on its own, so there is
	// no reason to hold the process open for the whole run. Start it, print how
	// to finish, and return. finish reattaches and finalizes when it is done.
	if *detach {
		if pub == nil {
			fmt.Fprintln(stderr, "--detach needs --publish")
			return errExitQuiet
		}
		ref, err := auditor.Start(ctx, paper)
		if err != nil {
			return err
		}
		encoded, err := encodeSessionRef(ref)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "session:   %s\n", ref.ID)
		fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", pub.repoFullName())
		fmt.Fprintf(stdout, "finish:    replichai finish %s %q --session-ref %s%s\n",
			pub.repoFullName(), paper, encoded, inviteFlag(*invite))
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
	reportInstrument(stdout, result.InstrumentPresent, result.InstrumentViolations)
	fmt.Fprintf(stdout, "elapsed:   %s\n", time.Since(started).Round(time.Second))

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

func encodeSessionRef(ref managed.SessionRef) (string, error) {
	raw, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeSessionRef(encoded string) (managed.SessionRef, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("decode --session-ref: %w", err)
	}
	var ref managed.SessionRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return managed.SessionRef{}, fmt.Errorf("decode --session-ref: %w", err)
	}
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		return managed.SessionRef{}, err
	}
	return ref, nil
}

func providerEventsFromManaged(events []managed.Event) []provider.Event {
	result := make([]provider.Event, 0, len(events))
	for _, event := range events {
		translated := provider.Event{
			Role: provider.Role(event.Role), Kind: provider.EventKind(event.Kind), Tool: event.Tool,
			Content: event.Content, Time: event.Time, Raw: event.Raw,
		}
		if event.Usage != nil {
			translated.Usage = &provider.Usage{
				InputTokens: event.Usage.InputFresh, CachedInputTokens: event.Usage.InputCached,
				CacheWriteTokens: event.Usage.CacheWrite, CacheWrite5mTokens: event.Usage.CacheWrite5m,
				CacheWrite1hTokens: event.Usage.CacheWrite1h, OutputTokens: event.Usage.Output,
			}
		}
		result = append(result, translated)
	}
	return result
}

// newPublisher resolves the paper, warns about any prior reproduction, allocates
// a collision-free name through the registry, and creates the private
// repository. The GitHub token comes from REPLICHAI_GITHUB_TOKEN.
func newPublisher(ctx context.Context, owner, paperRef, methodDigest string, timeout time.Duration, stderr *os.File) (*publisher, error) {
	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("--publish needs REPLICHAI_GITHUB_TOKEN set")
	}
	id, err := paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: timeout}))
	if err != nil {
		return nil, err
	}

	gh := github.New(token)
	reg := registry.New(registry.NewGitHubSource(gh, owner),
		registry.WithWarner(func(m string) { fmt.Fprintf(stderr, "  %s\n", m) }))

	// Tell the operator about any prior reproduction before a sibling is created.
	priors, err := reg.Lookup(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("check prior reproductions: %w", err)
	}
	if len(priors) > 0 {
		fmt.Fprintf(stderr, "  ! %s reproduced before:\n", id)
		for _, p := range priors {
			note := ""
			if p.MethodDigest == methodDigest {
				note = " — same method, re-running"
			}
			fmt.Fprintf(stderr, "      %s (%s · %s · %s)%s\n",
				p.Repo, p.Created.Format("2006-01-02"), shortDigest(p.MethodDigest), p.Verdict, note)
		}
	}

	name, err := reg.AllocateName(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("allocate name: %w", err)
	}

	repo, err := gh.CreateRepo(ctx, owner, name, true)
	if errors.Is(err, github.ErrRepoExists) {
		repo, err = gh.GetRepo(ctx, owner, name)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare repository: %w", err)
	}

	// Record the resolved identity into the repository so finish reads it from
	// here rather than re-resolving over the (slow, rate-limited) paper source.
	if err := publish.RecordIdentity(ctx, gh, owner, name, id); err != nil {
		fmt.Fprintf(stderr, "  could not record identity (finish will re-resolve): %v\n", err)
	}

	return &publisher{
		gh:    gh,
		owner: owner,
		name:  name,
		id:    id,
		mount: provider.RepoMount{URL: repo.HTMLURL, Token: token, MountPath: audit.DefaultMountPath},
	}, nil
}

// readRecordedIdentity returns the identity the publisher recorded in the repo,
// or a zero identity when none is present (an older or hand-created repository),
// so the caller falls back to re-resolving.
func readRecordedIdentity(ctx context.Context, gh *github.Client, owner, name string) (paper.Identity, error) {
	id, _, err := publish.RecordedIdentity(ctx, gh, owner, name)
	return id, err
}

// shortDigest abbreviates "sha256:1f2e3a…" to "sha256:1f2e" for operator output.
func shortDigest(d string) string {
	if i := strings.IndexByte(d, ':'); i >= 0 && len(d) > i+5 {
		return d[:i+5]
	}
	return d
}

// finalizePublication verifies the committed tree, writes replication.json,
// commits the transcript, and invites the collaborator — the system-authored
// attestation (verdict + evidence) beside the agent's work. It reads the
// repository from GitHub, so it works whether the run finished in-process or is
// picked up afterwards by finish.
func finalizePublication(ctx context.Context, gh *github.Client, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, events []provider.Event, invite string, stdout *os.File) error {
	repoFull := owner + "/" + name

	// The instrument check is a CLI-only report (007a), read from the committed
	// tree — a report, not a gate, so it stays outside the shared finalize.
	present, iviol, err := verifyInstrument(ctx, gh, owner, name)
	if err != nil {
		return err
	}

	// The finalize itself — verify the tree, write the manifest, commit the
	// transcript — is the shared path the server also drives, so the two cannot
	// drift. The CLI only reports what it produced.
	rep, err := publish.FinalizeLive(ctx, gh, owner, name, id, methodDigest, verdict, events, time.Now)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", repoFull)
	fmt.Fprintf(stdout, "commit:    %s\n", rep.Head)
	if !rep.ManifestCommitted {
		fmt.Fprintln(stdout, "manifest:  already current (no new commit)")
	}
	if len(rep.Leaked) == 0 {
		fmt.Fprintln(stdout, "committed: clean (only worker-written files)")
	} else {
		fmt.Fprintf(stdout, "committed: %d third-party path(s) leaked — repo kept private:\n", len(rep.Leaked))
		for _, path := range rep.Leaked {
			fmt.Fprintf(stdout, "  - %s\n", path)
		}
	}
	reportInstrument(stdout, present, iviol)

	if rep.TranscriptEvents > 0 {
		if rep.TranscriptCommitted {
			fmt.Fprintf(stdout, "transcript: %d events across %d file(s) committed\n", rep.TranscriptEvents, rep.TranscriptFiles)
		} else {
			fmt.Fprintln(stdout, "transcript: already current (no new commit)")
		}
	}
	if len(rep.TranscriptLeaks) > 0 {
		fmt.Fprintf(stdout, "transcript: %d event(s) carry a secret — bars the public flip:\n", len(rep.TranscriptLeaks))
		for _, l := range rep.TranscriptLeaks {
			fmt.Fprintf(stdout, "  - event %d: %s\n", l.Index, l.Reason)
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

// verifyInstrument reads instrument.json from the committed tree and checks it
// against the 007a schema, mirroring the auditor's finalize-time report for the
// live mount path. Absence is not an error — criterion 7 fails it in the run's
// grade-and-revise loop; this only reports what reached the repository.
func verifyInstrument(ctx context.Context, gh *github.Client, owner, name string) (present bool, violations []instrument.Violation, err error) {
	raw, ok, err := gh.GetFileContent(ctx, owner, name, audit.InstrumentFile)
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", audit.InstrumentFile, err)
	}
	if !ok {
		return false, nil, nil
	}
	vs, verr := instrument.Validate(raw)
	if verr != nil {
		return true, []instrument.Violation{{Field: audit.InstrumentFile, Message: verr.Error()}}, nil
	}
	return true, vs, nil
}

// reportInstrument prints the finalize-time instrument check as one status line,
// with any schema violations beneath it. It is a report, not a gate — a bad
// instrument does not block a publish; criterion 7 handles it in the run.
func reportInstrument(stdout *os.File, present bool, violations []instrument.Violation) {
	switch {
	case !present:
		fmt.Fprintln(stdout, "instrument: none written (criterion 7 fails the run)")
	case len(violations) == 0:
		fmt.Fprintln(stdout, "instrument: valid")
	default:
		fmt.Fprintf(stdout, "instrument: %d schema violation(s):\n", len(violations))
		for _, v := range violations {
			fmt.Fprintf(stdout, "  - %s\n", v)
		}
	}
}

// runFinish reattaches to a still-running or finished audit session, waits for
// it to settle, then finalizes the publication of an already-created repository.
// This is the recovery path when `audit --publish` times out while the agent
// keeps committing to the mounted repo.
func runFinish(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("finish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		session      = fs.String("session", "", "legacy session id to reattach to")
		sessionRef   = fs.String("session-ref", "", "complete session reference printed by audit --detach")
		providerName = fs.String("provider", defaultCLIRuntime, "managed runtime for a legacy --session id")
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
	var verdictOverride provider.Grade
	if *verdictFlag != "" {
		if err := verdictOverride.UnmarshalJSON([]byte(`"` + *verdictFlag + `"`)); err != nil {
			return fmt.Errorf("invalid --verdict %q: %w", *verdictFlag, err)
		}
	}

	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("finish needs REPLICHAI_GITHUB_TOKEN set")
	}
	gh := github.New(token)

	// Prefer the identity the publisher recorded in the repository; only fall
	// back to re-resolving (which hits the paper source) when it is absent.
	id, err := readRecordedIdentity(ctx, gh, owner, name)
	if err != nil {
		return err
	}
	if id.Key == "" {
		if id, err = paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: 30 * time.Second})); err != nil {
			return err
		}
	}

	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}

	verdict := provider.GradeUnknown
	if *sessionRef != "" || *session != "" {
		if *sessionRef != "" && *session != "" {
			return errors.New("set only one of --session-ref or legacy --session")
		}
		var ref managed.SessionRef
		rawRuntime := *providerName
		if *sessionRef != "" {
			ref, err = decodeSessionRef(*sessionRef)
			rawRuntime = string(ref.Runtime)
			if err != nil {
				return err
			}
		}
		controller, runtime, err := controllerFor(ctx, rawRuntime, *interval)
		if err != nil {
			return err
		}
		if *sessionRef == "" {
			ref = managed.SessionRef{Runtime: runtime.Name(), ID: *session, Revision: 1, LocatorVersion: 1}
		}
		hooks := managed.Hooks{
			Status: func(_ context.Context, status managed.Status) error {
				fmt.Fprintf(stderr, "  %s\n", status.State)
				return nil
			},
			Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
				if evidence.Status.Verdict != nil {
					verdict = provider.Grade(evidence.Status.Verdict.Grade)
				}
				return finalizePublication(ctx, gh, owner, name, id, m.Digest(), verdict, providerEventsFromManaged(evidence.Events), *invite, stdout)
			},
		}
		destination := filepath.Join(os.TempDir(), "replichai-finish-"+ref.ID)
		_, err = audit.NewSessionAuditor(controller, m,
			audit.WithSessionHooks(hooks), audit.WithSessionTimeout(*timeout),
		).Resume(ctx, ref, paperRef, destination)
		return err
	} else if *verdictFlag != "" {
		verdict = verdictOverride
	}

	return finalizePublication(ctx, gh, owner, name, id, m.Digest(), verdict, nil, *invite, stdout)
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

// httpFetcher is the resolver's network seam at the CLI edge: an HTTP GET that
// retries transient failures. The internal/paper package stays network-free and
// testable; the real transport lives here. Retry matters because arXiv's
// metadata API is slow and rate-limited, and a single timeout there would
// otherwise fail an audit's identity resolution.
type httpFetcher struct{ timeout time.Duration }

// fetchAttempts is how many times a fetch is tried before giving up.
const fetchAttempts = 3

func (h httpFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := range fetchAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second): // linear backoff
			}
		}
		body, retryable, err := h.fetchOnce(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

// fetchOnce performs one GET. retryable is true for a timeout, a network error,
// or a 429/5xx — the failures a retry can recover from.
func (h httpFetcher) fetchOnce(ctx context.Context, url string) (body []byte, retryable bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "replichai")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b, false, err
}

func usage(w *os.File) {
	fmt.Fprint(w, `replichai - audit the reproducibility of a research paper

usage:
  replichai audit <paper-ref> [flags]
  replichai resolve <paper-ref>
  replichai publish <artifact-dir> <paper-ref> [flags]
  replichai finish <owner>/<repo> <paper-ref> --session <id> [flags]
  replichai providers
  replichai rates [flags]

a paper-ref is a DOI, an arXiv URL or bare ID, a publisher URL, or a PDF URL

flags for audit:
  -out            directory for artifacts (default "runs")
  -provider       managed runtime to use (default "anthropic")
  -method         path to a method directory
  -interval       gap between status checks
  -timeout        how long a run may take
  -max-revisions  revision budget
  -publish        mount a GitHub repo and publish the reproduction to it
  -owner          GitHub owner for -publish (default "`+publish.DefaultOrg+`")
  -invite         GitHub login to invite as a collaborator
  -detach         with -publish, start the run and return; finish it later

flags for rates:
  -provider       managed runtime to ask (default "anthropic")
  -database-url   read the live rate card from a deployment (default $DATABASE_URL)
  -json           machine-readable output

flags for finish:
  -session        session id to reattach to and wait for
  -session-ref    complete session reference printed by audit --detach
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
