// Package job is the lifecycle of one submission: its states, the single
// transition function that is the only writer of them, and the store it lives
// in. A submission becomes a durable job so it outlives the request, the
// connection, and a restart (006b).
//
// This is the one place the project keeps mutable state. Jobs hold work in
// flight — a paper submitted, now resolving, now running — never finished
// reproductions, which are their published repositories read back through the
// derived registry (003). A job row is disposable; losing one loses a bit of
// history, not a reproduction.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// Phase is where a job is in its lifecycle. It is a column, never inferred from
// a side effect.
type Phase string

// The phases a job moves through. A job runs an audit or an experiment, and the
// phases are identical for both.
const (
	PhaseSubmitted  Phase = "submitted"
	PhaseResolving  Phase = "resolving"
	PhaseRunning    Phase = "running"
	PhasePublishing Phase = "publishing"
	PhaseDone       Phase = "done"
	PhaseFailed     Phase = "failed"
)

// Kind is what a job runs. The lifecycle is the same either way; only the callee
// differs (007).
type Kind string

// The kinds of work a job drives.
const (
	KindAudit      Kind = "audit"
	KindExperiment Kind = "experiment"
)

// Errors this package returns.
var (
	// ErrNotFound reports a missing job.
	ErrNotFound = errors.New("job not found")
	// ErrIllegalTransition reports a phase change the state machine forbids. The
	// transition function is the only writer of phase, and it refuses — which is
	// what makes it a state machine rather than a mutable column.
	ErrIllegalTransition = errors.New("illegal job phase transition")
	// ErrEmptyRef reports enqueuing with no paper or experiment reference.
	ErrEmptyRef = errors.New("job needs a paper or experiment reference")
	// ErrActiveSession reports a submit refused because a non-terminal session for
	// the same paper already exists — in the same project, or anywhere the same
	// principal owns. It is the double-trigger guard, a 409, not a 500: a
	// deliberate re-run after the prior finished is allowed, an accidental
	// concurrent double-fire is not.
	ErrActiveSession = errors.New("an active session for this paper already exists")
	// ErrUnpriceable reports a submit refused because this deployment meters
	// money but cannot price a session — an empty rate card. Tokens nobody can
	// price are not free tokens, so the refusal is at the door rather than at
	// settlement, where it would have been a session that ran for nothing.
	ErrUnpriceable = errors.New("this deployment cannot price a session")
	// ErrNoCredits reports a submit refused because the project cannot commit
	// the start reserve (011). Like the double-trigger guard it is a refusal,
	// not a failure: the check runs in the transaction that inserts, so it is
	// exact under concurrency and cannot be skipped by a new submit path.
	ErrNoCredits = errors.New("this project does not have the credits to start a session")
	// ErrRetryablePublish marks a publishing dependency failure that must leave
	// the job in publishing for restart reconciliation instead of making the
	// already-completed managed run terminally failed.
	ErrRetryablePublish = errors.New("retryable publishing failure")
)

// RetryablePublish preserves cause while marking it for the worker's recovery
// policy. The server runner uses it for forge/source/transcript reads that can
// succeed when publishing is reconciled later.
func RetryablePublish(cause error) error {
	return fmt.Errorf("%w: %w", ErrRetryablePublish, cause)
}

// resolveCard reads the price list a session starting now is admitted under.
//
// A nil seam is the deliberate unpriced mode and yields an empty card, which
// prices nothing. A seam that fails resolves to the compiled-in list rather
// than to nothing: a fault must not make sessions free while it lasts. A seam
// that succeeds but yields a card with no models is a metered deployment that
// cannot price anything, and that refuses the submit — settling it at zero
// would be the silent underspend the refusal exists to prevent.
func resolveCard(ctx context.Context, seam credit.CardSnapshot) (credit.Card, error) {
	if seam == nil {
		return credit.Card{}, nil
	}
	card, err := seam(ctx)
	if err != nil {
		log.Printf("rate card: %v; falling back to the compiled-in list", err)
		return credit.DefaultCard(), nil
	}
	if len(card.Models) == 0 {
		return credit.Card{}, ErrUnpriceable
	}
	return card, nil
}

