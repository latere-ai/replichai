// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"latere.ai/x/pkg/atomicfile"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// runRun drives a whole replication project: a list of papers, run in
// parallel, each in its own session, with a report at the end (045h).
func runRun(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out          = fs.String("out", "runs", "directory for artifacts")
		runtimeName  = runtimeFlag(fs, "local")
		methodDir    = fs.String("method", "", "path to a method directory")
		parallel     = fs.Int("parallel", 0, fmt.Sprintf("how many papers to run at once (default %d here: half the cores, capped at 4)", batch.DefaultParallel()))
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "gap between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "how long one paper may take")
		maxRevisions = fs.Int("max-revisions", audit.DefaultMaxRevisions, "revision budget per paper")
		doPublish    = fs.Bool("publish", false, "publish each reproduction to its own repository")
		ownerFlag    = fs.String("owner", "", "GitHub owner for --publish (default $REPLICHAI_GITHUB_OWNER, then the project file, then \""+publish.DefaultOrg+"\")")
		inviteFlag   = fs.String("invite", "", "GitHub login to invite as a collaborator (default the project file's publish.invite)")
		resume       = fs.Bool("resume", false, "skip the papers that already finished")
		local        localOptions
	)
	local.bind(fs)
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
		_, _ = fmt.Fprintln(stderr, "run takes exactly one project file")
		return errExitQuiet
	}

	file, err := batch.ReadFile(positional[0])
	if err != nil {
		return err
	}
	owner, invite := resolvePublish(*ownerFlag, *inviteFlag, os.Getenv, file.Publish)
	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}

	runner := &projectRunner{
		method: m, options: local, runtimeName: resolveRuntime(*runtimeName, os.Getenv, "local"), out: *out,
		owner: owner, invite: invite, publish: *doPublish,
		interval: *interval, timeout: *timeout, maxRevisions: *maxRevisions,
		stdout: stdout, stderr: stderr,
		publishers: make(map[string]*publisher),
	}
	statePath := filepath.Join(batch.Root(positional[0]), "batch.json")
	reportPath := filepath.Join(batch.Root(positional[0]), "report.md")

	state, err := batch.Run(ctx, file, runner, batch.Options{
		Parallel: *parallel, StatePath: statePath, Resume: *resume, Publishing: *doPublish,
		Progress: func(state batch.State) {
			// The report is rewritten after every change, the same way the
			// state is, so a reader never sees half of one; and a write
			// that fails is said, because a report that silently stops
			// updating reads as a batch that stopped.
			if err := atomicfile.Write(reportPath, []byte(state.Report()), 0o644); err != nil {
				_, _ = fmt.Fprintf(stderr, "  ! could not write %s: %v\n", reportPath, err)
			}
		},
	})
	_, _ = fmt.Fprintln(stdout, state.Report())
	_, _ = fmt.Fprintf(stderr, "\nstate:  %s\nreport: %s\n", statePath, reportPath)
	if errors.Is(err, batch.ErrIncomplete) {
		return errExitQuiet
	}
	return err
}

// resolvePublish settles the publish block's two settings by one rule, in the
// order a person expects: what they typed, then their environment, then the
// project file, then the built-in default. The flags default to empty so an
// unset flag can be told from one typed to the environment's value; while the
// -owner flag defaulted to the environment or DefaultOrg, it was never empty
// and the project file's owner could never apply.
func resolvePublish(ownerFlag, inviteFlag string, getenv func(string) string, file batch.PublishConfig) (owner, invite string) {
	owner = firstNonEmpty(ownerFlag, getenv("REPLICHAI_GITHUB_OWNER"), file.Owner, publish.DefaultOrg)
	invite = firstNonEmpty(inviteFlag, file.Invite)
	return owner, invite
}

// firstNonEmpty is the resolution rule itself: the first value anybody set.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// projectRunner runs one paper of a project. It holds the publishers it
// prepared so an execution reaches the repository its own preparation created.
type projectRunner struct {
	method       method.Method
	options      localOptions
	runtimeName  string
	out          string
	owner        string
	invite       string
	publish      bool
	interval     time.Duration
	timeout      time.Duration
	maxRevisions int
	stdout       *os.File
	stderr       *os.File
	// repositories is where a paper's repository comes from. Nil means
	// GitHub, over the credential this machine holds; a test substitutes a
	// source that talks to nothing.
	repositories repositorySource

	mu         sync.Mutex
	publishers map[string]*publisher
}

// repositorySource hands Prepare the repository for one paper. The two calls
// are the two things a batch can ask for: a repository for a paper's first
// attempt, and the repository an earlier attempt already created.
type repositorySource interface {
	// Allocate names and creates the repository for a first attempt.
	Allocate(ctx context.Context, paperRef string) (*publisher, error)
	// Reattach returns to the repository an earlier attempt created, named
	// as <owner>/<repo>.
	Reattach(ctx context.Context, repo, paperRef string) (*publisher, error)
}

