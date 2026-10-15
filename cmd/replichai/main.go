// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command replichai audits the reproducibility of research papers.
//
// This package owns argument parsing and output. All behavior lives in
// internal/audit, so an HTTP server can drive the same code path.
//
// Every write to stdout or stderr here discards its error explicitly. A failed
// write to the terminal cannot be recovered from or reported, because the
// terminal is the only place a report could go. The discard is written at each
// call rather than hidden in a helper, so a reader can see the decision.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"latere.ai/x/pkg/retry"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/cloud"
	"github.com/latere-ai/replichai/internal/fetch"
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
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}
}

// errExitQuiet signals a non-zero exit whose reason was already reported.
var errExitQuiet = errors.New("exit")

// checkRevisions refuses a negative revision budget. Zero is a budget: the run
// is graded once and never revised, which is how a study keeps what a run
// produced before the rubric pushed it.
func checkRevisions(maximum int, stderr *os.File) error {
	if maximum >= 0 {
		return nil
	}
	_, _ = fmt.Fprintf(stderr, "--max-revisions is %d; it is how many times a run may revise, and 0 means never\n", maximum)
	return errExitQuiet
}

func runAudit(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("audit", stderr)
	var (
		out          = fs.String("out", "runs", "directory for artifacts")
		runtimeName  = runtimeFlag(fs, defaultCLIRuntime)
		methodDir    = fs.String("method", "", "path to a method directory")
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "time between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "maximum time for the run")
		maxRevisions = fs.Int("max-revisions", audit.DefaultMaxRevisions, "maximum number of revisions; 0 grades the run once and never revises it")
		doPublish    = fs.Bool("publish", false, "create a GitHub repository and publish the reproduction to it")
		owner        = fs.String("owner", envOr("REPLICHAI_GITHUB_OWNER", publish.DefaultOrg), "GitHub owner for --publish")
		invite       = fs.String("invite", "", "GitHub login to invite as a collaborator")
		projectID    = fs.String("project", "", "with --publish: the project the platform creates the repository under")
		detach       = fs.Bool("detach", false, "with --publish: start the run and exit; complete it later with finish")
		model        = fs.String("model", audit.DefaultModelPreference, "model to run on: an Effort level (quick, thorough, sustained, or fast, balanced, best) or one model as provider/id")
		local        localOptions
	)
	local.bind(fs)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stderr, "audit takes exactly one paper reference")
		return errExitQuiet
	}
	if err := checkRevisions(*maxRevisions, stderr); err != nil {
		return err
	}
	paper := positional[0]

	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}
	controller, runtime, err := controllerFor(ctx, resolveRuntime(*runtimeName, os.Getenv, defaultCLIRuntime), *interval, local)
	if err != nil {
		return err
	}

	hooks := managed.Hooks{Status: func(_ context.Context, status managed.Status) error {
		_, _ = fmt.Fprintf(stderr, "  %s\n", status.State)
		return nil
	}}
	opts := []audit.SessionOption{
		audit.WithSessionModel(*model),
		audit.WithSessionMaxRevisions(*maxRevisions),
		audit.WithSessionTimeout(*timeout),
		audit.WithSessionHooks(hooks),
	}

	// When publishing, the repository is created up front in both cases.
	// Whether it is also mounted depends on the runtime. A runtime that can
	// take a writable repository has the agent commit and push into it as it
	// runs (006f). One that cannot is given a local repository with no remote,
	// and the host pushes what was committed once the session has ended (045g).
	// The choice follows the reported capability rather than the runtime's
	// name, so a future runtime that can mount takes the same branch.
	var pub *publisher
	if *doPublish {
		pub, err = publisherFor(ctx, *projectID, *owner, paper, m.Digest(), *invite, *timeout, stderr)
		if err != nil {
			return err
		}
		defer pub.done(context.WithoutCancel(ctx), stderr)
		capabilities, capErr := runtime.Capabilities(ctx)
		if capErr != nil {
			return capErr
		}
		mounts := capabilities.RepositoryModes[managed.AccessReadWrite]
		_, keepsWorkspace := runtime.(workspaceReader)
		if mounts {
			// The agent is about to clone this repository, so the identity
			// must be recorded in it before the clone.
			pub.recordIdentity(ctx, stderr)
			opts = append(opts, audit.WithSessionMount(audit.SessionMount{
				URL: pub.mount.URL, Token: pub.mount.Token, MountPath: pub.mount.MountPath, ReadOnly: pub.mount.ReadOnly,
			}))
		} else if keepsWorkspace {
			opts = append(opts, audit.WithLocalWorkspace())
		}
		hooks.Retain = func(ctx context.Context, evidence managed.EvidenceReader) error {
			// The order is capture, verify, push, finalize, and only then
			// release. The workspace holds the only copy of the work until
			// the push has taken it.
			if !mounts {
				workspace, err := hostWorkspace(ctx, runtime, evidence, filepath.Join(*out, slugify(paper)+"-workspace-"+evidence.Status.Ref.ID))
				if err != nil {
					return err
				}
				if err := pushWorkspace(ctx, pub, workspace, paper, evidence.Status.Ref, stdout, stderr); err != nil {
					return err
				}
				// The remote's history now descends from the workspace, so a
				// commit made here is a commit on top of the reproduction.
				pub.recordIdentity(ctx, stderr)
			}
			grade := provider.GradeUnknown
			if evidence.Status.Verdict != nil {
				grade = provider.Grade(evidence.Status.Verdict.Grade)
			}
			record := runRecord(runtime, evidence.Status, *model, audit.PromptDigest(ctx), stderr).WithModels(evidence.Events)
			if err := finalizePublication(ctx, pub.gh, pub.owner, pub.name, pub.id, m.Digest(), grade, record,
				providerEventsFromManaged(evidence.Events), *invite, stdout); err != nil {
				return err
			}
			// The work the credential was minted for is done, so it is revoked
			// now rather than left to expire within the hour.
			pub.done(ctx, stderr)
			return nil
		}
		opts = append(opts, audit.WithSessionHooks(hooks))
		_, _ = fmt.Fprintf(stderr, "  publishing to %s\n", pub.repoFullName())
	}

	auditor := audit.NewSessionAuditor(controller, m, opts...)

	// In detached mode the agent commits to the mounted repository on its
	// own, so the process need not stay open for the whole run. The session
	// is started, the finish command is printed, and the function returns.
	// The finish subcommand reattaches and finalizes later.
	if *detach {
		if pub == nil {
			_, _ = fmt.Fprintln(stderr, "--detach needs --publish")
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
		_, _ = fmt.Fprintf(stdout, "session:   %s\n", ref.ID)
		_, _ = fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", pub.repoFullName())
		_, _ = fmt.Fprintf(stdout, "finish:    replichai finish %s %q --session-ref %s%s\n",
			pub.repoFullName(), paper, encoded, inviteFlag(*invite))
		return nil
	}

	dst := filepath.Join(*out, slugify(paper))
	started := time.Now()
	result, err := auditor.Run(ctx, paper, dst)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "verdict:   %s\n", result.Grade())
	_, _ = fmt.Fprintf(stdout, "artifacts: %d -> %s\n", len(result.Artifacts), result.ArtifactDir)
	reportInstrument(stdout, result.InstrumentPresent, result.InstrumentViolations)
	_, _ = fmt.Fprintf(stdout, "elapsed:   %s\n", time.Since(started).Round(time.Second))

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
	// minted is the platform-issued credential, when the platform issued one
	// (045l). It is revoked when the work it was minted for is done. The token
	// would expire within the hour regardless, but revoking it marks completion
	// explicitly rather than relying on expiry.
	minted string
	api    *cloud.Client
}

