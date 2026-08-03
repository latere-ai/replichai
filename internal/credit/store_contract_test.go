package credit

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
)

// people used across the contract.
var (
	admin  = access.Principal{Sub: "s-admin", Email: "Admin@Latere.ai"}
	lead   = access.Principal{Sub: "s-lead", Email: "lead@latere.ai"}
	mate   = access.Principal{Sub: "s-mate", Email: "mate@latere.ai"}
	outlaw = access.Principal{Sub: "s-out", Email: "stranger@latere.ai"}
)

// fakeAuth answers tiers from a map, so the contract can drive an admin-only
// write without an access store.
type fakeAuth map[string]access.Tier

func (f fakeAuth) Resolve(_ context.Context, p access.Principal) (access.Tier, bool, error) {
	t, ok := f[p.Sub]
	return t, ok, nil
}

// fakeMembers answers roles from a map keyed "project/sub".
type fakeMembers map[string]access.Role

func (f fakeMembers) Role(_ context.Context, p access.Principal, project string) (access.Role, bool, error) {
	r, ok := f[project+"/"+p.Sub]
	return r, ok, nil
}

func testAuthority() (Authority, Members) {
	return fakeAuth{
			admin.Sub: access.TierAdmin,
			lead.Sub:  access.TierUser,
			mate.Sub:  access.TierUser,
		}, fakeMembers{
			"p1/" + lead.Sub: access.RoleLead,
			"p1/" + mate.Sub: access.RoleCollaborator,
		}
}

