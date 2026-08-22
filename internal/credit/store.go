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

// The ledger itself is latere.ai/x/pay/ledger. What lives here is everything
// that ledger deliberately has no opinion about: who may move money, what a
// move is called on a statement, and this product's two holder namespaces.
//
// The split is the whole reason the ledger could be shared. Its Store takes a
// holder and an amount and checks nothing; the moment it took an
// access.Principal it would belong to this product forever.

// reasons are the product's names for why an entry exists. They are what the
// shared ledger records in Reason, and what this package reconstructs the
// historical Kind from, so the statement a browser reads is byte-identical to
// the one it read before the ledger moved out.
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
// The shared ledger stores labels and never interprets them, which is exactly
// what a snapshot needs: a purged project still reads correctly here.
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
}

// MemStore is the in-memory ledger with this product's permission rules.
type MemStore struct {
	store
	mem *ledger.MemStore
}

// NewMemStore builds an empty in-memory ledger. A nil Authority or Members
// means the check it would perform is skipped, which is what a unit test that
// is not about permissions wants.
func NewMemStore(auth Authority, members Members) *MemStore {
	mem := ledger.NewMemStore()
	return &MemStore{store: store{led: mem, auth: auth, members: members}, mem: mem}
}

// PGStore is the Postgres ledger with this product's permission rules.
type PGStore struct {
	store
	pg     *pgledger.Store
	dbpool *pgxpool.Pool
}

// NewPGStore builds a ledger over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, auth Authority, members Members) *PGStore {
	pg := pgledger.New(pool)
	return &PGStore{store: store{led: pg, auth: auth, members: members}, pg: pg, dbpool: pool}
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

// NegativeWallets returns every wallet in the red, most-owed first. Pots are
// excluded: a project pot may sit negative legitimately, which is not a debt an
// account holder owes.
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

// Topup creates credits in one email's wallet. Platform admin only, which is
// how money exists at all until a payment provider posts the same entry.
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
	// reference of its own. The ledger requires one for a credit, so a minted
	// id carries the idempotency; grantRef marks it as internal, and the
	// statement mapping keeps it out of ExternalRef so the wire shape is what
	// it always was.
	return s.led.Credit(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(amount),
		Reason: reasonTopup,
		Ref:    grantRefPrefix + ref,
		Actor:  byAdmin.Email,
	})
}

// TopupPaid posts a purchased credit into a wallet, idempotent on ref. It has
// no principal to check: authority came from a verified webhook signature,
// upstream.
func (s *store) TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error {
	if err := checkPaid(email, credited, ref); err != nil {
		return err
	}
	return s.led.Credit(ctx, ledger.Posting{
		Holder: ledger.Holder(Wallet(email)),
		Amount: money.Micro(credited),
		Reason: reasonTopup,
		Ref:    ref,
		Actor:  actor,
	})
}

// RefundPaid reverses a purchase by the exact micro-USD it credited, keyed on
// its own reference so it dedupes independently. The wallet may go negative;
// that is the debt a clawed-back purchase leaves.
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
// and it never refuses for want of balance: the tokens were spent before this
// is called, and a project that has just gone under is one that owes rather
// than one that got the work free.
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

// Reclaim moves credits from a project's pot back to the caller's wallet. Leads
// only: the pot is fungible and does not remember who funded it, so reclaiming
// is a lead's authority over the project's money rather than a refund of one's
// own.
func (s *store) Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := s.requireLead(ctx, by, project); err != nil {
		return err
	}
	return s.move(ctx, Pot(project), Wallet(by.Email), amount, reasonReclaim, by.Email, project, projectName)
}

// Fund moves credits from an email's wallet into a project's pot in the request
// that creates that project.
//
// It is separate from Allocate because the authority behind it differs in kind:
// the server has just created the project for this caller, in this transaction,
// and re-deriving their membership from a row it would have to write first is
// circular.
func (s *store) Fund(ctx context.Context, email, project, projectName string, amount int64) error {
	if err := checkMove(email, project, amount); err != nil {
		return err
	}
	return s.move(ctx, Wallet(email), Pot(project), amount, reasonAllocate, email, project, projectName)
}

// Unwind returns everything a project's pot can still commit to an email's
// wallet, as the system: the compensating move for a create that was refused
// after its pot was funded.
//
// It moves the available balance, so credits held by a session that did start
// are never pulled out from under it, and a pot with nothing free is a no-op
// rather than an error, which is what makes it safe on every failure path.
func (s *store) Unwind(ctx context.Context, project, email string) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if strings.TrimSpace(project) == "" {
		return ErrNoProject
	}
	avail, err := s.led.Available(ctx, ledger.Holder(Pot(project)))
	if err != nil {
		return err
	}
	if avail <= 0 {
		return nil
	}
	return s.move(ctx, Pot(project), Wallet(email), int64(avail), reasonReclaim, ActorSystem, project, "")
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

// holdErr maps the shared ledger's refusal onto this package's, so callers keep
// comparing against credit.ErrInsufficient.
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

// grantRefPrefix marks a reference this package minted for an admin grant
// rather than one a processor issued.
const grantRefPrefix = "grant:"

// fromLedger projects a shared ledger entry back into this product's shape.
//
// The historical Kind is reconstructed from Reason, so a statement a browser
// reads is what it always was: the ledger's vocabulary is mechanical (does this
// commit, what sign is it) and this product's is editorial.
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
// this package wrote; anything else falls back to the ledger's own name, which
// is what an entry written by some other writer would honestly read as.
func kindFor(e ledger.Entry) Kind {
	switch e.Reason {
	case reasonTopup:
		return KindTopup
	case reasonAllocate:
		return kindByDirection(e, KindAllocate, KindAllocate)
	case reasonReclaim:
		return kindByDirection(e, KindReclaim, KindReclaim)
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

// kindByDirection exists so a transfer's two sides can be named differently if
// this product ever needs it. Today both sides of an allocate read as
// "allocate", which is what the statement showed before.
func kindByDirection(_ ledger.Entry, out, in Kind) Kind {
	if out != in {
		return out
	}
	return out
}

// requireAdmin refuses a caller who is not a platform admin.
func (s *store) requireAdmin(ctx context.Context, p access.Principal) error {
	if s.auth == nil {
		return nil
	}
	tier, ok, err := s.auth.Resolve(ctx, p)
	if err != nil {
		return fmt.Errorf("resolve admin: %w", err)
	}
	if !ok || tier != access.TierAdmin {
		return ErrNotAdmin
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
