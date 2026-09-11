// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/provider"
)

func TestSatisfiesTheProviderContract(t *testing.T) {
	var _ provider.Provider = New()
}

func TestRecordsWhatItWasAskedToDo(t *testing.T) {
	p := New()
	ctx := context.Background()

	ws, err := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "w"})
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	worker, err := p.EnsureWorker(ctx, provider.WorkerSpec{Name: "n", Instructions: "i", Model: "m"})
	if err != nil {
		t.Fatalf("EnsureWorker: %v", err)
	}
	handle, err := p.StartRun(ctx, worker, ws, provider.RunSpec{Task: "t", Rubric: "r"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := p.CloseRun(ctx, handle); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}

	if len(p.Workspaces()) != 1 || len(p.Workers()) != 1 || len(p.Runs()) != 1 {
		t.Error("provider did not record the calls it received")
	}
	if closed := p.Closed(); len(closed) != 1 || closed[0] != handle.ID {
		t.Errorf("closed = %v, want [%s]", closed, handle.ID)
	}
}

// The contract carries an experiment's shape (007b): a read-only mount and a
// dataset attached outside it. The provider records the RunSpec verbatim, so a
// runner's choices can be checked without a backend.
func TestRecordsReadOnlyMountAndAttachments(t *testing.T) {
	p := New()
	ctx := context.Background()
	ws, _ := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "w"})
	worker, _ := p.EnsureWorker(ctx, provider.WorkerSpec{Name: "n", Instructions: "i", Model: "m"})

	spec := provider.RunSpec{
		Task:  "run the analysis",
		Mount: provider.RepoMount{URL: "https://example.com/r", Token: "t", MountPath: "/workspace/repro", ReadOnly: true},
		Attachments: []provider.Attachment{
			{Path: "/workspace/input/data.csv", Content: []byte("rt\n1\n")},
		},
	}
	if _, err := p.StartRun(ctx, worker, ws, spec); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	runs := p.Runs()
	if len(runs) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(runs))
	}
	got := runs[0]
	if !got.Mount.ReadOnly {
		t.Error("read-only mount intent was not recorded")
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "/workspace/input/data.csv" {
		t.Errorf("attachment not recorded: %+v", got.Attachments)
	}
	// The attachment path is outside the mount, which is the structural
	// guarantee.
	if strings.HasPrefix(got.Attachments[0].Path, got.Mount.MountPath) {
		t.Error("the dataset must land outside the mount")
	}
}

func TestEachRunGetsItsOwnIdentity(t *testing.T) {
	p := New()
	ctx := context.Background()
	ws, _ := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "w"})
	worker, _ := p.EnsureWorker(ctx, provider.WorkerSpec{Name: "n", Instructions: "i", Model: "m"})
	spec := provider.RunSpec{Task: "t", Rubric: "r"}

	first, _ := p.StartRun(ctx, worker, ws, spec)
	second, _ := p.StartRun(ctx, worker, ws, spec)
	if first.ID == second.ID {
		t.Errorf("both runs share the id %q", first.ID)
	}
}

func TestPollProgressesToTerminal(t *testing.T) {
	p := New()
	p.PollsBeforeDone = 2
	ctx := context.Background()
	ws, _ := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "w"})
	worker, _ := p.EnsureWorker(ctx, provider.WorkerSpec{Name: "n", Instructions: "i", Model: "m"})
	handle, _ := p.StartRun(ctx, worker, ws, provider.RunSpec{Task: "t", Rubric: "r"})

	first, _ := p.Poll(ctx, handle)
	if first.State != provider.StateRunning {
		t.Errorf("first poll = %s, want running", first.State)
	}
	second, _ := p.Poll(ctx, handle)
	if !second.State.Terminal() || !second.Passed() {
		t.Errorf("second poll = %s (passed=%v), want a terminal pass", second.State, second.Passed())
	}
}

func TestAFailingGradeReportsAFailedState(t *testing.T) {
	p := New()
	p.Grade = provider.GradeFailed
	status, err := p.Poll(context.Background(), provider.RunHandle{ID: "run-1"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if status.State != provider.StateFailed || status.Passed() {
		t.Errorf("state = %s, passed = %v; want failed and not passed", status.State, status.Passed())
	}
}

func TestArtifactsAreListedAndFetched(t *testing.T) {
	p := New()
	p.Files = map[string][]byte{"b.md": []byte("second"), "a.md": []byte("first")}
	ctx := context.Background()
	handle := provider.RunHandle{ID: "run-1"}

	arts, err := p.ListArtifacts(ctx, handle)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 2 || arts[0].Path != "a.md" {
		t.Fatalf("artifacts should be sorted by path, got %+v", arts)
	}

	body, err := p.FetchArtifact(ctx, handle, arts[0])
	if err != nil || string(body) != "first" {
		t.Errorf("FetchArtifact = %q, %v", body, err)
	}
	if _, err := p.FetchArtifact(ctx, handle, provider.Artifact{Path: "absent"}); err == nil {
		t.Error("fetching an unknown artifact should error")
	}
}

func TestInjectedErrorsSurface(t *testing.T) {
	p := New()
	p.Err = errors.New("backend down")
	if _, err := p.ResolveModel(context.Background(), "best"); err == nil {
		t.Error("injected error did not surface")
	}
}

func TestCancellationIsHonoured(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.ResolveModel(ctx, "best"); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestIdentity(t *testing.T) {
	p := New()
	if p.Name() != "memory" {
		t.Errorf("Name = %q, want memory", p.Name())
	}
	if p.OutputDir() == "" {
		t.Error("OutputDir must not be empty")
	}
}

func TestEveryCallHonorsCancellation(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := map[string]func() error{
		"EnsureWorkspace": func() error { _, err := p.EnsureWorkspace(ctx, provider.WorkspaceSpec{}); return err },
		"EnsureWorker":    func() error { _, err := p.EnsureWorker(ctx, provider.WorkerSpec{}); return err },
		"StartRun": func() error {
			_, err := p.StartRun(ctx, provider.WorkerRef{}, provider.WorkspaceRef{}, provider.RunSpec{})
			return err
		},
		"Poll":          func() error { _, err := p.Poll(ctx, provider.RunHandle{}); return err },
		"ListArtifacts": func() error { _, err := p.ListArtifacts(ctx, provider.RunHandle{}); return err },
		"FetchArtifact": func() error { _, err := p.FetchArtifact(ctx, provider.RunHandle{}, provider.Artifact{}); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s ignored cancellation: %v", name, err)
		}
	}
}