// runStoreContract asserts the invariants every credit Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// An empty holder has nothing, and that is a balance rather than an error:
	// a wallet exists the moment someone asks about it.
	if n, err := s.Balance(ctx, Wallet(lead.Email)); err != nil || n != 0 {
		t.Fatalf("empty wallet = %d, %v; want 0", n, err)
	}

	// Only an admin creates credits.
	if err := s.Topup(ctx, lead, lead.Email, 10*Dollar); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin top-up: want ErrNotAdmin, got %v", err)
	}
	if err := s.Topup(ctx, admin, "  ", 10*Dollar); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("blank email: want ErrNoEmail, got %v", err)
	}
	for _, bad := range []int64{0, -1} {
		if err := s.Topup(ctx, admin, lead.Email, bad); !errors.Is(err, ErrNotPositive) {
			t.Fatalf("top-up of %d: want ErrNotPositive, got %v", bad, err)
		}
	}
	if err := s.Topup(ctx, admin, lead.Email, 10*Dollar); err != nil {
		t.Fatalf("Topup: %v", err)
	}
	// The wallet is keyed by normalised email, so a grant seeded one way is
	// found by a login typed another.
	if n, _ := s.Balance(ctx, Wallet("LEAD@latere.ai ")); n != 10*Dollar {
		t.Fatalf("wallet after top-up = %d, want %d", n, 10*Dollar)
	}

	// Any member may fund a project; a stranger may not.
	if err := s.Allocate(ctx, outlaw, "p1", 1*Dollar); !errors.Is(err, ErrNotMember) {
		t.Fatalf("stranger allocating: want ErrNotMember, got %v", err)
	}
	// Nor may a member spend money they do not have.
	if err := s.Allocate(ctx, mate, "p1", 1*Dollar); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("allocating an empty wallet: want ErrInsufficient, got %v", err)
	}
	if err := s.Allocate(ctx, lead, "p1", 4*Dollar); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	// Both sides moved, and neither invented money.
	if n, _ := s.Balance(ctx, Wallet(lead.Email)); n != 6*Dollar {
		t.Fatalf("wallet after allocating = %d, want %d", n, 6*Dollar)
	}
	if n, _ := s.Balance(ctx, Pot("p1")); n != 4*Dollar {
		t.Fatalf("pot after allocating = %d, want %d", n, 4*Dollar)
	}

	// A collaborator may fund the pot but may not take from it: the pot is
	// fungible and does not remember who put what in, so reclaiming is a lead's
	// authority over the project's money rather than a refund of one's own.
	if err := s.Topup(ctx, admin, mate.Email, 2*Dollar); err != nil {
		t.Fatalf("Topup mate: %v", err)
	}
	if err := s.Allocate(ctx, mate, "p1", 2*Dollar); err != nil {
		t.Fatalf("collaborator Allocate: %v", err)
	}
	if err := s.Reclaim(ctx, mate, "p1", 1*Dollar); !errors.Is(err, ErrNotLead) {
		t.Fatalf("collaborator reclaiming: want ErrNotLead, got %v", err)
	}
	// And a lead may reclaim what somebody else funded — the model as decided.
	if err := s.Reclaim(ctx, lead, "p1", 6*Dollar); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if n, _ := s.Balance(ctx, Pot("p1")); n != 0 {
		t.Fatalf("pot after reclaiming = %d, want 0", n)
	}
	if n, _ := s.Balance(ctx, Wallet(lead.Email)); n != 12*Dollar {
		t.Fatalf("lead wallet = %d, want %d — a lead reclaimed a collaborator's funding", n, 12*Dollar)
	}
	// Overdrawing a pot is refused, not allowed to go negative: only a session
	// that has already run may take a pot below zero.
	if err := s.Reclaim(ctx, lead, "p1", 1*Micro); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("reclaiming an empty pot: want ErrInsufficient, got %v", err)
	}

	// Fund is the create path's move: no membership, because the server has just
	// made this project for this caller and has not written a membership row it
	// would then have to read back. It still cannot conjure money.
	if err := s.Fund(ctx, mate.Email, "fresh", 1*Dollar); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("funding from an empty wallet: want ErrInsufficient, got %v", err)
	}
	if err := s.Fund(ctx, lead.Email, "fresh", 3*Dollar); err != nil {
		t.Fatalf("Fund: %v", err)
	}
	if n, _ := s.Balance(ctx, Pot("fresh")); n != 3*Dollar {
		t.Fatalf("funded pot = %d, want %d", n, 3*Dollar)
	}

	// Unwind is its compensating move, for a create that was refused after its
	// pot was funded. Without it the allocation would sit in a project that is
	// about to be deleted, unreachable: the ledger has no foreign key to clean
	// up after, and a reclaim needs a role in a project that no longer exists.
	if err := s.Unwind(ctx, "fresh", lead.Email); err != nil {
		t.Fatalf("Unwind: %v", err)
	}
	if n, _ := s.Balance(ctx, Pot("fresh")); n != 0 {
		t.Fatalf("pot after unwinding = %d, want 0", n)
	}
	if n, _ := s.Balance(ctx, Wallet(lead.Email)); n != 12*Dollar {
		t.Fatalf("wallet after unwinding = %d, want the %d it held before funding", n, 12*Dollar)
	}
	// And it is safe on every failure path, including one where nothing was
	// funded at all.
	if err := s.Unwind(ctx, "never-funded", lead.Email); err != nil {
		t.Fatalf("unwinding an empty pot: %v", err)
	}

	// The statement explains the balance: every move is a row naming its actor.
	entries, err := s.Entries(ctx, Pot("p1"), 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("pot entries = %d, want 3 (two allocations and a reclaim)", len(entries))
	}
	var sum int64
	for _, e := range entries {
		sum += e.Amount
		if e.ID == "" || e.Actor == "" || e.CreatedAt.IsZero() {
			t.Fatalf("entry %+v is missing its identity, actor or time", e)
		}
	}
	if sum != 0 {
		t.Fatalf("statement sums to %d but the balance is 0 — the ledger disagrees with itself", sum)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	auth, members := testAuthority()
	runStoreContract(t, NewMemStore(auth, members))
}

func TestPGStoreSatisfiesTheContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	auth, members := testAuthority()
	runStoreContract(t, NewPGStore(pool, auth, members))
}

