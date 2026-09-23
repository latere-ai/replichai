// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package credit is the ledger: the money this platform holds, who holds it,
// and what happened to it (011).
//
// Every balance in the product is a sum over one append-only table. No balance
// is stored anywhere, following the rule 006l applied to the session count it
// replaces: a materialized total is a second source of truth, and it drifts
// the first time a row is corrected. Summing also means the rows themselves,
// each naming its actor and its cause, explain why a holder has the balance
// it has.
//
// Money is held at two levels. A person's wallet holds credits they were given;
// a project's pot holds credits allocated to it. A replication session charges
// the pot of the project it belongs to, never a wallet. This package therefore
// has no notion of a session's payer, and sponsorship does not exist: a project
// holding money is the standing consent that sponsorship was meant to express.
package credit

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// Amounts are micro-USD: int64 millionths of a dollar, never a float. Model
// prices are quoted per million tokens, so a rate multiplies into this unit
// without rounding at the step where rounding would compound, and a payment
// provider charges in minor units of the same currency. Formatting to "$1.23"
// is a display concern and is done in the client.
const (
	// Micro is one micro-USD, the unit every amount in this package is in.
	Micro = 1
	// Dollar is one US dollar in micro-USD, for readable constants and tests.
	Dollar = 1_000_000 * Micro
)

// Errors this package returns.
var (
	// ErrNotAdmin reports a top-up by someone who is not a platform admin.
	ErrNotAdmin = errors.New("only a platform admin may create credits")
	// ErrNotMember reports an allocation into a project the caller is not in.
	ErrNotMember = errors.New("only a member of this project may fund it")
	// ErrNotLead reports a reclaim by a member who is not a project lead.
	ErrNotLead = errors.New("only a lead of this project may reclaim its credits")
	// ErrNoEmail reports a wallet operation with no email to key on.
	ErrNoEmail = errors.New("a wallet needs an email")
	// ErrNoProject reports a pot operation with no project to key on.
	ErrNoProject = errors.New("a project pot needs a project")
	// ErrNoRef reports a paid entry posted with no external reference. A purchase
	// or refund must be idempotent, and the reference deduplicates it, so a
	// missing one is a programming error rather than a refusal.
	ErrNoRef = errors.New("a paid entry needs an external reference")
	// ErrNotPositive reports a move of zero or less. A negative allocation would
	// be a reclaim without its permission check, which is the reason there are
	// two operations.
	ErrNotPositive = errors.New("an amount must be positive")
	// ErrInsufficient reports a move larger than what the source holder has
	// available. It is a refusal, not a failure.
	ErrInsufficient = errors.New("not enough credits")
	// ErrNoRates reports pricing attempted with no rate card. It is fail-closed:
	// tokens that cannot be priced are not free, so nothing may run.
	ErrNoRates = errors.New("no rate card: this deployment cannot price a session")
)

// Reserve reports how much a project must have available before a session may
// start there, in micro-USD. It is injected rather than read, so the
// enforcement point does not import the policy store that holds the number
// (011, following 006l's Allowance). A nil Reserve means unmetered, which is
// the mode for an offline run and every test that is not about money.
type Reserve func(ctx context.Context) (int64, error)

// CardSnapshot resolves the price list a session starting now will be charged
// at. It is injected for the same reason Reserve is: the enforcement point does
// not import the policy store. A nil CardSnapshot means unpriced: the gate
// still holds the reserve, and settlement releases it and charges nothing,
// which is the mode for a test about the gate rather than about money.
type CardSnapshot func(ctx context.Context) (Card, error)

// Holder identifies the account an entry is against: a person's wallet or a
// project's pot. It is one namespaced string rather than two nullable columns
// so that a balance is one predicate, and so that a level added later does not
// change the table's shape.
type Holder string

// Wallet returns one person's holder key. It is keyed by email, not by
// principal, so an admin can fund somebody before they have signed in, which
// is the seed-by-email rule a grant follows (006c).
func Wallet(email string) Holder { return Holder("user:" + normalizeEmail(email)) }

// WalletBalance is one wallet's email and its settled balance in micro-USD. It
// is what NegativeWallets returns, so the admin surface names the debtor and
// shows what they owe without a second read.
type WalletBalance struct {
	Email   string
	Balance int64
}

// Pot is one project's holder key.
func Pot(projectID string) Holder { return Holder("project:" + strings.TrimSpace(projectID)) }

// valid reports a well-formed holder key: a namespace and an id.
func (h Holder) valid() bool {
	i := strings.IndexByte(string(h), ':')
	return i > 0 && i < len(string(h))-1
}

// walletNamespace is the namespace a wallet holder carries, without its
// separator, for the shared ledger's namespace filters.
const walletNamespace = "user"

