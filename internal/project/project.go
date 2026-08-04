// Package project is the entity a user owns and works inside: a research
// container that holds one or more replication sessions (006g). A project is a
// first-class, durable record — not a string threaded through a job row — so
// collaboration ([006f]) and the public flip have something to key on, and a
// user's work lists as projects rather than a flat run history.
//
// The paper a session targets lives on the session (the job), never here: a
// project usually gathers replications of one paper (and its title defaults to
// that paper), but the model does not forbid grouping several. The store is an
// interface so the HTTP layer builds against an in-memory fake with no database;
// the Postgres implementation lands against the same contract.
package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Visibility is whether a project is private to its owner and members or open in
// the public zone. Private by default; the public flip is 006f's, and 006o gives
// it an end — an owner may withdraw a publication back to private. Visibility is
// the only question a read path may ask about openness: a withdrawn project still
// carries the sign-off it retracted, so the presence of one proves nothing.
type Visibility string

// The visibilities a project may hold.
const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

// Errors this package returns.
var (
	// ErrNotFound reports a missing project.
	ErrNotFound = errors.New("project not found")
	// ErrNoOwner reports creating a project with no owning principal.
	ErrNoOwner = errors.New("a project needs an owner")
)

// Project is the durable record of one research container.
type Project struct {
	ID         string     `json:"id"`
	Owner      string     `json:"owner"`      // the owning principal (006c)
	Title      string     `json:"title"`      // defaults from the first session's paper
	Visibility Visibility `json:"visibility"` // private by default; the public flip is 006f
	// CreditCap overrides the platform's overdraft floor for this project, in
	// micro-USD; zero means the platform default. It is never on the wire: a
	// published overdraft is an allowance somebody plans around, which is the
	// one thing this number must not become (011).
	CreditCap int64     `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	// The public-flip provenance (006f): who signed the responsibility
	// attestation, when, and which attestation text version. Zero until the
	// owner makes the project public.
	SignedOffBy        string     `json:"signed_off_by,omitempty"`
	SignedOffAt        *time.Time `json:"signed_off_at,omitempty"`
	AttestationVersion string     `json:"attestation_version,omitempty"`
	// The signer, as they were when they signed (006n). A snapshot, not a view of
	// their profile: editing a profile later must never rewrite what an already
	// published attestation claimed. Empty on a v1 sign-off, which predates it.
	SignedOffName        string `json:"signed_off_name,omitempty"`
	SignedOffInstitution string `json:"signed_off_institution,omitempty"`
	SignedOffContact     string `json:"signed_off_contact,omitempty"` // published for inquiries
	SignedOffORCID       string `json:"signed_off_orcid,omitempty"`
	SignedOffScholar     string `json:"signed_off_scholar,omitempty"`
	// SignedOffEmail identifies the signer, stored already redacted
	// (h***n@example.org). It is derived server-side from the session's verified
	// email, never taken from a request body — so a published identity cannot be
	// typed in — and redacted before storage rather than at render, so the full
	// address is never put on the wire.
	SignedOffEmail string `json:"signed_off_email,omitempty"`
	// The retraction (006o), zero until the owner withdraws a publication. It sits
	// beside the sign-off rather than clearing it: withdrawing ends a publication,
	// it does not un-say the attestation, and a project published, withdrawn, and
	// published again under a different signer must leave evidence of all three.
	// Cleared by a later SetPublic, which supersedes it with a fresh sign-off.
	WithdrawnAt     *time.Time `json:"withdrawn_at,omitempty"`
	WithdrawnBy     string     `json:"withdrawn_by,omitempty"`
	WithdrawnReason string     `json:"withdrawn_reason,omitempty"`
}

// Withdrawal is one recorded retraction: who ended the publication and why. A
// struct for the same reason SignOff is one — two adjacent strings are two
// strings a caller can swap.
type Withdrawal struct {
	By     string // the deciding principal (006c)
	Reason string // the owner's own words; the store does not interpret it
}

// SignOff is one recorded responsibility attestation: the deciding principal,
// the version of the text they signed, and the snapshot of who they said they
// were. It travels as a struct because a positional SetPublic with seven
// strings invites a caller to swap two of them.
type SignOff struct {
	By                 string // the deciding principal (006c)
	AttestationVersion string
	Name               string
	Institution        string
	Contact            string
	ORCID              string
	Scholar            string
	Email              string // already redacted; see Project.SignedOffEmail
}

// Store is where projects live. Sessions are not stored here — they are jobs in
// the job store, joined to a project by the handler through job.ListByProject —
// so this store owns only the container, keeping the two relations independent.
type Store interface {
	// Create opens a new project for an owner, its title defaulted from the
	// first paper. A blank owner is refused.
	Create(ctx context.Context, owner, title string) (Project, error)
	// Get returns one project by id, or ErrNotFound.
	Get(ctx context.Context, id string) (Project, error)
	// ListByOwner returns a principal's own projects, newest first.
	ListByOwner(ctx context.Context, owner string) ([]Project, error)
	// SetTitle updates a project's title, so the worker can replace the offline
	// placeholder with the resolved paper title once its Resolve phase has run.
	// A missing project is not an error — the enrichment is best-effort.
	SetTitle(ctx context.Context, id, title string) error
	// SetCreditCap overrides this project's overdraft floor, in micro-USD; zero
	// restores the platform default. Platform-admin policy, never the owner's:
	// a project deciding how far past its own balance it may run is the one
	// party with an interest in that number being large.
	SetCreditCap(ctx context.Context, id string, cap int64) error
	// SetPublic marks a project public and records the responsibility sign-off
	// (006f, 006n): who signed, the attestation text version, the signer snapshot,
	// and the time. Repeatable: publishing a withdrawn project overwrites the
	// snapshot with the new attestation and clears the retraction (006o), so the
	// row never claims to be both live and withdrawn. The caller (the HTTP owner
	// check and the manifest-clear gate) authorises; this store only records.
	SetPublic(ctx context.Context, id string, s SignOff) error
	// SetPrivate withdraws a publication (006o): visibility returns to private and
	// the retraction is recorded, leaving every sign-off field untouched. On a
	// project that is already private it is a success that changes nothing — the
	// timestamp must keep pointing at the moment the publication actually ended,
	// not at the last time somebody asked. The caller authorises (owner-only).
	SetPrivate(ctx context.Context, id string, w Withdrawal) error
	// ListPublic returns every public project, newest first — the public zone's
	// read surface, across all owners.
	ListPublic(ctx context.Context) ([]Project, error)
	// Delete removes a project by id. It is the compensating action when the
	// first session of a just-created project fails to enqueue, so a project is
	// never left empty. Idempotent: deleting a missing project is not an error.
	Delete(ctx context.Context, id string) error
}

// newID mints a random project identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint project id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
