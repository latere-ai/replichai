// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
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
	modelProvider := j.Status.ModelProvider
	if modelProvider == "" {
		modelProvider = "anthropic"
	}
	rates, err := j.Rates.ForRef(managed.ModelRef{Provider: modelProvider, ID: j.Status.Model, BillingVariant: j.Status.BillingVariant})
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
	Total   int   `json:"total"`
	Running int   `json:"running"`
	Latest  Phase `json:"latest"`
	// Published is how many of them committed a repository — what the project
	// archive has anything to put in it (020). It is not derivable from Total or
	// from the phase: RepoName marks that publishing *began*, and a project whose
	// every session failed before that has sessions and no artifacts.
	Published int       `json:"published"`
	LatestAt  time.Time `json:"latest_at"`
	// Spend is what the project's sessions cost inside the caller's window, in
	// micro-USD (021e). It is summed in Go rather than in SQL because cost is
	// derived from usage against the rate card the session carries, and a second
	// implementation of the pricing in SQL is a second answer to what a run cost.
	Spend int64 `json:"spend"`
}

// Job is the durable record of one submission.
type Job struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	Principal string `json:"principal"`            // who submitted it; an opaque OIDC sub (006c)
	ProjectID string `json:"project_id,omitempty"` // the project it belongs to (006g)
	Ref       string `json:"ref"`                  // the paper reference, or an experiment id
	PaperKey  string `json:"paper_key,omitempty"`  // the offline paper identity the session targets, keying the double-trigger guard (006g)
	Phase     Phase  `json:"phase"`
	// Origin says where the work happened (045k). Platform is a session
	// this deployment ran and metered; local is one somebody ran on their
	// own machine with their own model credential and pushed the record
	// here. Empty reads as platform, which is what every row written before
	// the column existed is.
	Origin Origin `json:"origin,omitempty"`
	// ExternalID is the runtime's own session id for an imported session,
	// and is what makes a re-push idempotent. Empty for a platform session.
	ExternalID string             `json:"external_id,omitempty"`
	Status     provider.RunStatus `json:"status"`              // latest observation while running
	RepoName   string             `json:"repo_name,omitempty"` // set once publishing begins
	Err        string             `json:"error,omitempty"`     // set with the failed phase
	// Tier is the model tier this session runs on, chosen by whoever started it
	// (031). Empty is not a missing value: it is a session nobody chose for —
	// every run before the choice existed, and every caller that omits it — and
	// Preference.OrDefault is where that resolves, once.
	//
	// It is durable rather than an argument to the run because this row is the
	// only thing the worker sees: the submission is gone once Enqueue returns,
	// and the worker that eventually runs the session may be a different process
	// on a different pod. A tier that stopped at the handler would reach nothing.
	Tier credit.Preference `json:"tier,omitempty"`
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

// LegacyRuntime is the runtime attributed to rows carrying only a run_id, from
// before a session recorded a managed.SessionRef.
//
// It is a constant rather than today's configured default on purpose: what ran
// those sessions is a fact about the past, and reading it from configuration
// would let a later runtime change silently re-attribute finished work.
const LegacyRuntime managed.RuntimeName = "anthropic-managed"

// ManagedSessionRef returns the complete durable reference. Historical rows
// carried only run_id; the production invariant before the managed contract was
// Anthropic, so that compatibility path is explicit and never uses today's
// configured default.
func (j Job) ManagedSessionRef() (managed.SessionRef, bool, error) {
	if len(j.Status.SessionRef) > 0 {
		var ref managed.SessionRef
		if err := json.Unmarshal(j.Status.SessionRef, &ref); err != nil {
			return managed.SessionRef{}, false, fmt.Errorf("decode managed session ref: %w", err)
		}
		if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
			return managed.SessionRef{}, false, fmt.Errorf("validate managed session ref: %w", err)
		}
		if j.Status.Runtime != "" && managed.RuntimeName(j.Status.Runtime) != ref.Runtime {
			return managed.SessionRef{}, false, &managed.RuntimeMismatchError{Expected: managed.RuntimeName(j.Status.Runtime), Actual: ref.Runtime}
		}
		return ref, true, nil
	}
	if j.Status.RunID == "" {
		return managed.SessionRef{}, false, nil
	}
	return managed.SessionRef{Runtime: LegacyRuntime, ID: j.Status.RunID, Revision: 1, LocatorVersion: 1}, true, nil
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