// walletPrefix is the namespace a wallet holder carries.
const walletPrefix = "user:"

// WalletEmail is the inverse of Wallet: it returns the email a wallet holder is
// keyed by, and whether h is a wallet at all, which a pot is not. A refund
// reversing a purchase has the entry and needs the person to post the reversal
// against (022c).
func WalletEmail(h Holder) (string, bool) {
	s := string(h)
	if !strings.HasPrefix(s, walletPrefix) {
		return "", false
	}
	return strings.TrimPrefix(s, walletPrefix), true
}

// Kind is the type of event an entry records.
type Kind string

// The kinds of entry. Their signs are fixed: a kind that takes money away is
// always negative, so a balance is a sum and never a case analysis.
const (
	// KindTopup creates credits in a wallet. Only an admin or a payment may do so.
	KindTopup Kind = "topup"
	// KindAllocate is the wallet side of a move into a project pot (negative),
	// paired with the pot side of the same move (positive).
	KindAllocate Kind = "allocate"
	// KindReclaim is the pot side of a move back to a wallet (negative), paired
	// with the wallet side (positive).
	KindReclaim Kind = "reclaim"
	// KindHold commits a pot's credits to a session about to start (negative).
	// It does not spend them: it makes them unavailable to a second session, so
	// a burst of submits cannot each be admitted against the same balance.
	KindHold Kind = "hold"
	// KindRelease returns a hold (positive) at settlement, paired with the
	// charge for what the session actually cost.
	KindRelease Kind = "release"
	// KindCharge spends a pot's credits for a finished session (negative). There
	// is at most one per session, which makes settlement exactly-once.
	KindCharge Kind = "charge"
	// KindDraft spends a pot's credits on a model call that is not a session
	// (039): drafting a project's description. It is a charge with no job, made
	// exactly-once by its own reference rather than by a session id, so a
	// retried request bills once and the project's statement accounts for every
	// token the platform spent on it.
	KindDraft Kind = "draft"
	// KindAdjust corrects a charge after the fact, with either sign, for example
	// for a session that settled from last-observed usage while its backend
	// kept spending and was reconciled afterwards. It is deliberately not
	// unique per session.
	KindAdjust Kind = "adjust"
)

