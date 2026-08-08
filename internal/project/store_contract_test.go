package project

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/job"
)

// runStoreContract asserts the invariants every project Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// A blank owner is refused.
	if _, err := s.Create(ctx, "  ", "t"); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("blank owner: want ErrNoOwner, got %v", err)
	}

	// Create and read back — private by default.
	p, err := s.Create(ctx, "owner", "Attention Is All You Need")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Visibility != VisibilityPrivate || p.Owner != "owner" || p.CreatedAt.IsZero() {
		t.Fatalf("created project = %+v", p)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil || got.Title != "Attention Is All You Need" {
		t.Fatalf("Get = %+v, err %v", got, err)
	}

	// A missing project is ErrNotFound.
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing Get: want ErrNotFound, got %v", err)
	}

	// ListByOwner is scoped and newest-first.
	second, _ := s.Create(ctx, "owner", "Second")
	_, _ = s.Create(ctx, "stranger", "Theirs")
	mine, err := s.ListByOwner(ctx, "owner")
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(mine) != 2 || mine[0].ID != second.ID || mine[1].ID != p.ID {
		t.Fatalf("ListByOwner = %+v; want [second, first], no stranger", mine)
	}

	// SetTitle enriches the placeholder; a missing project is not an error.
	if err := s.SetTitle(ctx, p.ID, "The Real Paper Title"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if got, _ := s.Get(ctx, p.ID); got.Title != "The Real Paper Title" {
		t.Errorf("after SetTitle: title = %q", got.Title)
	}
	if err := s.SetTitle(ctx, "nope", "x"); err != nil {
		t.Errorf("SetTitle on a missing project: %v", err)
	}

	// SetPublic marks a project public and records the sign-off — including the
	// signer snapshot (006n), which every implementation must round-trip whole,
	// since it is what a public page reports. ListPublic returns it (and never a
	// private one).
	so := SignOff{
		By: "owner", AttestationVersion: "v2",
		Name: "Ada Lovelace", Institution: "LMU Munich", Contact: "ada@example.org",
		ORCID: "0000-0002-1825-0097", Scholar: "https://scholar.google.com/citations?user=abc",
		Email: "a**a@example.org",
	}
	if err := s.SetPublic(ctx, second.ID, so); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	pub, err := s.Get(ctx, second.ID)
	if err != nil || pub.Visibility != VisibilityPublic || pub.SignedOffBy != "owner" || pub.AttestationVersion != "v2" || pub.SignedOffAt == nil {
		t.Fatalf("after SetPublic: %+v err=%v", pub, err)
	}
	if pub.SignedOffName != so.Name || pub.SignedOffInstitution != so.Institution ||
		pub.SignedOffContact != so.Contact || pub.SignedOffORCID != so.ORCID ||
		pub.SignedOffScholar != so.Scholar || pub.SignedOffEmail != so.Email {
		t.Fatalf("signer snapshot not round-tripped: %+v", pub)
	}
	list, err := s.ListPublic(ctx)
	if err != nil || len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("ListPublic = %+v err=%v; want only the public project", list, err)
	}
	if list[0].SignedOffName != so.Name || list[0].SignedOffContact != so.Contact {
		t.Errorf("ListPublic drops the signer snapshot: %+v", list[0])
	}
	if err := s.SetPublic(ctx, "nope", SignOff{By: "owner", AttestationVersion: "v2"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetPublic on a missing project: want ErrNotFound, got %v", err)
	}

	// SetPrivate withdraws the publication (006o). It is a retraction, not an
	// erasure: visibility returns to private and the project leaves the public
	// zone, but every signed_off_* field must survive intact — a store that
	// cleared them would let a project be published, withdrawn, and republished
	// under a different signer with no evidence of any of it.
	if err := s.SetPrivate(ctx, second.ID, Withdrawal{By: "owner", Reason: "unreleased figure"}); err != nil {
		t.Fatalf("SetPrivate: %v", err)
	}
	gone, err := s.Get(ctx, second.ID)
	if err != nil || gone.Visibility != VisibilityPrivate {
		t.Fatalf("after SetPrivate: %+v err=%v", gone, err)
	}
	if gone.WithdrawnAt == nil || gone.WithdrawnBy != "owner" || gone.WithdrawnReason != "unreleased figure" {
		t.Errorf("withdrawal not recorded: %+v", gone)
	}
	if gone.SignedOffBy != so.By || gone.AttestationVersion != so.AttestationVersion ||
		gone.SignedOffAt == nil || gone.SignedOffName != so.Name ||
		gone.SignedOffInstitution != so.Institution || gone.SignedOffContact != so.Contact ||
		gone.SignedOffORCID != so.ORCID || gone.SignedOffScholar != so.Scholar ||
		gone.SignedOffEmail != so.Email {
		t.Errorf("withdrawal erased the sign-off it retracts: %+v", gone)
	}
	if list, err := s.ListPublic(ctx); err != nil || len(list) != 0 {
		t.Errorf("ListPublic after withdrawal = %+v err=%v; want empty", list, err)
	}

	// Withdrawing again succeeds and changes nothing — the timestamp keeps
	// pointing at the moment the publication actually ended, not at the last time
	// somebody asked.
	first := *gone.WithdrawnAt
	if err := s.SetPrivate(ctx, second.ID, Withdrawal{By: "someone-else", Reason: "again"}); err != nil {
		t.Fatalf("repeated SetPrivate: %v", err)
	}
	again, _ := s.Get(ctx, second.ID)
	if !again.WithdrawnAt.Equal(first) || again.WithdrawnBy != "owner" || again.WithdrawnReason != "unreleased figure" {
		t.Errorf("repeated SetPrivate overwrote the first withdrawal: %+v", again)
	}
	if err := s.SetPrivate(ctx, "nope", Withdrawal{By: "owner"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetPrivate on a missing project: want ErrNotFound, got %v", err)
	}

	// Republishing supersedes the retraction: a fresh sign-off, and no withdrawal
	// left on the row, so it never claims to be both live and withdrawn.
	if err := s.SetPublic(ctx, second.ID, SignOff{By: "owner", AttestationVersion: "v2", Name: "Ada Again", Contact: "ada@example.org"}); err != nil {
		t.Fatalf("republish: %v", err)
	}
	back, _ := s.Get(ctx, second.ID)
	if back.Visibility != VisibilityPublic || back.WithdrawnAt != nil || back.WithdrawnBy != "" || back.WithdrawnReason != "" {
		t.Errorf("republish did not clear the withdrawal: %+v", back)
	}
	if back.SignedOffName != "Ada Again" {
		t.Errorf("republish did not overwrite the snapshot: %+v", back)
	}
	// Leave it private again so the rest of the contract sees a clean slate.
	if err := s.SetPrivate(ctx, second.ID, Withdrawal{By: "owner", Reason: "tidy"}); err != nil {
		t.Fatalf("SetPrivate (tidy): %v", err)
	}

	// Delete removes it, idempotently.
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}

	runPaperContract(t, s)
	runModifiedContract(t, s)
	runTrashContract(t, s)
}

