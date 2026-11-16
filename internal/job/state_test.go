// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/provider"
)

// isolatedPG opens a pool whose search path is a schema of its own, migrated
// and given the minimal projects table the job store reads, so the test does
// not depend on which other packages migrated the database.
func isolatedPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres state tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("jobstate_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close()
		t.Fatalf("parse url: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		admin.Close()
		t.Fatalf("connect to schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
		admin.Close()
	})
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE projects (
		id TEXT PRIMARY KEY, visibility TEXT NOT NULL DEFAULT 'private', deleted_at TIMESTAMPTZ)`); err != nil {
		t.Fatalf("create projects: %v", err)
	}
	return pool
}

func TestTerminalPhasesMapToNil(t *testing.T) {
	for _, p := range []Phase{PhaseDone, PhaseFailed} {
		if !Phases.Terminal(p) || len(Phases.Next(p)) != 0 {
			t.Errorf("%s has successors %v", p, Phases.Next(p))
		}
		if !p.Terminal() || p.Active() {
			t.Errorf("%s: Terminal=%v Active=%v", p, p.Terminal(), p.Active())
		}
	}
	if Phase("bogus").Terminal() {
		t.Error("a phase the table does not list is terminal")
	}
	if got := sourcesOf(PhaseFailed); !slices.Equal(got, ActivePhases()) {
		t.Errorf("sources of failed = %v, want the active phases %v", got, ActivePhases())
	}
	if got := sourcesOf(PhaseResolving); !slices.Equal(got, []Phase{PhaseSubmitted}) {
		t.Errorf("sources of resolving = %v", got)
	}
}

// TestThePhaseConstraintMatchesTheTable reads the CHECK constraint back from
// the database, so a phase added to the table without a migration fails here.
func TestThePhaseConstraintMatchesTheTable(t *testing.T) {
	pool := isolatedPG(t)
	var def string
	if err := pool.QueryRow(context.Background(), `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'jobs'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%phase%'
		  AND convalidated`).Scan(&def); err != nil {
		t.Fatalf("read the phase constraint: %v", err)
	}
	var got []Phase
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(def, -1) {
		got = append(got, Phase(m[1]))
	}
	slices.Sort(got)
	if want := States(); !slices.Equal(got, want) {
		t.Fatalf("the jobs.phase constraint lists %v but the transition table has %v (%s)", got, want, def)
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO jobs (id, kind, principal, ref, phase, created_at, updated_at) VALUES ('x','audit','p','r','paused',now(),now())`)
	if err == nil {
		t.Fatal("the database accepted a phase outside the table")
	}
}

