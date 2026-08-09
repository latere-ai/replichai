// Package credit is the ledger: what money this platform holds, whose it is,
// and what happened to it (011).
//
// Every balance in the product is a fold over one append-only table. There is
// no stored balance anywhere, on the same rule 006l applied to the session
// count it replaces — a materialised total is a second source of truth, and it
// drifts the first time a row is corrected. Folding also means the answer to
// "why does this holder have this much?" is the rows themselves, each naming
// its actor and its cause.
//
// Money is at two levels. A person's *wallet* holds credits they were given; a
// project's *pot* holds credits allocated to it. A replication session charges
// the pot of the project it belongs to, never a wallet — which is why this
// package has no notion of a session's payer, and why sponsorship does not
// exist: a project holding money is the standing consent that sponsorship was
// trying to express.
package credit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// Amounts are micro-USD: int64 millionths of a dollar, never a float. Model
// prices are quoted per million tokens, so a rate multiplies into this unit
// without rounding at the step where rounding would compound, and a payment
// provider charges in minor units of the same currency. Formatting to "$1.23"
// is a display concern and lives in the client.
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
	// ErrNotPositive reports a move of zero or less. A negative allocation would
	// be a reclaim without its permission check, which is the whole point of
	// having two operations.
	ErrNotPositive = errors.New("an amount must be positive")
	// ErrInsufficient reports a move larger than what the source holder has
	// available. It is a refusal, not a failure.
	ErrInsufficient = errors.New("not enough credits")
	// ErrNoRates reports pricing attempted with no rate card. It is fail-closed:
	// tokens that cannot be priced are not free, so nothing may run.
	ErrNoRates = errors.New("no rate card: this deployment cannot price a session")
)

// Reserve reports how much a project must have available before a session may
// start there, in micro-USD. It is injected rather than read, so the enforcement
// point does not import the policy store that holds the number (011, after
// 006l's Allowance). A nil Reserve means unmetered — what an offline run and
// every test that is not about money want.
type Reserve func(ctx context.Context) (int64, error)

// CardSnapshot resolves the price list a session starting now will be charged
// at. Injected for the same reason Reserve is: the enforcement point does not
// import the policy store. A nil CardSnapshot means unpriced — the gate still
// holds the reserve, and settlement releases it and charges nothing, which is
// what a test about the gate rather than about money wants.
type CardSnapshot func(ctx context.Context) (Card, error)

// Holder is who an entry is against: a person's wallet or a project's pot. It
// is one namespaced string rather than two nullable columns so a balance is one
// predicate, and so a level added later does not reshape the table.
type Holder string

// Wallet is one person's holder key. It is keyed by email, not by principal, so
// an admin can fund somebody before they have ever signed in — the seed-by-
// email rule a grant follows (006c).
func Wallet(email string) Holder { return Holder("user:" + normalizeEmail(email)) }

// Pot is one project's holder key.
func Pot(projectID string) Holder { return Holder("project:" + strings.TrimSpace(projectID)) }

// Kind is what an entry records.
type Kind string

// The kinds of entry. Their signs are not free: a kind that takes money away
// is always negative, so a balance is a sum and never a case analysis.
const (
	// KindTopup creates credits in a wallet. Only an admin, and later a payment.
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
	// KindCharge spends a pot's credits for a finished session (negative). At
	// most one per session, which is what makes settlement exactly-once.
	KindCharge Kind = "charge"
	// KindAdjust corrects a charge after the fact (either sign) — a session that
	// settled from last-observed usage while its backend kept burning, found and
	// reconciled afterwards. Deliberately not unique per session.
	KindAdjust Kind = "adjust"
)

// commits reports whether a kind moves money out of what a holder may still
// commit without having spent it yet. Holds and their releases are exactly
// that: an available balance counts them, a settled balance does not.
func (k Kind) commits() bool { return k == KindHold || k == KindRelease }

// Entry is one line of the ledger. Nothing here is ever updated or deleted; a
// mistake is corrected by another entry, which is what keeps the history and
// the balance the same fact.
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
	// credits to or from; empty on every other kind. ProjectName is a snapshot
	// taken at the moment of the move, not a reference — it is what lets a
	// purged project still read in the history, the same reason the ledger
	// keeps holder and job_id as opaque strings it will never resolve (024b).
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	// Actor is who caused this entry — the admin who granted, the member who
	// allocated, or the system, when settlement did.
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