// done revokes the platform-issued credential. If the process is killed before
// this runs, the token is left to expire on its own, which the design accepts.
func (p *publisher) done(ctx context.Context, stderr *os.File) {
	if p.minted == "" {
		return
	}
	if err := p.gh.RevokeToken(ctx, p.minted); err != nil {
		_, _ = fmt.Fprintf(stderr, "  could not revoke the push credential (it expires on its own): %v\n", err)
	}
	p.minted = ""
}

func (p *publisher) repoFullName() string { return p.owner + "/" + p.name }

// recordIdentity writes the resolved identity into the repository, so that
// finish reads it from there rather than resolving the paper source again.
//
// The timing matters for correctness. A mounted run clones the repository, so
// its history descends from whatever is recorded before the clone, and this
// must run first. A local run commits into a fresh repository with no remote
// (045g), so a commit made here before the push gives the remote a history the
// workspace does not have, and the push is refused as a non-fast-forward. That
// would fail every local --publish run after the agent's work is done. The
// caller therefore decides when to call this.
//
// A failure is reported and is not fatal: finish resolves the paper again when
// the file is absent, and a lost lookup shortcut must not lose a publication.
func (p *publisher) recordIdentity(ctx context.Context, stderr *os.File) {
	if err := publish.RecordIdentity(ctx, p.gh, p.owner, p.name, p.id); err != nil {
		_, _ = fmt.Fprintf(stderr, "  could not record identity (finish will re-resolve): %v\n", err)
	}
}

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