// runTrashContract asserts what the trash must do (019): a trashed project
// leaves every list, stays readable by id, keeps the moment it left, and comes
// back whole.
func runTrashContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	p, err := s.Create(ctx, "tidy-owner", "Thrown away")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetPublic(ctx, p.ID, SignOff{By: "tidy-owner", AttestationVersion: "v2", Contact: "a@b.c"}); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	if err := s.Trash(ctx, p.ID); err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Gone from both lists a reader reaches it through.
	if mine, err := s.ListByOwner(ctx, "tidy-owner"); err != nil || containsID(mine, p.ID) {
		t.Errorf("a trashed project is still in its owner's list (%v)", err)
	}
	if pub, err := s.ListPublic(ctx); err != nil || containsID(pub, p.ID) {
		t.Errorf("a trashed project is still in the public zone (%v)", err)
	}
	// Still readable by id: restore, purge, and the owner's read-only view all
	// need it to answer.
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get of a trashed project: %v", err)
	}
	if !got.Trashed() {
		t.Error("Get of a trashed project does not report it trashed")
	}
	trashed, err := s.ListTrashed(ctx, "tidy-owner")
	if err != nil || !containsID(trashed, p.ID) {
		t.Errorf("ListTrashed = %v, %v; want the project", trashed, err)
	}
	// Somebody else's trash is not yours.
	if other, err := s.ListTrashed(ctx, "stranger"); err != nil || len(other) != 0 {
		t.Errorf("ListTrashed for a stranger = %v, %v; want nothing", other, err)
	}

	// Trashing again keeps the first moment: a repeated call must not rewrite
	// when the project actually left the list.
	first := *got.DeletedAt
	if err := s.Trash(ctx, p.ID); err != nil {
		t.Fatalf("repeated Trash: %v", err)
	}
	again, _ := s.Get(ctx, p.ID)
	if !again.DeletedAt.Equal(first) {
		t.Errorf("a repeated Trash moved the timestamp from %v to %v", first, *again.DeletedAt)
	}

	// Restore brings it back, with everything it carried.
	if err := s.Restore(ctx, p.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	back, _ := s.Get(ctx, p.ID)
	if back.Trashed() {
		t.Error("Restore left the project trashed")
	}
	if back.Visibility != VisibilityPublic || back.SignedOffBy != "tidy-owner" {
		t.Errorf("Restore did not return the project whole: %+v", back)
	}
	if mine, _ := s.ListByOwner(ctx, "tidy-owner"); !containsID(mine, p.ID) {
		t.Error("a restored project is not back in its owner's list")
	}
	if err := s.Restore(ctx, p.ID); err != nil {
		t.Errorf("idempotent Restore: %v", err)
	}

	// Neither acts on a project that is not there.
	if err := s.Trash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Trash of a missing project = %v, want ErrNotFound", err)
	}
	if err := s.Restore(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Restore of a missing project = %v, want ErrNotFound", err)
	}
}

