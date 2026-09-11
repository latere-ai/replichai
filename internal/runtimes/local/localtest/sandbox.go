// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package localtest is a scripted sandbox driver. It runs no process and
// isolates nothing. Instead it writes the stream a harness would have written
// and the files a stage would have produced. It exists so that the local
// runtime's lifecycle, which is the stage sequence, the pump, the grading loop
// and recovery by reference, can be tested without a machine that has srt or a
// container engine.
package localtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

// Script describes what the fake driver does with each stage.
type Script struct {
	// WorkerExit is the exit code every worker and revision stage reports.
	WorkerExit int
	// BootstrapExit is the exit code the bootstrap stage reports, so that a
	// session whose dependencies fail to install can be tested separately
	// from one whose agent failed.
	BootstrapExit int
	// Grades are the grades successive judge stages write, in order. A
	// judge past the end of the slice repeats the last one.
	Grades []string
	// MalformedRounds are the judge rounds that write unparsable output
	// instead of a verdict, keyed by round.
	MalformedRounds map[int]bool
	// SilentRounds are the judge rounds that write no verdict at all.
	SilentRounds map[int]bool
	// PollsBeforeExit is the number of observations a stage stays running
	// for.
	PollsBeforeExit int
	// FinalLineOnExit holds back a harness stage's last line until the
	// observation that finds the stage finished, so that the line is written
	// and the stage exits between two polls. This reproduces the race a real
	// stage has: a runtime that drains before it observes loses that line,
	// and the line a harness writes last is its result event, which carries
	// the run's total cost.
	FinalLineOnExit bool
	// WorkerCommits makes the worker commit what it wrote, as a real worker
	// does. Without a commit the push path has nothing to send, so a test
	// that reaches the push needs a workspace with history in it.
	WorkerCommits bool
	// FailLaunch makes Launch fail, which is how a driver that cannot start
	// anything is tested.
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
	// finalLine is a harness stage's last line, held back from the log until
	// the observation that finds the stage finished. It is empty once
	// written, and empty from the start when the script does not hold it
	// back.
	finalLine string
}

// New returns a scripted driver. The zero Script is a usable success path:
// every worker exits zero and every judge passes.
func New(script Script) *Sandbox {
	if len(script.Grades) == 0 {
		script.Grades = []string{"passed"}
	}
	return &Sandbox{script: script, stages: make(map[string]*stage)}
}

// Name returns the name of the driver this fake stands in for. It uses the
// host driver's name so that a session started under it routes the way a real
// one would.
func (s *Sandbox) Name() sandbox.DriverName { return sandbox.Host }

// Preflight returns the script's NotReady error.
func (s *Sandbox) Preflight(context.Context) error { return s.script.NotReady }

// Capabilities returns a permissive declaration. This driver enforces nothing,
// and it declares what the real host driver declares rather than every mode.
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

// writeFinalLine appends the stage's held-back last line to its log, once.
func (e *stage) writeFinalLine() error {
	if e.finalLine == "" {
		return nil
	}
	file, err := os.OpenFile(e.spec.LogPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(e.finalLine + "\n"); err != nil {
		return err
	}
	e.finalLine = ""
	return nil
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

// produce writes the stage's stream and its files. A worker writes a
// reproduction in the workspace; a judge writes a verdict in the only
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
	// When the script asks, a harness stage's last line is held back so that
	// it is written between two polls rather than at launch. That is the race
	// a runtime that drains before it observes loses.
	if s.script.FinalLineOnExit && entry.role != "bootstrap" && len(lines) > 1 {
		entry.finalLine = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	if err := os.WriteFile(entry.spec.LogPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	switch entry.role {
	case "judge":
		return s.writeVerdict(entry)
	case "bootstrap":
		entry.exit = s.script.BootstrapExit
		return nil
	default:
		entry.exit = s.script.WorkerExit
		workspace := writablePath(entry.spec, "/workspace")
		if workspace == "" || entry.exit != 0 {
			return nil
		}
		if err := os.WriteFile(filepath.Join(workspace, "REPRODUCIBILITY.md"),
			[]byte("# Reproduction\n\nWritten by "+entry.spec.Name+".\n"), 0o600); err != nil {
			return err
		}
		if !s.script.WorkerCommits {
			return nil
		}
		return commit(workspace, entry.spec.Env, entry.spec.Name)
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

// Observe reports the stage as running until the scripted number of polls has
// passed, so that a caller exercises the running path as well as the finished
// one.
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
			// The stage finishes on this observation, and its held-back last
			// line is written now, after any drain that ran before this
			// observation. A runtime that does not drain again loses it.
			if err := entry.writeFinalLine(); err != nil {
				return sandbox.StageStatus{}, err
			}
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

// Stages returns every stage this driver was asked to launch, in order, so
// that a test can check what the runtime built and not only what it produced.
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

// writablePath returns the host path behind one guest destination, or the
// empty string when the stage was not given that destination as writable.
func writablePath(spec sandbox.StageSpec, guest string) string {
	for _, path := range spec.Paths {
		if path.Destination() == guest && path.Access == sandbox.ReadWrite {
			return path.Host
		}
	}
	return ""
}

// commit is the only operation for which this driver runs a process. A real
// worker commits its work, and the host's push reads a committed tree rather
// than a directory. It runs git in the stage's own environment, because a
// driver that read this process's environment would not stand in for one that
// is given everything it may see.
func commit(workspace string, environ map[string]string, stage string) error {
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "Reproduction written by " + stage}} {
		command := exec.Command("git", append([]string{"-C", workspace}, args...)...) //nolint:gosec,noctx // a fixed git verb over this package's own arguments
		command.Env = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}
		for name, value := range environ {
			command.Env = append(command.Env, name+"="+value)
		}
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}
