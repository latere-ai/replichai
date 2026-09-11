// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package managed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"latere.ai/x/pkg/wait"
)

// RuntimeRouter chooses a runtime for a new specification or a durable ref.
type RuntimeRouter interface {
	ForNew(SessionSpec) (Runtime, error)
	ForRef(SessionRef) (Runtime, error)
}

// Hooks are the caller's synchronous callbacks on the session lifecycle. Every
// field is optional. An error from Started, Checkpoint, or Status is treated
// as a caller failure: the controller cancels the session, retains whatever
// evidence exists, releases, and returns the hook error joined with any
// cleanup errors. A Retain failure is deliberately handled differently; see
// the Retain field.
type Hooks struct {
	// Started runs immediately after the runtime accepted the session and
	// before the first poll, so a successful remote start can never become
	// invisible. The server persists the durable reference here.
	Started func(context.Context, SessionRef) error
	// Checkpoint receives the previous and the advanced reference whenever a
	// runtime raises its locator revision. It must persist the new reference,
	// because the controller calls the runtime again only after it returns.
	Checkpoint func(context.Context, SessionRef, SessionRef) error
	// Status receives the initial pending observation and every later
	// observation that differs from the one before it; duplicates are dropped.
	Status func(context.Context, Status) error
	// Retain is the evidence handoff, called once while the remote resources
	// still exist. Only a successful Retain permits release, so a failure here
	// deliberately leaves the session unreleased and recoverable.
	Retain func(context.Context, EvidenceReader) error
}

// EvidenceReader is the view of a finished or cancelled session handed to
// Hooks.Retain. It is usable only for the duration of that call, because the
// release that follows may make the runtime's evidence unreadable.
type EvidenceReader struct {
	// Status is the terminal observation, or the last partial one when the
	// session was cancelled or abandoned before it settled.
	Status Status
	// Events is the transcript as the runtime normalised it, in the order the
	// runtime reports; the controller neither filters nor reorders it.
	Events []Event
	// Artifacts is the metadata for every file the session produced; the bytes
	// are read through Fetch.
	Artifacts []Artifact
	runtime   Runtime
}

// Fetch reads one artifact's bytes from the still-live runtime. Call it only
// from inside the Retain hook: afterwards the session is released and the
// remote copy may be gone.
func (e EvidenceReader) Fetch(ctx context.Context, artifact Artifact) ([]byte, error) {
	return e.runtime.FetchArtifact(ctx, e.Status.Ref, artifact)
}

// Result is what a controller operation reports back. It is returned alongside
// an error as well as on success, so a caller can still settle usage and keep
// the evidence of a run that ended badly.
type Result struct {
	// Ref is the last durable reference the controller saw, and is zero only
	// when the session never started.
	Ref SessionRef
	// Status is the final observation, terminal unless the runtime stopped
	// answering, in which case the controller names the terminal reason itself.
	Status Status
	// Events is the transcript captured before release.
	Events []Event
	// Artifacts is the artifact metadata captured before release.
	Artifacts []Artifact
	// Released is true only when Release succeeded. False is not proof that the
	// session still exists, because a failure early in the lifecycle returns
	// before the cleanup it triggered can report back.
	Released bool
}

// WaitPolicy is the single waiting and failure budget every caller shares, so
// a CLI run and a server job give up on an unresponsive runtime for the same
// reasons.
type WaitPolicy struct {
	// InitialDelay is the pause after the first poll, doubled on each further
	// poll until it reaches MaxDelay.
	InitialDelay time.Duration
	// MaxDelay caps the poll delay. Setting it equal to InitialDelay turns the
	// backoff into a fixed interval, which is what the CLI does.
	MaxDelay time.Duration
	// MaxTemporaryErrors is how many consecutive temporary poll failures are
	// tolerated; one successful poll resets the count.
	MaxTemporaryErrors int
	// MaxSilence is how long the controller waits without a successful poll
	// before it declares the session timed out rather than failed.
	MaxSilence time.Duration
	// CleanupTimeout bounds cancel, terminal wait, retention, and release,
	// which run on a context detached from the caller's so an abandoned
	// request still cleans up after itself.
	CleanupTimeout time.Duration
}