func TestMigrationBackfillsTheStopReason(t *testing.T) {
	pool := isolatedPG(t)
	ctx := context.Background()
	// Take the column and the constraint back out, insert rows as the earlier
	// code wrote them, and run the backfill statement again.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET stop_reason = ''`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for id, row := range map[string][2]string{
		"c": {"failed", "cancelled"},
		"i": {"failed", "interrupted by a restart in running; no live run to rejoin"},
		"s": {"failed", "stopped: interrupted by a restart in running; the agent session was closed"},
		"e": {"failed", "boom"},
		"d": {"done", ""},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO jobs (id, kind, principal, ref, phase, error, created_at, updated_at)
			VALUES ($1,'audit','p','r',$2,$3,now(),now())`, id, row[0], row[1]); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	var backfill string
	for _, m := range migrations {
		if m.version == 12 {
			backfill = m.sql
		}
	}
	m := regexp.MustCompile(`(?s)UPDATE jobs SET stop_reason.*?WHERE phase = 'failed';`).FindString(backfill)
	if m == "" {
		t.Fatal("migration 12 has no stop reason backfill")
	}
	if _, err := pool.Exec(ctx, m); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	want := map[string]string{"c": "canceled", "i": "interrupted", "s": "interrupted", "e": "error", "d": ""}
	for id, w := range want {
		var got string
		if err := pool.QueryRow(ctx, `SELECT stop_reason FROM jobs WHERE id = $1`, id).Scan(&got); err != nil || got != w {
			t.Errorf("row %s stop_reason = %q, %v; want %q", id, got, err, w)
		}
	}
}

// runStopReasonContract checks the reason a failed session carries for each way
// it can get there, and that no other phase carries one.
func runStopReasonContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	sub := func(key string) Job {
		j, err := s.Enqueue(ctx, Submission{PaperKey: key, Principal: "sub", Ref: key, Kind: KindAudit})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		return j
	}

	canceled := sub("k1")
	got, err := s.Cancel(ctx, canceled.ID)
	if err != nil || got.StopReason != StopCanceled || got.Err != CancelCause {
		t.Fatalf("Cancel = %+v, %v; want stop reason canceled", got, err)
	}

	failed := sub("k2")
	if _, ok, _ := s.Claim(ctx, "w"); !ok {
		t.Fatal("claim")
	}
	if err := s.Advance(ctx, failed.ID, PhaseFailed, func(j *Job) { j.Err = "boom" }); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if got, _ := s.Get(ctx, failed.ID); got.StopReason != StopError {
		t.Errorf("a failure with no reason set carries %q, want error", got.StopReason)
	}

	stranded := sub("k3")
	if _, ok, _ := s.Claim(ctx, "w"); !ok {
		t.Fatal("claim")
	}
	if err := s.Advance(ctx, stranded.ID, PhaseFailed, func(j *Job) { j.Err, j.StopReason = "gone", StopInterrupted }); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if got, _ := s.Get(ctx, stranded.ID); got.StopReason != StopInterrupted {
		t.Errorf("an interrupted session carries %q", got.StopReason)
	}

	done := sub("k4")
	if _, ok, _ := s.Claim(ctx, "w"); !ok {
		t.Fatal("claim")
	}
	for _, to := range []Phase{PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, done.ID, to, func(j *Job) { j.StopReason = StopError }); err != nil {
			t.Fatalf("advance to %s: %v", to, err)
		}
	}
	if got, _ := s.Get(ctx, done.ID); got.StopReason != "" {
		t.Errorf("a done session carries stop reason %q", got.StopReason)
	}
}

// runRepoContract checks that a repository recorded in the resolving phase is
// on the row, and that a finished session refuses the write.
func runRepoContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, Submission{PaperKey: "repo-key", Principal: "sub", Ref: "r", Kind: KindAudit})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, ok, err := s.Claim(ctx, "pod-a"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.SetRepo(WithWorker(ctx, "pod-b"), j.ID, "o/n"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("SetRepo by a replica without the lease: %v", err)
	}
	if err := s.SetRepo(WithWorker(ctx, "pod-a"), j.ID, "o/n"); err != nil {
		t.Fatalf("SetRepo: %v", err)
	}
	if got, _ := s.Get(ctx, j.ID); got.RepoName != "o/n" || got.Phase != PhaseResolving {
		t.Fatalf("after SetRepo: repo %q phase %s", got.RepoName, got.Phase)
	}
	if _, err := s.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := s.SetRepo(ctx, j.ID, "o/m"); !errors.Is(err, ErrSessionFinished) {
		t.Fatalf("SetRepo on a finished session: %v", err)
	}
}

func TestMemStoreStopReasonAndRepo(t *testing.T) {
	runStopReasonContract(t, NewMemStore())
	runRepoContract(t, NewMemStore())
}

func TestPGStoreStopReasonAndRepo(t *testing.T) {
	pool := isolatedPG(t)
	runStopReasonContract(t, NewPGStore(pool))
	runRepoContract(t, NewPGStore(pool))
}

// runProjectGateContract checks that a session is refused for a project that is
// published or in the trash, for a submitted session and an imported record.
func runProjectGateContract(t *testing.T, s Store, setState func(project, visibility string, trashed bool)) {
	t.Helper()
	ctx := context.Background()
	enqueue := func(project, key string) error {
		_, err := s.Enqueue(ctx, Submission{PaperKey: key, Principal: "sub", Ref: key, Kind: KindAudit, Project: project})
		return err
	}
	importIt := func(project, id string) error {
		_, err := s.ImportLocal(ctx, Import{Principal: "sub", Project: project, Ref: "r", PaperKey: "pk-" + id,
			Runtime: "local", ExternalID: id, Phase: PhaseDone, Status: provider.RunStatus{State: provider.StateDone}})
		return err
	}
	setState("open", "private", false)
	if err := enqueue("open", "a"); err != nil {
		t.Fatalf("a private project refused a session: %v", err)
	}
	if err := importIt("open", "i1"); err != nil {
		t.Fatalf("a private project refused a record: %v", err)
	}
	setState("pub", "public", false)
	if err := enqueue("pub", "b"); !errors.Is(err, ErrProjectPublic) {
		t.Errorf("enqueue into a public project: %v", err)
	}
	if err := importIt("pub", "i2"); !errors.Is(err, ErrProjectPublic) {
		t.Errorf("import into a public project: %v", err)
	}
	setState("bin", "private", true)
	if err := enqueue("bin", "c"); !errors.Is(err, ErrProjectTrashed) {
		t.Errorf("enqueue into a trashed project: %v", err)
	}
	if err := importIt("bin", "i3"); !errors.Is(err, ErrProjectTrashed) {
		t.Errorf("import into a trashed project: %v", err)
	}
}

func TestMemStoreRefusesAPublicOrTrashedProject(t *testing.T) {
	type state struct {
		visibility string
		trashed    bool
	}
	states := map[string]state{}
	s := NewMemStore(WithProjects(func(_ context.Context, id string) (string, bool, bool, error) {
		st, ok := states[id]
		return st.visibility, st.trashed, ok, nil
	}))
	runProjectGateContract(t, s, func(p, v string, trashed bool) { states[p] = state{v, trashed} })
}

func TestPGStoreRefusesAPublicOrTrashedProject(t *testing.T) {
	pool := isolatedPG(t)
	ctx := context.Background()
	runProjectGateContract(t, NewPGStore(pool), func(p, v string, trashed bool) {
		del := "NULL"
		if trashed {
			del = "now()"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO projects (id, visibility, deleted_at) VALUES ($1,$2,`+del+`)
			ON CONFLICT (id) DO UPDATE SET visibility = EXCLUDED.visibility, deleted_at = EXCLUDED.deleted_at`, p, v); err != nil {
			t.Fatalf("set project: %v", err)
		}
	})
}