// newPlatformPublisher asks the platform for the repository and the credential
// to push to it (045l). This path leaves no organization credential on this
// machine: the token it returns can write to one repository, expires within
// the hour, and is revoked when the push is done.
//
// A deployment that cannot mint reports so, and the caller falls back to the
// credential this machine holds. The reason is printed, because a security
// property that silently stops holding is worse than one that was never
// claimed.
func newPlatformPublisher(ctx context.Context, projectID, paperRef string, timeout time.Duration, stderr *os.File) (*publisher, error) {
	client, err := apiClient()
	if err != nil {
		return nil, err
	}
	id, err := paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: timeout}))
	if err != nil {
		return nil, err
	}
	// A session may only run against a paper the project holds, so the paper
	// is filed before its repository is requested. push keeps the same order,
	// and filing is idempotent for the same reason.
	if err := client.AddPaper(ctx, projectID, paperRef); err != nil {
		return nil, err
	}
	// The key is derived offline, the same way the app derives it when it
	// files the paper. paper.Resolve falls back to an unqualified key when the
	// paper source is unreachable, so a lookup key derived from it would depend
	// on the network being up.
	key, err := paper.Key(paperRef)
	if err != nil {
		return nil, err
	}
	allocated, err := client.ProvisionRepository(ctx, projectID, key)
	if err != nil {
		return nil, err
	}
	owner, name, _ := strings.Cut(allocated.Repo, "/")
	gh := githubClient(allocated.Token)
	_, _ = fmt.Fprintf(stderr, "  repository %s, credential expires %s\n",
		allocated.Repo, allocated.Expires.Format(time.RFC3339))
	return &publisher{
		gh: gh, owner: owner, name: name, id: id, minted: allocated.Token, api: client,
		mount: provider.RepoMount{URL: allocated.URL, Token: allocated.Token, MountPath: audit.DefaultMountPath},
	}, nil
}

