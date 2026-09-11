// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Retention is what Release does with a session directory. The default is
// keep, because the directory is the artifact: the workspace is the
// reproduction, and deleting it on success would destroy the only copy of
// anything the push has not yet taken.
type Retention string

// The retention policies.
const (
	RetainAlways    Retention = "keep"
	RetainOnFailure Retention = "on-failure"
	RetainNever     Retention = "discard"
)

// Config is what a composition root hands this package. Sandbox and Harness
// are the two seams; Catalog is the operator's model declaration; Root is
// where session directories are made.
type Config struct {
	Sandbox   sandbox.Sandbox
	Harness   harness.Harness
	Catalog   *Catalog
	Root      string
	Retention Retention
	// Machine is the little a harness needs to know about where it runs in
	// order to say what it needs of it: the platform, the operator's home,
	// the temporary root, and the user id.
	Machine harness.Machine
	// Lookup reads one variable of the process environment, os.LookupEnv at
	// the composition root. The git this runtime runs inherits only what
	// sandbox.HostEnvironment admits through it; nothing inside the seam
	// reads the environment itself, because a second, unscoped source of
	// configuration is how a credential meant for one runtime reaches
	// another.
	Lookup LookupEnv
	// Clock is the time source, so a test does not depend on wall time.
	Clock func() time.Time
}

// Runtime executes a managed session on this machine. It owns the stage
// sequence, the transcript pump, and the grading loop; it owns no isolation
// (that is the driver) and no harness specifics (that is the adapter).
type Runtime struct {
	config Config

	// mu guards reservations: the sessions Capabilities has allocated and no
	// Start has yet consumed, keyed by workspace path. There is no single
	// pending slot — the server runs a worker pool against one Runtime, and a
	// shared slot handed the same workspace to every concurrent caller, so two
	// jobs templated their instructions against one directory and then one ran
	// in another's. A reservation belongs to the caller whose specification
	// names its workspace; Start resolves it from that.
	mu           sync.Mutex
	reservations map[string]*Session
}

