// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package platform stores the platform settings row: the deployment-wide
// policy an admin tunes at runtime.
//
// The package stores policy and does not enforce it. The start reserve is
// enforced inside job.Enqueue (011), which reads it from here through a
// function field. Every write re-checks the caller's tier through an
// Authority interface satisfied by access.Access, so an admin-only write
// holds even if a route is mounted without the tier guard. This is the same
// double check that package access uses.
//
// The settings are a table rather than an environment variable because 006c
// rejected an environment-variable grant list: every change costs a redeploy
// and leaves no record of who made it. The same applies to a number an admin
// is expected to tune.
//
// Balances, admin grants and session costs are not stored here. They live in
// internal/credit, because a ledger is a record rather than a policy.
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
// session may start there. It is both the value the settings row is seeded
// with and the value a failed or missing read resolves to, so the two cannot
// drift.
//
// The fallback is deliberately high. A reserve that fell to zero when a
// Postgres read failed would let any project start a session with no funds,
// which is the unbounded spend this model exists to prevent. 006l made the
// same argument for a session count; this applies it to money. The number is
// not calibrated against any particular paper. It is deliberately non-trivial,
// and an admin sets what this deployment can afford.
const DefaultStartReserve = 10 * credit.Dollar

// DefaultOverdraftCap is how far below zero a project's balance may go before
// a run is killed. It is both the value the settings row is seeded with and
// the value a failed or missing read resolves to, so the two cannot drift.
//
// The fallback is deliberately tight, the opposite direction from the
// reserve. A cap that grew when the settings read failed would remove the
// only bound on a runaway session at the moment the platform is least able to
// notice. A cap that stays at the constant kills a run no later than it would
// have. An admin sets the number against what this deployment can afford to
// lose on one replication.
//
// The cap is not published anywhere a user can read (011). A known overdraft
// would be treated as an allowance to spend, and the number exists so that
// nobody plans around it.
const DefaultOverdraftCap = 200 * credit.Dollar

// The purchase-spread defaults (022b). The platform takes its margin at the
// point of purchase, so the rate card stays at vendor rates and a session's
// cost still reconciles against them. The spread is a percentage plus a fixed
// cut, the same shape as a card processor's `2.9% + $0.30`, so a small top-up
// still covers its fixed cost. All three are admin-tunable; these are the seed
// values.
const (
	// DefaultSpreadBps is the percentage cut at purchase, in basis points (5%).
	DefaultSpreadBps = 500
	// DefaultSpreadFixedMicro is the fixed cut per purchase, in micro-USD ($0.30).
	DefaultSpreadFixedMicro = 300_000
	// DefaultMinTopupMicro is the smallest purchase allowed, in micro-USD ($5).
	// Below it the processor's fixed fee is large relative to the amount.
	DefaultMinTopupMicro = 5 * credit.Dollar
)

// Errors this package returns.
var (
	// ErrNotAdmin reports a settings write by a non-admin.
	ErrNotAdmin = errors.New("only a platform admin may change platform settings")
	// ErrNegative reports a negative setting, which cannot mean anything.
	ErrNegative = errors.New("a platform setting may not be negative")
)

// Settings is the platform policy row. It is a typed struct rather than a
// JSON map, so adding a setting requires a migration, which is the review
// step a platform-wide policy change needs.
type Settings struct {
	// StartReserve is how much a project must have available, in micro-USD,
	// before a session may start there. 011 enforces it; this field only
	// stores it.
	StartReserve int64 `json:"start_reserve"`
	// OverdraftCap is how far below zero a project's balance may fall before a
	// run is killed, in micro-USD. It is admin-only: it appears on the admin
	// settings payload and on no surface a project's own members can read.
	OverdraftCap int64 `json:"overdraft_cap"`
	// Rates is what each model's tokens cost. It is stored as policy rather
	// than compiled in because vendor prices change: a compiled-in list makes
	// every price change a redeploy and misprices sessions until then.
	Rates credit.Card `json:"rates"`
	// SpreadBps and SpreadFixedMicro are the purchase spread (022b). A top-up
	// of gross micro-USD credits gross − gross×SpreadBps/10_000 −
	// SpreadFixedMicro. The margin is taken here, at purchase, so the rate
	// card above stays at vendor rates.
	SpreadBps        int64 `json:"spread_bps"`
	SpreadFixedMicro int64 `json:"spread_fixed_micro"`
	// MinTopupMicro is the smallest purchase allowed, in micro-USD. Below it
	// the processor's fixed fee is large relative to the amount. It is public,
	// so the client can refuse a too-small amount before it reaches the
	// processor.
	MinTopupMicro int64 `json:"min_topup_micro"`
	// Draft is the model that writes a project's description on request
	// (039): a protocol, an endpoint and a model id. The credential is not
	// stored here; it belongs to the deployment and is read from its
	// environment. An empty value means this deployment does not draft. The
	// app renders that state in any case, because a project's description can
	// always be typed.
	Draft     draft.Config `json:"draft,omitzero"`
	UpdatedBy string       `json:"updated_by,omitempty"`
	UpdatedAt time.Time    `json:"updated_at,omitzero"`
}