// Entry is one line of the ledger. An entry is never updated or deleted; a
// mistake is corrected by another entry, which keeps the history and the
// balance consistent.
type Entry struct {
	ID     string `json:"id"`
	Holder Holder `json:"holder"`
	// Amount is signed micro-USD: positive adds to the holder, negative takes.
	Amount int64 `json:"amount"`
	Kind   Kind  `json:"kind"`
	// JobID is the session an entry is about, on hold, release, charge and
	// adjust; empty on the others.
	JobID string `json:"job_id,omitempty"`
	// ProjectID and ProjectName name the project an allocate or reclaim moved
	// credits to or from, and are empty on every other kind. ProjectName is a
	// snapshot taken at the moment of the move, not a reference, so a purged
	// project is still readable in the history. For the same reason the ledger
	// keeps holder and job_id as opaque strings it never resolves (024b).
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	// Actor is who caused this entry: the admin who granted, the member who
	// allocated, or the system, when settlement did.
	Actor string `json:"actor"`
	// ExternalRef is a payment provider's reference for an entry through which
	// money entered or left: a Stripe payment_intent on a purchased top-up, or a
	// refund or dispute id on a reversed one (022a). It is empty on every
	// internal move. It carries the ledger's idempotency: a unique index refuses
	// a second row for the same reference, so a retried webhook cannot double a
	// balance.
	ExternalRef string    `json:"external_ref,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ActorSystem is the actor recorded on entries the system writes rather than a
// person: a hold taken at enqueue, and the release and charge that settle it.
const ActorSystem = "system"

// Authority reports what a caller may do platform-wide. access.Access
// satisfies it, so this package depends on the tier and not on the access
// store's internals, which is the same interface platform uses.
type Authority interface {
	Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

// Members reports what a caller may do in one project. access.Membership
// satisfies it.
type Members interface {
	Role(ctx context.Context, p access.Principal, project string) (access.Role, bool, error)
}

// Store is the ledger. Reads take no caller: a balance is shown by surfaces
// that have already decided who may look, and the enforcement path, the
// enqueue gate, has no caller. Every write names its actor and checks their
// authority here, so a route mounted without a guard still cannot move money.
type Store interface {
	// Balance is what a holder has: everything received less everything spent.
	// It excludes holds, because a hold has not been spent, and a project whose
	// only commitment is a running session has not yet paid for it.
	Balance(ctx context.Context, h Holder) (int64, error)
	// Available is Balance less every open hold: what a holder may still commit.
	// This is the number the enqueue gate compares against the start reserve,
	// and the number a reclaim may not exceed, because credits already committed
	// to a running session cannot be withdrawn from it.
	Available(ctx context.Context, h Holder) (int64, error)
	// Entries lists a holder's ledger, newest first, capped at limit; zero means
	// the default limit. It is the statement view that explains the balance.
	Entries(ctx context.Context, h Holder, limit int) ([]Entry, error)
	// Topup creates credits in one email's wallet. Only a platform admin may
	// call it; until a payment provider posts the same entry, this is the only
	// way money enters.
	Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error
	// TopupPaid posts a purchased credit into a wallet, idempotent on ref. It has
	// no principal to check: authority came from a verified webhook signature
	// upstream (022a/022b). actor records the payment, e.g. "stripe:pi_123", so
	// the statement distinguishes a purchase from an admin grant. A second call
	// with the same ref is a no-op, so a retried webhook credits exactly once.
	TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error
	// Spend charges a pot for work that is not a session; today that is one
	// model call that drafts a project's description (039). It is idempotent on
	// ref, so a retried request bills once, and it never refuses for lack of
	// balance: the tokens were spent before this is called, and a project that
	// has just gone negative owes the amount rather than getting the work free.
	// Whether the work may be paid for is checked before the work, not here.
	Spend(ctx context.Context, pot Holder, amount int64, ref, actor string) error
	// RefundPaid reverses a purchase with a negative adjust of amount, which is
	// a positive magnitude. It is idempotent on its own ref, the refund or
	// dispute id, which is distinct from the purchase's, so it deduplicates
	// independently. The wallet may go negative; that is the debt a reversed
	// purchase leaves (022a/022c).
	RefundPaid(ctx context.Context, email string, amount int64, ref, actor string) error
	// EntryByExternalRef finds the entry carrying ref, if any. A refund uses it to
	// reverse a purchase by the exact micro-USD it credited (022c).
	EntryByExternalRef(ctx context.Context, ref string) (Entry, bool, error)
	// TotalOutstanding is every credit the platform has sold and not yet
	// delivered as compute: the sum over every holder, wallets and pots, less
	// open holds. It is the solvency number an operator compares against what
	// the vendor account holds (022c), and it replaces coupling a customer
	// payment to a vendor top-up.
	TotalOutstanding(ctx context.Context) (int64, error)
	// BalancesFor sums the settled balance of every given holder in one pass. It
	// is the read the admin people table needs for a page of accounts without a
	// query per row (026). It excludes holds exactly as Balance does, so the two
	// cannot disagree. A holder with no ledger entry is omitted rather than
	// reported as zero; its balance is zero and the caller defaults a missing
	// key to zero.
	BalancesFor(ctx context.Context, holders []Holder) (map[Holder]int64, error)
	// NegativeWallets returns every wallet whose settled balance is below zero,
	// which are the accounts that owe and are frozen (022f), using one predicate
	// rather than a sum over every account. Pots are excluded: a project pot may
	// legitimately be negative under 011's overdraft, which is not a debt an
	// account holder owes. The result is ordered most-owed first, so the
	// operator sees the largest debt at the top.
	NegativeWallets(ctx context.Context) ([]WalletBalance, error)
	// Allocate moves credits from the caller's wallet into a project's pot. Any
	// member of the project may fund it. Both sides are one transaction, so
	// money is always in exactly one place. projectName is snapshotted onto
	// both entries so the statement can name the project even after it is
	// purged (024b).
	Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error
	// Reclaim moves credits from a project's pot back to the caller's wallet.
	// Only a lead may call it: the pot is fungible and does not record who
	// funded it, so reclaiming is a lead's authority over the project's money
	// rather than a refund of the caller's own. A member invited to collaborate
	// must not be able to empty the funding. projectName is snapshotted onto
	// both entries (024b).
	Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error
}

// normalizeEmail lowercases and trims, so a wallet funded with one spelling
// matches a login typed with another. access and platform follow the same
// rule.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// checkMove validates the shape of a transfer before any authority or balance
// is consulted, so both stores refuse the same invalid input the same way.
func checkMove(email, project string, amount int64) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if strings.TrimSpace(project) == "" {
		return ErrNoProject
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	return nil
}

// checkPaid validates a payment-referenced entry before it is posted, so both
// stores refuse the same invalid input the same way. amount is a positive
// magnitude, which a refund negates, and the reference makes the post
// idempotent, so an empty one is a programming error rather than a refusal.
func checkPaid(email string, amount int64, ref string) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	if strings.TrimSpace(ref) == "" {
		return ErrNoRef
	}
	return nil
}