func containsID(ps []Project, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

// runModifiedContract asserts what a project's modified time must do (018): it
// starts at creation and moves when a paper is added, so a list can sort on the
// work rather than on when a container was opened.
func runModifiedContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	p, err := s.Create(ctx, "owner", "Modified")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	at, err := s.ModifiedAt(ctx, []string{p.ID})
	if err != nil {
		t.Fatalf("ModifiedAt: %v", err)
	}
	// A project with no papers answers its creation time rather than dropping out
	// of the result — the row still has to sort somewhere.
	if got, want := at[p.ID].UTC(), p.CreatedAt.UTC(); !got.Equal(want) {
		t.Errorf("ModifiedAt with no papers = %v, want the creation time %v", got, want)
	}

	added := p.CreatedAt.Add(time.Hour)
	if err := s.AddPaper(ctx, p.ID, Paper{Key: "arxiv:2401.1", Ref: "r", AddedBy: "owner", AddedAt: added}); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	at, err = s.ModifiedAt(ctx, []string{p.ID})
	if err != nil {
		t.Fatalf("ModifiedAt after AddPaper: %v", err)
	}
	if got := at[p.ID].UTC(); !got.Equal(added.UTC()) {
		t.Errorf("ModifiedAt after AddPaper = %v, want the paper's time %v", got, added.UTC())
	}

	// A project nobody asked about is not answered for, and asking for none is
	// not an error — an empty list is what a signed-in newcomer's page sends.
	if at, err := s.ModifiedAt(ctx, nil); err != nil || len(at) != 0 {
		t.Errorf("ModifiedAt(nil) = %v, %v; want an empty map and no error", at, err)
	}
	if at, err := s.ModifiedAt(ctx, []string{"nope"}); err != nil || len(at) != 0 {
		t.Errorf("ModifiedAt of a missing project = %v, %v; want an empty map and no error", at, err)
	}
}

