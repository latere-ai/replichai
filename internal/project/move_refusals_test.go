// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/job"
)

// runMoveRefusalContract asserts what a paper move refuses, whichever store
// performs it: a public project at either end, a trashed one, and a paper with a
// session in flight. A refused move leaves the paper where it was. start puts a
// session in flight for the paper in the project and returns what finishes it.
func runMoveRefusalContract(t *testing.T, s Store, start func(projectID, key string) (finish func())) {
	t.Helper()
	ctx := context.Background()
	const key = "doi:10.1145/3544548.3581433"

	mk := func(title string) Project {
		p, err := s.Create(ctx, "owner", title)
		if err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
		return p
	}
	from, to := mk("From"), mk("To")
	if err := s.AddPaper(ctx, from.ID, Paper{Key: key, Ref: key, AddedBy: "owner", AddedAt: time.Now()}); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	stays := func(label string) {
		t.Helper()
		if !holdsKey(t, s, from.ID, key) || holdsKey(t, s, to.ID, key) {
			t.Errorf("%s: a refused move changed where the paper is", label)
		}
	}

	so := SignOff{By: "owner", AttestationVersion: "v1"}
	if err := s.SetPublic(ctx, to.ID, so); err != nil {
		t.Fatalf("publish destination: %v", err)
	}
	if err := s.MovePaper(ctx, from.ID, to.ID, key); !errors.Is(err, ErrPublic) {
		t.Errorf("move into a public project = %v, want ErrPublic", err)
	}
	stays("into public")
	if err := s.MovePaper(ctx, to.ID, from.ID, key); !errors.Is(err, ErrPublic) {
		t.Errorf("move out of a public project = %v, want ErrPublic", err)
	}
	if err := s.SetPrivate(ctx, to.ID, Withdrawal{By: "owner"}); err != nil {
		t.Fatalf("withdraw destination: %v", err)
	}

	if err := s.Trash(ctx, to.ID); err != nil {
		t.Fatalf("trash destination: %v", err)
	}
	if err := s.MovePaper(ctx, from.ID, to.ID, key); !errors.Is(err, ErrTrashed) {
		t.Errorf("move into a trashed project = %v, want ErrTrashed", err)
	}
	stays("into trash")
	if err := s.Restore(ctx, to.ID); err != nil {
		t.Fatalf("restore destination: %v", err)
	}

	finish := start(from.ID, key)
	if err := s.MovePaper(ctx, from.ID, to.ID, key); !errors.Is(err, ErrSessionsActive) {
		t.Errorf("move with a session in flight = %v, want ErrSessionsActive", err)
	}
	stays("session in flight")
	finish()
	if err := s.MovePaper(ctx, from.ID, to.ID, key); err != nil {
		t.Errorf("move after the session finished: %v", err)
	}
}

func holdsKey(t *testing.T, s Store, projectID, key string) bool {
	t.Helper()
	papers, err := s.ListPapers(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ListPapers: %v", err)
	}
	return slices.ContainsFunc(papers, func(p Paper) bool { return p.Key == key })
}

func TestMemStoreRefusesAMoveItMustNotMake(t *testing.T) {
	m := &recordingMover{}
	runMoveRefusalContract(t, NewMemStore(WithSessions(m)), func(_, key string) func() {
		m.running = key
		return func() { m.running = "" }
	})
}

func TestPGStoreRefusesAMoveItMustNotMake(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	m := &recordingMover{}
	s := NewPGStore(pool, WithPGSessions(m), WithActivePhases(activeStrings()))
	runMoveRefusalContract(t, s, func(projectID, key string) func() {
		id := "move-" + projectID
		seedSession(t, pool, legacySession{id: id, project: projectID, principal: "owner", ref: "r", key: key, phase: string(job.PhaseRunning), created: time.Now()})
		return func() {
			if _, err := pool.Exec(context.Background(), `UPDATE jobs SET phase=$2 WHERE id=$1`, id, string(job.PhaseDone)); err != nil {
				t.Fatalf("finish the session: %v", err)
			}
		}
	})
}

// TestMemStoresDoNotDeadlockOnASessionStartAgainstAPublish wires the two
// in-memory stores to each other the way the server does and runs a session
// start against a publish, a trash and a move of the same project many times.
// Each pair of calls must finish, and a project that ended up public or in the
// trash must not hold a session that started.
func TestMemStoresDoNotDeadlockOnASessionStartAgainstAPublish(t *testing.T) {
	for _, op := range []string{"publish", "trash", "move"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			jobs := job.NewMemStore()
			projects := NewMemStore(WithSessions(jobs),
				WithActiveSessions(func(ctx context.Context, projectID string) (bool, error) {
					sessions, err := jobs.ListByProject(ctx, projectID)
					if err != nil {
						return false, err
					}
					return slices.ContainsFunc(sessions, func(j job.Job) bool { return j.Phase.Active() }), nil
				}))
			jobs.UseProjects(func(ctx context.Context, id string) (string, bool, bool, error) {
				p, err := projects.Get(ctx, id)
				if errors.Is(err, ErrNotFound) {
					return "", false, false, nil
				}
				if err != nil {
					return "", false, false, err
				}
				return string(p.Visibility), p.Trashed(), true, nil
			})

			done := make(chan error, 1)
			go func() { done <- raceStartAgainst(ctx, op, jobs, projects) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("a session start and a " + op + " at the same moment never finished")
			}
		})
	}
}

func raceStartAgainst(ctx context.Context, op string, jobs *job.MemStore, projects *MemStore) error {
	for i := range 300 {
		p, err := projects.Create(ctx, "owner", "Race")
		if err != nil {
			return err
		}
		other, err := projects.Create(ctx, "owner", "Other")
		if err != nil {
			return err
		}
		key := fmt.Sprintf("doi:10.1/%d", i)
		if err := projects.AddPaper(ctx, p.ID, Paper{Key: key, Ref: key, AddedBy: "owner"}); err != nil {
			return err
		}
		var (
			wg                     sync.WaitGroup
			startErr, opErr, mvErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, startErr = jobs.Enqueue(ctx, job.Submission{Kind: job.KindAudit, Principal: "owner", Project: p.ID, Ref: key, PaperKey: key})
		}()
		go func() {
			defer wg.Done()
			switch op {
			case "publish":
				opErr = projects.SetPublic(ctx, p.ID, SignOff{By: "owner", AttestationVersion: "v1"})
			case "trash":
				opErr = projects.Trash(ctx, p.ID)
			case "move":
				mvErr = projects.MovePaper(ctx, p.ID, other.ID, key)
			}
		}()
		wg.Wait()
		switch {
		case op == "move":
			// A session that started before the move is a refusal; one that
			// started after it stays in the project it was started in. Either
			// way no running session is carried to the destination.
			if mvErr == nil {
				sessions, err := jobs.ListByProject(ctx, other.ID)
				if err != nil {
					return err
				}
				if len(sessions) != 0 {
					return fmt.Errorf("round %d: a running session moved with its paper", i)
				}
			}
			if mvErr != nil && !errors.Is(mvErr, ErrSessionsActive) {
				return fmt.Errorf("round %d: move: %w", i, mvErr)
			}
		case startErr == nil && opErr == nil:
			return fmt.Errorf("round %d: a session started and the project was %sed while it ran", i, op)
		}
	}
	return nil
}