// DefaultWaitPolicy returns the budget tuned for production: poll from two
// seconds up to fifteen, tolerate five consecutive temporary failures or two
// minutes of silence, and allow thirty seconds to clean up. Tests shrink these
// durations instead of inventing their own policy.
func DefaultWaitPolicy() WaitPolicy {
	return WaitPolicy{
		InitialDelay:       2 * time.Second,
		MaxDelay:           15 * time.Second,
		MaxTemporaryErrors: 5,
		MaxSilence:         2 * time.Minute,
		CleanupTimeout:     30 * time.Second,
	}
}

type controllerClock struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// Controller is the only application-facing lifecycle API for managed
// sessions. It routes a specification or a durable reference to a runtime and
// then owns validation, checkpointing, contract enforcement on every
// observation, transient retry, cancellation, evidence retention, and release,
// so no caller has to drive the runtime's individual methods in the right
// order.
type Controller struct {
	router RuntimeRouter
	policy WaitPolicy
	clock  controllerClock
}

// ControllerOption overrides a controller default at construction. The
// defaults are the production wait policy and the real clock.
type ControllerOption func(*Controller)

// WithWaitPolicy replaces the waiting and failure budget. The CLI uses it to
// pin the poll interval an operator asked for, and tests to remove the sleeps.
func WithWaitPolicy(policy WaitPolicy) ControllerOption {
	return func(controller *Controller) { controller.policy = policy }
}

func withControllerClock(now func() time.Time, sleep func(context.Context, time.Duration) error) ControllerOption {
	return func(controller *Controller) {
		controller.clock = controllerClock{now: now, sleep: sleep}
	}
}