// newPublisher resolves the paper, warns about any prior reproduction, allocates
// a collision-free name through the registry, and creates the private
// repository. The GitHub token comes from REPLICHAI_GITHUB_TOKEN.
func newPublisher(ctx context.Context, owner, paperRef, methodDigest string, timeout time.Duration, stderr *os.File) (*publisher, error) {
	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("--publish needs REPLICHAI_GITHUB_TOKEN set, or --project so the platform can provide one")
	}
	id, err := paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: timeout}))
	if err != nil {
		return nil, err
	}

	gh := githubClient(token)
	reg := registry.New(registry.NewGitHubSource(gh, owner),
		registry.WithWarner(func(m string) { _, _ = fmt.Fprintf(stderr, "  %s\n", m) }))

	// Report any prior reproduction to the operator before a sibling is created.
	priors, err := reg.Lookup(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("check prior reproductions: %w", err)
	}
	if len(priors) > 0 {
		_, _ = fmt.Fprintf(stderr, "  ! %s has been reproduced before:\n", id)
		for _, p := range priors {
			note := ""
			if p.MethodDigest == methodDigest {
				note = ", same method as this run"
			}
			_, _ = fmt.Fprintf(stderr, "      %s (%s · %s · %s)%s\n",
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

	return &publisher{
		gh:    gh,
		owner: owner,
		name:  name,
		id:    id,
		mount: provider.RepoMount{URL: repo.HTMLURL, Token: token, MountPath: audit.DefaultMountPath},
	}, nil
}

// reattachPublisher returns to a repository an earlier attempt created, so a
// resumed paper publishes into it rather than into a sibling. The identity is
// read from the repository where the first attempt recorded it, and resolved
// again only when it was not recorded. A repository that no longer exists is
// an error rather than a fresh allocation: the operator asked for a retry into
// this one, and --resume exists to prevent a second repository for the same
// paper.
func reattachPublisher(ctx context.Context, repo, paperRef string, timeout time.Duration) (*publisher, error) {
	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("--publish needs REPLICHAI_GITHUB_TOKEN set to return to %s", repo)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("the recorded repository %q is not <owner>/<repo>", repo)
	}
	gh := githubClient(token)
	found, err := gh.GetRepo(ctx, owner, name)
	if err != nil {
		return nil, fmt.Errorf("return to %s: %w", repo, err)
	}
	id, err := readRecordedIdentity(ctx, gh, owner, name)
	if err != nil {
		return nil, err
	}
	if id.Key == "" {
		if id, err = paper.Resolve(ctx, paperRef, paper.WithFetcher(httpFetcher{timeout: timeout})); err != nil {
			return nil, err
		}
	}
	return &publisher{
		gh: gh, owner: owner, name: name, id: id,
		mount: provider.RepoMount{URL: found.HTMLURL, Token: token, MountPath: audit.DefaultMountPath},
	}, nil
}

// readRecordedIdentity returns the identity the publisher recorded in the
// repository. It returns a zero identity when none is present, as in an older
// or hand-created repository, so the caller falls back to resolving again.
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

// workspaceReader is a runtime that can name the directory a referenced
// session worked in. Only a local runtime can: a hosted one has no directory.
type workspaceReader interface {
	Workspace(managed.SessionRef) (string, error)
}

// workspaceOf asks the runtime where the referenced session worked. The
// reference names the session and the session names its directory, so the
// push reads from the session that ran, never from a directory named before
// the run, which under a pool of concurrent sessions could belong to another
// session. Only a runtime with a local directory can answer; a hosted one has
// none, and its work leaves through the mount instead.
func workspaceOf(rt managed.Runtime, ref managed.SessionRef) (string, error) {
	located, ok := rt.(workspaceReader)
	if !ok {
		return "", fmt.Errorf("runtime %s keeps no local workspace to push from", rt.Name())
	}
	return located.Workspace(ref)
}

// hostWorkspace is the local repository the host pushes a session's work
// from. A local runtime names the directory the session worked in. A hosted
// runtime that mounted nothing, such as the Latere platform, keeps its files
// on its side, so they are fetched while the session is still held and
// committed once, by the host, into dir.
func hostWorkspace(ctx context.Context, rt managed.Runtime, evidence managed.EvidenceReader, dir string) (string, error) {
	if _, ok := rt.(workspaceReader); ok {
		return workspaceOf(rt, evidence.Status.Ref)
	}
	files := make(map[string][]byte, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		body, err := evidence.Fetch(ctx, artifact)
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", artifact.Path, err)
		}
		files[artifact.Path] = body
	}
	message := fmt.Sprintf("the work of %s session %s, as it left its working directory", rt.Name(), evidence.Status.Ref.ID)
	if err := publish.Materialize(ctx, dir, files, message); err != nil {
		return "", err
	}
	return dir, nil
}

// pushWorkspace publishes what the agent committed into a local repository
// that had no remote. The system's own outputs, the environment record and
// the judge's verdict, are committed after the push, so the agent's history
// and the system's evidence stay distinguishable.
func pushWorkspace(ctx context.Context, pub *publisher, workspace, paperRef string, ref managed.SessionRef, stdout, stderr *os.File) error {
	push := publish.LocalPush{Workspace: workspace, RemoteURL: pub.mount.URL, Token: pub.mount.Token}
	pushed, err := push.Run(ctx)
	if errors.Is(err, publish.ErrNothingCommitted) {
		// This is the outcome 044d describes, detected before the push
		// rather than found as an empty repository afterwards. The finalize
		// still runs and records that the run produced nothing.
		_, _ = fmt.Fprintln(stderr, "  ! the run committed nothing; the repository will record that")
		return nil
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "  ! %v\n", err)
		if errors.Is(err, publish.ErrNoWorkspace) {
			// No workspace was kept, so there is nothing to retry from and
			// no retry command is printed.
			return err
		}
		_, _ = fmt.Fprintf(stderr, "    the workspace is kept at %s\n", workspace)
		if encoded, encodeErr := encodeSessionRef(ref); encodeErr == nil && ref.ID != "" {
			_, _ = fmt.Fprintf(stderr, "    retry with:\n      replichai finish %s %q --runtime local --session-ref %s\n",
				pub.repoFullName(), paperRef, encoded)
		}
		return err
	}
	_, _ = fmt.Fprintf(stdout, "pushed:    %d commit(s) to %s (%s)\n", pushed.Commits, pushed.Branch, pushed.Head[:min(7, len(pushed.Head))])

	outputs, err := publish.WorkspaceOutputs(filepath.Dir(workspace))
	if err != nil || len(outputs) == 0 {
		return err
	}
	report, err := publish.CommitOutputs(ctx, pub.gh, pub.owner, pub.name, outputs)
	if err != nil {
		return err
	}
	if len(report.Committed) > 0 {
		_, _ = fmt.Fprintf(stdout, "evidence:  %s\n", strings.Join(report.Committed, ", "))
	}
	return nil
}

