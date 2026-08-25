package local

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// Clock is the time source, so a test does not depend on wall time.
	Clock func() time.Time
}

// Runtime executes a managed session on this machine. It owns the stage
// sequence, the transcript pump, and the grading loop; it owns no isolation
// (that is the driver) and no harness specifics (that is the adapter).
type Runtime struct {
	config Config

	// mu guards pending, which is the session allocated by Capabilities and
	// consumed by the next Start. The two are paired by the caller — the
	// audit reads the output directory, then starts the session that writes
	// to it — and on this runtime that directory is per-session, so the
	// allocation has to happen at the first of the two calls.
	mu      sync.Mutex
	pending *Session
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
	if _, _, err := r.config.Catalog.Resolve(r.config.Harness.Name(), spec.Model); err != nil {
		return fmt.Errorf("%w: %s", managed.ErrInvalidSpec, err)
	}
	return nil
}

// Start creates the session and launches its first worker stage. It returns a
// reference only once that reference alone is enough to recover the session:
// the directory exists, the journal is written, and the stage is detached.
func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (managed.SessionRef, error) {
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
	session, err := r.take()
	if err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.writeAttachments(session, spec); err != nil {
		return managed.SessionRef{}, err
	}
	if err := session.writeJSON(specFile, spec); err != nil {
		return managed.SessionRef{}, err
	}
	record := NewEnvironment(r.config.Sandbox.Name(), r.config.Harness.Name(), model, gateway, string(spec.Sandbox.Network.Mode))
	record.Harness.Version = r.harnessVersion(ctx)
	if err := session.WriteEnvironment(record); err != nil {
		return managed.SessionRef{}, err
	}

	locator := Locator{Driver: r.config.Sandbox.Name(), Harness: r.config.Harness.Name()}
	if err := r.launch(ctx, session, &locator, spec, model, gateway, harness.StageWorker, spec.Task); err != nil {
		return managed.SessionRef{}, err
	}
	status := managed.Status{
		Model: model, State: managed.StateRunning, StartedAt: r.config.Clock(),
		Usage: managed.Usage{Model: model},
	}
	ref, err := r.reference(session, locator, 1)
	if err != nil {
		return managed.SessionRef{}, err
	}
	status.Ref = ref
	if err := session.WriteStatus(status); err != nil {
		return managed.SessionRef{}, err
	}
	return ref, nil
}

// Poll advances the session: it drains whatever the current stage has written,
// observes it, and where it has finished decides what comes next — a judge
// after a worker, a revision after a rejection, or a terminal state.
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
	usage, err := r.drain(session, locator.Harness, current)
	if err != nil {
		return managed.Status{}, err
	}
	status.Usage = harness.Fold(status.Usage, usage)
	status.Model = model

	observed, err := r.config.Sandbox.Observe(ctx, current.Handle)
	if err != nil {
		return managed.Status{}, err
	}
	if observed.Running {
		status.State = managed.StateRunning
		return status, r.save(session, locator, status, ref)
	}
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
	return status, r.save(session, locator, status, ref)
}

// advance decides what follows a finished stage.
func (r *Runtime) advance(ctx context.Context, session *Session, locator *Locator, status *managed.Status,
	spec managed.SessionSpec, model managed.ModelRef, gateway harness.Gateway) error {
	current := locator.Stages[len(locator.Stages)-1]
	switch current.Role {
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
		_ = file
		r.finish(status, managed.StateDone, managed.ReasonEvaluationFailed, "")
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
	argv, err := r.config.Harness.Argv(role, request)
	if err != nil {
		return err
	}
	env, secrets, err := r.config.Harness.Env(role, request)
	if err != nil {
		return err
	}
	files, err := r.config.Harness.Files(role, request)
	if err != nil {
		return err
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
	stage := sandbox.StageSpec{
		Name:    name,
		Argv:    argv,
		Env:     r.stageEnv(session, env),
		EnvFile: secrets,
		Workdir: session.Workspace(),
		Paths:   r.stagePaths(session, role),
		Network: sandbox.Network{Mode: string(spec.Sandbox.Network.Mode), Domains: spec.Sandbox.Network.AllowedDomains},
		LogPath: session.RawLog(name),
	}
	if stage.Network.Mode == "" {
		stage.Network.Mode = sandbox.NetworkNone
	}
	if role == harness.StageJudge {
		stage.Workdir = session.Grade()
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
// directory inside the session, and the session's virtual environment first on
// PATH so an install lands outside the repository.
func (r *Runtime) stageEnv(session *Session, env map[string]string) map[string]string {
	merged := map[string]string{
		"TMPDIR":       session.Tmp(),
		"VIRTUAL_ENV":  session.Venv(),
		"REPLICHAI_WS": session.Workspace(),
	}
	for name, value := range env {
		merged[name] = value
	}
	return merged
}

// drain reads whatever the current stage has written since the last poll,
// decodes it, appends it to the transcript, and returns the usage it reported.
// The pump's position is journalled, so a restart resumes rather than
// re-decoding — which is what keeps event ids stable across a restart.
func (r *Runtime) drain(session *Session, harnessName harness.Name, record *StageRecord) (managed.Usage, error) {
	if harnessName != r.config.Harness.Name() {
		return managed.Usage{}, fmt.Errorf("session was started with harness %q, this runtime has %q", harnessName, r.config.Harness.Name())
	}
	stream, err := r.config.Sandbox.Output(context.Background(), record.Handle, record.Offset)
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
			decoded, decodeErr := r.config.Harness.Decode(record.Name, record.Lines, line)
			if decodeErr != nil {
				return usage, decodeErr
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
	if usage.TotalTokens() > 0 || len(usage.Details) > 0 {
		record.Usage = &usage
	}
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

// save persists the journal and the status together, then stamps the
// reference's revision so a checkpoint can be written with compare-and-swap.
func (r *Runtime) save(session *Session, locator Locator, status managed.Status, ref managed.SessionRef) error {
	if err := session.WriteLocator(locator); err != nil {
		return err
	}
	next, err := r.reference(session, locator, ref.Revision+1)
	if err != nil {
		return err
	}
	status.Ref = next
	return session.WriteStatus(status)
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

// reserve allocates the session the next Start will use, or returns the one
// already allocated.
func (r *Runtime) reserve() (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending != nil {
		return r.pending, nil
	}
	id, err := sessionID()
	if err != nil {
		return nil, err
	}
	session, err := CreateSession(r.config.Root, id)
	if err != nil {
		return nil, err
	}
	r.pending = session
	return session, nil
}

// take consumes the reserved session, so the next Capabilities allocates a
// fresh one and one runtime can start more than one session.
func (r *Runtime) take() (*Session, error) {
	session, err := r.reserve()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.pending = nil
	r.mu.Unlock()
	return session, nil
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
