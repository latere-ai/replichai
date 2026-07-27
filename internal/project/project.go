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
// the public zone. Private by default; the one-way public flip is 006f's.
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
	CreatedAt  time.Time  `json:"created_at"`
	// The public-flip provenance (006f): who signed the responsibility
	// attestation, when, and which attestation text version. Zero until the
	// owner makes the project public.
	SignedOffBy        string     `json:"signed_off_by,omitempty"`
	SignedOffAt        *time.Time `json:"signed_off_at,omitempty"`
	AttestationVersion string     `json:"attestation_version,omitempty"`
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
	// SetPublic marks a project public and records the responsibility sign-off
	// (006f): who signed, the attestation text version, and the time. One-way —
	// there is no un-publish. The caller (the HTTP owner check and the
	// manifest-clear gate) authorises; this store only records.
	SetPublic(ctx context.Context, id, signedOffBy, attestationVersion string) error
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