// New composes a local runtime, refusing a configuration that could not run a
// session rather than failing at the first stage.
func New(config Config) (*Runtime, error) {
	if config.Sandbox == nil {
		return nil, errors.New("local runtime needs a sandbox driver")
	}
	if config.Harness == nil {
		return nil, errors.New("local runtime needs a harness")
	}
	if config.Catalog == nil {
		return nil, errors.New("local runtime needs a model catalogue")
	}
	if strings.TrimSpace(config.Root) == "" {
		return nil, errors.New("local runtime needs a session root")
	}
	if config.Retention == "" {
		config.Retention = RetainAlways
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Runtime{config: config}, nil
}

// Name is the runtime name stamped into every reference this runtime issues.
func (r *Runtime) Name() managed.RuntimeName { return Name }

// Capabilities is the driver's declaration plus what the runtime provides
// itself. OutputDir is the workspace of the session the next Start will use,
// because on this runtime the output directory is a per-session path and the
// caller reads it before starting.
func (r *Runtime) Capabilities(ctx context.Context) (managed.Capabilities, error) {
	if err := r.config.Sandbox.Preflight(ctx); err != nil {
		return managed.Capabilities{}, err
	}
	session, err := r.reserve()
	if err != nil {
		return managed.Capabilities{}, err
	}
	return r.capabilities(session.Workspace()), nil
}

func (r *Runtime) capabilities(outputDir string) managed.Capabilities {
	driver := r.config.Sandbox.Capabilities()
	networks := make(map[managed.NetworkMode]bool, len(driver.NetworkModes))
	for mode, supported := range driver.NetworkModes {
		networks[managed.NetworkMode(mode)] = supported
	}
	limit := driver.MaxAttachmentBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	return managed.Capabilities{
		OutputDir:       outputDir,
		PackageManagers: driver.PackageManagers,
		NetworkModes:    networks,
		// The local path mounts no writable remote: the host pushes after
		// the run, which is what keeps a forge credential out of the
		// sandbox entirely (045g).
		RepositoryModes:    map[managed.AccessMode]bool{managed.AccessReadOnly: true},
		Evaluation:         true,
		DurableEvents:      true,
		Artifacts:          true,
		Cancellation:       true,
		MaxAttachmentBytes: limit,
		MaxLocatorBytes:    managed.DefaultMaxLocatorBytes,
	}
}

// Models is the operator's declaration for the selected harness.
func (r *Runtime) Models(context.Context) ([]managed.Model, error) {
	return r.config.Catalog.Models(r.config.Harness.Name()), nil
}

// Validate refuses a specification this runtime cannot execute, before
// anything is created. It is pure and is repeated defensively in Start.
func (r *Runtime) Validate(spec managed.SessionSpec) error {
	if err := managed.ValidateCapabilities(Name, r.capabilities("/workspace"), spec); err != nil {
		return err
	}
	model, gateway, err := r.config.Catalog.Resolve(r.config.Harness.Name(), spec.Model)
	if err != nil {
		return fmt.Errorf("%w: %w", managed.ErrInvalidSpec, err)
	}
	return r.carriesCredential(model, gateway)
}

// loginCarrier is a sandbox that knows whether a stage inside it can reach the
// operator's own machine at all. It is an optional interface rather than a
// capability, because it is a fact about the sandbox rather than about the
// specification.
type loginCarrier interface{ CarriesHarnessLogin() bool }

// carriesCredential refuses a session that would start with no way to
// authenticate, rather than letting it fail as "not logged in" once a stage is
// already running. The refusal names what to do instead: a credential the
// specification can carry, which the sandbox passes through untouched.
func (r *Runtime) carriesCredential(model managed.ModelRef, gateway harness.Gateway) error {
	if gateway.Auth != harness.AuthHarness {
		return nil
	}
	carrier, ok := r.config.Sandbox.(loginCarrier)
	if !ok || carrier.CarriesHarnessLogin() {
		return nil
	}
	return fmt.Errorf(
		"%w: %s/%s is declared to use the %s harness's own login, and a %s sandbox cannot reach it: it holds none of "+
			"this machine's files. Declare a gateway with a key_env in the model catalogue instead",
		managed.ErrUnsupported, model.Provider, model.ID, r.config.Harness.Name(), r.config.Sandbox.Name())
}

// Start creates the session and launches its first worker stage. It returns a
// reference only once that reference alone is enough to recover the session:
// the directory exists, the journal is written, and the stage is detached.
func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (ref managed.SessionRef, err error) {
	if err := r.Validate(spec); err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.config.Sandbox.Preflight(ctx); err != nil {
		return managed.SessionRef{}, err
	}
	model, gateway, err := r.config.Catalog.Resolve(r.config.Harness.Name(), spec.Model)
	if err != nil {
		return managed.SessionRef{}, err
	}
	session, err := r.claim(spec)
	if err != nil {
		return managed.SessionRef{}, err
	}
	// Until the reference is returned the session is not recoverable by
	// anyone, so a failure past this point must not leave its directory on
	// disk nor a bootstrap it launched still running: both would be a leak no
	// later call could find to clean up.
	locator := Locator{Driver: r.config.Sandbox.Name(), Harness: r.config.Harness.Name()}
	defer func() {
		if err != nil {
			r.abandon(ctx, session, locator)
		}
	}()

	if err = r.writeAttachments(session, spec); err != nil {
		return managed.SessionRef{}, err
	}
	if err = session.writeJSON(specFile, spec); err != nil {
		return managed.SessionRef{}, err
	}
	effective := r.network(spec, gateway)
	record := NewEnvironment(ctx, r.config.Sandbox.Name(), r.config.Harness.Name(), model, gateway, effective.Mode)
	// What a run was allowed to reach is part of what it is: a hunt with a
	// bound and a hunt without one find different things, and a reader
	// comparing two reproductions is entitled to know which they have.
	record.Network.Allowed = effective.Domains
	record.Harness.Version = r.harnessVersion(ctx)
	// What the run could read of the operator's own machine is part of what
	// it is. Only a session on the operator's sign-in is opened any of it,
	// and the record lists exactly what launch will grant.
	if gateway.Auth == harness.AuthHarness {
		record.Sandbox.ConfigRead = harness.LoginPaths(r.config.Harness.Needs(r.config.Machine))
	}
	// The container driver pins a toolchain and an image; the host driver
	// pins neither and leaves both empty. Read through the optional interfaces
	// a driver satisfies, so this package names no concrete driver.
	if reporter, ok := r.config.Sandbox.(interface{ Image() string }); ok {
		record.Sandbox.Image = reporter.Image()
	}
	if reporter, ok := r.config.Sandbox.(interface{ Toolchain() string }); ok {
		record.Sandbox.Version = reporter.Toolchain()
	}
	if err = session.WriteEnvironment(record); err != nil {
		return managed.SessionRef{}, err
	}

	// The bootstrap runs first and as a stage of its own, so a dependency
	// that will not install fails the session with a reason rather than
	// surfacing an hour later as an agent that cannot import.
	if err = r.launch(ctx, session, &locator, spec, model, gateway, harness.StageBootstrap, spec.Task); err != nil {
		return managed.SessionRef{}, err
	}
	status := managed.Status{
		Model: model, State: managed.StateRunning, StartedAt: r.config.Clock(),
		Usage: managed.Usage{Model: model},
	}
	revision, err := session.NextRevision()
	if err != nil {
		return managed.SessionRef{}, err
	}
	ref, err = r.reference(session, locator, revision)
	if err != nil {
		return managed.SessionRef{}, err
	}
	status.Ref = ref
	if err = session.WriteStatus(status); err != nil {
		return managed.SessionRef{}, err
	}
	return ref, nil
}

// abandon undoes a Start that could not finish: it stops every stage the
// attempt launched, so no bootstrap is left running detached, and removes the
// session directory, which no reference names and so nothing else will ever
// clean up.
func (r *Runtime) abandon(ctx context.Context, session *Session, locator Locator) {
	for _, stage := range locator.Stages {
		_ = r.config.Sandbox.Discard(ctx, stage.Handle)
	}
	_ = session.Remove()
}

// Poll advances the session: it observes the current stage, drains whatever it
// has written, and where it has finished decides what comes next — a judge
// after a worker, a revision after a rejection, or a terminal state.
//
// The observation comes before the drain, and a stage found finished is
// drained once more to end of file before Poll returns. A stage writes its
// last line and exits between two polls, so a drain that ran before the
// observation would leave that line for a next poll that a terminal stage never
// gets — and the line a harness writes last is its result event, which carries
// the run's total cost. Draining first folded the bill from a per-message
// maximum instead of the session total.
func (r *Runtime) Poll(ctx context.Context, ref managed.SessionRef) (managed.Status, error) {
	session, locator, err := r.attach(ref)
	if err != nil {
		return managed.Status{}, err
	}
	status, err := session.ReadStatus()
	if err != nil {
		return managed.Status{}, err
	}
	if status.State.Terminal() {
		status.Ref = ref
		return status, nil
	}
	spec, err := readSpec(session)
	if err != nil {
		return managed.Status{}, err
	}
	model, gateway, err := r.config.Catalog.Resolve(locator.Harness, spec.Model)
	if err != nil {
		return managed.Status{}, err
	}

	current := &locator.Stages[len(locator.Stages)-1]
	status.Model = model

	// Observe first, then drain: a stage that has just written its final line
	// and exited must have that line read, not held for a poll a terminal
	// stage never receives.
	observed, err := r.config.Sandbox.Observe(ctx, current.Handle)
	if err != nil {
		return managed.Status{}, err
	}
	usage, err := r.drain(ctx, session, locator.Harness, current)
	if err != nil {
		return managed.Status{}, err
	}
	status.Usage = harness.Fold(status.Usage, usage)
	if observed.Running {
		status.State = managed.StateRunning
		return status, r.save(session, locator, &status, ref)
	}
	// The stage has finished. Drain once more to end of file, so anything it
	// wrote between the observation and now — its result event above all — is
	// in the transcript and its usage in the total before the stage is closed.
	final, err := r.drain(ctx, session, locator.Harness, current)
	if err != nil {
		return managed.Status{}, err
	}
	status.Usage = harness.Fold(status.Usage, final)
	// A stage the driver has lost is not a stage that succeeded. Its exit is
	// unknowable, and reporting success on an unknown exit is exactly the
	// class of defect 044 recorded.
	exit := observed.ExitCode
	if observed.Gone && current.Exit == nil {
		exit = -1
	}
	if current.Exit == nil {
		current.Exit = &exit
	}
	if err := r.advance(ctx, session, &locator, &status, spec, model, gateway); err != nil {
		return managed.Status{}, err
	}
	return status, r.save(session, locator, &status, ref)
}

// advance decides what follows a finished stage.
func (r *Runtime) advance(ctx context.Context, session *Session, locator *Locator, status *managed.Status,
	spec managed.SessionSpec, model managed.ModelRef, gateway harness.Gateway) error {
	current := locator.Stages[len(locator.Stages)-1]
	switch current.Role {
	case harness.StageBootstrap:
		if *current.Exit != 0 {
			r.finish(status, managed.StateFailed, managed.ReasonRuntimeFailed,
				fmt.Sprintf("the bootstrap exited %d; the session's dependencies are not installed", *current.Exit))
			return nil
		}
		r.recordPackages(session)
		return r.launch(ctx, session, locator, spec, model, gateway, harness.StageWorker, spec.Task)
	case harness.StageWorker, harness.StageRevision:
		if *current.Exit != 0 {
			r.finish(status, managed.StateFailed, managed.ReasonRuntimeFailed,
				fmt.Sprintf("the %s stage exited %d", current.Role, *current.Exit))
			return nil
		}
		if spec.Evaluation == nil {
			r.finish(status, managed.StateDone, managed.ReasonSucceeded, "")
			return nil
		}
		round := currentRound(*locator)
		task := judgeTask(spec.Evaluation.Rubric, session.Workspace(), session.Grade(), round, model)
		return r.launch(ctx, session, locator, spec, model, gateway, harness.StageJudge, task)
	case harness.StageJudge:
		round := currentRound(*locator)
		file, verdict, err := ReadVerdict(session.Grade(), round)
		if err != nil {
			// One retry: a judge can fail to produce a parsable verdict
			// once. A session that still cannot be graded ends failed with
			// that reason, never done with no verdict.
			if judgeAttempts(*locator, round) < 2 {
				task := judgeTask(spec.Evaluation.Rubric, session.Workspace(), session.Grade(), round, model)
				return r.launch(ctx, session, locator, spec, model, gateway, harness.StageJudge, task)
			}
			r.finish(status, managed.StateFailed, managed.ReasonRuntimeFailed, "grading failed: "+err.Error())
			return nil
		}
		locator.Stages[len(locator.Stages)-1].Verdict = &verdict
		status.Verdict = &verdict
		if verdict.Passed() {
			r.finish(status, managed.StateDone, managed.ReasonSucceeded, "")
			return nil
		}
		revisions := revisionsSpent(*locator)
		if verdict.Grade == managed.GradeNeedsRevision && revisions < spec.Evaluation.MaxRevisions {
			task := spec.Task + harness.RevisionHeader + verdict.Explanation
			return r.launch(ctx, session, locator, spec, model, gateway, harness.StageRevision, task)
		}
		if verdict.Grade == managed.GradeNeedsRevision {
			exhausted := managed.Verdict{Grade: managed.GradeExhausted, Explanation: verdict.Explanation}
			status.Verdict = &exhausted
		}
		// A rejected evaluation is a failed session, not a done one. The other
		// two runtimes end it StateFailed, and the job store maps state one to
		// one, so ending it StateDone here landed the same outcome as done on
		// local and failed everywhere else.
		_ = file
		r.finish(status, managed.StateFailed, managed.ReasonEvaluationFailed, "")
		return nil
	default:
		return fmt.Errorf("unknown stage role %q", current.Role)
	}
}

func (r *Runtime) finish(status *managed.Status, state managed.State, reason managed.TerminalReason, message string) {
	status.State, status.TerminalReason, status.FinishedAt = state, reason, r.config.Clock()
	if message != "" {
		status.Error = message
	}
}

// launch builds one stage and starts it, appending it to the journal.
func (r *Runtime) launch(ctx context.Context, session *Session, locator *Locator, spec managed.SessionSpec,
	model managed.ModelRef, gateway harness.Gateway, role harness.Stage, task string) error {
	request := harness.Request{
		Instructions: spec.Instructions, Task: task, Model: model, Gateway: gateway,
		Paths: session.Paths(),
	}
	if role == harness.StageJudge {
		request.Instructions = ""
	}
	var (
		argv    []string
		env     map[string]string
		secrets []string
		files   []harness.File
		err     error
	)
	if role.Agentic() {
		if argv, err = r.config.Harness.Argv(role, request); err != nil {
			return err
		}
		if env, secrets, err = r.config.Harness.Env(role, request); err != nil {
			return err
		}
		if files, err = r.config.Harness.Files(role, request); err != nil {
			return err
		}
	} else {
		argv = r.bootstrapArgv(spec, session)
	}
	for _, file := range files {
		if !harness.Outside(session.Workspace(), file.Path) {
			return fmt.Errorf("harness %q would write %s inside the workspace", r.config.Harness.Name(), file.Path)
		}
		mode := os.FileMode(file.Mode)
		if mode == 0 {
			mode = 0o600
		}
		if err := writeFileMode(file.Path, file.Body, mode); err != nil {
			return err
		}
	}
	name := fmt.Sprintf("%s-%d", role, len(locator.Stages))
	paths := r.stagePaths(session, role)
	stageNetwork := r.network(spec, gateway)
	var denied []string
	if role.Agentic() {
		needs := r.config.Harness.Needs(r.config.Machine)
		// What the harness needs of the machine to run at all. Codex
		// writes its own state back into its configuration directory and
		// will not start otherwise; Claude Code needs nothing here,
		// because its scratch is redirected into the session instead.
		for _, path := range needs.Writes {
			paths = append(paths, sandbox.Path{Host: path, Access: sandbox.ReadWrite})
		}
		stageNetwork.Passthrough = needs.TLSPassthrough
		// The operator's own sign-in is reached only by a session that
		// uses it; one carrying its own credential is granted none of it.
		// This is the one decision point: the harness says where its login
		// lives, and only here is it decided whether that is opened.
		if gateway.Auth == harness.AuthHarness {
			for _, grant := range needs.Login {
				access := sandbox.ReadOnly
				if grant.Write {
					access = sandbox.ReadWrite
				}
				paths = append(paths, sandbox.Path{Host: grant.Path, Access: access})
			}
		}
		// Withheld either way: the operator's standing instructions and
		// plugins are not the method.
		denied = needs.Denies
	}
	stage := sandbox.StageSpec{
		Name:    name,
		Argv:    argv,
		Env:     r.stageEnv(session, env),
		EnvFile: secrets,
		Workdir: session.Workspace(),
		Paths:   paths,
		Denied:  denied,
		Network: stageNetwork,
		LogPath: session.RawLog(name),
	}
	if stage.Network.Mode == "" {
		stage.Network.Mode = sandbox.NetworkNone
	}
	switch role {
	case harness.StageJudge:
		stage.Workdir = session.Grade()
	case harness.StageBootstrap:
		// The bootstrap never touches the workspace: nothing it installs
		// belongs in the reproduction.
		stage.Workdir, stage.Paths = session.Venv(), r.bootstrapPaths(session)
	}
	handle, err := r.config.Sandbox.Launch(ctx, stage)
	if err != nil {
		return err
	}
	locator.Stages = append(locator.Stages, StageRecord{
		Role: role, Name: name, Handle: handle, StartedAt: r.config.Clock(),
	})
	return session.WriteLocator(*locator)
}

// network is the egress policy a stage runs under. Under an allowlist the
// model's own endpoint is added: a session that cannot reach the model it was
// told to run on does nothing at all, and the caller who wrote the allowlist
// does not know which endpoint the catalogue resolved. The effective list is
// recorded in the run's environment record rather than left implicit.
func (r *Runtime) network(spec managed.SessionSpec, gateway harness.Gateway) sandbox.Network {
	network := sandbox.Network{
		Mode:    string(spec.Sandbox.Network.Mode),
		Domains: append([]string(nil), spec.Sandbox.Network.AllowedDomains...),
	}
	if network.Mode != sandbox.NetworkAllowlist {
		return network
	}
	for _, host := range gatewayHosts(gateway) {
		if !slices.Contains(network.Domains, host) {
			network.Domains = append(network.Domains, host)
		}
	}
	return network
}

// gatewayHosts are the endpoints a session must reach to talk to its model. A
// gateway with no base URL is the harness's own vendor, whose endpoints the
// harness knows and this package does not, so the harness's own defaults are
// named here rather than guessed at request time.
func gatewayHosts(gateway harness.Gateway) []string {
	if gateway.BaseURL == "" {
		return defaultVendorHosts(gateway.Dialect)
	}
	parsed, err := url.Parse(gateway.BaseURL)
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	return []string{parsed.Hostname()}
}

// defaultVendorHosts are where a harness talks when nobody redirected it.
func defaultVendorHosts(dialect harness.Dialect) []string {
	switch dialect {
	case harness.DialectAnthropic:
		// The model endpoint only. statsig.anthropic.com and sentry.io are
		// the harness's telemetry and error reporting, which a reproduction
		// does not open — and the harness is told not to reach them at all.
		return []string{"api.anthropic.com"}
	default:
		// Codex signs in against ChatGPT and streams over a websocket on
		// the same host, so the whole of it is named rather than the API
		// host alone.
		return []string{
			"api.openai.com", "auth.openai.com",
			"chatgpt.com", "*.chatgpt.com", "*.oaistatic.com", "*.oaiusercontent.com",
		}
	}
}

// stagePaths is the sandbox's whole access decision. The judge gets the
// workspace read-only and grade/ read-write, which is the mechanism behind a
// grader that cannot edit what it grades; session/ is in neither list, which
// is the mechanism behind a transcript the agent cannot rewrite.
func (r *Runtime) stagePaths(session *Session, role harness.Stage) []sandbox.Path {
	workspace := sandbox.Path{Host: session.Workspace(), Guest: "/workspace", Access: sandbox.ReadWrite}
	if role == harness.StageJudge {
		workspace.Access = sandbox.ReadOnly
		return []sandbox.Path{
			workspace,
			{Host: session.Grade(), Guest: "/grade", Access: sandbox.ReadWrite},
			{Host: session.Home(), Guest: "/home", Access: sandbox.ReadWrite},
			{Host: session.Tmp(), Guest: "/tmp-session", Access: sandbox.ReadWrite},
		}
	}
	return []sandbox.Path{
		workspace,
		{Host: session.Tmp(), Guest: "/tmp-session", Access: sandbox.ReadWrite},
		{Host: session.Venv(), Guest: "/venv", Access: sandbox.ReadWrite},
		{Host: session.Home(), Guest: "/home", Access: sandbox.ReadWrite},
	}
}

// stageEnv adds what every stage needs whichever harness it runs: a temporary
// directory inside the session, and the session's virtual environment, so an
// install lands outside the repository.
func (r *Runtime) stageEnv(session *Session, env map[string]string) map[string]string {
	merged := map[string]string{
		"TMPDIR":       session.Tmp(),
		"VIRTUAL_ENV":  session.Venv(),
		"REPLICHAI_WS": session.Workspace(),
		// git reads the operator's global configuration unless told not
		// to, and the sandbox denies it — so every git command a stage
		// runs fails on a file it has no business reading. The
		// reproduction's own repository is configured by the runtime.
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,
		// Every pip in the session, not only the bootstrap's. A
		// reproduction installs what the paper needs, and it hits the
		// same wall: pip verifies through a trust service the sandbox
		// does not open. The sandbox still validates the index itself.
		"PIP_TRUSTED_HOST": strings.Join(PackageIndexHosts(), " "),
	}
	maps.Copy(merged, env)
	return merged
}

// drain reads whatever the current stage has written since the last poll,
// decodes it, appends it to the transcript, and returns the usage it reported.
// The pump's position is journalled, so a restart resumes rather than
// re-decoding — which is what keeps event ids stable across a restart.
func (r *Runtime) drain(ctx context.Context, session *Session, harnessName harness.Name, record *StageRecord) (managed.Usage, error) {
	if harnessName != r.config.Harness.Name() {
		return managed.Usage{}, fmt.Errorf("session was started with harness %q, this runtime has %q", harnessName, r.config.Harness.Name())
	}
	stream, err := r.config.Sandbox.Output(ctx, record.Handle, record.Offset)
	if err != nil {
		if errors.Is(err, sandbox.ErrGone) {
			return managed.Usage{}, nil
		}
		return managed.Usage{}, err
	}
	defer func() { _ = stream.Close() }()

	var (
		usage  managed.Usage
		events []managed.Event
		read   int64
	)
	reader := bufio.NewReaderSize(stream, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && err == nil {
			read += int64(len(line))
			// A stage that runs no harness has no harness stream: the
			// bootstrap is a shell, and its output is lines of text.
			var decoded []managed.Event
			if record.Role.Agentic() {
				var decodeErr error
				if decoded, decodeErr = r.config.Harness.Decode(record.Name, record.Lines, line); decodeErr != nil {
					return usage, decodeErr
				}
			} else if strings.TrimSpace(string(line)) != "" {
				decoded = []managed.Event{harness.PlainEvent(record.Name, record.Lines, line)}
			}
			record.Lines++
			for index := range decoded {
				redactEvent(&decoded[index], r.config.Catalog.Credentials())
				if decoded[index].Usage != nil {
					usage = harness.Fold(usage, *decoded[index].Usage)
				}
			}
			events = append(events, decoded...)
			continue
		}
		// A partial final line is left for the next poll: the stage is
		// still writing it, and decoding half a line would put a torn
		// record in the transcript and move the offset past it.
		if err != nil {
			break
		}
	}
	if err := session.AppendEvents(events); err != nil {
		return usage, err
	}
	record.Offset += read
	return usage, nil
}

// Events is the transcript, read from disk so it survives the process that
// produced it.
func (r *Runtime) Events(_ context.Context, ref managed.SessionRef) ([]managed.Event, error) {
	session, _, err := r.attach(ref)
	if err != nil {
		return nil, err
	}
	return session.Events()
}

// ListArtifacts is what the run produced.
func (r *Runtime) ListArtifacts(_ context.Context, ref managed.SessionRef) ([]managed.Artifact, error) {
	session, _, err := r.attach(ref)
	if err != nil {
		return nil, err
	}
	return session.Artifacts()
}

// FetchArtifact reads one of them.
func (r *Runtime) FetchArtifact(_ context.Context, ref managed.SessionRef, artifact managed.Artifact) ([]byte, error) {
	session, _, err := r.attach(ref)
	if err != nil {
		return nil, err
	}
	return session.Fetch(artifact)
}

// Cancel stops the running stage and records why the session ended. The
// directory is left intact, so evidence stays readable until Release.
func (r *Runtime) Cancel(ctx context.Context, ref managed.SessionRef) error {
	session, locator, err := r.attach(ref)
	if err != nil {
		return err
	}
	status, err := session.ReadStatus()
	if err != nil {
		return err
	}
	if status.State.Terminal() {
		return nil
	}
	if len(locator.Stages) > 0 {
		if err := r.config.Sandbox.Stop(ctx, locator.Stages[len(locator.Stages)-1].Handle); err != nil &&
			!errors.Is(err, sandbox.ErrGone) {
			return err
		}
	}
	r.finish(&status, managed.StateFailed, managed.ReasonCancelled, "")
	status.Ref = ref
	return session.WriteStatus(status)
}

// Release discards what the driver created. What happens to the directory is
// the retention policy's, and the default keeps it: the workspace is the
// reproduction, and the push may not have taken it yet.
func (r *Runtime) Release(ctx context.Context, ref managed.SessionRef) error {
	session, locator, err := r.attach(ref)
	if err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			return nil
		}
		return err
	}
	for _, stage := range locator.Stages {
		if err := r.config.Sandbox.Discard(ctx, stage.Handle); err != nil && !errors.Is(err, sandbox.ErrGone) {
			return err
		}
	}
	if r.discards(session) {
		return session.Remove()
	}
	return session.writeJSON(releasedFile, map[string]any{"released_at": r.config.Clock()})
}