// NewController returns a controller that reaches runtimes through router,
// which is the only thing it cannot supply itself: the wait policy and the
// clock have defaults that options replace.
func NewController(router RuntimeRouter, options ...ControllerOption) *Controller {
	controller := &Controller{
		router: router,
		policy: DefaultWaitPolicy(),
		clock:  controllerClock{now: time.Now, sleep: wait.Sleep},
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

// Capabilities reports what the runtime routed for spec supports, without
// starting or mutating anything, so a caller can reject an unsupported request
// before any remote work happens.
func (c *Controller) Capabilities(ctx context.Context, spec SessionSpec) (Capabilities, error) {
	runtime, err := c.router.ForNew(spec)
	if err != nil {
		return Capabilities{}, err
	}
	return runtime.Capabilities(ctx)
}

// Start validates and creates a durable session, invoking Started before it
// returns. It exists for the CLI's explicit detached workflow. Ordinary callers
// use Execute, so that retention and release cannot be forgotten.
func (c *Controller) Start(ctx context.Context, spec SessionSpec, hooks Hooks) (SessionRef, error) {
	// The runtime that start resolves is discarded here. The detached workflow
	// reattaches by reference on the next call. Only Execute keeps the
	// runtime, to poll in the same process.
	_, ref, err := c.start(ctx, spec, hooks)
	if err != nil {
		return ref, err
	}
	return ref, nil
}

// Execute runs a session from specification to release: start, poll to a
// terminal observation, hand the evidence to Retain, release. Because it sees
// the specification, it also knows whether an evaluation was requested, and so
// holds the runtime to the matching verdict, which Resume cannot do.
func (c *Controller) Execute(ctx context.Context, spec SessionSpec, hooks Hooks) (Result, error) {
	runtime, ref, err := c.start(ctx, spec, hooks)
	if err != nil {
		return Result{Ref: ref}, err
	}
	graded := spec.Evaluation != nil
	return c.wait(ctx, runtime, ref, hooks, &graded)
}

func (c *Controller) start(ctx context.Context, spec SessionSpec, hooks Hooks) (Runtime, SessionRef, error) {
	runtime, err := c.router.ForNew(spec)
	if err != nil {
		return nil, SessionRef{}, err
	}
	if err := runtime.Validate(spec); err != nil {
		return nil, SessionRef{}, err
	}
	ref, err := runtime.Start(ctx, spec)
	if err != nil {
		return nil, SessionRef{}, err
	}
	if err := validateRuntimeRef(runtime, ref); err != nil {
		_, abortErr := c.abort(ctx, runtime, ref, Status{Ref: ref, State: StateFailed, TerminalReason: ReasonRuntimeFailed}, hooks, err)
		return nil, ref, abortErr
	}
	if hooks.Started != nil {
		if err := hooks.Started(ctx, ref); err != nil {
			_, abortErr := c.abort(ctx, runtime, ref, Status{Ref: ref, State: StateFailed, TerminalReason: ReasonRuntimeFailed}, hooks, err)
			return nil, ref, abortErr
		}
	}
	initial := Status{Ref: ref, State: StatePending}
	if hooks.Status != nil {
		if err := hooks.Status(ctx, initial); err != nil {
			_, abortErr := c.abort(ctx, runtime, ref, initial, hooks, err)
			return nil, ref, abortErr
		}
	}
	return runtime, ref, nil
}

// Resume reattaches to a session started earlier, after a process restart or
// when reconciliation retries a retention that failed, and drives the same
// wait, retain, and release path to its end.
func (c *Controller) Resume(ctx context.Context, ref SessionRef, hooks Hooks) (Result, error) {
	runtime, err := c.router.ForRef(ref)
	if err != nil {
		return Result{Ref: ref}, err
	}
	if err := validateRuntimeRef(runtime, ref); err != nil {
		return Result{Ref: ref}, err
	}
	// The persisted reference does not encode whether the original request was
	// graded. Resume therefore validates any verdict it sees, but does not infer
	// that a missing verdict means the runtime violated the original request.
	return c.wait(ctx, runtime, ref, hooks, nil)
}

// Cancel stops a live session and still finishes the lifecycle: it waits for a
// terminal observation within the cleanup budget, retains the partial
// evidence, and releases. It is idempotent, so cancelling a session that has
// already ended is success.
func (c *Controller) Cancel(ctx context.Context, ref SessionRef, hooks Hooks) (Result, error) {
	runtime, err := c.router.ForRef(ref)
	if err != nil {
		return Result{Ref: ref}, err
	}
	status := Status{Ref: ref, State: StateFailed, TerminalReason: ReasonCancelled}
	return c.abort(ctx, runtime, ref, status, hooks, nil)
}

func (c *Controller) wait(ctx context.Context, runtime Runtime, ref SessionRef, hooks Hooks, graded *bool) (Result, error) {
	var previous Status
	lastSuccess := c.clock.now()
	temporaryErrors := 0
	delay := c.policy.InitialDelay
	for {
		status, err := runtime.Poll(ctx, ref)
		if err != nil {
			if !IsTemporary(err) || ctx.Err() != nil {
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref, false), hooks, err)
			}
			silent := c.clock.now().Sub(lastSuccess) >= c.policy.MaxSilence
			temporaryErrors++
			if temporaryErrors >= c.policy.MaxTemporaryErrors || silent {
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref, silent), hooks, err)
			}
			if err := c.clock.sleep(ctx, retryDelay(err, delay)); err != nil {
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref, false), hooks, err)
			}
			delay = nextDelay(delay, c.policy.MaxDelay)
			continue
		}
		temporaryErrors = 0
		lastSuccess = c.clock.now()
		delay = c.policy.InitialDelay
		if err := validateObservation(runtime, ref, previous, status, graded); err != nil {
			return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref, false), hooks, err)
		}
		if status.Ref.Revision > ref.Revision {
			if hooks.Checkpoint != nil {
				if err := hooks.Checkpoint(ctx, ref, status.Ref); err != nil {
					return c.abort(ctx, runtime, ref, status, hooks, err)
				}
			}
			ref = status.Ref
		}
		if hooks.Status != nil && !sameObservation(previous, status) {
			if err := hooks.Status(ctx, status); err != nil {
				return c.abort(ctx, runtime, ref, status, hooks, err)
			}
		}
		previous = status
		if status.State.Terminal() {
			return c.retainAndRelease(ctx, runtime, status, hooks, nil)
		}
		if err := c.clock.sleep(ctx, delay); err != nil {
			return c.abort(ctx, runtime, ref, status, hooks, err)
		}
		delay = nextDelay(delay, c.policy.MaxDelay)
	}
}

