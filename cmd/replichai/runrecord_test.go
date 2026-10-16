// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managed "latere.ai/x/managed-agents"
	managedmemory "latere.ai/x/managed-agents/memory"

	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/publish"
)

// manifestIn reads the manifest a run left in its repository on the fake
// forge.
func manifestIn(t *testing.T, writes *forgeWrites, repo string) publish.Manifest {
	t.Helper()
	raw := writes.file(repo, publish.ManifestFile)
	if raw == nil {
		t.Fatalf("%s holds no manifest", repo)
	}
	var manifest publish.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("the manifest in %s is not JSON: %v\n%s", repo, err, raw)
	}
	return manifest
}

// TestARunsManifestSaysWhichRunItWas drives a project that runs one paper
// twice and reads back each repository's manifest. Before the run record, the
// two manifests differed only in their repository name: nothing said which run
// either was, what it ran on or how it ended, and the pilot that mixed two
// agent systems under one method digest could only be untangled from the
// transcripts.
func TestARunsManifestSaysWhichRunItWas(t *testing.T) {
	const ref = "10.1145/3654777.3676417"
	forge, writes := fakeForgeRecording(t, nil)
	repos := &fakeRepositories{forge: forge}
	runner := testProjectRunner(t, repos)
	runner.project = "chi-2026-audit"
	file := batch.File{Project: "chi-2026-audit", Papers: []batch.Entry{{Paper: ref, Runs: 2, Model: "balanced"}}}

	state, err := batch.Run(t.Context(), file, runner, batch.Options{
		Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json"), Publishing: true,
	})
	if err != nil {
		t.Fatalf("Run: %v: %+v", err, state.Entries)
	}
	for _, entry := range state.Entries {
		record := manifestIn(t, writes, entry.Repo).Run
		if record == nil {
			t.Fatalf("run %d: the manifest carries no run record", entry.Run)
		}
		if record.Project != "chi-2026-audit" || record.Run != entry.Run || record.Runs != 2 {
			t.Errorf("run %d is placed as %q run %d of %d", entry.Run, record.Project, record.Run, record.Runs)
		}
		if record.Runtime != "memory" || record.Session != entry.Session || record.Session == "" {
			t.Errorf("run %d ran as %q session %q, want memory session %q", entry.Run, record.Runtime, record.Session, entry.Session)
		}
		if record.RequestedModel != "balanced" || record.Model == nil || record.Model.ID != entry.Model.ID {
			t.Errorf("run %d asked for %q and got %+v, want balanced and %+v", entry.Run, record.RequestedModel, record.Model, entry.Model)
		}
		if record.State != "done" || record.Reason != string(managed.ReasonSucceeded) {
			t.Errorf("run %d ended %q for %q, want done for succeeded", entry.Run, record.State, record.Reason)
		}
		if !strings.HasPrefix(record.PromptDigest, "sha256:") {
			t.Errorf("run %d has prompt digest %q", entry.Run, record.PromptDigest)
		}
	}
	first, second := manifestIn(t, writes, state.Entries[0].Repo).Run, manifestIn(t, writes, state.Entries[1].Repo).Run
	if first.PromptDigest != second.PromptDigest {
		t.Error("two runs of one paper under one method were given different prompts")
	}
}