// Actor for entries the system writes rather than a person: a hold taken at
// enqueue, and the release and charge that settle it.
const ActorSystem = "system"

// Authority answers what a caller may do platform-wide. Satisfied by
// access.Access, so this package depends on the tier and not on the access
// store's internals — the seam platform already uses.
type Authority interface {
	Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

// Members answers what a caller may do in one project. Satisfied by
// access.Membership.
type Members interface {
	Role(ctx context.Context, p access.Principal, project string) (access.Role, bool, error)
}

// Store is the ledger. Reads take no caller: a balance is shown by surfaces
// that have already decided who may look, and the enforcement path (the enqueue
// gate) is nobody. Every write names its actor and checks their authority here,
// so a route mounted without a guard still cannot move money.
type Store interface {
	// Balance is what a holder has: everything received less everything spent.
	// It excludes holds — a hold has not been spent, and a project whose only
	// commitment is a running session has not yet paid for it.
	Balance(ctx context.Context, h Holder) (int64, error)
	// Available is Balance less every open hold: what a holder may still commit.
	// This is the number the enqueue gate compares against the start reserve,
	// and the number a reclaim may not exceed — credits already committed to a
	// running session cannot be pulled out from under it.
	Available(ctx context.Context, h Holder) (int64, error)
	// Entries lists a holder's ledger, newest first, capped at limit (zero means
	// a sensible default). It is the statement view: why the balance is this.
	Entries(ctx context.Context, h Holder, limit int) ([]Entry, error)
	// Topup creates credits in one email's wallet. Platform admin only, which is
	// how money exists at all until a payment provider posts the same entry.
	Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error
	// Allocate moves credits from the caller's wallet into a project's pot. Any
	// member of the project may fund it. Both sides are one transaction: money
	// is never in neither place. projectName is snapshotted onto both entries so
	// the statement can name the project even after it is purged (024b).
	Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error
	// Reclaim moves credits from a project's pot back to the caller's wallet.
	// Leads only: the pot is fungible and does not remember who funded it, so
	// reclaiming is a lead's authority over the project's money rather than a
	// refund of one's own. An invitation to collaborate is not the ability to
	// empty the funding. projectName is snapshotted onto both entries (024b).
	Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error

	creationOps
}

// defaultEntryLimit caps a statement view that asked for no limit.
const defaultEntryLimit = 200

// normalizeEmail lowercases and trims, so a wallet funded one way matches a
// login typed another — the rule access and platform both follow.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// newID mints a ledger entry identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint credit entry id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// checkMove validates the shape of a transfer before any authority or balance
// is consulted, so both stores refuse the same nonsense the same way.
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

// creationOps are the moves the *creation of a project* needs, and they are
// separate from Allocate and Reclaim because the authority behind them is
// different in kind.
//
// Allocate asks "is this caller a member of this project?" — the right question
// for funding a project that already exists. It is the wrong question in the
// request that creates one: the server has just created the project for this
// caller, in this transaction, and re-deriving their authority from a
// membership row it would have to write first is circular. Worse, writing that
// row first leaves it orphaned when the create is refused and the project is
// deleted again.
//
// So the create path funds with Fund, seeds membership only once the create has
// actually succeeded, and unwinds with Unwind when it has not. Neither is a way
// to move somebody else's money: the caller of Fund is the person whose wallet
// it debits, and Unwind only ever moves a pot back to the wallet that filled it
// on a project that is being deleted in the same request.
type creationOps interface {
	// Fund moves credits from an email's wallet into a project's pot in the
	// request that creates that project. Insufficient funds is ErrInsufficient,
	// exactly as for Allocate. projectName is snapshotted like Allocate's (024b).
	Fund(ctx context.Context, email, project, projectName string, amount int64) error
	// Unwind returns everything a project's pot can still commit to an email's
	// wallet, as the system — the compensating move for a create that was
	// refused after its pot was funded. Without it the allocation would sit in
	// a deleted project forever: the ledger has no foreign key to clean up
	// after, and Reclaim needs a role in a project that no longer exists.
	//
	// It moves the *available* balance, so credits held by a session that did
	// start are never pulled out from under it. A pot with nothing free is a
	// no-op rather than an error, which is what makes it safe to call on every
	// failure path.
	Unwind(ctx context.Context, project, email string) error
}