func (c *Controller) abort(ctx context.Context, runtime Runtime, ref SessionRef, status Status, hooks Hooks, cause error) (Result, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.policy.CleanupTimeout)
	defer cancel()
	var cleanupErrors []error
	if err := runtime.Cancel(cleanupCtx, ref); err != nil && !errors.Is(err, ErrNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cancel %s: %w", ref.ID, err))
	}
	if status.Ref.ID == "" {
		status.Ref = ref
	}
	if status.State == StateUnknown || !status.State.Terminal() {
		status.State = StateFailed
		status.TerminalReason = ReasonRuntimeFailed
	}
	terminal, terminalErr := c.waitAfterCancel(cleanupCtx, runtime, ref, status, hooks)
	if terminalErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("wait after cancel: %w", terminalErr))
	} else {
		status = terminal
	}
	result, finishErr := c.retainAndRelease(cleanupCtx, runtime, status, hooks, nil)
	if finishErr != nil {
		cleanupErrors = append(cleanupErrors, finishErr)
	}
	if len(cleanupErrors) > 0 {
		return result, errors.Join(append([]error{cause}, cleanupErrors...)...)
	}
	return result, cause
}

func (c *Controller) retainAndRelease(ctx context.Context, runtime Runtime, status Status, hooks Hooks, cause error) (Result, error) {
	result := Result{Ref: status.Ref, Status: status}
	events, err := runtime.Events(ctx, status.Ref)
	if err != nil {
		return result, errors.Join(cause, fmt.Errorf("read events: %w", err))
	}
	artifacts, err := runtime.ListArtifacts(ctx, status.Ref)
	if err != nil {
		return result, errors.Join(cause, fmt.Errorf("list artifacts: %w", err))
	}
	result.Events = events
	result.Artifacts = artifacts
	if hooks.Retain != nil {
		reader := EvidenceReader{Status: status, Events: events, Artifacts: artifacts, runtime: runtime}
		if err := hooks.Retain(ctx, reader); err != nil {
			return result, errors.Join(cause, fmt.Errorf("retain evidence: %w", err))
		}
	}
	if err := runtime.Release(ctx, status.Ref); err != nil {
		return result, errors.Join(cause, &CleanupError{Err: err})
	}
	result.Released = true
	return result, cause
}

func validateRuntimeRef(runtime Runtime, ref SessionRef) error {
	if ref.Runtime != runtime.Name() {
		return &RuntimeMismatchError{Expected: runtime.Name(), Actual: ref.Runtime}
	}
	return ref.Validate(0)
}

