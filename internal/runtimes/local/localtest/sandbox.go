// Package localtest is a scripted sandbox driver: it runs no process and
// isolates nothing, and instead writes the stream a harness would have written
// and the files a stage would have produced. It exists so the local runtime's
// own lifecycle — the stage sequence, the pump, the grading loop, recovery by
// reference — is testable without a machine that has srt or an engine on it.
package localtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Script is what the fake driver does with each stage.
type Script struct {
	// WorkerExit is the exit code every worker and revision stage reports.
	WorkerExit int
	// Grades are the grades successive judge stages write, in order. A
	// judge past the end of the slice repeats the last one.
	Grades []string
	// MalformedRounds are the judge rounds that write unparsable output
	// instead of a verdict, keyed by round.
	MalformedRounds map[int]bool
	// SilentRounds are the judge rounds that write no verdict at all.
	SilentRounds map[int]bool
	// PollsBeforeExit is how many observations a stage stays running for.
	PollsBeforeExit int
	// FailLaunch makes Launch fail, which is how a driver that cannot start
	// anything is exercised.
	FailLaunch error
	// NotReady makes Preflight fail.
	NotReady error
}

// Sandbox is the scripted driver.
type Sandbox struct {
	script Script

	mu     sync.Mutex
	stages map[string]*stage
	next   int
}

type stage struct {
	spec      sandbox.StageSpec
	polls     int
	exit      int
	running   bool
	discarded bool
	role      string
	round     int
}

// New returns a scripted driver. The zero Script is a usable happy path: every
// worker exits zero and every judge passes.
func New(script Script) *Sandbox {
	if len(script.Grades) == 0 {
		script.Grades = []string{"passed"}
	}
	return &Sandbox{script: script, stages: make(map[string]*stage)}
}

// Name is the driver this fake stands in for. It answers to the host driver's
// name so a session started under it routes the way a real one would.
func (s *Sandbox) Name() sandbox.DriverName { return sandbox.Host }

// Preflight reports whatever the script says.
func (s *Sandbox) Preflight(context.Context) error { return s.script.NotReady }

// Capabilities is a permissive declaration: this driver enforces nothing, and
// it says so by declaring what the real host driver declares rather than
// everything.
func (s *Sandbox) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		PackageManagers: map[string]bool{"pip": true, "npm": true},
		NetworkModes: map[string]bool{
			sandbox.NetworkNone: true, sandbox.NetworkAllowlist: true, sandbox.NetworkOpen: true,
		},
		MaxAttachmentBytes: 64 << 20,
	}
}

