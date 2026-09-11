// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	// zero. It lifts the account freeze at the moment the debt is paid, rather
	// than by a write on every later read.
	onSolvent func(ctx context.Context, email string)
}

// Option configures a store.
type Option func(*store)

// WithOnSolvent registers fn to be called the moment a top-up leaves a wallet
// solvent. The composition root uses it to retire the freeze override, so the
// top-up that made the wallet solvent is the single event that clears it.
func WithOnSolvent(fn func(ctx context.Context, email string)) Option {
	return func(s *store) { s.onSolvent = fn }
}

// solvent calls onSolvent for a wallet that just received money, when its
// balance is at or above zero. It is best-effort: a read failure here leaves
// the override for the next top-up to retire.
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
	s.store = store{led: mem, auth: auth, members: members}
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
	s.store = store{led: pg, auth: auth, members: members}
	for _, opt := range opts {
		opt(&s.store)
	}
	return s
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
// money enters.
func (s *store) Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	ref, err := ledger.NewID()
	if err != nil {
		return err
	}
	// An admin grant is money entering from inside, so it has no processor
	// reference of its own. The ledger requires one for a credit, so a generated
	// id carries the idempotency. grantRefPrefix marks it as internal, and the
	// statement mapping keeps it out of ExternalRef so the wire shape is
	// unchanged.
	if err := s.led.Credit(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(amount),
		Reason: reasonTopup,
		Ref:    grantRefPrefix + ref,
		Actor:  byAdmin.Email,
	}); err != nil {
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
	return s.move(ctx, Pot(project), Wallet(by.Email), amount, reasonReclaim, by.Email, project, projectName)
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
