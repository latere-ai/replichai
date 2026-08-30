// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package platform is the policy this deployment owns and an admin tunes at
// runtime: the platform settings row.
//
// It stores policy; it does not enforce it. The start reserve is spent inside
// job.Enqueue (011), which reads it from here through a function field.
// Authority is not re-invented either: every write re-checks the caller's tier
// through an Authority seam satisfied by access.Access, so an admin-only write
// holds even if a route were mounted without the tier guard — the same
// belt-and-braces access itself uses.
//
// Why a table and not an environment variable: 006c rejected an env-var grant
// list because every change costs a redeploy and leaves no record of who made
// it. A number an admin is expected to tune deserves the same treatment.
//
// Money itself is not here. Balances, what an admin grants and what a session
// cost live in internal/credit, because a ledger is a record rather than a
// policy.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
)

// DefaultStartReserve is how much a project must have available before a
// session may start there: the value the settings row is seeded with, and the
// value a failed or missing read resolves to. The two are the same constant so
// they cannot drift.
//
// It fails **high** on purpose. A reserve that collapsed to zero when Postgres
// hiccuped would let any project start a session on an empty pot, which is the
// unbounded spend this model exists to prevent — the direction 006l argued for
// a count, applied to money. The number itself is not calibrated against any
// particular paper: it is deliberately non-trivial, and an admin sets what this
// deployment can actually afford.
const DefaultStartReserve = 10 * credit.Dollar

// DefaultOverdraftCap is how far below zero a project's balance may go before
// a run is killed: the value the settings row is seeded with, and the value a
// failed or missing read resolves to. One constant, so the two cannot drift.
//
// It fails **tight**, the opposite direction from the reserve. A cap that grew
// when the settings read failed would remove the one bound on a runaway
// session at exactly the moment the platform is least able to notice; a cap
// that stays put kills a run no later than it would have. The number is an
// admin's to set against what this deployment can afford to lose on one
// replication.
//
// It is not published anywhere a user can read (011). A known overdraft is an
// allowance to be spent, and the whole point of this number is that nobody
// plans around it.
const DefaultOverdraftCap = 200 * credit.Dollar

// The purchase-spread defaults (022b). The spread is where the platform takes
// its margin — at the till, so the rate card stays raw and a session's cost
// still reconciles against vendor rates. The shape is a percentage plus a fixed
// cut, mirroring a card processor's own `2.9% + $0.30`, so a small top-up still
// covers its fixed cost. All admin-tunable; these are only the seed.
const (
	// DefaultSpreadBps is the percentage cut at purchase, in basis points (5%).
	DefaultSpreadBps = 500
	// DefaultSpreadFixedMicro is the fixed cut per purchase, in micro-USD ($0.30).
	DefaultSpreadFixedMicro = 300_000
	// DefaultMinTopupMicro is the smallest purchase allowed, in micro-USD ($5):
	// below it the processor's fixed fee dwarfs the amount.
	DefaultMinTopupMicro = 5 * credit.Dollar
)

// Errors this package returns.
var (
	// ErrNotAdmin reports a settings write by a non-admin.
	ErrNotAdmin = errors.New("only a platform admin may change platform settings")
	// ErrNegative reports a negative setting, which cannot mean anything.
	ErrNegative = errors.New("a platform setting may not be negative")
)

// Settings is the platform policy row. It is typed, not a JSON bag: adding a
// setting is a migration, which is the review step a platform-wide policy
// change deserves.
type Settings struct {
	// StartReserve is how much a project must have available, in micro-USD,
	// before a session may start there (011 enforces; this only stores).
	StartReserve int64 `json:"start_reserve"`
	// OverdraftCap is how far below zero a project's balance may fall before a
	// run is killed, in micro-USD. It is admin-only: it appears on the admin
	// settings payload and on no surface a project's own members can read.
	OverdraftCap int64 `json:"overdraft_cap"`
	// Rates is what each model's tokens cost. It is policy rather than a
	// constant because a vendor's prices move: a compiled-in list makes every
	// price change a redeploy, and a silent mispricing until then.
	Rates credit.Card `json:"rates"`
	// SpreadBps and SpreadFixedMicro are the purchase spread (022b): a top-up of
	// gross micro-USD credits gross − gross×SpreadBps/10_000 − SpreadFixedMicro.
	// The margin lives here, at the till, so the rate card above stays raw.
	SpreadBps        int64 `json:"spread_bps"`
	SpreadFixedMicro int64 `json:"spread_fixed_micro"`
	// MinTopupMicro is the smallest purchase allowed, in micro-USD, below which
	// the processor's fixed fee dwarfs the amount. It is public, so the client
	// can refuse a too-small amount before it ever reaches the processor.
	MinTopupMicro int64 `json:"min_topup_micro"`
	// Draft is which model writes a project's description when somebody asks for
	// one (039) — a protocol, an endpoint and a model id. The credential is not
	// here and never will be: it is the deployment's, read from its environment.
	// Empty means this deployment does not draft, which is a state the app has
	// to render anyway (a project's description is typed).
	Draft     draft.Config `json:"draft,omitzero"`
	UpdatedBy string       `json:"updated_by,omitempty"`
	UpdatedAt time.Time    `json:"updated_at,omitzero"`
}