// TestARunThatNeverStartedLeavesAManifestSayingSo checks the run that fails
// before a session exists. Its repository was already created, and it used to
// stay as it was created: in the pilot, eight such repositories held no commit
// and nothing in them said why, so whether they were failures of the papers or
// of the launch had to be read off their creation times.
func TestARunThatNeverStartedLeavesAManifestSayingSo(t *testing.T) {
	const ref = "10.1145/3654777.3676417"
	forge, writes := fakeForgeRecording(t, nil)
	repos := &fakeRepositories{forge: forge}
	runner := testProjectRunner(t, repos)
	runner.project = "chi-2026-audit"
	runner.runtimeName = "no-such-runtime"
	file := batch.File{Papers: []batch.Entry{{Paper: ref}}}

	state, err := batch.Run(t.Context(), file, runner, batch.Options{
		Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json"), Publishing: true,
	})
	if !errors.Is(err, batch.ErrIncomplete) {
		t.Fatalf("Run = %v, want the batch to report a failed run", err)
	}
	entry := state.Entries[0]
	if entry.Phase != batch.Failed || !strings.Contains(entry.Error, "no-such-runtime") {
		t.Fatalf("the run is recorded as %s: %q", entry.Phase, entry.Error)
	}
	if entry.Repo == "" {
		t.Fatal("the failed run lost the repository it was given, so a retry would allocate another")
	}
	manifest := manifestIn(t, writes, entry.Repo)
	record := manifest.Run
	if record == nil {
		t.Fatal("the manifest of a run that never started carries no run record")
	}
	if record.State != "failed" || record.Reason != publish.ReasonNotStarted {
		t.Errorf("the run is recorded as %q for %q, want failed for %s", record.State, record.Reason, publish.ReasonNotStarted)
	}
	if record.Project != "chi-2026-audit" || record.Run != 1 || record.Runs != 1 || record.Session != "" {
		t.Errorf("the run is placed as %+v", record)
	}
	if manifest.Findings == nil || manifest.Findings.Reproduction {
		t.Errorf("the manifest claims a reproduction for a run that never started: %+v", manifest.Findings)
	}
}

// localLike is the memory runtime with a session directory on this disk, which
// is the one thing a local runtime has that a hosted one does not.
type localLike struct {
	managed.Runtime
	workspace string
	err       error
}

func (l localLike) Workspace(managed.SessionRef) (string, error) { return l.workspace, l.err }

// TestTheRunRecordReadsTheSessionDirectory checks what a session on this
// machine adds to its record: the harness and the sandbox from the environment
// the host wrote, and the rounds from the grades it kept.
func TestTheRunRecordReadsTheSessionDirectory(t *testing.T) {
	session := t.TempDir()
	for name, body := range map[string]string{
		"session/environment.json": `{"sandbox":{"driver":"host","network_mode":"allowlist"},"harness":{"name":"claude-code","version":"2.1.269"},"network":{"allowed":["github.com","arxiv.org"]}}`,
		"grade/verdict-0.json":     `{}`,
		"grade/verdict-1.json":     `{}`,
	} {
		path := filepath.Join(session, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := managed.Status{Ref: managed.SessionRef{Runtime: "local", ID: "s-1"}, State: managed.StateDone, TerminalReason: managed.ReasonSucceeded}
	stderr := newTemp(t)

	record := runRecord(localLike{Runtime: managedmemory.New(), workspace: filepath.Join(session, "workspace")}, status, "best", "sha256:cafe", stderr)
	if record.Harness != "claude-code" || record.HarnessVersion != "2.1.269" || record.Sandbox != "host" || record.Network != "allowlist" || record.NetworkAllowed != 2 {
		t.Errorf("the environment did not reach the record: %+v", record)
	}
	if record.Rounds != 2 || record.RequestedModel != "best" || record.PromptDigest != "sha256:cafe" || record.Session != "s-1" {
		t.Errorf("record = %+v", record)
	}

	// A hosted runtime keeps no directory, and its record is what the status
	// says, with nothing reported as missing.
	hosted := runRecord(managedmemory.New(), status, "best", "", stderr)
	if hosted.Harness != "" || hosted.Rounds != 0 || hosted.State != "done" {
		t.Errorf("a hosted session's record = %+v", hosted)
	}
	if body := readAll(t, stderr); body != "" {
		t.Errorf("nothing was missing, and this was printed: %q", body)
	}

	// A session directory that cannot be found is said so, and the record
	// still carries what the status reports.
	lost := runRecord(localLike{Runtime: managedmemory.New(), err: errors.New("the session directory is gone")}, status, "best", "", stderr)
	if lost.State != "done" || lost.Harness != "" {
		t.Errorf("record of a session whose directory is gone = %+v", lost)
	}
	if body := readAll(t, stderr); !strings.Contains(body, "the session directory is gone") {
		t.Errorf("the missing directory was not reported: %q", body)
	}

	// A run that never started has a record of its own.
	if record := notStarted("best"); record.State != "failed" || record.Reason != publish.ReasonNotStarted || record.RequestedModel != "best" {
		t.Errorf("notStarted = %+v", record)
	}
}