// Credited returns the amount a gross top-up of grossMicro micro-USD adds to
// a wallet after the purchase spread: the percentage cut and the fixed cut,
// both taken at the moment of sale so the rate card stays at vendor rates
// (011/022b). It is computed when the checkout is created and stored on the
// session, so an admin editing the spread while a purchase is in flight
// cannot change what that purchase credits. The result is never negative.
func (s Settings) Credited(grossMicro int64) int64 {
	cut := (grossMicro*s.SpreadBps + 5_000) / 10_000 // round half up
	credited := grossMicro - cut - s.SpreadFixedMicro
	if credited < 0 {
		return 0
	}
	return credited
}

// Authority resolves a caller's tier. access.Access satisfies it, so this
// package depends on the tier and not on the access store's internals.
type Authority interface {
	Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

// Store is the platform policy store. Reads take no caller, because the
// enforcement path (011's gate) is not an admin. Every write is admin-only.
type Store interface {
	// Settings reads the platform row. It fails closed: a missing row or a
	// failing backend yields the conservative default, never a permissive
	// one, and the error is returned with it so the caller can log it.
	Settings(ctx context.Context) (Settings, error)
	// SetSettings writes the platform row, recording the admin who did it.
	SetSettings(ctx context.Context, byAdmin access.Principal, s Settings) error
}

// RateSnapshot adapts a Store to credit.CardSnapshot, the interface through
// which the job store takes its price list at enqueue. A session is therefore
// charged at the rates in force when it started rather than at whatever an
// admin has set by the time it ends.
//
// A failed read yields the compiled-in default rather than an empty card. An
// empty card cannot price anything and would refuse every submit, which is
// the wrong failure mode for a transient database error. The default is the
// published price list, so the worst case is a session charged at the seeded
// rates rather than an admin's edited ones.
func RateSnapshot(s Store) credit.CardSnapshot {
	return func(ctx context.Context) (credit.Card, error) {
		set, err := s.Settings(ctx)
		if err != nil || len(set.Rates.Models) == 0 {
			return credit.DefaultCard(), nil //nolint:nilerr // a failed read falls back to the published price list, as documented above
		}
		return set.Rates, nil
	}
}

// StartReserve adapts a Store to credit.Reserve, the interface through which
// the job store reads its gate threshold, so internal/job never imports this
// package.
//
// A failed read resolves to the constant rather than to zero. A reserve that
// disappears on a transient database error would not bound anything.
func StartReserve(s Store) credit.Reserve {
	return func(ctx context.Context) (int64, error) {
		set, err := s.Settings(ctx)
		if err != nil {
			return DefaultStartReserve, nil //nolint:nilerr // a failed read keeps the reserve in force rather than dropping it to zero
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
	// The price list is written by hand in the console, so it is checked the
	// same way before it replaces the one in force.
	return s.Rates.Validate()
}

// OverdraftCap adapts a Store to the number the spend watchdog compares a
// project's projected balance against. A failed read resolves to the
// constant, so the bound stays in force rather than disappearing.
func OverdraftCap(s Store) func(ctx context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		set, err := s.Settings(ctx)
		if err != nil || set.OverdraftCap <= 0 {
			return DefaultOverdraftCap, nil //nolint:nilerr // the bound stays on a failed read rather than disappearing
		}
		return set.OverdraftCap, nil
	}
}