// A hold is a commitment, not a spend: it does not move the balance, it moves
// what may still be committed, and it is what stops a burst of submits each
// being admitted against the same money. Both stores must agree, because the
// enqueue gate reads Available and the settlement reads Balance.
func TestPGStoreAndMemStoreAgreeOnHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T) (Store, func())
	}{
		{"mem", func(t *testing.T) (Store, func()) {
			auth, members := testAuthority()
			return NewMemStore(auth, members), func() {}
		}},
		{"postgres", func(t *testing.T) (Store, func()) {
			pool := testPool(t)
			auth, members := testAuthority()
			return NewPGStore(pool, auth, members), pool.Close
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()

			if err := s.Topup(ctx, admin, lead.Email, 10*Dollar); err != nil {
				t.Fatalf("Topup: %v", err)
			}
			if err := s.Allocate(ctx, lead, "p1", 10*Dollar); err != nil {
				t.Fatalf("Allocate: %v", err)
			}
			writeHold(t, s, Pot("p1"), "job-1", 3*Dollar)

			// The pot still holds ten: nothing has been spent.
			if n, _ := s.Balance(ctx, Pot("p1")); n != 10*Dollar {
				t.Errorf("balance under a hold = %d, want %d — a hold is not a spend", n, 10*Dollar)
			}
			// But only seven may be committed to anything else.
			if n, _ := s.Available(ctx, Pot("p1")); n != 7*Dollar {
				t.Errorf("available under a hold = %d, want %d", n, 7*Dollar)
			}
			// And the held money cannot be reclaimed out from under the session.
			if err := s.Reclaim(ctx, lead, "p1", 8*Dollar); !errors.Is(err, ErrInsufficient) {
				t.Errorf("reclaiming into a hold: want ErrInsufficient, got %v", err)
			}

			// Settling: the hold comes back and the real cost is charged. The
			// two balances agree again, which is what makes a hold temporary.
			writeSettlement(t, s, Pot("p1"), "job-1", 3*Dollar, 2*Dollar)
			bal, _ := s.Balance(ctx, Pot("p1"))
			avail, _ := s.Available(ctx, Pot("p1"))
			if bal != 8*Dollar || avail != 8*Dollar {
				t.Errorf("after settling: balance %d, available %d; want %d and %d", bal, avail, 8*Dollar, 8*Dollar)
			}
		})
	}
}

// writeHold and writeSettlement post the entries the enqueue gate and the
// settlement will post once they exist, so the ledger's own invariants are
// pinned before anything depends on them.
func writeHold(t *testing.T, s Store, h Holder, job string, amount int64) {
	t.Helper()
	post(t, s, Entry{Holder: h, Amount: -amount, Kind: KindHold, JobID: job, Actor: ActorSystem})
}

func writeSettlement(t *testing.T, s Store, h Holder, job string, held, cost int64) {
	t.Helper()
	post(t, s, Entry{Holder: h, Amount: held, Kind: KindRelease, JobID: job, Actor: ActorSystem})
	post(t, s, Entry{Holder: h, Amount: -cost, Kind: KindCharge, JobID: job, Actor: ActorSystem})
}

// post writes one entry directly, reaching past the public API on purpose:
// these kinds are the system's, and the operations that will write them land in
// later commits. It keeps this test about the ledger's arithmetic.
func post(t *testing.T, s Store, e Entry) {
	t.Helper()
	switch st := s.(type) {
	case *MemStore:
		st.mu.Lock()
		defer st.mu.Unlock()
		if err := st.append(e); err != nil {
			t.Fatalf("append: %v", err)
		}
	case *PGStore:
		ctx := context.Background()
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := st.insertTx(ctx, tx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	default:
		t.Fatalf("unknown store %T", s)
	}
}

// creditTestDB is this package's own database. `go test ./...` runs the
// packages in parallel against one server, and a test that drops and recreates
// its schema cannot share one.
const creditTestDB = "replichai_credit_test"

// testPool returns a clean credit test database with the ledger schema applied.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres credit tests")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, _ = adminPool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, creditTestDB)
	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS `+creditTestDB); err != nil {
		adminPool.Close()
		t.Fatalf("drop db: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+creditTestDB); err != nil {
		adminPool.Close()
		t.Fatalf("create db: %v", err)
	}
	adminPool.Close()

	pool, err := pgxpool.New(ctx, swapDBName(t, base, creditTestDB))
	if err != nil {
		t.Fatalf("connect to %s: %v", creditTestDB, err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("credit migrate: %v", err)
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
