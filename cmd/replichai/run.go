// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
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
	fs := flagSet("run", stderr)
	var (
		out          = fs.String("out", "runs", "directory for artifacts")
		runtimeName  = runtimeFlag(fs, "local")
		methodDir    = fs.String("method", "", "path to a method directory")
		parallel     = fs.Int("parallel", 0, fmt.Sprintf("how many papers to run at once (default %d on this machine: half the cores, at most 4)", batch.DefaultParallel()))
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "time between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "maximum time for one paper")
		maxRevisions = fs.Int("max-revisions", audit.DefaultMaxRevisions, "maximum number of revisions per run; 0 grades each run once and never revises it")
		doPublish    = fs.Bool("publish", false, "publish each reproduction to its own repository")
		ownerFlag    = fs.String("owner", "", "GitHub owner for --publish (default $REPLICHAI_GITHUB_OWNER, then the project file, then \""+publish.DefaultOrg+"\")")
		inviteFlag   = fs.String("invite", "", "GitHub login to invite as a collaborator (default the project file's publish.invite)")
		resume       = fs.Bool("resume", false, "skip papers that have already finished")
		network      = fs.String("network", "", "network profile for runs whose project file names none: artifacts (the default) or open")
		environment  = fs.String("environment", "", "environment for runs whose project file names none, such as science (default: the runtime's base)")
		local        localOptions
	)
	local.bind(fs)
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stderr, "run takes exactly one project file")
		return errExitQuiet
	}
	if err := checkRevisions(*maxRevisions, stderr); err != nil {
		return err
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

	// Every run of the study gets the same inputs (046): the network profile
	// each run is given is settled, and a copy of every paper and a snapshot of
	// every artifact it lists taken, before any run starts, so a study that
	// cannot give its runs the same inputs spends nothing finding out.
	entries, err := file.Entries()
	if err != nil {
		return err
	}
	profiles, err := networkProfiles(entries, *network)
	if err != nil {
		return err
	}
	inputs, err := batch.Inputs(ctx, batch.Root(positional[0]), entries,
		batch.Sources{Paper: fetchStudyPaper, Artifact: fetchStudyArtifact}, *resume)
	if err != nil {
		return err
	}

	runner := &projectRunner{
		method: m, options: local, runtimeName: resolveRuntime(*runtimeName, os.Getenv, "local"), out: *out,
		owner: owner, invite: invite, publish: *doPublish,
		interval: *interval, timeout: *timeout, maxRevisions: *maxRevisions,
		stdout: stdout, stderr: stderr,
		publishers: make(map[int]*publisher), project: file.Project,
		profiles: profiles, inputs: inputs, environment: *environment,
	}
	statePath := filepath.Join(batch.Root(positional[0]), "batch.json")
	reportPath := filepath.Join(batch.Root(positional[0]), "report.md")

	state, err := batch.Run(ctx, file, runner, batch.Options{
		Parallel: *parallel, StatePath: statePath, Resume: *resume, Publishing: *doPublish,
		Progress: func(state batch.State) {
			// The report is rewritten atomically after every change, as the
			// state is, so a reader never sees a partial file. A failed
			// write is reported, because a report that silently stops
			// updating looks like a batch that stopped.
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

// networkProfiles is the network profile each run is given, by the run's
// index: the one its paper or its project file names, else the one the flag
// names, else the default. Every name is checked before anything runs, so a
// misspelled profile fails the study rather than one run of it.
func networkProfiles(entries []batch.Resolved, flag string) (map[int]audit.NetworkProfile, error) {
	profiles := make(map[int]audit.NetworkProfile, len(entries))
	for _, entry := range entries {
		profile, err := audit.ParseNetworkProfile(firstNonEmpty(entry.Network, flag, string(audit.DefaultNetworkProfile)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Reference(), err)
		}
		profiles[entry.Index] = profile
	}
	return profiles, nil
}

// paperFetchTimeout bounds one fetch of a paper's full text, and
// paperMaxBytes its size: a scanned paper with its appendices runs to tens of
// megabytes, and the cap is the largest input a session takes.
const (
	paperFetchTimeout       = 2 * time.Minute
	paperMaxBytes     int64 = 64 << 20
)

// fetchStudyPaper fetches the copy of a paper every run of a study is handed.
// It is a variable so the tests of this command run with no network.
var fetchStudyPaper batch.FetchPaper = studyFetcher(httpFetcher{timeout: paperFetchTimeout, maxBytes: paperMaxBytes})

// studyFetcher fetches a paper's full text through fetcher.
func studyFetcher(fetcher paper.Fetcher) batch.FetchPaper {
	return func(ctx context.Context, reference string) (paper.Copy, error) {
		return paper.FullText(ctx, reference, fetcher)
	}
}

// resolvePublish resolves the publish block's two settings by one rule: the
// flag, then the environment, then the project file, then the built-in
// default. The flags default to empty so an unset flag can be told apart from
// one set to the environment's value. While the -owner flag defaulted to the
// environment or DefaultOrg, it was never empty and the project file's owner
// could never apply.
func resolvePublish(ownerFlag, inviteFlag string, getenv func(string) string, file batch.PublishConfig) (owner, invite string) {
	owner = firstNonEmpty(ownerFlag, getenv("REPLICHAI_GITHUB_OWNER"), file.Owner, publish.DefaultOrg)
	invite = firstNonEmpty(inviteFlag, file.Invite)
	return owner, invite
}

// firstNonEmpty returns the first value that is not empty after trimming.
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
// They are kept by the run's index rather than by paper reference, because two
// runs of one paper may both be prepared before either executes.
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
	// GitHub, over the credential this machine holds. A test substitutes a
	// source that makes no network calls.
	repositories repositorySource
	// project names the project the runs belong to, for each run's record.
	project string
	// profiles is the network profile each run is given, by the run's index,
	// and inputs the copy of each paper and the snapshots of its artifacts
	// every run of it is handed, by the paper's reference. Both are settled
	// before any run starts.
	profiles map[int]audit.NetworkProfile
	inputs   map[string]batch.StudyInputs
	// environment is what a run's machine runs when its project file names
	// none.
	environment string

	mu         sync.Mutex
	publishers map[int]*publisher
}

// repositorySource supplies Prepare with the repository for one paper. A batch
// asks for one of two things: a repository for a paper's first attempt, or the
// repository an earlier attempt already created.
type repositorySource interface {
	// Allocate names and creates the repository for a first attempt.
	Allocate(ctx context.Context, paperRef string) (*publisher, error)
	// Reattach returns to the repository an earlier attempt created, named
	// as <owner>/<repo>.
	Reattach(ctx context.Context, repo, paperRef string) (*publisher, error)
}

// githubRepositories is the production source: names are allocated through
// the registry in the owner's organization and the repositories are created
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
// name and creates the repository. A retry returns to the repository its
// first attempt created, which is what --resume promises; a second repository
// for the same paper would be an unwanted sibling. The batch serializes
// Prepare, because allocation reads what already exists, and two concurrent
// papers whose slugs collide would otherwise choose the same name.
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
		_, _ = fmt.Fprintf(r.stderr, "  reusing repository %s\n", prior.Repo)
		pub, err = source.Reattach(ctx, prior.Repo, entry.Reference())
	} else {
		pub, err = source.Allocate(ctx, entry.Reference())
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.publishers[entry.Index] = pub
	r.mu.Unlock()
	return nil
}

// Execute audits one paper and publishes it.
//
// Every run that was given a repository leaves a manifest in it, whatever
// became of the run. A run that fails before its session exists never reaches
// the hook that finalizes a session, and its repository used to stay as
// Prepare left it: created, empty, and with nothing to say why.
func (r *projectRunner) Execute(ctx context.Context, entry batch.Resolved) (batch.Outcome, error) {
	options := r.options
	if entry.Harness != "" {
		options.harness = entry.Harness
	}
	if entry.Sandbox != "" {
		options.sandbox = entry.Sandbox
	}
	model := entry.Model
	if model == "" {
		model = audit.DefaultModelPreference
	}

	r.mu.Lock()
	pub := r.publishers[entry.Index]
	r.mu.Unlock()

	var outcome batch.Outcome
	if key, keyErr := paper.Key(entry.Reference()); keyErr == nil {
		outcome.PaperKey = key
	}
	if pub != nil {
		outcome.Repo = pub.repoFullName()
	}

	controller, runtime, err := controllerFor(ctx, r.runtimeName, r.interval, options)
	if err != nil {
		return outcome, r.recordUnstarted(ctx, pub, entry, model, err)
	}
	opts := []audit.SessionOption{
		audit.WithSessionModel(model),
		audit.WithSessionMaxRevisions(r.maxRevisions),
		audit.WithSessionTimeout(r.timeout),
		audit.WithEnvironment(firstNonEmpty(entry.Environment, r.environment)),
	}
	profile, given, handed := r.given(entry)
	if profile != "" {
		opts = append(opts, audit.WithNetworkProfile(profile))
	}
	if handed {
		opts = append(opts, audit.WithPaperCopy(given.Paper))
		if len(given.Artifacts) > 0 {
			opts = append(opts, audit.WithArtifacts(given.Artifacts))
		}
	}

	// retained reports that the session reached the hook that finalizes it.
	// Whatever that hook then did, the session's own account is the one to
	// record, and no second one is written after it.
	retained := false
	if pub != nil {
		capabilities, capErr := runtime.Capabilities(ctx)
		if capErr != nil {
			return outcome, r.recordUnstarted(ctx, pub, entry, model, capErr)
		}
		mounts := capabilities.RepositoryModes[managed.AccessReadWrite]
		if mounts {
			// The agent is about to clone this repository, so the identity
			// must be recorded in it before the clone.
			pub.recordIdentity(ctx, r.stderr)
			opts = append(opts, audit.WithSessionMount(audit.SessionMount{
				URL: pub.mount.URL, Token: pub.mount.Token, MountPath: pub.mount.MountPath, ReadOnly: pub.mount.ReadOnly,
			}))
		} else {
			opts = append(opts, audit.WithLocalWorkspace())
		}
		opts = append(opts, audit.WithSessionHooks(managed.Hooks{
			Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
				retained = true
				if !mounts {
					workspace, err := workspaceOf(runtime, evidence.Status.Ref)
					if err != nil {
						return err
					}
					if err := pushWorkspace(ctx, pub, workspace, entry.Reference(), evidence.Status.Ref, r.stdout, r.stderr); err != nil {
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
				record := r.placed(runRecord(runtime, evidence.Status, model, audit.PromptDigest(ctx), r.stderr), entry)
				record.NetworkProfile = string(profile)
				record.Environment = firstNonEmpty(entry.Environment, r.environment)
				if handed {
					record.PaperDigest = given.Paper.Digest()
					for _, snapshot := range given.Artifacts {
						record.Artifacts = append(record.Artifacts, publish.RunArtifact{
							Source: snapshot.Source, Commit: snapshot.Commit, Digest: snapshot.Digest(),
						})
					}
				}
				return finalizePublication(ctx, pub.gh, pub.owner, pub.name, pub.id, r.method.Digest(),
					grade, record, providerEventsFromManaged(evidence.Events), r.invite, r.stdout)
			},
		}))
	}

	auditor := audit.NewSessionAuditor(controller, r.method, opts...)
	destination := filepath.Join(r.out, runDirectory(entry))
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
		if !retained && result.Ref.ID == "" {
			err = r.recordUnstarted(ctx, pub, entry, model, err)
		}
		return outcome, err
	}
	if result.Status.State == managed.StateFailed {
		return outcome, fmt.Errorf("%s: %s", result.Status.TerminalReason, strings.TrimSpace(result.Status.Error))
	}
	return outcome, nil
}

// given is what a run of the study is handed: its network profile, and the copy
// of its paper with the snapshots of its artifacts. A runner built without
// them gives a run none, and the run has what its runtime allows and finds its
// own copy.
func (r *projectRunner) given(entry batch.Resolved) (audit.NetworkProfile, batch.StudyInputs, bool) {
	inputs, handed := r.inputs[entry.Reference()]
	return r.profiles[entry.Index], inputs, handed
}

// placed adds where a run sits in its project to its record: the project, and
// which run of the paper this was, of how many.
func (r *projectRunner) placed(record publish.Run, entry batch.Resolved) publish.Run {
	record.Project, record.Run, record.Runs = r.project, entry.Run, entry.Runs
	return record
}

// recordUnstarted writes the manifest of a run that failed before a session
// existed, and returns the failure that stopped it. A run with no repository
// has nowhere to record anything, and its failure comes back as it was. A
// manifest that could not be written is reported with the failure, since the
// operator then has an empty repository to explain as well.
func (r *projectRunner) recordUnstarted(ctx context.Context, pub *publisher, entry batch.Resolved, model string, cause error) error {
	if pub == nil {
		return cause
	}
	record := r.placed(notStarted(model), entry)
	// The batch may be stopping because its context was cancelled. The record
	// of that is still worth one short write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unstartedRecordTimeout)
	defer cancel()
	if err := finalizePublication(ctx, pub.gh, pub.owner, pub.name, pub.id, r.method.Digest(),
		provider.GradeUnknown, record, nil, r.invite, r.stdout); err != nil {
		return errors.Join(cause, fmt.Errorf("record that the run did not start: %w", err))
	}
	return cause
}

// unstartedRecordTimeout bounds the write of a manifest for a run that never
// started.
const unstartedRecordTimeout = 30 * time.Second

// runDirectory names the local directory for one run's artifacts under --out.
// A paper that runs once keeps the directory it always had; the runs of a
// paper that runs more than once are told apart by a run suffix, so they do
// not write over each other's output.
func runDirectory(entry batch.Resolved) string {
	if entry.Runs > 1 {
		return fmt.Sprintf("%s-run-%d", slugify(entry.Reference()), entry.Run)
	}
	return slugify(entry.Reference())
}