func validateObservation(runtime Runtime, expected SessionRef, previous, current Status, graded *bool) error {
	if err := validateRuntimeRef(runtime, current.Ref); err != nil {
		return fmt.Errorf("%w: %w", ErrContract, err)
	}
	if !current.Ref.SameIdentity(expected) || current.Ref.Revision < expected.Revision {
		return fmt.Errorf("%w: invalid session checkpoint", ErrContract)
	}
	if current.Ref.Revision == expected.Revision && !reflect.DeepEqual(current.Ref, expected) {
		return fmt.Errorf("%w: locator changed without a revision", ErrContract)
	}
	if !current.State.Valid() {
		return fmt.Errorf("%w: invalid state %s", ErrContract, current.State)
	}
	if !current.Model.Valid() {
		return fmt.Errorf("%w: status has no resolved model", ErrContract)
	}
	if previous.State.Valid() {
		if previous.State.Terminal() && current.State != previous.State {
			return fmt.Errorf("%w: terminal state changed", ErrContract)
		}
		if stateRank(current.State) < stateRank(previous.State) {
			return fmt.Errorf("%w: state regressed from %s to %s", ErrContract, previous.State, current.State)
		}
		if !current.Usage.MonotonicFrom(previous.Usage) {
			return fmt.Errorf("%w: usage regressed", ErrContract)
		}
		if previous.Model.Valid() && current.Model != previous.Model {
			return fmt.Errorf("%w: resolved model changed", ErrContract)
		}
	}
	if current.State == StateDone {
		if graded != nil && *graded && (current.Verdict == nil || !current.Verdict.Grade.Decided()) {
			return fmt.Errorf("%w: graded session completed without a decided verdict", ErrContract)
		}
		if graded != nil && !*graded && current.Verdict != nil {
			return fmt.Errorf("%w: ungraded session completed with a verdict", ErrContract)
		}
		if graded == nil && current.Verdict != nil && !current.Verdict.Grade.Decided() {
			return fmt.Errorf("%w: completed session has an undecided verdict", ErrContract)
		}
	}
	return nil
}

func stateRank(state State) int {
	switch state {
	case StatePending:
		return 1
	case StateRunning:
		return 2
	case StateDone, StateFailed:
		return 3
	default:
		return 0
	}
}

func sameObservation(a, b Status) bool {
	return reflect.DeepEqual(a, b)
}

func (c *Controller) waitAfterCancel(ctx context.Context, runtime Runtime, ref SessionRef, fallback Status, hooks Hooks) (Status, error) {
	started := c.clock.now()
	temporaryErrors := 0
	delay := c.policy.InitialDelay
	for {
		status, err := runtime.Poll(ctx, ref)
		if err == nil {
			temporaryErrors = 0
			if validationErr := validateObservation(runtime, ref, Status{}, status, nil); validationErr != nil {
				return fallback, validationErr
			}
			if status.Ref.Revision > ref.Revision && hooks.Checkpoint != nil {
				if checkpointErr := hooks.Checkpoint(ctx, ref, status.Ref); checkpointErr != nil {
					return fallback, checkpointErr
				}
			}
			fallback = status
			ref = status.Ref
			if status.State.Terminal() {
				return status, nil
			}
		} else if errors.Is(err, ErrNotFound) && fallback.State.Terminal() {
			return fallback, nil
		} else if !IsTemporary(err) {
			return fallback, err
		} else {
			temporaryErrors++
			if temporaryErrors >= c.policy.MaxTemporaryErrors {
				return fallback, err
			}
		}
		if c.clock.now().Sub(started) >= c.policy.MaxSilence {
			return fallback, context.DeadlineExceeded
		}
		wait := delay
		if err != nil {
			wait = retryDelay(err, delay)
		}
		if err := c.clock.sleep(ctx, wait); err != nil {
			return fallback, err
		}
		delay = nextDelay(delay, c.policy.MaxDelay)
	}
}

func retryDelay(err error, fallback time.Duration) time.Duration {
	var retry interface{ RetryAfter() time.Duration }
	if errors.As(err, &retry) && retry.RetryAfter() > fallback {
		return retry.RetryAfter()
	}
	return fallback
}

// previousOrFailure returns the terminal status for a session the runtime
// never reported a valid observation for. A session that stopped responding
// is a timeout, not a fault the runtime reported, and operators investigate
// the two in different places.
func previousOrFailure(previous Status, ref SessionRef, timedOut bool) Status {
	if previous.State.Valid() {
		return previous
	}
	reason := ReasonRuntimeFailed
	if timedOut {
		reason = ReasonTimedOut
	}
	return Status{Ref: ref, State: StateFailed, TerminalReason: reason}
}

func nextDelay(current, maximum time.Duration) time.Duration {
	if current <= 0 {
		return 0
	}
	if maximum <= 0 {
		return current
	}
	return time.Duration(math.Min(float64(current*2), float64(maximum)))
}
