// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"latere.ai/x/pay/ledger"
	"latere.ai/x/pay/ledger/pgledger"
	"latere.ai/x/pay/money"

	"github.com/latere-ai/replichai/internal/access"
)

// The ledger itself is latere.ai/x/pay/ledger. This file holds everything that
// ledger deliberately leaves to the product: who may move money, what a move
// is called on a statement, and this product's two holder namespaces.
//
// This split is what allows the ledger to be shared. Its Store takes a holder
// and an amount and checks nothing; if it took an access.Principal it would be
// specific to this product.

// The reasons are the product's names for why an entry exists. The shared
// ledger records them in Reason, and this package reconstructs the historical
// Kind from them, so the statement a browser reads is byte-identical to the
// one it read before the ledger moved out.
const (
	reasonTopup    ledger.Reason = "topup"
	reasonAllocate ledger.Reason = "allocate"
	reasonReclaim  ledger.Reason = "reclaim"
	reasonHold     ledger.Reason = "hold"
	reasonRelease  ledger.Reason = "release"
	reasonCharge   ledger.Reason = "charge"
	reasonDraft    ledger.Reason = "draft"
	reasonAdjust   ledger.Reason = "adjust"
)

// labelProjectID and labelProjectName are the snapshotted project dimensions.
// The shared ledger stores labels and never interprets them, which is what a
// snapshot needs: a purged project still reads correctly here.
const (
	labelProjectID   = "project_id"
	labelProjectName = "project_name"
)

// store is the authority layer over the shared ledger. Both concrete stores
// embed it, so the permission rules are written once and cannot drift between
// an in-memory run and Postgres.
type store struct {
	led     ledger.Store
	auth    Authority
	members Members
	// onSolvent is called when money entering a wallet leaves it at or above
	// zero, by any path. It retires the freeze waiver at the moment the wallet
	// is solvent again, rather than by a write on every later read.
	onSolvent func(ctx context.Context, email string)
	// lockWallet serializes the reversals that land on one wallet across
	// processes. Nil means this process's own mutex is the only guard, which is
	// all a single in-memory ledger needs.
	lockWallet func(ctx context.Context, key string) (unlock func(), err error)
	// won records the disputes decided for the merchant, so a dispute whose own
	// delivery arrives after its win takes nothing.
	won wonDisputes
}

// wonDisputes remembers which disputes were decided for the merchant. A win is
// recorded whether or not the dispute had taken anything yet, which is what
// makes the order of the two deliveries not matter.
type wonDisputes interface {
	markWon(ctx context.Context, disputeRef string) error
	isWon(ctx context.Context, disputeRef string) (bool, error)
}

// memWon is wonDisputes for the in-memory ledger.
type memWon struct {
	mu  sync.Mutex
	set map[string]bool
}

func (m *memWon) markWon(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.set == nil {
		m.set = make(map[string]bool)
	}
	m.set[ref] = true
	return nil
}

func (m *memWon) isWon(_ context.Context, ref string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.set[ref], nil
}

// pgWon is wonDisputes over the credit_won_disputes table.
type pgWon struct{ pool *pgxpool.Pool }

func (p pgWon) markWon(ctx context.Context, ref string) error {
	if _, err := p.pool.Exec(ctx, `INSERT INTO credit_won_disputes (dispute_ref) VALUES ($1) ON CONFLICT DO NOTHING`, ref); err != nil {
		return fmt.Errorf("record the won dispute %s: %w", ref, err)
	}
	return nil
}

