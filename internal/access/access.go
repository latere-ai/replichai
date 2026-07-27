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
//
// There are two grantable tiers, User and PlatformAdmin (006c). Values are
// explicit and stable across this two-tier rework: an invited user stays 1 and
// an admin stays 3, so no grant row is renumbered. Value 2 was the removed
// "trusted" tier — publishing is now authorised by project ownership plus a
// sign-off (006f), not a tier — and a defensive migration folds any stray 2 to
// User.
type Tier uint8

// The tiers, in ascending authority.
const (
	TierNone  Tier = 0 // signed in, not granted
	TierUser  Tier = 1 // invited: own projects, submit, watch, read, invite, publish own
	TierAdmin Tier = 3 // grant, revoke, promote — and all below
)

// String names the tier for logs and API responses.
func (t Tier) String() string {
	switch t {
	case TierUser:
		return "user"
	case TierAdmin:
		return "admin"
	default:
		return "none"
	}
}

// Granted reports whether the tier admits a principal to the operator surface.
func (t Tier) Granted() bool { return t >= TierUser }

// Principal is everything an authorisation decision may consume: the stable
// subject and, for seeding a not-yet-bound grant, the IdP-authenticated email.
// There is deliberately nowhere to put a role, scope, or org claim — reading one
// would weld the beta to a single issuer's schema. The email is auth's assertion
// for the session (not self-asserted), and the operator whitelist is the guard.
type Principal struct {
	Sub   string
	Email string
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

// Role is a principal's role within one project (007). It is orthogonal to Tier:
// a role decides which project's experiments and data a principal may touch, and
// admits nobody to the platform. The zero value is RoleNone.
type Role uint8

// The project roles.
const (
	RoleNone         Role = iota
	RoleCollaborator      // invited into a project
	RoleLead              // owns the project and its membership
)

// String names the role.
func (r Role) String() string {
	switch r {
	case RoleCollaborator:
		return "collaborator"
	case RoleLead:
		return "lead"
	default:
		return "none"
	}
}

// Member is one project-membership row as the owner's members view reads it:
// the seeded email, the role, and whether a principal has bound (logged in) yet.
type Member struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`
	Bound bool   `json:"bound"`
}

// Membership is the second relation, sharing Access's seed-by-email and
// bind-on-first-login rules and none of its tier authority. It never admits a
// principal to the platform — that is a grant's job, resolved independently.
type Membership interface {
	// Add invites an email into a project with a role. The caller must be the
	// project's lead.
	Add(ctx context.Context, byLead Principal, project, email string, r Role) error
	// Remove drops a member by email. The caller must be the project's lead.
	Remove(ctx context.Context, byLead Principal, project, email string) error
	// Role returns a principal's role in a project, binding a seeded membership
	// to it on first login. It consumes a verified email to bind, exactly as a
	// grant does, and refuses a second principal on a bound email.
	Role(ctx context.Context, p Principal, project string) (Role, bool, error)
	// List returns a project's members, for the owner's members view.
	List(ctx context.Context, project string) ([]Member, error)
	// SeedLead creates a project's lead with no lead-check — the bootstrap seed
	// project creation uses, and the bridge the owner-driven members API (006f)
	// uses to seed the project owner as its lead before an owner-authorised
	// add/remove.
	SeedLead(ctx context.Context, project, email string) error
}

// normalizeEmail lowercases and trims, so a seed and a login match regardless of
// how either was typed.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
