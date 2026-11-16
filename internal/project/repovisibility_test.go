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

	"github.com/jackc/pgx/v5/pgxpool"
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

// seedRepo gives a repository a project with the given visibility and one
// session that published it, which is what the apply step reads to decide
// whether a public want still holds.
func seedRepo(t *testing.T, pool *pgxpool.Pool, repo string, visibility Visibility) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, owner, title, visibility, created_at) VALUES ($1, 'owner', 't', $2, now())`,
		"proj-"+repo, string(visibility)); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs (id, kind, principal, project_id, ref, phase, repo_name, created_at, updated_at)
		VALUES ($1, 'session', 'owner', $2, 'ref', 'done', $3, now(), now())`,
		"job-"+repo, "proj-"+repo, "org/"+repo); err != nil {
		t.Fatalf("seed session: %v", err)
	}
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
	seedRepo(t, pool, "paper-aaaa", VisibilityPublic)
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

	// The failed attempt is on the row: counted, explained and pushed out.
	var attempts int
	var lastError string
	var wantedAt, nextTry time.Time
	readRow := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT attempts, last_error, wanted_at, next_try_at FROM repo_visibility WHERE repo = 'paper-aaaa'`).
			Scan(&attempts, &lastError, &wantedAt, &nextTry); err != nil {
			t.Fatalf("read the row: %v", err)
		}
	}
	readRow()
	if attempts != 1 || lastError == "" || !nextTry.After(wantedAt) {
		t.Fatalf("after a failed attempt: attempts=%d last_error=%q next_try_at=%v wanted_at=%v; want 1, an error and a later next try",
			attempts, lastError, nextTry, wantedAt)
	}

	// Not due yet: the failure set a wait, so a sweep right away leaves it and
	// does not call the forge.
	calls := forge.calls
	if n, err := v.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep before the wait = %d, %v; want nothing applied", n, err)
	}
	if forge.calls != calls {
		t.Fatalf("a sweep before next_try_at called the forge %d times", forge.calls-calls)
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
	seedRepo(t, pool, "paper-bbbb", VisibilityPublic)
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

// TestAPublishWantWrittenAfterAWithdrawalLeavesTheRepositoryPrivate checks the
// forge follows the project when the two requests write their wants in the
// other order: the withdrawal's private want lands first, then the publish
// request's public want, with the project already private.
func TestAPublishWantWrittenAfterAWithdrawalLeavesTheRepositoryPrivate(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	forge := &scriptedForge{}
	seedRepo(t, pool, "paper-dddd", VisibilityPrivate)
	v := NewRepoVisibility(pool, forge)

	if err := v.SetPrivate(ctx, "paper-dddd"); err != nil {
		t.Fatalf("SetPrivate: %v", err)
	}
	if err := v.SetPublic(ctx, "paper-dddd"); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	if public, known := forge.state("paper-dddd"); !known || public {
		t.Errorf("the forge holds public=%v known=%v, want private", public, known)
	}
	var public bool
	if err := pool.QueryRow(ctx, `SELECT public FROM repo_visibility WHERE repo = 'paper-dddd'`).Scan(&public); err != nil || public {
		t.Errorf("the recorded want is public=%v (%v), want private", public, err)
	}
}

// TestRunResumesWorkLeftByARestart checks Run applies a pending flip on entry
// and stops when its context ends.
func TestRunResumesWorkLeftByARestart(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	forge := &scriptedForge{failing: true}
	seedRepo(t, pool, "paper-cccc", VisibilityPublic)
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