// finishLocalPush pushes a local session's workspace during finish. It is a
// no-op for a runtime that mounted a repository: the agent pushed as it went,
// and there is nothing here to send.
func finishLocalPush(ctx context.Context, runtime managed.Runtime, gh *github.Client, owner, name string,
	ref managed.SessionRef, stdout, stderr *os.File) error {
	reader, ok := runtime.(workspaceReader)
	if !ok {
		return nil
	}
	workspace, err := reader.Workspace(ref)
	if err != nil {
		return err
	}
	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	pub := &publisher{gh: gh, owner: owner, name: name,
		mount: provider.RepoMount{URL: "https://github.com/" + owner + "/" + name + ".git", Token: token}}
	return pushWorkspace(ctx, pub, workspace, "", ref, stdout, stderr)
}

// finalizePublication verifies the committed tree, writes replication.json,
// commits the transcript, and invites the collaborator. Together these place
// the system-authored attestation, the verdict and its evidence, beside the
// agent's work. It reads the repository from GitHub, so it works whether the
// run finished in-process or is picked up afterwards by finish.
func finalizePublication(ctx context.Context, gh *github.Client, owner, name string, id paper.Identity, methodDigest string, verdict provider.Grade, run publish.Run, events []provider.Event, invite string, stdout *os.File) error {
	repoFull := owner + "/" + name

	// The instrument check is a CLI-only report (007a) read from the committed
	// tree. It does not gate the publish, so it stays outside the shared
	// finalize.
	present, iviol, err := verifyInstrument(ctx, gh, owner, name)
	if err != nil {
		return err
	}

	// The finalize itself, which verifies the tree, writes the manifest, and
	// commits the transcript, is the shared path the server also drives, so
	// the two cannot drift. The CLI only reports what it produced.
	rep, err := publish.FinalizeLive(ctx, gh, owner, name, id, methodDigest, verdict, run, events, time.Now)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "repo:      https://github.com/%s\n", repoFull)
	_, _ = fmt.Fprintf(stdout, "commit:    %s\n", rep.Head)
	if !rep.ManifestCommitted {
		_, _ = fmt.Fprintln(stdout, "manifest:  already current (no new commit)")
	}
	if len(rep.Leaked) == 0 {
		_, _ = fmt.Fprintln(stdout, "committed: clean (only worker-written files)")
	} else {
		_, _ = fmt.Fprintf(stdout, "committed: %d third-party path(s) leaked; the repository stays private:\n", len(rep.Leaked))
		for _, path := range rep.Leaked {
			_, _ = fmt.Fprintf(stdout, "  - %s\n", path)
		}
	}
	reportInstrument(stdout, present, iviol)

	if rep.TranscriptEvents > 0 {
		if rep.TranscriptCommitted {
			_, _ = fmt.Fprintf(stdout, "transcript: %d events across %d file(s) committed\n", rep.TranscriptEvents, rep.TranscriptFiles)
		} else {
			_, _ = fmt.Fprintln(stdout, "transcript: already current (no new commit)")
		}
	}
	if len(rep.TranscriptLeaks) > 0 {
		_, _ = fmt.Fprintf(stdout, "transcript: %d event(s) contain a secret, so the repository cannot be made public:\n", len(rep.TranscriptLeaks))
		for _, l := range rep.TranscriptLeaks {
			_, _ = fmt.Fprintf(stdout, "  - event %d: %s\n", l.Index, l.Reason)
		}
	}

	if invite != "" {
		if err := gh.Invite(ctx, owner, name, invite); err != nil {
			return fmt.Errorf("invite %s: %w", invite, err)
		}
		_, _ = fmt.Fprintf(stdout, "invited:   %s\n", invite)
	}
	return nil
}