// runPaperContract asserts what a project's papers must do (012). A paper is
// what the project is *about*, so it exists before any session and outlives
// every one — which is the difference from the session list this store
// deliberately does not hold.
func runPaperContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	p, err := s.Create(ctx, "owner", "Irreversible interactions")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A fresh project holds no papers. That is an answer, not a failure.
	papers, err := s.ListPapers(ctx, p.ID)
	if err != nil || len(papers) != 0 {
		t.Fatalf("ListPapers on a new project = %v, err %v", papers, err)
	}

	// A paper the project is about, before anything has been run against it.
	first := Paper{Key: "doi:10.1145/3544548.3581433", Ref: "https://doi.org/10.1145/3544548.3581433", AddedBy: "owner"}
	if err := s.AddPaper(ctx, p.ID, first); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	second := Paper{Key: "arxiv:2401.00001", Ref: "arXiv:2401.00001", AddedBy: "collaborator"}
	if err := s.AddPaper(ctx, p.ID, second); err != nil {
		t.Fatalf("AddPaper second: %v", err)
	}

	// A project is about a paper once, however many times it is run.
	if err := s.AddPaper(ctx, p.ID, first); !errors.Is(err, ErrPaperExists) {
		t.Errorf("duplicate paper: want ErrPaperExists, got %v", err)
	}

	// Added order, not reverse: the paper a project was created for reads first.
	papers, err = s.ListPapers(ctx, p.ID)
	if err != nil || len(papers) != 2 {
		t.Fatalf("ListPapers = %v, err %v", papers, err)
	}
	if papers[0].Key != first.Key || papers[1].Key != second.Key {
		t.Errorf("paper order = %s, %s", papers[0].Key, papers[1].Key)
	}
	if papers[0].Ref != first.Ref || papers[0].AddedBy != "owner" || papers[0].AddedAt.IsZero() {
		t.Errorf("first paper = %+v", papers[0])
	}
	if papers[0].Title != "" {
		t.Errorf("a paper nobody has resolved should carry no title, got %q", papers[0].Title)
	}

	// The title arrives later, from whichever resolve answers first.
	if err := s.SetPaperTitle(ctx, p.ID, first.Key, "Point of no Undo"); err != nil {
		t.Fatalf("SetPaperTitle: %v", err)
	}
	papers, _ = s.ListPapers(ctx, p.ID)
	if papers[0].Title != "Point of no Undo" {
		t.Errorf("title after SetPaperTitle = %q", papers[0].Title)
	}
	// Best-effort, exactly as SetTitle is: naming a paper nobody holds is not an
	// error, because the enrichment races a paper that may have been removed.
	if err := s.SetPaperTitle(ctx, p.ID, "url:nobody", "x"); err != nil {
		t.Errorf("SetPaperTitle on a missing paper: %v", err)
	}

	// Removal is the store's to do and idempotent; who may ask is the caller's.
	if err := s.RemovePaper(ctx, p.ID, second.Key); err != nil {
		t.Fatalf("RemovePaper: %v", err)
	}
	if err := s.RemovePaper(ctx, p.ID, second.Key); err != nil {
		t.Errorf("idempotent RemovePaper: %v", err)
	}
	papers, _ = s.ListPapers(ctx, p.ID)
	if len(papers) != 1 || papers[0].Key != first.Key {
		t.Errorf("after RemovePaper: %+v", papers)
	}

	// A project that is not there is ErrNotFound on both sides, so "no papers"
	// and "no project" never read as the same answer.
	if err := s.AddPaper(ctx, "nope", first); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddPaper to a missing project: want ErrNotFound, got %v", err)
	}
	if _, err := s.ListPapers(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ListPapers of a missing project: want ErrNotFound, got %v", err)
	}

	// Deleting a project takes its papers with it, so a later project reusing
	// the id inherits nothing.
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.ListPapers(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("papers of a deleted project: want ErrNotFound, got %v", err)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	runStoreContract(t, NewMemStore())
}

// TestPGStoreSatisfiesTheContract runs the same contract against a real
// Postgres, skipping when TEST_DATABASE_URL is not set.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	runStoreContract(t, NewPGStore(pool))
}

// TestMigrationBackfillsProjectsFromSessions is 006g AC6: the backfill turns the
// distinct (principal, project_id) pairs already in jobs into projects, reusing
// the slug as the id so sessions' FK already points at them — no re-run, no data
// loss. It replays the real migration order — jobs exist under the job schema
// BEFORE the project migration introduces the table, the backfill, and the
// session FK — since the FK (correctly) forbids a session pointing at a project
// that does not yet exist. Postgres-only.
func TestMigrationBackfillsProjectsFromSessions(t *testing.T) {
	pool := freshDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Only the job schema exists — no projects table, no FK — so three finished
	// CLI sessions seed against three project slugs, as they did before 006g.
	if err := job.Migrate(ctx, pool); err != nil {
		t.Fatalf("job migrate: %v", err)
	}
	js := job.NewPGStore(pool)
	for _, slug := range []string{"paper-a", "paper-b", "paper-c"} {
		j, err := js.Enqueue(ctx, job.Submission{Principal: "cli-owner", Ref: "ref-" + slug, Kind: job.KindAudit, Project: slug})
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
		for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
			if err := js.Advance(ctx, j.ID, p, nil); err != nil {
				t.Fatalf("advance %s: %v", slug, err)
			}
		}
	}

	// Now introduce the project migration — it backfills projects from those
	// jobs, then adds the FK; the sessions' project_id already points at the
	// slug-named projects, so the FK holds.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("project migrate: %v", err)
	}

	// Three one-session projects now exist, owned by the CLI principal.
	ps, err := NewPGStore(pool).ListByOwner(ctx, "cli-owner")
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("backfilled %d projects, want 3", len(ps))
	}
	for _, p := range ps {
		sessions, err := js.ListByProject(ctx, p.ID)
		if err != nil {
			t.Fatalf("ListByProject %s: %v", p.ID, err)
		}
		if len(sessions) != 1 {
			t.Errorf("project %s has %d sessions, want 1", p.ID, len(sessions))
		}
	}
}

