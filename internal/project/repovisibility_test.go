// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// scriptedForge records every visibility call and fails the ones a test marks.
type scriptedForge struct {
	mu      sync.Mutex
	failing bool
	public  map[string]bool
	calls   int
}

func (f *scriptedForge) set(repo string, public bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failing {
		return errors.New("forge unavailable")
	}
	if f.public == nil {
		f.public = map[string]bool{}
	}
	f.public[repo] = public
	return nil
}

func (f *scriptedForge) SetPublic(_ context.Context, repo string) error  { return f.set(repo, true) }
func (f *scriptedForge) SetPrivate(_ context.Context, repo string) error { return f.set(repo, false) }

func (f *scriptedForge) state(repo string) (public, known bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	public, known = f.public[repo]
	return
}

// TestAFailedFlipIsRecordedAndRetriedUntilTheForgeMatches checks the wanted
// visibility survives a forge failure and a sweep applies it once the forge
// answers.
func TestAFailedFlipIsRecordedAndRetriedUntilTheForgeMatches(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	forge := &scriptedForge{failing: true}
	now := time.Now()
	v := NewRepoVisibility(pool, forge)
	v.clock = func() time.Time { return now }

	if err := v.SetPublic(ctx, "paper-aaaa"); err != nil {
		t.Fatalf("SetPublic recorded nothing: %v", err)
	}
	pending, err := v.Pending(ctx)
	if err != nil || !slices.Contains(pending, "paper-aaaa") {
		t.Fatalf("pending = %v, %v; want the repository whose flip failed", pending, err)
	}
	if _, known := forge.state("paper-aaaa"); known {
		t.Fatal("the failing forge applied a visibility")
	}

	// Not due yet: the failure set a wait, so a sweep right away leaves it.
	if n, err := v.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep before the wait = %d, %v; want nothing applied", n, err)
	}

	// The forge recovers and the wait has passed.
	forge.failing = false
	now = now.Add(repoRetryMax + time.Second)
	n, err := v.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep after the wait = %d, %v; want one applied", n, err)
	}
	if public, known := forge.state("paper-aaaa"); !known || !public {
		t.Errorf("the forge holds public=%v known=%v, want public", public, known)
	}
	if pending, _ := v.Pending(ctx); slices.Contains(pending, "paper-aaaa") {
		t.Errorf("an applied repository is still pending: %v", pending)
	}
}

// TestTheLastWantedVisibilityWins checks a withdrawal recorded after a
// publication whose flip failed ends private, so the retry never reopens a
// repository an owner closed.
func TestTheLastWantedVisibilityWins(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	forge := &scriptedForge{failing: true}
	now := time.Now()
	v := NewRepoVisibility(pool, forge)
	v.clock = func() time.Time { return now }

	if err := v.SetPublic(ctx, "paper-bbbb"); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	if err := v.SetPrivate(ctx, "paper-bbbb"); err != nil {
		t.Fatalf("SetPrivate: %v", err)
	}
	forge.failing = false
	now = now.Add(repoRetryMax + time.Second)
	if _, err := v.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if public, known := forge.state("paper-bbbb"); !known || public {
		t.Errorf("the forge holds public=%v known=%v, want private", public, known)
	}
}

// TestRunResumesWorkLeftByARestart checks Run applies a pending flip on entry
// and stops when its context ends.
func TestRunResumesWorkLeftByARestart(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	forge := &scriptedForge{failing: true}
	v := NewRepoVisibility(pool, forge)
	if err := v.SetPublic(context.Background(), "paper-cccc"); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	forge.mu.Lock()
	forge.failing = false
	forge.mu.Unlock()
	// A fresh instance stands for the restarted server; the failure's wait is
	// in the database, so the sweep runs on a clock past it.
	restarted := NewRepoVisibility(pool, forge)
	restarted.clock = func() time.Time { return time.Now().Add(repoRetryMax + time.Minute) }
	restarted.every = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { restarted.Run(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for {
		if public, known := forge.state("paper-cccc"); known && public {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("Run did not apply the pending flip")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