// verifyInstrument reads instrument.json from the committed tree and checks it
// against the 007a schema, mirroring the auditor's finalize-time report for the
// live mount path. A missing file is not an error: the rubric's Instrument
// criterion fails it in the run's grade-and-revise loop, and this only reports what reached the
// repository.
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
		return true, []instrument.Violation{{Field: audit.InstrumentFile, Message: verr.Error()}}, nil //nolint:nilerr // a malformed instrument is one violation to report, not a failed check
	}
	return true, vs, nil
}

// reportInstrument prints the finalize-time instrument check as one status line,
// with any schema violations beneath it. It does not gate the publish: a bad
// instrument does not block it, because the rubric's Instrument criterion
// handles it in the run.
func reportInstrument(stdout *os.File, present bool, violations []instrument.Violation) {
	switch {
	case !present:
		_, _ = fmt.Fprintln(stdout, "instrument: none written (the rubric's Instrument criterion requires one)")
	case len(violations) == 0:
		_, _ = fmt.Fprintln(stdout, "instrument: valid")
	default:
		_, _ = fmt.Fprintf(stdout, "instrument: %d schema violation(s):\n", len(violations))
		for _, v := range violations {
			_, _ = fmt.Fprintf(stdout, "  - %s\n", v)
		}
	}
}

// runFinish reattaches to a still-running or finished audit session, waits for
// it to settle, then finalizes the publication of an already-created repository.
// This is the recovery path when `audit --publish` times out while the agent
// keeps committing to the mounted repo.
func runFinish(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("finish", stderr)
	var (
		session     = fs.String("session", "", "legacy session id to reattach to")
		sessionRef  = fs.String("session-ref", "", "session reference printed by audit --detach")
		runtimeName = runtimeFlag(fs, defaultCLIRuntime)
		methodDir   = fs.String("method", "", "path to the method directory the audit used")
		invite      = fs.String("invite", "", "GitHub login to invite as a collaborator")
		verdictFlag = fs.String("verdict", "", "verdict to record when no session is given")
		interval    = fs.Duration("interval", audit.DefaultPollInterval, "time between status checks")
		timeout     = fs.Duration("timeout", audit.DefaultTimeout, "maximum time to wait for the session")
		local       localOptions
	)
	local.bind(fs)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		_, _ = fmt.Fprintln(stderr, "finish takes <owner>/<repo> and a paper reference")
		return errExitQuiet
	}
	owner, name, ok := strings.Cut(positional[0], "/")
	if !ok {
		_, _ = fmt.Fprintln(stderr, "the repository must be <owner>/<repo>")
		return errExitQuiet
	}
	paperRef := positional[1]
	var verdictOverride provider.Grade
	if *verdictFlag != "" {
		if err := verdictOverride.UnmarshalJSON([]byte(`"` + *verdictFlag + `"`)); err != nil {
			return fmt.Errorf("invalid --verdict %q: %w", *verdictFlag, err)
		}
	}
	// The arguments are checked before anything is fetched, so a malformed
	// reference is refused here rather than after a round trip to the forge.
	if *sessionRef != "" && *session != "" {
		return errors.New("set only one of --session-ref or legacy --session")
	}
	var ref managed.SessionRef
	if *sessionRef != "" {
		var err error
		if ref, err = decodeSessionRef(*sessionRef); err != nil {
			return err
		}
	}

	token := os.Getenv("REPLICHAI_GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("finish needs REPLICHAI_GITHUB_TOKEN set")
	}
	gh := githubClient(token)

	// The identity the publisher recorded in the repository is preferred. Only
	// when it is absent is the paper resolved again, which reads the paper
	// source.
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
		rawRuntime := resolveRuntime(*runtimeName, os.Getenv, defaultCLIRuntime)
		if *sessionRef != "" {
			rawRuntime = string(ref.Runtime)
		}
		controller, runtime, err := controllerFor(ctx, rawRuntime, *interval, local)
		if err != nil {
			return err
		}
		if *sessionRef == "" {
			ref = managed.SessionRef{Runtime: runtime.Name(), ID: *session, Revision: 1, LocatorVersion: 1}
		}
		hooks := managed.Hooks{
			Status: func(_ context.Context, status managed.Status) error {
				_, _ = fmt.Fprintf(stderr, "  %s\n", status.State)
				return nil
			},
			Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
				// A session that was never mounted has committed into a
				// directory that has not been pushed. Finalizing without
				// pushing would verify an empty repository and record the
				// outcome 044d describes for a run that produced a
				// reproduction.
				if err := finishLocalPush(ctx, runtime, gh, owner, name, evidence.Status.Ref, stdout, stderr); err != nil {
					return err
				}
				if evidence.Status.Verdict != nil {
					verdict = provider.Grade(evidence.Status.Verdict.Grade)
				}
				// The model that was asked for and the prompt the session was
				// given were fixed by the process that started it, so neither
				// is known here and neither is recorded.
				record := runRecord(runtime, evidence.Status, "", audit.PromptDigest(ctx), stderr)
				return finalizePublication(ctx, gh, owner, name, id, m.Digest(), verdict, record, providerEventsFromManaged(evidence.Events), *invite, stdout)
			},
		}
		destination := finishDestination(ref)
		result, err := audit.NewSessionAuditor(controller, m,
			audit.WithSessionHooks(hooks), audit.WithSessionTimeout(*timeout),
		).Resume(ctx, ref, paperRef, destination)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "artifacts: %d -> %s\n", len(result.Artifacts), destination)
		return nil
	} else if *verdictFlag != "" {
		verdict = verdictOverride
	}

	// No session is in hand, so nothing is known about the run. The manifest
	// keeps whatever an earlier finalize recorded.
	return finalizePublication(ctx, gh, owner, name, id, m.Digest(), verdict, publish.Run{}, nil, *invite, stdout)
}

