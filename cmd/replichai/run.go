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

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// runRun drives a whole replication project: a list of papers, run in
// parallel, each in its own session, with a report at the end (045h).
func runRun(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out          = fs.String("out", "runs", "directory for artifacts")
		runtimeName  = fs.String("runtime", "local", "managed runtime to use")
		methodDir    = fs.String("method", "", "path to a method directory")
		parallel     = fs.Int("parallel", 0, "how many papers to run at once (default: half the cores, capped at 4)")
		interval     = fs.Duration("interval", audit.DefaultPollInterval, "gap between status checks")
		timeout      = fs.Duration("timeout", audit.DefaultTimeout, "how long one paper may take")
		maxRevisions = fs.Int("max-revisions", audit.DefaultMaxRevisions, "revision budget per paper")
		doPublish    = fs.Bool("publish", false, "publish each reproduction to its own repository")
		owner        = fs.String("owner", envOr("REPLICHAI_GITHUB_OWNER", "open-hai"), "GitHub owner for --publish")
		invite       = fs.String("invite", "", "GitHub login to invite as a collaborator")
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
		fmt.Fprintln(stderr, "run takes exactly one project file")
		return errExitQuiet
	}

	file, err := batch.ReadFile(positional[0])
	if err != nil {
		return err
	}
	// The project file's publish block is the default; a flag beats it.
	if *owner == "" {
		*owner = file.Publish.Owner
	}
	if *invite == "" {
		*invite = file.Publish.Invite
	}
	m, err := method.Load(*methodDir)
	if err != nil {
		return err
	}

	runner := &projectRunner{
		method: m, options: local, runtimeName: *runtimeName, out: *out,
		owner: *owner, invite: *invite, publish: *doPublish,
		interval: *interval, timeout: *timeout, maxRevisions: *maxRevisions,
		stdout: stdout, stderr: stderr,
		publishers: make(map[string]*publisher),
	}
	statePath := filepath.Join(batch.Root(positional[0]), "batch.json")
	reportPath := filepath.Join(batch.Root(positional[0]), "report.md")

	state, err := batch.Run(ctx, file, runner, batch.Options{
		Parallel: *parallel, StatePath: statePath, Resume: *resume, Publishing: *doPublish,
		Progress: func(state batch.State) {
			_ = os.WriteFile(reportPath, []byte(state.Report()), 0o644)
		},
	})
	fmt.Fprintln(stdout, state.Report())
	fmt.Fprintf(stderr, "\nstate:  %s\nreport: %s\n", statePath, reportPath)
	if errors.Is(err, batch.ErrIncomplete) {
		return errExitQuiet
	}
	return err
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

	mu         sync.Mutex
	publishers map[string]*publisher
}

// Prepare allocates the repository name and creates the repository. The batch
// serialises it, because allocation reads what already exists: two concurrent
// papers whose slugs collide would otherwise decide the same name.
func (r *projectRunner) Prepare(ctx context.Context, entry batch.Resolved) error {
	if !r.publish {
		return nil
	}
	pub, err := newPublisher(ctx, r.owner, entry.Reference(), r.method.Digest(), r.timeout, r.stderr)
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
					if err := pushWorkspace(ctx, pub, capabilities.OutputDir, r.stdout, r.stderr); err != nil {
						return err
					}
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