// TestACancelOnAnotherReplicaStopsTheRun checks that the replica driving a
// session stops its run when the row went terminal by a cancel made elsewhere,
// which the heartbeat sees as a lease it no longer holds.
func TestACancelOnAnotherReplicaStopsTheRun(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	j, _ := s.Enqueue(ctx, Submission{PaperKey: anyKey(), Principal: "sub", Ref: "ref", Kind: KindAudit})
	held, stopped := make(chan struct{}), make(chan struct{})
	r := &fakeRunner{}
	r.runExecute = func(ctx context.Context) error {
		close(held)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	driver := NewWorker(s, r, WithWorkerID("pod-a"), WithPoll(time.Millisecond))
	go func() { _, _ = driver.Process(ctx) }()
	<-held
	if _, err := s.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	driver.beat(ctx)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the run kept going after the session was cancelled on another replica")
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseFailed || got.StopReason != StopCanceled {
		t.Fatalf("after the cancel: phase %s stop %q", got.Phase, got.StopReason)
	}
}

// TestAStrandedSessionIsInterrupted checks the reason the sweep records for a
// session whose driver is gone.
func TestAStrandedSessionIsInterrupted(t *testing.T) {
	now := time.Now()
	s := NewMemStore(WithClock(func() time.Time { return now }))
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{PaperKey: anyKey(), Principal: "sub", Ref: "ref", Kind: KindAudit})
	if _, ok, _ := s.Claim(ctx, "dead-pod"); !ok {
		t.Fatal("claim")
	}
	now = now.Add(2 * LeaseTTL)
	w := NewWorker(s, &fakeRunner{}, WithWorkerID("pod-b"))
	if _, failed, err := w.Reconcile(ctx); err != nil || failed != 1 {
		t.Fatalf("Reconcile = failed %d, %v", failed, err)
	}
	if got, _ := s.Get(ctx, j.ID); got.StopReason != StopInterrupted {
		t.Fatalf("stop reason %q, want interrupted", got.StopReason)
	}
}

// TestStartReturnsOnlyAfterARejoinedRunStops checks that Start does not return
// while a run it rejoined is still driving, so a caller that closes the store
// after Start returns cannot cut a write off.
func TestStartReturnsOnlyAfterARejoinedRunStops(t *testing.T) {
	s := NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, _ := s.Enqueue(ctx, Submission{PaperKey: anyKey(), Principal: "sub", Ref: "ref", Kind: KindAudit})
	if _, ok, _ := s.Claim(ctx, "pod-a"); !ok {
		t.Fatal("claim")
	}
	if err := s.Advance(WithWorker(ctx, "pod-a"), j.ID, PhaseRunning, nil); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := s.Observe(WithWorker(ctx, "pod-a"), j.ID, provider.RunStatus{RunID: "run-1", Runtime: "anthropic-managed"}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	resuming, release := make(chan struct{}), make(chan struct{})
	r := &fakeRunner{onResume: func() {
		close(resuming)
		<-release
	}}
	w := NewWorker(s, r, WithWorkerID("pod-a"), WithPoll(time.Millisecond))
	if resumed, _, err := w.Reconcile(ctx); err != nil || resumed != 1 {
		t.Fatalf("Reconcile = %d, %v", resumed, err)
	}
	<-resuming
	returned := make(chan struct{})
	go func() {
		w.Start(ctx, 1)
		close(returned)
	}()
	cancel()
	select {
	case <-returned:
		t.Fatal("Start returned while a rejoined run was still driving")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after the rejoined run stopped")
	}
}
