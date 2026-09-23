// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package job manages the lifecycle of one submission: its phases, the single
// transition function that is the only writer of them, and the store that
// holds it. A submission becomes a durable job so that it outlives the
// request, the connection, and a restart (006b).
//
// This is the only place the project keeps mutable state. A job holds work in
// flight: a paper submitted, resolving, or running. It never holds a finished
// reproduction, which is a published repository read back through the derived
// registry (003). A job row is disposable; losing one loses some history, not
// a reproduction.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// Phase is the stage a job has reached in its lifecycle. It is stored as a
// column and never inferred from a side effect.
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

// Kind is the type of work a job runs. The lifecycle is the same for both
// kinds; only the callee differs (007).
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
	// transition function is the only writer of phase and refuses the change,
	// which is what makes phase a state machine rather than a mutable column.
	ErrIllegalTransition = errors.New("illegal job phase transition")
	// ErrEmptyRef reports enqueuing with no paper or experiment reference.
	ErrEmptyRef = errors.New("job needs a paper or experiment reference")
	// ErrImportedElsewhere reports a re-push of a session record that already
	// belongs to another project. A record is moved through the app, never by
	// pushing it again under a different project.
	ErrImportedElsewhere = errors.New("this session record already belongs to another project")
	// ErrActiveSession reports a submit refused because a non-terminal session
	// for the same paper already exists, either in the same project or in any
	// project the same principal owns. It is the double-trigger guard and maps
	// to a 409, not a 500: a deliberate re-run after the previous session
	// finished is allowed, and an accidental concurrent duplicate is not.
	ErrActiveSession = errors.New("an active session for this paper already exists")
	// ErrUnpriceable reports a submit refused because this deployment meters
	// money but has an empty rate card and so cannot price a session. Tokens
	// that cannot be priced are not free, so the submit is refused rather than
	// settled at zero after the session has run.
	ErrUnpriceable = errors.New("this deployment cannot price a session")
	// ErrNoCredits reports a submit refused because the project cannot commit
	// the start reserve (011). Like the double-trigger guard it is a refusal,
	// not a failure. The check runs in the transaction that inserts the job, so
	// it is exact under concurrency and cannot be skipped by a new submit path.
	ErrNoCredits = errors.New("this project does not have the credits to start a session")
	// ErrRetryablePublish marks a publishing dependency failure that must leave
	// the job in publishing for restart reconciliation instead of making the
	// already-completed managed run terminally failed.
	ErrRetryablePublish = errors.New("retryable publishing failure")
)

// RetryablePublish wraps cause so the worker's recovery policy treats it as
// retryable. The server runner uses it for forge, source, and transcript reads
// that can succeed when publishing is reconciled later.
func RetryablePublish(cause error) error {
	return fmt.Errorf("%w: %w", ErrRetryablePublish, cause)
}

// resolveCard reads the price list a session starting now is admitted under.
//
// A nil seam is the deliberate unpriced mode and yields an empty card, which
// prices nothing. A seam that fails resolves to the compiled-in list rather
// than to an empty card, because a fault must not make sessions free while it
// lasts. A seam that succeeds but yields a card with no models is a metered
// deployment that cannot price anything, and the submit is refused: settling
// it at zero would be the silent underspend the refusal exists to prevent.
func resolveCard(ctx context.Context, seam credit.CardSnapshot) (credit.Card, error) {
	if seam == nil {
		return credit.Card{}, nil
	}
	card, err := seam(ctx)
	if err != nil {
		slog.WarnContext(ctx, "rate card unreadable; falling back to the compiled-in list", "error", err)
		return credit.DefaultCard(), nil
	}
	if len(card.Models) == 0 {
		return credit.Card{}, ErrUnpriceable
	}
	return card, nil
}

// cost returns the price of this session's recorded usage under the rates it
// was admitted at, in micro-USD (011).
//
// It prices the last observation, which is what the session is settled from.
// On the ordinary path that is the terminal poll and therefore exact. On a run
// that ended without one, such as a cancel or a worker that lost the backend,
// it is the last usage seen, and spend after that point is reconciled later
// with an adjusting ledger entry rather than estimated here.
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

// withCost returns the job with its derived cost filled in, which is what a
// read path returns. Deriving the cost on read rather than storing it keeps the
// number the client sees and the number settlement charges the same
// expression.
func (j Job) withCost() Job {
	j.Cost = j.cost()
	return j
}

// CancelCause is the error recorded on a session a user stopped, so the UI can
// tell a deliberate stop from a real failure.
const CancelCause = "cancelled"

