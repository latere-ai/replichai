// Package access is the deployment's own authorisation: who may sign in, who may
// operate, and at what tier. Authentication is portable — an OIDC provider
// proves who someone is — but authorisation is ours, decided against the
// access_grants table this package owns and nothing read out of a token (006c).
//
// The table is the whole model. An empty table admits nobody: the gate is
// fail-closed by construction, the same shape as the redistribution gate. A
// grant seeds by verified email, binds one-way to a principal on first login,
// and is never satisfied by a claim the identity provider issued.
package access

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Tier is what a principal may do on the platform. The zero value is TierNone —
// not granted — so an unresolved principal is never mistaken for an operator.
type Tier uint8

// The tiers, in ascending authority.
const (
	TierNone    Tier = iota // signed in, not granted
	TierSeed                // submit, watch, read; claim if a forge account is linked
	TierTrusted             // all of seed, and flip a reproduction public
	TierAdmin               // grant, revoke, promote, and all below
)

// String names the tier for logs and API responses.
func (t Tier) String() string {
	switch t {
	case TierSeed:
		return "seed"
	case TierTrusted:
		return "trusted"
	case TierAdmin:
		return "admin"
	default:
		return "none"
	}
}

// Granted reports whether the tier admits a principal to the operator surface.
func (t Tier) Granted() bool { return t >= TierSeed }

// Principal is everything an authorisation decision may consume: the stable
// subject and, only for seeding a not-yet-bound grant, a verified email. There
// is deliberately nowhere to put a role, scope, or org claim — reading one would
// weld the beta to a single issuer's schema.
type Principal struct {
	Sub           string
	Email         string
	EmailVerified bool
}

// Errors this package returns.
var (
	// ErrNotAdmin reports a grant, revoke, or promote by a non-admin.
	ErrNotAdmin = errors.New("only a platform admin may grant, revoke, or promote")
	// ErrNotLead reports a membership change by someone who is not the project's lead.
	ErrNotLead = errors.New("only a project lead may change its membership")
	// ErrNoEmail reports a grant seeded with no email.
	ErrNoEmail = errors.New("a grant needs an email to seed against")
)

// Grant is one row of the ACL: an email it was seeded against, the principal it
// bound to on first login (empty until then), and the tier. No name, no
// avatar — display data is read from the IDP per request, never stored here.
type Grant struct {
	Email     string
	Principal string // bound one-way on first login; empty until then
	Tier      Tier
	GrantedBy string
	GrantedAt time.Time
}

// Access is the grant side of authorisation. Resolve binds on first login and is
// the only place a principal is learned; everything else an admin drives.
type Access interface {
	// Resolve returns a principal's tier, binding a seeded grant to it on first
	// login. granted is false for an ungranted principal and for a second
	// principal presenting an email already bound to someone else.
	Resolve(ctx context.Context, p Principal) (tier Tier, granted bool, err error)
	// Grant seeds or promotes a grant by email. Admin only.
	Grant(ctx context.Context, byAdmin Principal, email string, tier Tier) error
	// Revoke removes a grant by email, locking its holder out on the next
	// request. Admin only; idempotent.
	Revoke(ctx context.Context, byAdmin Principal, email string) error
	// List returns every grant, for the admin surface. Admin only.
	List(ctx context.Context, byAdmin Principal) ([]Grant, error)
	// LinkedAccount returns a principal's proven forge login, if any. Written
	// only by the link callback (006c's forge link), read by 006f's invitation.
	LinkedAccount(ctx context.Context, principalSub, forge string) (login string, ok bool, err error)
	// LinkAccount records a proven forge login against a principal. The account
	// is proven by an OAuth handshake, never taken from a request body.
	LinkAccount(ctx context.Context, principalSub, forge, login string) error
}

// normalizeEmail lowercases and trims, so a seed and a login match regardless of
// how either was typed.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