func (p pgWon) isWon(ctx context.Context, ref string) (bool, error) {
	var won bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credit_won_disputes WHERE dispute_ref = $1)`, ref).Scan(&won); err != nil {
		return false, fmt.Errorf("read the won dispute %s: %w", ref, err)
	}
	return won, nil
}

// reverseMu serializes the reversals of purchases inside this process. A
// reversal reads what earlier ones took back and then writes its own, so two of
// them running at once could each read the same total and together take back
// more than the purchase credited.
var reverseMu sync.Mutex

// Option configures a store.
type Option func(*store)

// WithOnSolvent registers fn to be called the moment money entering a wallet
// leaves it solvent, whether it came from a top-up, an admin grant or a reclaim
// from a project. The composition root uses it to retire the freeze override.
func WithOnSolvent(fn func(ctx context.Context, email string)) Option {
	return func(s *store) { s.onSolvent = fn }
}

// solvent calls onSolvent for a wallet that just received money, when its
// balance is at or above zero. Every path that puts money in a wallet calls it:
// a top-up, an admin grant and a reclaim from a project. It is best-effort: a
// read failure here leaves the override for the next deposit to retire.
func (s *store) solvent(ctx context.Context, email string) {
	if s.onSolvent == nil {
		return
	}
	if bal, err := s.Balance(ctx, Wallet(email)); err == nil && bal >= 0 {
		s.onSolvent(ctx, email)
	}
}

// MemStore is the in-memory ledger with this product's permission rules.
type MemStore struct {
	store
	mem *ledger.MemStore
}

// NewMemStore builds an empty in-memory ledger. A nil Authority or Members
// means the check it would perform is skipped, which is the mode for a unit
// test that is not about permissions.
func NewMemStore(auth Authority, members Members, opts ...Option) *MemStore {
	mem := ledger.NewMemStore()
	s := &MemStore{mem: mem}
	s.store = store{led: mem, auth: auth, members: members, won: &memWon{}}
	for _, opt := range opts {
		opt(&s.store)
	}
	return s
}

// PGStore is the Postgres ledger with this product's permission rules.
type PGStore struct {
	store
	pg     *pgledger.Store
	dbpool *pgxpool.Pool
}

// NewPGStore builds a ledger over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, auth Authority, members Members, opts ...Option) *PGStore {
	pg := pgledger.New(pool)
	s := &PGStore{pg: pg, dbpool: pool}
	s.store = store{led: pg, auth: auth, members: members, lockWallet: advisoryLock(pool), won: pgWon{pool: pool}}
	for _, opt := range opts {
		opt(&s.store)
	}
	return s
}

// advisoryLock takes a Postgres advisory lock keyed on key, on a connection of
// its own, so replicas serialize the reversals that land on one wallet. The
// lock is held until the returned function runs.
func advisoryLock(pool *pgxpool.Pool) func(ctx context.Context, key string) (func(), error) {
	return func(ctx context.Context, key string) (func(), error) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("lock %s: %w", key, err)
		}
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "reversal:"+key); err != nil {
			conn.Release()
			return nil, fmt.Errorf("lock %s: %w", key, err)
		}
		return func() {
			// A fresh context: the caller's may be cancelled by now, and a lock
			// left on a pooled connection would outlive this call. A connection
			// that cannot unlock is closed instead, which ends its session and
			// so the lock with it.
			bg := context.WithoutCancel(ctx)
			if _, err := conn.Exec(bg, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "reversal:"+key); err != nil {
				slog.WarnContext(ctx, "credit: reversal lock not released, closing its connection", "key", key, "error", err)
				if cerr := conn.Conn().Close(bg); cerr != nil {
					slog.WarnContext(ctx, "credit: closing a connection that holds a reversal lock", "key", key, "error", cerr)
				}
			}
			conn.Release()
		}, nil
	}
}

// pool exposes the connection pool to this package's tests, which need to open
// a transaction to drive HoldTx and SettleTx the way the job store does.
func (s *PGStore) pool() *pgxpool.Pool { return s.dbpool }

// Balance is everything a holder received less everything spent, excluding
// holds: a hold is a commitment rather than spend.
func (s *store) Balance(ctx context.Context, h Holder) (int64, error) {
	v, err := s.led.Balance(ctx, ledger.Holder(h))
	return int64(v), err
}

// Available is Balance less every open hold: what a holder may still commit.
func (s *store) Available(ctx context.Context, h Holder) (int64, error) {
	v, err := s.led.Available(ctx, ledger.Holder(h))
	return int64(v), err
}

// Entries lists a holder's statement, newest first, capped at limit.
func (s *store) Entries(ctx context.Context, h Holder, limit int) ([]Entry, error) {
	rows, err := s.led.Entries(ctx, ledger.Holder(h), ledger.Page{Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromLedger(r))
	}
	return out, nil
}

// EntryByExternalRef finds the entry carrying ref, if any.
func (s *store) EntryByExternalRef(ctx context.Context, ref string) (Entry, bool, error) {
	e, ok, err := s.led.EntryByRef(ctx, ref)
	if err != nil || !ok {
		return Entry{}, ok, err
	}
	return fromLedger(e), true, nil
}

// BalancesFor folds the settled balance of every given holder in one pass.
func (s *store) BalancesFor(ctx context.Context, holders []Holder) (map[Holder]int64, error) {
	keys := make([]ledger.Holder, len(holders))
	for i, h := range holders {
		keys[i] = ledger.Holder(h)
	}
	got, err := s.led.BalancesFor(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[Holder]int64, len(got))
	for h, v := range got {
		out[Holder(h)] = int64(v)
	}
	return out, nil
}

// NegativeWallets returns every wallet with a negative balance, most-owed
// first. Pots are excluded: a project pot may legitimately be negative, which
// is not a debt an account holder owes.
func (s *store) NegativeWallets(ctx context.Context) ([]WalletBalance, error) {
	rows, err := s.led.NegativeHolders(ctx, walletNamespace)
	if err != nil {
		return nil, err
	}
	out := make([]WalletBalance, 0, len(rows))
	for _, r := range rows {
		email, ok := WalletEmail(Holder(r.Holder))
		if !ok {
			continue
		}
		out = append(out, WalletBalance{Email: email, Balance: int64(r.Balance)})
	}
	return out, nil
}

// TotalOutstanding is every credit sold and not yet delivered as compute: the
// sum over every holder, wallets and pots, less open holds.
func (s *store) TotalOutstanding(ctx context.Context) (int64, error) {
	v, err := s.led.TotalOutstanding(ctx)
	return int64(v), err
}

// Topup creates credits in one email's wallet. Only a platform admin may call
// it; until a payment provider posts the same entry, this is the only way
// money enters. A non-empty key is the grant's idempotency key, so a retried
// request credits once.
func (s *store) Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64, key string) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	ref, err := grantRef(key)
	if err != nil {
		return err
	}
	// An admin grant is money entering from inside, so it has no processor
	// reference of its own. The ledger requires one for a credit: the caller's
	// key when it sent one, so a retry is the same grant, and a generated id
	// otherwise. grantRefPrefix marks it as internal, and the statement mapping
	// keeps it out of ExternalRef so the wire shape is unchanged.
	if err := s.led.Credit(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(amount),
		Reason: reasonTopup,
		Ref:    ref,
		Actor:  byAdmin.Email,
	}); err != nil {
		if errors.Is(err, ledger.ErrRefConflict) {
			return ErrKeyReused
		}
		return err
	}
	s.solvent(ctx, email)
	return nil
}

// TopupPaid posts a purchased credit into a wallet, idempotent on ref. It has
// no principal to check: authority came from a verified webhook signature,
// upstream.
func (s *store) TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error {
	if err := checkPaid(email, credited, ref); err != nil {
		return err
	}
	if err := s.led.Credit(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(credited),
		Reason: reasonTopup,
		Ref:    ref,
		Actor:  actor,
	}); err != nil {
		return err
	}
	s.solvent(ctx, email)
	return nil
}

// RefundPaid reverses a purchase by the exact micro-USD it credited, keyed on
// its own reference so it deduplicates independently. The wallet may go
// negative; that is the debt a reversed purchase leaves.
func (s *store) RefundPaid(ctx context.Context, email string, amount int64, ref, actor string) error {
	if err := checkPaid(email, amount, ref); err != nil {
		return err
	}
	return s.led.Adjust(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(amount),
		Reason: reasonAdjust,
		Ref:    ref,
		Actor:  actor,
	}, false)
}

// ErrUnknownPurchase reports a reversal of a purchase this ledger never
// recorded, or one that is not a wallet top-up.
var ErrUnknownPurchase = errors.New("no wallet purchase with that reference")

// labelReverses names, on a reversal's entry, the purchase it takes credit back
// from. It is how a later reversal knows what earlier ones already took.
const labelReverses = "reverses"

// reversalPage is how many statement rows are read at a time when summing what
// a purchase has had reversed.
const reversalPage = 500

// RefundShare takes back the share of a purchase's credit that a refund covers.
// refunded is everything refunded on the charge so far and charged is what the
// charge was, in the same unit. The total taken back for the purchase becomes
// the credit times refunded over charged, rounded down, so rounding can only
// leave a little credit with the person and never take more than was refunded.
// Reversals already recorded for the purchase count toward that total, so a
// replay or an out-of-order delivery takes back nothing extra. It returns the
// credit taken back by this call.
func (s *store) RefundShare(ctx context.Context, purchaseRef, reversalRef string, refunded, charged int64) (Reversal, error) {
	if refunded < 0 || charged <= 0 {
		return Reversal{}, ErrNotPositive
	}
	return s.reverse(ctx, purchaseRef, reversalRef, "", func(credited, taken int64) int64 {
		return RefundReversal(credited, charged, refunded, taken)
	})
}

// RefundRemainder takes back whatever credit of a purchase has not been taken
// back yet, which is what a dispute does. reversalRef is the dispute's id, and
// a dispute already decided for the merchant takes nothing, whichever of its
// deliveries arrived first. It returns the credit taken back by this call.
func (s *store) RefundRemainder(ctx context.Context, purchaseRef, reversalRef string) (Reversal, error) {
	return s.reverse(ctx, purchaseRef, reversalRef, reversalRef, func(credited, taken int64) int64 { return max(credited-taken, 0) })
}

// lockReversals serializes the reversals and restorations that land on one
// wallet, inside this process and across processes, until the returned function
// runs.
func (s *store) lockReversals(ctx context.Context, h Holder) (func(), error) {
	reverseMu.Lock()
	if s.lockWallet == nil {
		return reverseMu.Unlock, nil
	}
	unlock, err := s.lockWallet(ctx, string(h))
	if err != nil {
		reverseMu.Unlock()
		return nil, err
	}
	return func() {
		unlock()
		reverseMu.Unlock()
	}, nil
}

// labelRestores names, on the entry that gives back what a won dispute took,
// the dispute whose reversal it undoes.
const labelRestores = "restores"

// RestoreDispute gives back exactly what the reversal keyed disputeRef took
// from purchaseRef, for a dispute decided for the merchant. The entry is keyed
// disputeRef+":won", so a second delivery of the outcome credits nothing more,
// and it carries the purchase's label so later refunds count the credit as no
// longer reversed. The win is recorded first, so a dispute whose own delivery
// comes after it takes nothing. It returns the credit given back, zero when the
// dispute took nothing or was already restored. A wallet this leaves at or above
// zero is reported through the solvency hook.
func (s *store) RestoreDispute(ctx context.Context, purchaseRef, disputeRef string) (int64, error) {
	if strings.TrimSpace(purchaseRef) == "" || strings.TrimSpace(disputeRef) == "" {
		return 0, ErrNoRef
	}
	orig, ok, err := s.EntryByExternalRef(ctx, purchaseRef)
	if err != nil {
		return 0, err
	}
	email, wallet := WalletEmail(orig.Holder)
	if !ok || !wallet || orig.Kind != KindTopup {
		return 0, ErrUnknownPurchase
	}
	unlock, err := s.lockReversals(ctx, orig.Holder)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if err := s.won.markWon(ctx, disputeRef); err != nil {
		return 0, err
	}
	took, ok, err := s.led.EntryByRef(ctx, disputeRef)
	if err != nil {
		return 0, err
	}
	if !ok || took.Labels[labelReverses] != purchaseRef || took.Amount >= 0 {
		return 0, nil // the dispute took nothing from this purchase, and now never will
	}
	wonRef := disputeRef + ":won"
	if _, done, err := s.led.EntryByRef(ctx, wonRef); err != nil || done {
		return 0, err // an adjust has no uniqueness of its own, so a repeat is found here
	}
	back := -int64(took.Amount)
	if err := s.led.Adjust(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(back),
		Reason: reasonAdjust,
		Ref:    wonRef,
		Actor:  "stripe:dispute_won:" + disputeRef,
		Labels: map[string]string{labelReverses: purchaseRef, labelRestores: disputeRef},
	}, true); err != nil {
		return 0, err
	}
	s.solvent(ctx, email)
	return back, nil
}

// reverse takes back owed(credited, taken) of one purchase's credit, where
// taken is what earlier reversals of the purchase already took back, by posting
// it under reversalRef. An amount of zero or less posts nothing, and so does a
// reversal for disputeRef, when it is set, once that dispute is won. The reversals that land on one wallet run
// one at a time, so the sum of what a purchase has had reversed is read and
// extended without another reversal in between, and the balance read before and
// after the post is the balance this reversal alone moved across zero.
func (s *store) reverse(ctx context.Context, purchaseRef, reversalRef, disputeRef string, owed func(credited, taken int64) int64) (Reversal, error) {
	if strings.TrimSpace(purchaseRef) == "" || strings.TrimSpace(reversalRef) == "" {
		return Reversal{}, ErrNoRef
	}
	orig, ok, err := s.EntryByExternalRef(ctx, purchaseRef)
	if err != nil {
		return Reversal{}, err
	}
	email, wallet := WalletEmail(orig.Holder)
	if !ok || !wallet || orig.Amount <= 0 || orig.Kind != KindTopup {
		return Reversal{}, ErrUnknownPurchase
	}
	unlock, err := s.lockReversals(ctx, orig.Holder)
	if err != nil {
		return Reversal{}, err
	}
	defer unlock()
	if disputeRef != "" {
		won, err := s.won.isWon(ctx, disputeRef)
		if err != nil {
			return Reversal{}, err
		}
		if won {
			return Reversal{}, nil
		}
	}
	taken, err := s.reversed(ctx, orig.Holder, purchaseRef)
	if err != nil {
		return Reversal{}, err
	}
	delta := min(owed(orig.Amount, taken), orig.Amount-taken)
	if delta <= 0 {
		return Reversal{}, nil
	}
	before, err := s.Balance(ctx, orig.Holder)
	if err != nil {
		return Reversal{}, err
	}
	if err := s.led.Adjust(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(delta),
		Reason: reasonAdjust,
		Ref:    reversalRef,
		Actor:  "stripe:refund:" + reversalRef,
		Labels: map[string]string{labelReverses: purchaseRef},
	}, false); err != nil {
		return Reversal{}, err
	}
	after, err := s.Balance(ctx, orig.Holder)
	if err != nil {
		// The reversal is posted; only whether it froze the account is unknown.
		slog.WarnContext(ctx, "credit: balance not read after a reversal", "reversal", reversalRef, "error", err)
		return Reversal{Took: delta}, nil
	}
	return Reversal{Took: delta, Froze: before >= 0 && after < 0}, nil
}

// reversed sums the credit already taken back from one purchase, as a positive
// number, from the reversals labeled with its reference.
//
// The statement pages by time alone, and entries written in one transaction
// share a time, so a page can end partway through the entries of its last
// time. Each next page starts just after that time instead, which reads the
// rest of them, and the entries already summed are skipped by id.
func (s *store) reversed(ctx context.Context, h Holder, purchaseRef string) (int64, error) {
	var total int64
	var before time.Time
	seen := make(map[string]bool)
	for {
		page, err := s.led.Entries(ctx, ledger.Holder(h), ledger.Page{Limit: reversalPage, Before: before})
		if err != nil {
			return 0, err
		}
		fresh := 0
		for _, e := range page {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			fresh++
			if e.Labels[labelReverses] == purchaseRef {
				total -= int64(e.Amount)
			}
		}
		if len(page) < reversalPage {
			return total, nil
		}
		if fresh == 0 {
			return 0, fmt.Errorf("credit: more than %d statement entries of %s share one time", reversalPage, h)
		}
		before = page[len(page)-1].CreatedAt.Add(time.Microsecond)
	}
}

// Spend charges a pot for work that is not a session. It is idempotent on ref,
// and it never refuses for lack of balance: the tokens were spent before this
// is called, and a project that has just gone negative owes the amount rather
// than getting the work free.
func (s *store) Spend(ctx context.Context, pot Holder, amount int64, ref, actor string) error {
	// The order and the zero case are the historical contract: a missing
	// reference is refused first, a negative is a programming error, and a
	// spend of nothing is a no-op rather than a refusal, because a model call
	// that consumed no tokens still completed.
	if strings.TrimSpace(ref) == "" {
		return ErrNoRef
	}
	if amount < 0 {
		return ErrNotPositive
	}
	if amount == 0 {
		return nil
	}
	if !pot.valid() {
		return ErrNoProject
	}
	return s.led.Debit(ctx, ledger.Posting{
		Holder: ledger.Holder(pot),
		Amount: money.Micro(amount),
		Reason: reasonDraft,
		Ref:    ref,
		Actor:  actor,
	})
}

// Allocate moves credits from the caller's wallet into a project's pot. Any
// member of the project may fund it.
func (s *store) Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := s.requireMember(ctx, by, project); err != nil {
		return err
	}
	// The ledger checks the wallet's balance under its own lock, but a reversal
	// does not take that lock, so an allocation could read the balance before a
	// reversal and land after the reversal had read the result. Taking the
	// reversals' lock puts it wholly before or after one, so the reversal sees
	// whether it moved the wallet below zero and the freeze mail goes out.
	unlock, err := s.lockReversals(ctx, Wallet(by.Email))
	if err != nil {
		return err
	}
	defer unlock()
	return s.move(ctx, Wallet(by.Email), Pot(project), amount, reasonAllocate, by.Email, project, projectName)
}

// Reclaim moves credits from a project's pot back to the caller's wallet. Only
// a lead may call it: the pot is fungible and does not record who funded it,
// so reclaiming is a lead's authority over the project's money rather than a
// refund of the caller's own.
func (s *store) Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := s.requireLead(ctx, by, project); err != nil {
		return err
	}
	if err := s.move(ctx, Pot(project), Wallet(by.Email), amount, reasonReclaim, by.Email, project, projectName); err != nil {
		return err
	}
	// Reclaimed credit can lift a negative wallet to zero, which ends the
	// freeze waiver just as a top-up does.
	s.solvent(ctx, by.Email)
	return nil
}

// move is the shared body of every transfer, so the two sides and their
// snapshotted project labels are built one way.
func (s *store) move(ctx context.Context, from, to Holder, amount int64, reason ledger.Reason, actor, projectID, projectName string) error {
	labels := map[string]string{labelProjectID: projectID}
	if projectName != "" {
		labels[labelProjectName] = projectName
	}
	err := s.led.Transfer(ctx, ledger.Transfer{
		From:   ledger.Holder(from),
		To:     ledger.Holder(to),
		Amount: money.Micro(amount),
		Reason: reason,
		Actor:  actor,
		Labels: labels,
	})
	if errors.Is(err, ledger.ErrInsufficient) {
		return ErrInsufficient
	}
	return err
}

// Hold reserves against a pot for a session about to start.
func (s *MemStore) Hold(ctx context.Context, h Holder, jobID string, amount int64) error {
	return holdErr(s.mem.Hold(ctx, holdPosting(h, jobID, amount)))
}

// Settle releases a session's hold and charges what it cost, once.
func (s *MemStore) Settle(ctx context.Context, h Holder, jobID string, cost int64) (bool, error) {
	return s.mem.Settle(ctx, settlement(h, jobID, cost))
}

// HoldTx reserves against a pot inside the caller's transaction, so the hold
// and the session row it guards commit together.
func (s *PGStore) HoldTx(ctx context.Context, tx pgx.Tx, h Holder, jobID string, amount int64) error {
	return holdErr(s.pg.Bind(tx).Hold(ctx, holdPosting(h, jobID, amount)))
}

// SettleTx releases a session's hold and charges what it cost inside the
// caller's transaction, so a session can never be terminal and unsettled.
func (s *PGStore) SettleTx(ctx context.Context, tx pgx.Tx, h Holder, jobID string, cost int64) (bool, error) {
	return s.pg.Bind(tx).Settle(ctx, settlement(h, jobID, cost))
}

func holdPosting(h Holder, jobID string, amount int64) ledger.Posting {
	return ledger.Posting{
		Holder: ledger.Holder(h),
		Amount: money.Micro(amount),
		Reason: reasonHold,
		Group:  jobID,
		Actor:  ActorSystem,
	}
}

func settlement(h Holder, jobID string, cost int64) ledger.Settlement {
	return ledger.Settlement{
		Holder: ledger.Holder(h),
		Group:  jobID,
		Cost:   money.Micro(cost),
		Reason: reasonCharge,
		Actor:  ActorSystem,
	}
}

// holdErr maps the shared ledger's refusals onto this package's errors, so
// callers keep comparing against credit.ErrInsufficient.
func holdErr(err error) error {
	switch {
	case errors.Is(err, ledger.ErrInsufficient):
		return ErrInsufficient
	case errors.Is(err, ledger.ErrNotPositive):
		return ErrNotPositive
	default:
		return err
	}
}

// grantRefPrefix marks a reference this package generated for an admin grant
// rather than one a processor issued.
const grantRefPrefix = "grant:"

// MaxGrantKey bounds an admin grant's idempotency key.
const MaxGrantKey = 128

// grantRef is the ledger reference for an admin grant: the caller's key when
// there is one, a fresh id otherwise.
func grantRef(key string) (string, error) {
	if key == "" {
		id, err := ledger.NewID()
		if err != nil {
			return "", err
		}
		return grantRefPrefix + id, nil
	}
	if len(key) > MaxGrantKey || strings.IndexFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) >= 0 {
		return "", ErrBadKey
	}
	return grantRefPrefix + "key:" + key, nil
}

// fromLedger converts a shared ledger entry back into this product's shape.
//
// The historical Kind is reconstructed from Reason, so a statement a browser
// reads is unchanged. The ledger's vocabulary is mechanical, recording whether
// an entry commits and what sign it has, and this product's is descriptive.
func fromLedger(e ledger.Entry) Entry {
	out := Entry{
		ID:        e.ID,
		Holder:    Holder(e.Holder),
		Amount:    int64(e.Amount),
		Kind:      kindFor(e),
		JobID:     e.Group,
		Actor:     e.Actor,
		CreatedAt: e.CreatedAt,
	}
	if !strings.HasPrefix(e.Ref, grantRefPrefix) {
		out.ExternalRef = e.Ref
	}
	if e.Labels != nil {
		out.ProjectID = e.Labels[labelProjectID]
		out.ProjectName = e.Labels[labelProjectName]
	}
	// A transfer's group is the project it moved to or from, not a session.
	if e.Kind == ledger.KindTransfer {
		out.JobID = ""
	}
	return out
}

// kindFor reconstructs the historical kind. Reason carries it for every entry
// this package wrote; anything else falls back to the ledger's own kind name,
// which is the accurate description of an entry written by another writer.
func kindFor(e ledger.Entry) Kind {
	switch e.Reason {
	case reasonTopup:
		return KindTopup
	case reasonAllocate:
		return KindAllocate
	case reasonReclaim:
		return KindReclaim
	case reasonHold:
		if e.Kind == ledger.KindRelease {
			return KindRelease
		}
		return KindHold
	case reasonRelease:
		return KindRelease
	case reasonCharge:
		if e.Kind == ledger.KindRelease {
			return KindRelease
		}
		return KindCharge
	case reasonDraft:
		return KindDraft
	case reasonAdjust:
		return KindAdjust
	default:
		return Kind(e.Kind)
	}
}

// requireAdmin refuses a caller who is not a platform admin. It applies the
// shared rule and reports the refusal with this package's error.
func (s *store) requireAdmin(ctx context.Context, p access.Principal) error {
	err := access.RequireAdmin(ctx, s.auth, p)
	if errors.Is(err, access.ErrNotAdmin) {
		return ErrNotAdmin
	}
	if err != nil {
		return fmt.Errorf("resolve admin: %w", err)
	}
	return nil
}

// requireMember refuses a caller who is not in the project.
func (s *store) requireMember(ctx context.Context, p access.Principal, project string) error {
	if s.members == nil {
		return nil
	}
	_, ok, err := s.members.Role(ctx, p, project)
	if err != nil {
		return fmt.Errorf("resolve membership: %w", err)
	}
	if !ok {
		return ErrNotMember
	}
	return nil
}

// requireLead refuses a caller who is not a lead of the project.
func (s *store) requireLead(ctx context.Context, p access.Principal, project string) error {
	if s.members == nil {
		return nil
	}
	role, ok, err := s.members.Role(ctx, p, project)
	if err != nil {
		return fmt.Errorf("resolve membership: %w", err)
	}
	if !ok {
		return ErrNotMember
	}
	if role != access.RoleLead {
		return ErrNotLead
	}
	return nil
}