// cost is what this session's recorded usage comes to under the rates it was
// admitted at, in micro-USD (011).
//
// It prices the *last observation*, which is what the session is settled from.
// On the ordinary path that is the terminal poll and therefore exact. On a run
// that ended without one — a cancel, a worker that lost the backend — it is
// what was last seen, and spend after that point is reconciled afterwards with
// an adjusting ledger entry rather than guessed at here.
//
// A session with no rates costs nothing: an unpriced deployment holds and
// releases the reserve and charges no money.
func (j Job) cost() int64 {
	rates, err := j.Rates.For(j.Status.Model)
	if err != nil {
		return 0
	}
	return rates.Price(j.Status.Usage)
}

// withCost returns the job with its derived cost filled in — what a read path
// hands out. Deriving it on the way out rather than storing it keeps the number
// the client sees and the number settlement charges the same expression.
func (j Job) withCost() Job {
	j.Cost = j.cost()
	return j
}

// CancelCause is the error recorded on a session a user stopped, so the UI can
// tell a deliberate stop from a real failure.
const CancelCause = "cancelled"

// ProjectStatus summarises a project's sessions for the list view: the total,
// how many are still running, the newest session's phase, and when any of them
// last changed.
//
// LatestAt is the newest UpdatedAt across the project's sessions — half of what
// 018 derives a project's modified time from. It is the session's *update*, not
// its creation, because a run that finished an hour ago changed the project an
// hour ago.
type ProjectStatus struct {
	Total    int       `json:"total"`
	Running  int       `json:"running"`
	Latest   Phase     `json:"latest"`
	LatestAt time.Time `json:"latest_at,omitempty"`
}

// Job is the durable record of one submission.
type Job struct {
	ID        string             `json:"id"`
	Kind      Kind               `json:"kind"`
	Principal string             `json:"principal"`            // who submitted it; an opaque OIDC sub (006c)
	ProjectID string             `json:"project_id,omitempty"` // the project it belongs to (006g)
	Ref       string             `json:"ref"`                  // the paper reference, or an experiment id
	PaperKey  string             `json:"paper_key,omitempty"`  // the offline paper identity the session targets, keying the double-trigger guard (006g)
	Phase     Phase              `json:"phase"`
	Status    provider.RunStatus `json:"status"`              // latest observation while running
	RepoName  string             `json:"repo_name,omitempty"` // set once publishing begins
	Err       string             `json:"error,omitempty"`     // set with the failed phase
	// Rates is the price list this session was admitted under, snapshotted at
	// enqueue (011). Settlement prices from it rather than from whatever the
	// admin console holds when the run ends, so the ceiling a run was measured
	// against and the amount it is charged cannot be computed from different
	// numbers. Not on the wire: what a session cost is a number, and the list it
	// was derived from is the admin's policy, not the owner's business.
	Rates credit.Card `json:"-"`
	// Cost is what this session has cost so far, in micro-USD — the same number
	// settlement charges. Derived from Rates and the recorded usage rather than
	// stored, so it cannot disagree with the charge. It is on the wire because
	// whoever's project paid for a run is entitled to know what it cost; the
	// rates it came from are not, being the platform's price list rather than
	// this owner's business.
	Cost      int64     `json:"cost"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// PhaseAt records when the job entered each phase, so the timeline is durable.
	PhaseAt map[Phase]time.Time `json:"phase_at,omitempty"`
}

// Active reports whether the job is still in a phase from which work continues —
// the phases a failure may interrupt.
func (p Phase) Active() bool {
	switch p {
	case PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further transition is possible.
func (p Phase) Terminal() bool { return p == PhaseDone || p == PhaseFailed }

// transitions is the whole state machine: the legal successors of each phase.
// A failure is reachable from every active phase; done only from publishing.
var transitions = map[Phase][]Phase{
	PhaseSubmitted:  {PhaseResolving, PhaseFailed},
	PhaseResolving:  {PhaseRunning, PhaseFailed},
	PhaseRunning:    {PhasePublishing, PhaseFailed},
	PhasePublishing: {PhaseDone, PhaseFailed},
	PhaseDone:       {},
	PhaseFailed:     {},
}

// CanTransition reports whether a job may move from one phase to another. It is
// the single source of truth the store's Advance consults, so no store can
// invent a transition the machine forbids.
func CanTransition(from, to Phase) bool {
	return slices.Contains(transitions[from], to)
}

// illegal builds a descriptive ErrIllegalTransition for a refused change.
func illegal(from, to Phase) error {
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}

// newID mints a random job identifier.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint job id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