// inviteFlag renders the --invite portion of the suggested finish command.
func inviteFlag(login string) string {
	if login == "" {
		return ""
	}
	return " --invite " + login
}

// githubClient builds the forge client over token. REPLICHAI_GITHUB_API_URL
// points it at another API root, such as a GitHub Enterprise host or a test's
// local server, and is unset for github.com.
func githubClient(token string) *github.Client {
	if base := os.Getenv("REPLICHAI_GITHUB_API_URL"); base != "" {
		return github.New(token, github.WithBaseURL(base))
	}
	return github.New(token)
}

// finishDestination is where finish keeps the artifacts of the session it
// reattaches to. The session id is an identifier from another system, so it
// is reduced to a path segment first: an id of "../../x" must not name a
// directory outside the temporary root.
func finishDestination(ref managed.SessionRef) string {
	return filepath.Join(os.TempDir(), "replichai-finish-"+slugify(ref.ID))
}

// envOr returns the environment value for key, or fallback when it is unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runResolve(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("resolve", stderr)
	timeout := fs.Duration("timeout", 30*time.Second, "network timeout for one fetch")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stderr, "resolve takes exactly one paper reference")
		return errExitQuiet
	}

	id, err := paper.Resolve(ctx, positional[0], paper.WithFetcher(httpFetcher{timeout: *timeout}))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "identity:  %s\n", id)
	_, _ = fmt.Fprintf(stdout, "scheme:    %s\n", id.Scheme)
	_, _ = fmt.Fprintf(stdout, "key:       %s\n", id.Key)
	_, _ = fmt.Fprintf(stdout, "slug:      %s\n", id.Slug)
	if id.Title != "" {
		_, _ = fmt.Fprintf(stdout, "title:     %s\n", id.Title)
	}
	for _, src := range id.Sources {
		_, _ = fmt.Fprintf(stdout, "source:    %s\n", src)
	}
	return nil
}

