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
	StartReserve int64     `json:"start_reserve"`
	UpdatedBy    string    `json:"updated_by,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
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

// StartReserve adapts a Store to credit.Reserve — the seam the job store reads
// its gate threshold through, so internal/job never imports this package.
//
// A failed read resolves to the constant rather than to nothing: a reserve that
// disappears when the database hiccups is not a reserve.
func StartReserve(s Store) credit.Reserve {
	return func(ctx context.Context) (int64, error) {
		set, err := s.Settings(ctx)
		if err != nil {
			return DefaultStartReserve, nil
		}
		return set.StartReserve, nil
	}
}

// validate rejects a settings value that cannot mean anything.
func (s Settings) validate() error {
	if s.StartReserve < 0 {
		return ErrNegative
	}
	return nil
}
