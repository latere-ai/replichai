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
	"slices"
	"time"

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
	// ErrQuotaExceeded reports a submit refused because the payer has no session
	// allowance left (006l). Like the double-trigger guard it is a refusal, not a
	// failure: the check runs in the transaction that inserts, so it is exact
	// under concurrency and cannot be skipped by a new submit path.
	ErrQuotaExceeded = errors.New("the session quota for this account is used up")
)

// Allowance reports how many sessions a payer may hold or have completed.
// Injected by the composition root so internal/job never imports the policy
// store; a nil Allowance means unlimited, which is what an offline run and the
// tests without a quota want.
type Allowance func(ctx context.Context, payerSub string) (n int, unlimited bool, err error)

// Spends reports whether a phase consumes allowance. Consumed is everything but
// failed: a completed session is spent, a running one holds its unit, and a
// failed or cancelled one (cancel lands in failed, carrying CancelCause) is
// refunded. Stating it as one predicate keeps the refund rule structural rather
// than a list of phases to keep in sync.
func (p Phase) Spends() bool { return p != PhaseFailed }

// CancelCause is the error recorded on a session a user stopped, so the UI can
// tell a deliberate stop from a real failure.
const CancelCause = "cancelled"

// ProjectStatus summarises a project's sessions for the list view: the total,
// how many are still running, and the newest session's phase.
type ProjectStatus struct {
	Total   int   `json:"total"`
	Running int   `json:"running"`
	Latest  Phase `json:"latest"`
}

// Job is the durable record of one submission.
type Job struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	Principal string `json:"principal"`            // who submitted it; an opaque OIDC sub (006c)
	ProjectID string `json:"project_id,omitempty"` // the project it belongs to (006g)
	Ref       string `json:"ref"`                  // the paper reference, or an experiment id
	PaperKey  string `json:"paper_key,omitempty"`  // the offline paper identity the session targets, keying the double-trigger guard (006g)
	// ChargedTo is whose session quota this run spends (006l): the submitter, or
	// the sponsor of a project that covers its members' sessions. It is set at
	// enqueue and never rewritten, so a refund, an admin's usage table, and the
	// remaining-quota display all read one column.
	ChargedTo string             `json:"charged_to,omitempty"`
	Phase     Phase              `json:"phase"`
	Status    provider.RunStatus `json:"status"`              // latest observation while running
	RepoName  string             `json:"repo_name,omitempty"` // set once publishing begins
	Err       string             `json:"error,omitempty"`     // set with the failed phase
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
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