// ProjectStatus summarizes a project's sessions for the list view: the total,
// how many are still running, the newest session's phase, and when any of them
// last changed.
//
// LatestAt is the newest UpdatedAt across the project's sessions, and is one
// of the two inputs 018 derives a project's modified time from. It uses the
// session's update time rather than its creation time because a run that
// finished an hour ago changed the project an hour ago.
type ProjectStatus struct {
	Total   int   `json:"total"`
	Running int   `json:"running"`
	Latest  Phase `json:"latest"`
	// Published is how many sessions committed a repository, which is what the
	// project archive has to include (020). It cannot be derived from Total or
	// from the phase: RepoName marks that publishing began, and a project whose
	// every session failed before that point has sessions and no artifacts.
	Published int       `json:"published"`
	LatestAt  time.Time `json:"latest_at"`
	// Spend is the cost of the project's sessions inside the caller's window, in
	// micro-USD (021e). It is summed in Go rather than in SQL because cost is
	// derived from usage against the rate card the session carries, and a second
	// implementation of the pricing in SQL could disagree with the first.
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
	// Origin records where the work happened (045k). Platform is a session
	// this deployment ran and metered; local is one somebody ran on their own
	// machine with their own model credential and pushed the record here.
	// Empty is read as platform, because every row written before the column
	// existed was a platform session.
	Origin Origin `json:"origin,omitempty"`
	// ExternalID is the runtime's own session id for an imported session, and
	// makes a re-push idempotent. It is empty for a platform session.
	ExternalID string             `json:"external_id,omitempty"`
	Status     provider.RunStatus `json:"status"`              // latest observation while running
	RepoName   string             `json:"repo_name,omitempty"` // set once publishing begins
	Err        string             `json:"error,omitempty"`     // set with the failed phase
	// Tier is the model tier this session runs on, chosen by whoever started it
	// (031). Empty is not a missing value: it means nobody chose a tier, which
	// covers every run before the choice existed and every caller that omits
	// it. Preference.OrDefault resolves an empty tier in one place.
	//
	// It is stored rather than passed as an argument to the run because this
	// row is the only thing the worker sees. The submission is gone once
	// Enqueue returns, and the worker that eventually runs the session may be a
	// different process on a different pod. A tier that stopped at the handler
	// would not reach the worker.
	Tier credit.Preference `json:"tier,omitempty"`
	// Rates is the price list this session was admitted under, snapshotted at
	// enqueue (011). Settlement prices from it rather than from whatever the
	// admin console holds when the run ends, so the ceiling a run was measured
	// against and the amount it is charged are computed from the same numbers.
	// It is not serialized: the cost of a session is a number the owner may
	// see, but the price list it was derived from is the admin's policy.
	Rates credit.Card `json:"-"`
	// Cost is what this session has cost so far, in micro-USD, and is the same
	// number settlement charges. It is derived from Rates and the recorded
	// usage rather than stored, so it cannot disagree with the charge. It is
	// serialized because the owner of the project that paid for a run is
	// entitled to know what it cost. The rates it came from are not serialized,
	// because they are the platform's price list rather than the owner's
	// concern.
	Cost      int64     `json:"cost"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// PhaseAt records when the job entered each phase, so the timeline is durable.
	PhaseAt map[Phase]time.Time `json:"phase_at,omitempty"`
}

// LegacyRuntime is the runtime attributed to rows that carry only a run_id,
// from before a session recorded a managed.SessionRef.
//
// It is a constant rather than the configured default because the runtime that
// ran those sessions is a fact about the past, and reading it from
// configuration would let a later runtime change silently re-attribute
// finished work.
const LegacyRuntime managed.RuntimeName = "anthropic-managed"

// ManagedSessionRef returns the complete durable reference. Historical rows
// carried only run_id. Before the managed contract every production session
// ran on Anthropic, so the compatibility path attributes those rows to
// LegacyRuntime and never to the configured default.
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

// Active reports whether the phase is one from which work continues, which
// are the phases a failure may interrupt.
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
// Failed is reachable from every active phase; done is reachable only from
// publishing.
var transitions = map[Phase][]Phase{
	PhaseSubmitted:  {PhaseResolving, PhaseFailed},
	PhaseResolving:  {PhaseRunning, PhaseFailed},
	PhaseRunning:    {PhasePublishing, PhaseFailed},
	PhasePublishing: {PhaseDone, PhaseFailed},
	PhaseDone:       {},
	PhaseFailed:     {},
}

// CanTransition reports whether a job may move from one phase to another.
// Every store's Advance consults it, so no store can permit a transition the
// machine forbids.
func CanTransition(from, to Phase) bool {
	return slices.Contains(transitions[from], to)
}

// illegal builds a descriptive ErrIllegalTransition for a refused change.
func illegal(from, to Phase) error {
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}

// newID returns a random job identifier.
func newID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error since Go 1.24: it fills the
	// buffer or aborts the process.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
