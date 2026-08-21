package managed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"
)

// RuntimeRouter chooses a runtime for a new specification or a durable ref.
type RuntimeRouter interface {
	ForNew(SessionSpec) (Runtime, error)
	ForRef(SessionRef) (Runtime, error)
}

type Hooks struct {
	Started    func(context.Context, SessionRef) error
	Checkpoint func(context.Context, SessionRef, SessionRef) error
	Status     func(context.Context, Status) error
	Retain     func(context.Context, EvidenceReader) error
}

type EvidenceReader struct {
	Status    Status
	Events    []Event
	Artifacts []Artifact
	runtime   Runtime
}

func (e EvidenceReader) Fetch(ctx context.Context, artifact Artifact) ([]byte, error) {
	return e.runtime.FetchArtifact(ctx, e.Status.Ref, artifact)
}

type Result struct {
	Ref       SessionRef
	Status    Status
	Events    []Event
	Artifacts []Artifact
	Released  bool
}

type WaitPolicy struct {
	InitialDelay       time.Duration
	MaxDelay           time.Duration
	MaxTemporaryErrors int
	MaxSilence         time.Duration
	CleanupTimeout     time.Duration
}

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

type Controller struct {
	router RuntimeRouter
	policy WaitPolicy
	clock  controllerClock
}

type ControllerOption func(*Controller)

func WithWaitPolicy(policy WaitPolicy) ControllerOption {
	return func(controller *Controller) { controller.policy = policy }
}

func withControllerClock(now func() time.Time, sleep func(context.Context, time.Duration) error) ControllerOption {
	return func(controller *Controller) {
		controller.clock = controllerClock{now: now, sleep: sleep}
	}
}

func NewController(router RuntimeRouter, options ...ControllerOption) *Controller {
	controller := &Controller{
		router: router,
		policy: DefaultWaitPolicy(),
		clock:  controllerClock{now: time.Now, sleep: sleepContext},
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

func (c *Controller) Execute(ctx context.Context, spec SessionSpec, hooks Hooks) (Result, error) {
	runtime, err := c.router.ForNew(spec)
	if err != nil {
		return Result{}, err
	}
	if err := runtime.Validate(spec); err != nil {
		return Result{}, err
	}
	ref, err := runtime.Start(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	if err := validateRuntimeRef(runtime, ref); err != nil {
		return c.abort(ctx, runtime, ref, Status{Ref: ref, State: StateFailed, TerminalReason: ReasonRuntimeFailed}, hooks, err)
	}
	if hooks.Started != nil {
		if err := hooks.Started(ctx, ref); err != nil {
			return c.abort(ctx, runtime, ref, Status{Ref: ref, State: StateFailed, TerminalReason: ReasonRuntimeFailed}, hooks, err)
		}
	}
	initial := Status{Ref: ref, State: StatePending}
	if hooks.Status != nil {
		if err := hooks.Status(ctx, initial); err != nil {
			return c.abort(ctx, runtime, ref, initial, hooks, err)
		}
	}
	graded := spec.Evaluation != nil
	return c.wait(ctx, runtime, ref, hooks, &graded)
}

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

func (c *Controller) Cancel(ctx context.Context, ref SessionRef, hooks Hooks) (Result, error) {
	runtime, err := c.router.ForRef(ref)
	if err != nil {
		return Result{Ref: ref}, err
	}
	status := Status{Ref: ref, State: StateFailed, TerminalReason: ReasonCancelled}
	return c.abort(ctx, runtime, ref, status, hooks, context.Canceled)
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
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref), hooks, err)
			}
			temporaryErrors++
			if temporaryErrors >= c.policy.MaxTemporaryErrors || c.clock.now().Sub(lastSuccess) >= c.policy.MaxSilence {
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref), hooks, err)
			}
			if err := c.clock.sleep(ctx, retryDelay(err, delay)); err != nil {
				return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref), hooks, err)
			}
			delay = nextDelay(delay, c.policy.MaxDelay)
			continue
		}
		temporaryErrors = 0
		lastSuccess = c.clock.now()
		delay = c.policy.InitialDelay
		if err := validateObservation(runtime, ref, previous, status, graded); err != nil {
			return c.abort(ctx, runtime, ref, previousOrFailure(previous, ref), hooks, err)
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
		return fmt.Errorf("%w: %v", ErrContract, err)
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

func previousOrFailure(previous Status, ref SessionRef) Status {
	if previous.State.Valid() {
		return previous
	}
	return Status{Ref: ref, State: StateFailed, TerminalReason: ReasonRuntimeFailed}
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

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