func runPublish(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flagSet("publish", stderr)
	var (
		out       = fs.String("out", "published", "root directory of the filesystem forge")
		live      = fs.Bool("live", false, "write the repository instead of previewing")
		verdict   = fs.String("verdict", "passed", "verdict to record in the manifest")
		methodDir = fs.String("method", "", "path to a method directory")
		timeout   = fs.Duration("timeout", 30*time.Second, "network timeout for identity resolution")
	)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		_, _ = fmt.Fprintln(stderr, "publish takes an artifact directory and a paper reference")
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

	_, _ = fmt.Fprintf(stdout, "repo:        %s\n", plan.Manifest.Reproduction.Repo)
	_, _ = fmt.Fprintf(stdout, "verdict:     %s\n", plan.Manifest.Reproduction.Verdict)
	_, _ = fmt.Fprintf(stdout, "included:    %d files\n", len(plan.Include))
	_, _ = fmt.Fprintf(stdout, "quarantined: %d files\n", len(plan.Quarantine))
	for _, q := range plan.Quarantine {
		_, _ = fmt.Fprintf(stdout, "  - %s: %s\n", q.Path, q.Reason)
	}

	forge := publish.NewFSForge(*out, publish.DefaultOrg)
	var opts []publish.Option
	if *live {
		opts = append(opts, publish.Live())
	}
	pub, err := publish.Publish(ctx, plan, forge, opts...)
	if err != nil {
		return err
	}
	if pub.Preview {
		_, _ = fmt.Fprintln(stdout, "preview only; pass -live to write the repository")
	} else {
		_, _ = fmt.Fprintf(stdout, "written:     %s\n", filepath.Join(*out, plan.RepoName))
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

// httpFetcher is the paper.Fetcher the CLI supplies: the shared guarded fetcher
// from internal/fetch, with retries on top. The internal/paper package stays
// free of network code and testable, and the transport lives in internal/fetch,
// where the address, redirect and size rules are decided once for every caller.
// Retries matter because arXiv's metadata API is slow and rate-limited, and a
// single timeout there would otherwise fail an audit's identity resolution.
type httpFetcher struct {
	timeout time.Duration
	// maxBytes raises the size cap above fetch's default, for a paper's full
	// text. Zero keeps the default, which is enough for a metadata record.
	maxBytes int64
	// private admits loopback and private addresses. Tests reaching an
	// httptest server set it; nothing else does.
	private bool
}

// fetchPolicy is how a fetch is retried: three attempts, one then two seconds
// apart, before giving up.
var fetchPolicy = retry.Policy{MaxAttempts: 3, Base: time.Second, Max: 2 * time.Second, Jitter: -1}

func (h httpFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	opts := []fetch.Option{fetch.WithTimeout(h.timeout)}
	if h.maxBytes > 0 {
		opts = append(opts, fetch.WithMaxBytes(h.maxBytes))
	}
	if h.private {
		opts = append(opts, fetch.AllowPrivate())
	}
	client := fetch.New(opts...)
	var body []byte
	err := retry.Do(ctx, fetchPolicy, func(ctx context.Context) error {
		var err error
		body, err = client.Fetch(ctx, url)
		if err != nil && !fetch.Retryable(err) {
			return retry.Stop(err)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

// slugify names a local scratch directory for one paper reference.
//
// It is deliberately simple, because this is not a published repository name.
// Repository naming is a separate concern with its own identity rules.
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

// publisherFor chooses where the repository comes from (045l). When a project
// is named, the platform is asked, which leaves no organization credential on
// this machine. When none is named, the credential this machine already holds
// is used.
//
// A deployment that cannot mint is not a failure: the CLI prints which setting
// is missing and uses its own credential where it has one, because refusing
// the run over a deployment setting would not help the operator.
func publisherFor(ctx context.Context, projectID, owner, paperRef, methodDigest, invite string,
	timeout time.Duration, stderr *os.File) (*publisher, error) {
	if projectID == "" {
		return newPublisher(ctx, owner, paperRef, methodDigest, timeout, stderr)
	}
	// A minted credential carries contents:write and nothing else, and adding a
	// collaborator is an administration call. Refusing here is better than
	// discovering it after the reproduction has been pushed.
	if invite != "" {
		return nil, errors.New("--invite needs a credential that can administer the repository; " +
			"use --publish without --project, or add the collaborator in the app")
	}
	pub, err := newPlatformPublisher(ctx, projectID, paperRef, timeout, stderr)
	if errors.Is(err, cloud.ErrMintingUnavailable) {
		_, _ = fmt.Fprintln(stderr, "  ! this deployment cannot provision a repository "+
			"(it needs REPLICHAI_GITHUB_APP_ID and REPLICHAI_GITHUB_APP_KEY)")
		_, _ = fmt.Fprintln(stderr, "    falling back to REPLICHAI_GITHUB_TOKEN on this machine")
		return newPublisher(ctx, owner, paperRef, methodDigest, timeout, stderr)
	}
	return pub, err
}