// discards reports whether the retention policy says this session's directory
// may go.
func (r *Runtime) discards(session *Session) bool {
	switch r.config.Retention {
	case RetainNever:
		return true
	case RetainOnFailure:
		status, err := session.ReadStatus()
		return err == nil && status.State == managed.StateDone
	default:
		return false
	}
}

// attach resolves a reference to its session and journal, refusing one that
// belongs to another runtime or another driver. Two drivers' handles are both
// opaque strings, so without the driver check a pid would be looked up as a
// container name.
func (r *Runtime) attach(ref managed.SessionRef) (*Session, Locator, error) {
	if ref.Runtime != Name {
		return nil, Locator{}, &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	var locator Locator
	if err := json.Unmarshal(ref.Locator, &locator); err != nil || locator.Dir == "" {
		return nil, Locator{}, &managed.NotFoundError{Runtime: Name, ID: ref.ID}
	}
	session, err := OpenSession(locator.Dir)
	if err != nil {
		return nil, Locator{}, err
	}
	stored, err := session.ReadLocator()
	if err != nil {
		return nil, Locator{}, err
	}
	// A released session is gone as far as the contract is concerned, even
	// where the retention policy kept its directory: the evidence stays on
	// disk for the push and for the operator, but the handle no longer
	// resolves.
	if _, err := os.Stat(filepath.Join(session.Dir(), releasedFile)); err == nil {
		return nil, Locator{}, &managed.NotFoundError{Runtime: Name, ID: ref.ID}
	}
	if stored.Driver != r.config.Sandbox.Name() {
		return nil, Locator{}, fmt.Errorf("%w: session was started under the %q sandbox, this runtime has %q",
			managed.ErrRuntimeMismatch, stored.Driver, r.config.Sandbox.Name())
	}
	return session, stored, nil
}

// save persists the journal and the status, and stamps the checkpoint onto the
// status the caller is about to receive. It takes a pointer for that reason:
// stamping a copy wrote the new checkpoint to disk and handed the caller the
// old one, so the reference a caller threaded was always one observation
// behind the journal it named.
func (r *Runtime) save(session *Session, locator Locator, status *managed.Status, ref managed.SessionRef) error {
	if err := session.WriteLocator(locator); err != nil {
		return err
	}
	// The session's own counter, not one derived from the caller's
	// reference: a caller may poll twice with the same reference, and a
	// revision computed from it would repeat while the journal moved.
	revision, err := session.NextRevision()
	if err != nil {
		return err
	}
	if revision <= ref.Revision {
		revision = ref.Revision + 1
	}
	next, err := r.reference(session, locator, revision)
	if err != nil {
		return err
	}
	status.Ref = next
	return session.WriteStatus(*status)
}

func (r *Runtime) reference(session *Session, locator Locator, revision uint64) (managed.SessionRef, error) {
	locator.Version, locator.Dir = LocatorVersion, session.Dir()
	encoded, err := json.Marshal(locator)
	if err != nil {
		return managed.SessionRef{}, err
	}
	ref := managed.SessionRef{
		Runtime: Name, ID: session.ID(), Revision: revision,
		LocatorVersion: LocatorVersion, Locator: encoded,
	}
	return ref, ref.Validate(managed.DefaultMaxLocatorBytes)
}

// reserve allocates a fresh session and records it as this caller's
// reservation. Each call makes its own: two concurrent Capabilities calls get
// two workspaces, so neither templates its instructions against the other's.
func (r *Runtime) reserve() (*Session, error) {
	session, err := r.newSession()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.reservations == nil {
		r.reservations = make(map[string]*Session)
	}
	r.reservations[session.Workspace()] = session
	r.mu.Unlock()
	return session, nil
}

// claim resolves the session a Start belongs to. A specification carries the
// workspace it was templated against — the audit reads Capabilities' output
// directory and writes it into the instructions and the task — so the
// reservation whose workspace the specification names is this caller's. A lone
// outstanding reservation is taken even where the specification names no path,
// which is the single-session flow a test drives. With nothing to bind to,
// Start allocates its own, which is a Start with no preceding Capabilities.
func (r *Runtime) claim(spec managed.SessionSpec) (*Session, error) {
	r.mu.Lock()
	if session := r.bindLocked(spec); session != nil {
		r.mu.Unlock()
		return session, nil
	}
	r.mu.Unlock()
	return r.newSession()
}

// bindLocked returns and removes the reservation this specification names, or
// the lone reservation where it names none. It runs under r.mu.
func (r *Runtime) bindLocked(spec managed.SessionSpec) *Session {
	for workspace, session := range r.reservations {
		if strings.Contains(spec.Instructions, workspace) || strings.Contains(spec.Task, workspace) {
			delete(r.reservations, workspace)
			return session
		}
	}
	if len(r.reservations) == 1 {
		for workspace, session := range r.reservations {
			delete(r.reservations, workspace)
			return session
		}
	}
	return nil
}

// newSession makes a fresh session directory under the root.
func (r *Runtime) newSession() (*Session, error) {
	id, err := sessionID()
	if err != nil {
		return nil, err
	}
	return CreateSession(r.config.Root, id, r.config.Lookup)
}

// writeAttachments places a specification's attachments into the workspace,
// refusing one whose path escapes it.
func (r *Runtime) writeAttachments(session *Session, spec managed.SessionSpec) error {
	for _, resource := range spec.Resources {
		if resource.Kind != managed.ResourceAttachment {
			continue
		}
		target, err := containedPath(session.Workspace(), resource.Attachment.Path)
		if err != nil {
			return err
		}
		if err := writeFileMode(target, resource.Attachment.Content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// harnessVersion records what actually produced the work. It is best effort:
// an unavailable version leaves the field empty rather than failing a run over
// a description of the harness.
func (r *Runtime) harnessVersion(ctx context.Context) string {
	argv := r.config.Harness.VersionArgv()
	if len(argv) == 0 {
		return ""
	}
	if reporter, ok := r.config.Sandbox.(interface {
		Version(context.Context, []string) (string, error)
	}); ok {
		version, err := reporter.Version(ctx, argv)
		if err != nil {
			return ""
		}
		return version
	}
	return ""
}

// recordPackages folds what the bootstrap actually resolved into the run's
// environment record. Asking for numpy is not the same as having numpy 2.1.0,
// and the record is what makes two reproductions comparable.
func (r *Runtime) recordPackages(session *Session) {
	manifest, err := os.ReadFile(filepath.Join(session.Venv(), PackageManifest))
	if err != nil {
		return
	}
	record, err := session.ReadEnvironment()
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(manifest), "\n") {
		name, version, found := strings.Cut(strings.TrimSpace(line), "==")
		if !found || name == "" {
			continue
		}
		record.Packages = append(record.Packages, EnvironmentPkg{Manager: "pip", Name: name, Version: version})
	}
	_ = session.WriteEnvironment(record)
}

func readSpec(session *Session) (managed.SessionSpec, error) {
	var spec managed.SessionSpec
	err := session.readJSON(specFile, &spec)
	return spec, err
}

// currentRound is which grading round the session is in, counted from zero:
// one per worker-side stage. It is the single definition, used both to tell
// the judge which file to write and to read that file back — two definitions
// is how the judge came to write verdict-1.json while the runtime looked for
// verdict-0.json.
func currentRound(locator Locator) int {
	rounds := 0
	for _, stage := range locator.Stages {
		if stage.Role == harness.StageWorker || stage.Role == harness.StageRevision {
			rounds++
		}
	}
	return rounds - 1
}

// judgeAttempts is how many times this round has already been graded, which
// is what bounds the retry of an unparsable verdict.
func judgeAttempts(locator Locator, round int) int {
	attempts, workers := 0, 0
	for _, stage := range locator.Stages {
		switch stage.Role {
		case harness.StageWorker, harness.StageRevision:
			workers++
		case harness.StageJudge:
			if workers-1 == round {
				attempts++
			}
		}
	}
	return attempts
}

func revisionsSpent(locator Locator) int {
	spent := 0
	for _, stage := range locator.Stages {
		if stage.Role == harness.StageRevision {
			spent++
		}
	}
	return spent
}

// redactEvent removes any configured credential from an event before it is
// written. A harness that echoes its own environment into a tool result would
// otherwise put one into the published evidence.
func redactEvent(event *managed.Event, credentials []string) {
	for _, credential := range credentials {
		if credential == "" {
			continue
		}
		event.Content = strings.ReplaceAll(event.Content, credential, "[redacted]")
		if len(event.Raw) > 0 {
			event.Raw = []byte(strings.ReplaceAll(string(event.Raw), credential, "[redacted]"))
		}
	}
}

func sessionID() (string, error) {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// Workspace is the directory a referenced session worked in. It is what lets a
// later process publish a session it did not start: the reference names the
// session, the session names its directory, and the push happens from there.
//
// It is deliberately not on the managed contract. A hosted runtime has no
// directory to name, and a caller that needs one is asking a question only a
// local runtime can answer.
func (r *Runtime) Workspace(ref managed.SessionRef) (string, error) {
	session, _, err := r.attach(ref)
	if err != nil {
		return "", err
	}
	return session.Workspace(), nil
}
