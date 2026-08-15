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
	// Owner is the owning principal (006c). It is omitted when empty because a
	// reader with no stake in the project is served a Redacted copy, and an
	// `"owner": ""` on that wire would be a claim about who owns it (028).
	Owner      string     `json:"owner,omitempty"`
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
	// DeletedAt is when the owner moved this project to the trash (019), nil for
	// one that is not there. It is a column rather than a second table because
	// both project_papers and jobs.project_id point at this row, and moving a row
	// means moving everything that points at it.
	//
	// Every list read filters it out. Get does not: the trash screen, the restore
	// and the permanent delete all have to read a trashed project, and a read that
	// refused would leave them nothing to work with.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// Redacted is the project as somebody with no stake in it reads it (028): the
// work, without the principal who owns it. An opaque OIDC subject names nobody
// to a stranger and identifies its holder to anybody who already knows one, so
// a public reader gets the sign-off block — which was published deliberately,
// under a name its signer chose — and not this.
// The signing and withdrawing principals go with it, for the same reason and
// with the same exception: the sign-off block's *name*, institution, and public
// contact stay, because a signer chose to publish those, and the subject behind
// them is not one of them.
func (p Project) Redacted() Project {
	p.Owner, p.SignedOffBy, p.WithdrawnBy = "", "", ""
	return p
}

// Trashed reports whether this project is in the trash.
func (p Project) Trashed() bool { return p.DeletedAt != nil }

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

// Paper is one paper a project holds (012).
//
// It is what the project is *about*, which is not the same as what has been run
// against it. Before this the paper existed only as `ref` on a session, so a
// project could not name a paper it had not started, and a paper whose only
// session failed left no record that it was ever part of the work. A paper row
// outlives every session it carries.
//
// Key is the offline identity from paper.Key — the same value a session stores
// in PaperKey, which is what joins the two without a column on either side.
// Title is filled in best-effort: blank until a source answers, which is a
// paper you can still start rather than a failure.
type Paper struct {
	Key     string    `json:"key"` // paper.Key: "doi:10.1145/…", "arxiv:2401.00001", "url:…"
	Ref     string    `json:"ref"` // the reference it was added from, as typed
	Title   string    `json:"title,omitempty"`
	AddedBy string    `json:"added_by"`
	AddedAt time.Time `json:"added_at"`
}

// ErrPaperExists reports adding a paper a project already holds. The store's
// primary key makes the duplicate impossible rather than merely unlikely, so
// this is the shape that refusal takes.
var ErrPaperExists = errors.New("the project already holds this paper")

// Store is where projects live. Sessions are not stored here — they are jobs in
// the job store, joined to a project by the handler through job.ListByProject —
// so this store owns only the container and the papers it is about, keeping the
// session relation independent.
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
	// ModifiedAt returns, for each named project, the newest of its creation and
	// its papers' additions — this store's half of a project's modified time
	// (018). The other half is the newest session update, which lives in the job
	// store; the handler takes the later of the two.
	//
	// It is derived on every read rather than kept in a column, so there is no
	// write site to forget: a rename, an added paper, a publish, a withdrawal and
	// a credit move would each have had to remember to touch one.
	ModifiedAt(ctx context.Context, ids []string) (map[string]time.Time, error)
	// Trash moves a project to the trash, and Restore takes it back out (019).
	// Both are idempotent, and Trash keeps the first timestamp: a repeated call
	// must not rewrite the moment the project actually left the list. A missing
	// project is ErrNotFound, because the caller has just authorised against one.
	Trash(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	// ListTrashed returns an owner's trashed projects, newest-trashed first — the
	// one read that answers with them, since every other list filters them out.
	ListTrashed(ctx context.Context, owner string) ([]Project, error)
	// Delete removes a project by id, and the papers it holds with it.
	// Idempotent: deleting a missing project is not an error.
	//
	// It does not touch the project's sessions: they live in the job store, joined
	// by project_id, and the handler deletes them there first (019).
	Delete(ctx context.Context, id string) error

	// AddPaper records a paper the project is about (012). It returns
	// ErrPaperExists when the project already holds that key — a project is
	// about a paper once, however many times it is run — and ErrNotFound for a
	// project that is not there.
	AddPaper(ctx context.Context, projectID string, p Paper) error
	// ListPapers returns a project's papers, oldest first: the order they were
	// added is the order somebody wrote them down, and reversing it would put
	// the paper a project was created for at the bottom.
	ListPapers(ctx context.Context, projectID string) ([]Paper, error)
	// SetPaperTitle fills in a paper's title once a source has answered — at
	// create, or from the first session's resolve. A missing project or paper is
	// not an error: the enrichment is best-effort, exactly as SetTitle is.
	SetPaperTitle(ctx context.Context, projectID, key, title string) error
	// RemovePaper drops a paper from a project. The caller decides whether it may
	// go (012 refuses one that has sessions, so the record of what was actually
	// reproduced stays); this store only records. Idempotent.
	RemovePaper(ctx context.Context, projectID, key string) error
}

// newID mints a random project identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint project id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
