// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"latere.ai/x/pkg/statemachine/statemachinetest"
)

// driverWorker is the replica every test row is leased to, and the one the
// phase writer is called as, so the lease check passes and the table is what
// decides.
const driverWorker = "pod-test"

func memDriver(s *MemStore) statemachinetest.Driver[Phase] {
	var n int
	var mu sync.Mutex
	return statemachinetest.Driver[Phase]{
		Enter: func(t *testing.T, p Phase) string {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			n++
			id := newID()
			now := time.Now()
			j := Job{ID: id, Kind: KindAudit, Principal: "sub", Ref: "r", Phase: p, CreatedAt: now, UpdatedAt: now,
				PhaseAt: map[Phase]time.Time{p: now}, StopReason: stopFor(p, "")}
			s.mu.Lock()
			s.jobs[id] = j
			s.order = append(s.order, id)
			s.leases[id] = lease{by: driverWorker, at: now}
			s.mu.Unlock()
			return id
		},
		Move: func(id string, to Phase) error {
			return s.Advance(WithWorker(context.Background(), driverWorker), id, to, nil)
		},
		State: func(t *testing.T, id string) Phase {
			t.Helper()
			j, err := s.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return j.Phase
		},
	}
}

func TestMemStorePhaseWritersFollowTheTable(t *testing.T) {
	d := memDriver(NewMemStore())
	statemachinetest.Exhaustive(t, Phases, d)
	statemachinetest.Race(t, Phases, d)
}

func pgDriver(pool *pgxpool.Pool, s *PGStore) statemachinetest.Driver[Phase] {
	ctx := context.Background()
	return statemachinetest.Driver[Phase]{
		Enter: func(t *testing.T, p Phase) string {
			t.Helper()
			id := newID()
			if _, err := pool.Exec(ctx, `
				INSERT INTO jobs (id, kind, principal, ref, phase, created_at, updated_at, adopted_by, adopted_at, stop_reason)
				VALUES ($1,'audit','sub','r',$2,now(),now(),$3,now(),$4)`,
				id, string(p), driverWorker, string(stopFor(p, ""))); err != nil {
				t.Fatalf("insert a row in %s: %v", p, err)
			}
			return id
		},
		Move: func(id string, to Phase) error {
			return s.Advance(WithWorker(ctx, driverWorker), id, to, nil)
		},
		State: func(t *testing.T, id string) Phase {
			t.Helper()
			j, err := s.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			return j.Phase
		},
	}
}

func TestPGStorePhaseWritersFollowTheTable(t *testing.T) {
	pool := isolatedPG(t)
	s := NewPGStore(pool)
	d := pgDriver(pool, s)
	statemachinetest.Exhaustive(t, Phases, d)
	statemachinetest.Race(t, Phases, d)
}

// runCancelFollowsTheTable checks Cancel, the second writer of failed: from a
// phase with a step to failed it lands there as canceled, from any other it
// returns the row unchanged, and racing the phase writer it leaves one failed
// row.
func runCancelFollowsTheTable(t *testing.T, s Store, d statemachinetest.Driver[Phase]) {
	t.Helper()
	ctx := context.Background()
	for _, from := range Phases.States() {
		t.Run("cancel from "+string(from), func(t *testing.T) {
			id := d.Enter(t, from)
			got, err := s.Cancel(ctx, id)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if Phases.Can(from, PhaseFailed) {
				if got.Phase != PhaseFailed || got.StopReason != StopCanceled {
					t.Fatalf("Cancel from %s = phase %s stop %q", from, got.Phase, got.StopReason)
				}
			} else if got.Phase != from {
				t.Fatalf("Cancel from %s moved the row to %s", from, got.Phase)
			}
		})
		if !Phases.Can(from, PhaseFailed) {
			continue
		}
		t.Run("cancel races fail from "+string(from), func(t *testing.T) {
			for range 4 {
				id := d.Enter(t, from)
				var wg sync.WaitGroup
				var failErr error
				start := make(chan struct{})
				wg.Go(func() { <-start; failErr = d.Move(id, PhaseFailed) })
				wg.Go(func() { <-start; _, _ = s.Cancel(ctx, id) })
				close(start)
				wg.Wait()
				if failErr != nil && !errors.Is(failErr, ErrIllegalTransition) {
					t.Fatalf("the phase writer failed with %v", failErr)
				}
				if got := d.State(t, id); got != PhaseFailed {
					t.Fatalf("after Cancel and a failing write the row is %s", got)
				}
			}
		})
	}
}

func TestMemStoreCancelFollowsTheTable(t *testing.T) {
	s := NewMemStore()
	runCancelFollowsTheTable(t, s, memDriver(s))
}

func TestPGStoreCancelFollowsTheTable(t *testing.T) {
	pool := isolatedPG(t)
	s := NewPGStore(pool)
	d := pgDriver(pool, s)
	runCancelFollowsTheTable(t, s, d)
}
