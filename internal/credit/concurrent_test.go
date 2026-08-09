package credit

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// A wallet cannot be spent twice. Two allocations reading the same balance and
// both committing would create money out of nothing — the balance and the debit
// are the same table, so the read has to be authoritative for the rest of the
// writing transaction, which is what the advisory lock buys.
//
// Confirmed to fail without the lock in the Postgres store: under READ
// COMMITTED both transactions see the other's uncommitted row as absent, and
// both are admitted.
func TestPGStoreAndMemStoreAdmitExactlyWhatAWalletHolds(t *testing.T) {
	const (
		funded  = 10 * Dollar
		each    = 3 * Dollar
		tries   = 8
		wantOK  = funded / each // 3 fit; the rest must be refused
		wantEnd = funded - wantOK*each
	)

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
			if err := s.Topup(ctx, admin, lead.Email, funded); err != nil {
				t.Fatalf("Topup: %v", err)
			}

			var (
				wg       sync.WaitGroup
				mu       sync.Mutex
				ok, over int
			)
			start := make(chan struct{})
			for range tries {
				wg.Go(func() {
					<-start
					err := s.Allocate(ctx, lead, "p1", "P1", each)
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						ok++
					case errors.Is(err, ErrInsufficient):
						over++
					default:
						t.Errorf("Allocate: %v", err)
					}
				})
			}
			close(start)
			wg.Wait()

			if ok != wantOK || over != tries-wantOK {
				t.Errorf("admitted %d and refused %d; want %d and %d", ok, over, wantOK, tries-wantOK)
			}
			// The arithmetic has to agree with the count, not merely look close:
			// a wallet that ended negative is money the platform invented.
			wallet, _ := s.Balance(ctx, Wallet(lead.Email))
			pot, _ := s.Balance(ctx, Pot("p1"))
			if wallet != wantEnd || pot != int64(wantOK)*each {
				t.Errorf("wallet %d, pot %d; want %d and %d", wallet, pot, int64(wantEnd), int64(wantOK)*each)
			}
			if wallet+pot != funded {
				t.Errorf("wallet + pot = %d, want %d — a transfer lost or invented credits", wallet+pot, funded)
			}
		})
	}
}

// Two settlements of one session, racing in separate transactions, produce one
// charge. Exactly one reports that it settled; the other says it did not.
//
// This is the concurrent half of the exactly-once guarantee, and it is asserted
// here rather than through the job store: there the phase machine serialises
// terminal writes, so a test driving cancels concurrently passes whether or not
// the ledger guards anything.
func TestPGStoreSettlesOnceUnderConcurrency(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	auth, members := testAuthority()
	s := NewPGStore(pool, auth, members)

	if err := s.Topup(ctx, admin, lead.Email, 10*Dollar); err != nil {
		t.Fatalf("Topup: %v", err)
	}
	if err := s.Allocate(ctx, lead, "p1", "P1", 10*Dollar); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	writeHold(t, s, Pot("p1"), "job-1", 3*Dollar)

	const racers = 6
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		settled     int
		alreadyDone int
	)
	start := make(chan struct{})
	for range racers {
		wg.Go(func() {
			<-start
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Errorf("begin: %v", err)
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			ok, err := s.SettleTx(ctx, tx, Pot("p1"), "job-1", 2*Dollar)
			if err != nil {
				// A loser may surface the unique violation instead of a clean
				// false; both are "somebody else settled", never a second charge.
				return
			}
			if err := tx.Commit(ctx); err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ok {
				settled++
			} else {
				alreadyDone++
			}
		})
	}
	close(start)
	wg.Wait()

	if settled != 1 {
		t.Errorf("%d racers settled, want exactly 1", settled)
	}
	if n, _ := s.Balance(ctx, Pot("p1")); n != 8*Dollar {
		t.Errorf("balance = %d after %d racing settlements, want %d", n, racers, 8*Dollar)
	}
	if n, _ := s.Available(ctx, Pot("p1")); n != 8*Dollar {
		t.Errorf("available = %d, want %d — the hold was released more than once", n, 8*Dollar)
	}
}