// githubRepositories is the production source: names are allocated through
// the registry in the owner's organisation and the repositories are created
// there, over REPLICHAI_GITHUB_TOKEN.
type githubRepositories struct {
	owner        string
	methodDigest string
	timeout      time.Duration
	stderr       *os.File
}

func (g githubRepositories) Allocate(ctx context.Context, paperRef string) (*publisher, error) {
	return newPublisher(ctx, g.owner, paperRef, g.methodDigest, g.timeout, g.stderr)
}

func (g githubRepositories) Reattach(ctx context.Context, repo, paperRef string) (*publisher, error) {
	return reattachPublisher(ctx, repo, paperRef, g.timeout)
}

// Prepare claims the repository for one paper. A first attempt allocates a
// name and creates it; a retry returns to the repository its first attempt
// created, which is what --resume promises — a second repository for the same
// paper would be a sibling nobody asked for. The batch serialises it, because
// allocation reads what already exists: two concurrent papers whose slugs
// collide would otherwise decide the same name.
func (r *projectRunner) Prepare(ctx context.Context, entry batch.Resolved, prior batch.EntryState) error {
	if !r.publish {
		return nil
	}
	source := r.repositories
	if source == nil {
		source = githubRepositories{owner: r.owner, methodDigest: r.method.Digest(), timeout: r.timeout, stderr: r.stderr}
	}
	var (
		pub *publisher
		err error
	)
	if prior.Repo != "" {
		_, _ = fmt.Fprintf(r.stderr, "  returning to %s\n", prior.Repo)
		pub, err = source.Reattach(ctx, prior.Repo, entry.Reference())
	} else {
		pub, err = source.Allocate(ctx, entry.Reference())
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.publishers[entry.Reference()] = pub
	r.mu.Unlock()
	return nil
}

// Execute audits one paper and publishes it.
func (r *projectRunner) Execute(ctx context.Context, entry batch.Resolved) (batch.Outcome, error) {
	options := r.options
	if entry.Harness != "" {
		options.harness = entry.Harness
	}
	if entry.Sandbox != "" {
		options.sandbox = entry.Sandbox
	}
	controller, runtime, err := controllerFor(ctx, r.runtimeName, r.interval, options)
	if err != nil {
		return batch.Outcome{}, err
	}
	model := entry.Model
	if model == "" {
		model = audit.DefaultModelPreference
	}
	opts := []audit.SessionOption{
		audit.WithSessionModel(model),
		audit.WithSessionMaxRevisions(r.maxRevisions),
		audit.WithSessionTimeout(r.timeout),
	}

	r.mu.Lock()
	pub := r.publishers[entry.Reference()]
	r.mu.Unlock()

	var outcome batch.Outcome
	if key, keyErr := paper.Key(entry.Reference()); keyErr == nil {
		outcome.PaperKey = key
	}
	if pub != nil {
		capabilities, capErr := runtime.Capabilities(ctx)
		if capErr != nil {
			return outcome, capErr
		}
		mounts := capabilities.RepositoryModes[managed.AccessReadWrite]
		if mounts {
			// The agent is about to clone this repository, so what the
			// repository says about the paper has to be true before it does.
			pub.recordIdentity(ctx, r.stderr)
			opts = append(opts, audit.WithSessionMount(audit.SessionMount{
				URL: pub.mount.URL, Token: pub.mount.Token, MountPath: pub.mount.MountPath, ReadOnly: pub.mount.ReadOnly,
			}))
		} else {
			opts = append(opts, audit.WithLocalWorkspace())
		}
		outcome.Repo = pub.repoFullName()
		opts = append(opts, audit.WithSessionHooks(managed.Hooks{
			Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
				if !mounts {
					if err := pushWorkspace(ctx, pub, capabilities.OutputDir, entry.Reference(), evidence.Status.Ref, r.stdout, r.stderr); err != nil {
						return err
					}
					// The remote's history now descends from the workspace, so
					// a commit made here lands on top of the reproduction.
					pub.recordIdentity(ctx, r.stderr)
				}
				grade := provider.GradeUnknown
				if evidence.Status.Verdict != nil {
					grade = provider.Grade(evidence.Status.Verdict.Grade)
				}
				return finalizePublication(ctx, pub.gh, pub.owner, pub.name, pub.id, r.method.Digest(),
					grade, providerEventsFromManaged(evidence.Events), r.invite, r.stdout)
			},
		}))
	}

	auditor := audit.NewSessionAuditor(controller, r.method, opts...)
	destination := filepath.Join(r.out, slugify(entry.Reference()))
	result, err := auditor.Run(ctx, entry.Reference(), destination)
	outcome.Session = result.Ref.ID
	outcome.Model = result.Status.Model
	outcome.Usage = result.Status.Usage
	outcome.Ref = result.Ref
	outcome.Directory = destination
	if result.Status.Verdict != nil {
		outcome.Grade = result.Status.Verdict.Grade.String()
	}
	if err != nil {
		return outcome, err
	}
	if result.Status.State == managed.StateFailed {
		return outcome, fmt.Errorf("%s: %s", result.Status.TerminalReason, strings.TrimSpace(result.Status.Error))
	}
	return outcome, nil
}