// Launch records the stage and writes what it would have produced.
func (s *Sandbox) Launch(_ context.Context, spec sandbox.StageSpec) (sandbox.StageHandle, error) {
	if s.script.FailLaunch != nil {
		return sandbox.StageHandle{}, s.script.FailLaunch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	role, _, _ := strings.Cut(spec.Name, "-")
	entry := &stage{spec: spec, running: true, role: role}
	if role == "judge" {
		entry.round = s.judgeRoundLocked()
	}
	id := fmt.Sprintf("fake:%d", s.next)
	s.next++
	s.stages[id] = entry
	if err := s.produce(entry); err != nil {
		return sandbox.StageHandle{}, err
	}
	return sandbox.StageHandle{Driver: sandbox.Host, ID: id}, nil
}

func (s *Sandbox) judgeRoundLocked() int {
	round := 0
	for _, existing := range s.stages {
		if existing.role == "judge" {
			round++
		}
	}
	return round
}

// produce writes the stage's stream and its files. A worker leaves a
// reproduction in the workspace; a judge leaves a verdict in the only
// directory it may write.
func (s *Sandbox) produce(entry *stage) error {
	lines := []string{
		`{"type":"system","subtype":"init","model":"claude-opus-5"}`,
		fmt.Sprintf(`{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"text","text":%q}],"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":5}}}`,
			"running "+entry.spec.Name),
		fmt.Sprintf(`{"type":"result","subtype":"success","result":%q,"total_cost_usd":0.01,"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":5}}`,
			"finished "+entry.spec.Name),
	}
	if err := os.MkdirAll(filepath.Dir(entry.spec.LogPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(entry.spec.LogPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	switch entry.role {
	case "judge":
		return s.writeVerdict(entry)
	default:
		entry.exit = s.script.WorkerExit
		workspace := writablePath(entry.spec, "/workspace")
		if workspace == "" || entry.exit != 0 {
			return nil
		}
		return os.WriteFile(filepath.Join(workspace, "REPRODUCIBILITY.md"),
			[]byte("# Reproduction\n\nWritten by "+entry.spec.Name+".\n"), 0o600)
	}
}

func (s *Sandbox) writeVerdict(entry *stage) error {
	grade := writablePath(entry.spec, "/grade")
	if grade == "" {
		return fmt.Errorf("the judge stage was given no writable grade directory")
	}
	round := 0
	for _, path := range entry.spec.Argv {
		if index := strings.Index(path, "verdict-"); index >= 0 {
			_, _ = fmt.Sscanf(path[index:], "verdict-%d.json", &round)
		}
	}
	if s.script.SilentRounds[entry.round] {
		return nil
	}
	target := filepath.Join(grade, fmt.Sprintf("verdict-%d.json", round))
	if s.script.MalformedRounds[entry.round] {
		return os.WriteFile(target, []byte("I read the reproduction and it seems fine to me."), 0o600)
	}
	verdict := map[string]any{
		"grade":       s.gradeFor(entry.round),
		"explanation": "scripted",
		"model":       map[string]string{"provider": "anthropic", "id": "claude-opus-5"},
		"criteria": []map[string]any{
			{"id": 1, "name": "Verdict", "passed": s.gradeFor(entry.round) == "passed", "evidence": "REPRODUCIBILITY.md"},
		},
	}
	body, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, body, 0o600)
}

func (s *Sandbox) gradeFor(round int) string {
	if round >= len(s.script.Grades) {
		return s.script.Grades[len(s.script.Grades)-1]
	}
	return s.script.Grades[round]
}

// Observe reports the stage running until the scripted number of polls has
// passed, so a caller exercises the running path as well as the terminal one.
func (s *Sandbox) Observe(_ context.Context, handle sandbox.StageHandle) (sandbox.StageStatus, error) {
	if err := sandbox.MatchHandle(sandbox.Host, handle); err != nil {
		return sandbox.StageStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.stages[handle.ID]
	if !ok {
		return sandbox.StageStatus{Gone: true}, nil
	}
	if entry.discarded {
		return sandbox.StageStatus{Gone: true}, nil
	}
	if entry.running {
		entry.polls++
		if entry.polls > s.script.PollsBeforeExit {
			entry.running = false
		}
	}
	return sandbox.StageStatus{Running: entry.running, ExitCode: entry.exit}, nil
}

// Output streams the stage's log from an offset, from a file the stage itself
// never had write access to.
func (s *Sandbox) Output(_ context.Context, handle sandbox.StageHandle, offset int64) (io.ReadCloser, error) {
	if err := sandbox.MatchHandle(sandbox.Host, handle); err != nil {
		return nil, err
	}
	s.mu.Lock()
	entry, ok := s.stages[handle.ID]
	s.mu.Unlock()
	if !ok {
		return nil, sandbox.ErrGone
	}
	file, err := os.Open(entry.spec.LogPath)
	if os.IsNotExist(err) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// Stop ends a running stage.
func (s *Sandbox) Stop(_ context.Context, handle sandbox.StageHandle) error {
	if err := sandbox.MatchHandle(sandbox.Host, handle); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.stages[handle.ID]; ok {
		entry.running, entry.exit = false, -1
	}
	return nil
}

// Discard forgets the stage. It never touches the session directory.
func (s *Sandbox) Discard(_ context.Context, handle sandbox.StageHandle) error {
	if err := sandbox.MatchHandle(sandbox.Host, handle); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.stages[handle.ID]; ok {
		entry.discarded, entry.running = true, false
	}
	return nil
}

// Stages is every stage this driver was asked to launch, in order, so a test
// can assert what the runtime built rather than only what came out.
func (s *Sandbox) Stages() []sandbox.StageSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	specs := make([]sandbox.StageSpec, 0, len(s.stages))
	for index := range s.next {
		if entry, ok := s.stages[fmt.Sprintf("fake:%d", index)]; ok {
			specs = append(specs, entry.spec)
		}
	}
	return specs
}

// writablePath is the host path behind one guest destination, or empty where
// the stage was not given it writable.
func writablePath(spec sandbox.StageSpec, guest string) string {
	for _, path := range spec.Paths {
		if path.Destination() == guest && path.Access == sandbox.ReadWrite {
			return path.Host
		}
	}
	return ""
}
