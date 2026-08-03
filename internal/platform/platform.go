// Package platform is the policy this deployment owns and an admin tunes at
// runtime: the platform settings row and the per-person quota top-ups (006k).
//
// It stores policy; it does not enforce it. The session quota is spent inside
// job.Enqueue (006l), which reads its allowance from here through a function
// field. Authority is not re-invented either: every write re-checks the caller's
// tier through an Authority seam satisfied by access.Access, so an admin-only
// write holds even if a route were mounted without the tier guard — the same
// belt-and-braces access itself uses.
//
// Why a table and not an environment variable: 006c rejected an env-var grant
// list because every change costs a redeploy and leaves no record of who made
// it. A number an admin is expected to tune weekly deserves the same treatment.
package platform

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// DefaultSessionQuota is the conservative fallback: the value the first row is
// seeded with, and the value a failed or missing read resolves to. The two are
// the same constant so they cannot drift, and neither is "unlimited" — a quota
// that disappears when Postgres hiccups is not a quota.
//
// It is zero since 006q. Sign-in is open, so this number is no longer a second
// lock behind an admin's guest list — it is the only lock, and a new arrival
// must start with nothing to spend. Budget arrives by an admin top-up or a
// project owner's sponsorship, and an admin can raise the platform-wide default
// at runtime if that is ever the right policy.
const DefaultSessionQuota = 0

// Errors this package returns.
var (
	// ErrNotAdmin reports a settings or top-up write by a non-admin.
	ErrNotAdmin = errors.New("only a platform admin may change platform settings")
	// ErrNoEmail reports a top-up with no email to key on.
	ErrNoEmail = errors.New("a quota top-up needs an email")
	// ErrNegative reports a negative quota, which would mean "owes sessions".
	ErrNegative = errors.New("a quota may not be negative")
)

// Settings is the platform policy row. It is typed, not a JSON bag: adding a
// setting is a migration, which is the review step a platform-wide policy
// change deserves.
type Settings struct {
	// DefaultSessionQuota is how many replication sessions an invited person may
	// hold or have completed (006l counts; this only stores).
	DefaultSessionQuota int       `json:"default_session_quota"`
	UpdatedBy           string    `json:"updated_by,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitzero"`
}

// Topup is one person's extra headroom, keyed by email so an admin can grant it
// before that person has ever signed in — the same seed-by-email rule a grant
// follows (006c).
type Topup struct {
	Email     string    `json:"email"`
	Extra     int       `json:"extra"`
	GrantedBy string    `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at,omitzero"`
}

// Authority answers what a caller may do. Satisfied by access.Access, so this
// package depends on the tier and not on the access store's internals.
type Authority interface {
	Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

// Store is the platform policy store. Reads need no caller — the enforcement
// path (006l) is not an admin — and every write is admin-only.
type Store interface {
	// Settings reads the platform row. It is fail-closed: a missing row or a
	// failing backend yields the conservative default, never unlimited, and the
	// error is returned alongside for logging.
	Settings(ctx context.Context) (Settings, error)
	// SetSettings writes the platform row, recording the admin who did it.
	SetSettings(ctx context.Context, byAdmin access.Principal, s Settings) error
	// SetTopup sets one email's extra headroom; zero removes it. Admin only.
	SetTopup(ctx context.Context, byAdmin access.Principal, email string, extra int) error
	// Topups lists every top-up, for the admin surface. Admin only.
	Topups(ctx context.Context, byAdmin access.Principal) ([]Topup, error)
	// Allowance is the effective cap for one email — default + top-up — and the
	// single place the two compose. A platform admin is unlimited: they own the
	// whole platform budget, so metering them against a number they set
	// themselves is friction with no safety value.
	Allowance(ctx context.Context, p access.Principal) (n int, unlimited bool, err error)
}

// normalizeEmail lowercases and trims, so a top-up seeded one way matches a
// login typed another — the rule access uses for grants.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validate rejects a settings value that cannot mean anything.
func (s Settings) validate() error {
	if s.DefaultSessionQuota < 0 {
		return ErrNegative
	}
	return nil
}