// Credited is what a gross top-up of grossMicro micro-USD lands in a wallet
// after the purchase spread — the percentage cut and the fixed cut, both taken
// at the moment of sale so the rate card stays raw (011/022b). Computed at
// checkout create and snapshotted onto the session, so an admin editing the
// spread mid-flight cannot change what an in-flight purchase credits. Never
// negative.
func (s Settings) Credited(grossMicro int64) int64 {
	cut := (grossMicro*s.SpreadBps + 5_000) / 10_000 // round half up
	credited := grossMicro - cut - s.SpreadFixedMicro
	if credited < 0 {
		return 0
	}
	return credited
}

// Authority answers what a caller may do. Satisfied by access.Access, so this
// package depends on the tier and not on the access store's internals.
type Authority interface {
	Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

// Store is the platform policy store. Reads need no caller — the enforcement
// path (011's gate) is not an admin — and every write is admin-only.
type Store interface {
	// Settings reads the platform row. It is fail-closed: a missing row or a
	// failing backend yields the conservative default, never a permissive one,
	// and the error is returned alongside so the caller can log it.
	Settings(ctx context.Context) (Settings, error)
	// SetSettings writes the platform row, recording the admin who did it.
	SetSettings(ctx context.Context, byAdmin access.Principal, s Settings) error
}

// RateSnapshot adapts a Store to credit.CardSnapshot — the seam the job store
// takes its price list through at enqueue, so a session is charged at the rates
// that were in force when it started rather than whatever an admin has set by
// the time it ends.
//
// A failed read yields the compiled-in default rather than an empty card: an
// empty card cannot price anything and would refuse every submit, which is
// fail-closed in the wrong direction for a database hiccup. The default is the
// published price list, so the worst case is being charged the seeded rates
// rather than an admin's edited ones.
func RateSnapshot(s Store) credit.CardSnapshot {
	return func(ctx context.Context) (credit.Card, error) {
		set, err := s.Settings(ctx)
		if err != nil || len(set.Rates.Models) == 0 {
			return credit.DefaultCard(), nil //nolint:nilerr // a database hiccup falls back to the published price list, as documented above
		}
		return set.Rates, nil
	}
}

// StartReserve adapts a Store to credit.Reserve — the seam the job store reads
// its gate threshold through, so internal/job never imports this package.
//
// A failed read resolves to the constant rather than to nothing: a reserve that
// disappears when the database hiccups is not a reserve.
func StartReserve(s Store) credit.Reserve {
	return func(ctx context.Context) (int64, error) {
		set, err := s.Settings(ctx)
		if err != nil {
			return DefaultStartReserve, nil //nolint:nilerr // a reserve that disappears when the database hiccups is not a reserve
		}
		return set.StartReserve, nil
	}
}

// validate rejects a settings value that cannot mean anything.
func (s Settings) validate() error {
	if s.StartReserve < 0 || s.OverdraftCap < 0 ||
		s.SpreadBps < 0 || s.SpreadFixedMicro < 0 || s.MinTopupMicro < 0 {
		return ErrNegative
	}
	return nil
}

// OverdraftCap adapts a Store to the number the spend watchdog compares a
// project's projected balance against. A failed read resolves to the constant,
// which is the tight direction: the bound stays rather than disappearing.
func OverdraftCap(s Store) func(ctx context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		set, err := s.Settings(ctx)
		if err != nil || set.OverdraftCap <= 0 {
			return DefaultOverdraftCap, nil //nolint:nilerr // the bound stays on a failed read rather than disappearing
		}
		return set.OverdraftCap, nil
	}
}