// projectTestDB is the dedicated database the project PG tests run in. The
// project migration adds a foreign key to the shared jobs table, which
// internal/job's own contract test (it inserts sessions with dangling
// project_ids) cannot tolerate — and `go test ./...` runs both packages in
// parallel against one server. A private database keeps the two hermetic.
const projectTestDB = "replichai_project_test"

// freshDB creates a clean, empty project test database and returns a pool to it,
// with no migrations applied — so a caller can control the migration order.
func freshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres project tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Terminate stragglers, then drop and recreate — a clean database per run.
	// These cannot run in a transaction, so they go through the pool directly.
	_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, projectTestDB)
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+projectTestDB); err != nil {
		admin.Close()
		t.Fatalf("drop db: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+projectTestDB); err != nil {
		admin.Close()
		t.Fatalf("create db: %v", err)
	}
	admin.Close()

	pool, err := pgxpool.New(ctx, swapDBName(t, base, projectTestDB))
	if err != nil {
		t.Fatalf("connect to %s: %v", projectTestDB, err)
	}
	return pool
}

// testPool returns a clean project test database with both migrations applied.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := freshDB(t)
	ctx := context.Background()
	if err := job.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("job migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("project migrate: %v", err)
	}
	return pool
}

// swapDBName rewrites a Postgres URL's database path to name.
func swapDBName(t *testing.T, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// TestMigrationBackfillsPapersFromSessions is 012 AC9: after the migration,
// every paper a project has ever run is listed on it — including one whose only
// session failed, which is the case that proves a paper is not the same thing as
// a successful run. Postgres-only.
func TestMigrationBackfillsPapersFromSessions(t *testing.T) {
	pool := freshDB(t)
	defer pool.Close()
	ctx := context.Background()

	if err := job.Migrate(ctx, pool); err != nil {
		t.Fatalf("job migrate: %v", err)
	}
	js := job.NewPGStore(pool)

	// One project, two papers: one that finished, one whose session failed. The
	// second is the point — before 012 the only record of that paper was a red
	// row, and after it the paper is still what the project is about.
	done, err := js.Enqueue(ctx, job.Submission{
		Principal: "owner", Ref: "https://doi.org/10.1145/3544548.3581433",
		Kind: job.KindAudit, Project: "proj-1", PaperKey: "doi:10.1145/3544548.3581433"})
	if err != nil {
		t.Fatalf("seed done: %v", err)
	}
	for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
		if err := js.Advance(ctx, done.ID, p, nil); err != nil {
			t.Fatalf("advance done: %v", err)
		}
	}
	failed, err := js.Enqueue(ctx, job.Submission{
		Principal: "owner", Ref: "arXiv:2401.00001",
		Kind: job.KindAudit, Project: "proj-1", PaperKey: "arxiv:2401.00001"})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := js.Advance(ctx, failed.ID, job.PhaseFailed, func(j *job.Job) { j.Err = "resolve: source unreachable" }); err != nil {
		t.Fatalf("fail: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("project migrate: %v", err)
	}

	papers, err := NewPGStore(pool).ListPapers(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ListPapers: %v", err)
	}
	if len(papers) != 2 {
		t.Fatalf("backfilled %d papers, want 2: %+v", len(papers), papers)
	}
	byKey := map[string]Paper{}
	for _, p := range papers {
		byKey[p.Key] = p
	}
	if p, ok := byKey["arxiv:2401.00001"]; !ok {
		t.Errorf("the failed session's paper is missing — a paper is not its run")
	} else if p.Ref != "arXiv:2401.00001" || p.AddedBy != "owner" || p.AddedAt.IsZero() {
		t.Errorf("failed session's paper = %+v", p)
	}
	if p := byKey["doi:10.1145/3544548.3581433"]; p.Title != "" {
		t.Errorf("the backfill must not guess a title, got %q", p.Title)
	}
}
