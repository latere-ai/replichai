// Package memory provides a deterministic, restartable managed-session
// runtime for tests and local development.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
)

const (
	Name      managed.RuntimeName = "memory"
	OutputDir                     = "/workspace/outputs/"
)

type FailurePoint string

const (
	FailValidate FailurePoint = "validate"
	FailStart    FailurePoint = "start"
	FailPoll     FailurePoint = "poll"
	FailEvents   FailurePoint = "events"
	FailList     FailurePoint = "list_artifacts"
	FailFetch    FailurePoint = "fetch_artifact"
	FailCancel   FailurePoint = "cancel"
	FailRelease  FailurePoint = "release"
)

type Config struct {
	Model           managed.ModelRef
	PollsBeforeDone int
	Grade           managed.Grade
	Files           map[string][]byte
	Events          []managed.Event
	Failures        map[FailurePoint]error
}

type Store struct {
	mu       sync.Mutex
	next     uint64
	sessions map[string]*session
}

type session struct {
	spec      managed.SessionSpec
	ref       managed.SessionRef
	model     managed.ModelRef
	polls     int
	cancelled bool
	released  bool
}

func NewStore() *Store { return &Store{sessions: make(map[string]*session)} }

type Runtime struct {
	store  *Store
	config Config
}

func New() *Runtime { return NewWithStore(NewStore(), Config{}) }

func NewWithStore(store *Store, config Config) *Runtime {
	if store == nil {
		store = NewStore()
	}
	if !config.Model.Valid() {
		config.Model = managed.ModelRef{Provider: "memory", ID: "memory-model"}
	}
	if config.PollsBeforeDone <= 0 {
		config.PollsBeforeDone = 1
	}
	if config.Files == nil {
		config.Files = make(map[string][]byte)
	}
	if config.Failures == nil {
		config.Failures = make(map[FailurePoint]error)
	}
	return &Runtime{store: store, config: config}
}

func (r *Runtime) Name() managed.RuntimeName { return Name }

func (r *Runtime) Capabilities(ctx context.Context) (managed.Capabilities, error) {
	if err := contextError(ctx); err != nil {
		return managed.Capabilities{}, err
	}
	return managed.Capabilities{
		OutputDir:          OutputDir,
		PackageManagers:    map[string]bool{"pip": true},
		NetworkModes:       map[managed.NetworkMode]bool{managed.NetworkNone: true, managed.NetworkOpen: true},
		RepositoryModes:    map[managed.AccessMode]bool{managed.AccessReadOnly: true, managed.AccessReadWrite: true},
		Evaluation:         true,
		DurableEvents:      true,
		Artifacts:          true,
		Cancellation:       true,
		MaxAttachmentBytes: 64 << 20,
		MaxLocatorBytes:    managed.DefaultMaxLocatorBytes,
	}, nil
}

func (r *Runtime) Models(ctx context.Context) ([]managed.Model, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return []managed.Model{{Ref: r.config.Model, Preferences: []string{"best", "balanced", "fast"}}}, nil
}

func (r *Runtime) Validate(spec managed.SessionSpec) error {
	if err := r.failure(FailValidate); err != nil {
		return err
	}
	capabilities, _ := r.Capabilities(context.Background())
	if err := managed.ValidateCapabilities(Name, capabilities, spec); err != nil {
		return err
	}
	if spec.Model.Exact != nil && *spec.Model.Exact != r.config.Model {
		return &managed.UnsupportedError{Runtime: Name, Field: "model.exact", Capability: "model:" + spec.Model.Exact.ID}
	}
	return nil
}

func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (managed.SessionRef, error) {
	if err := contextError(ctx); err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.Validate(spec); err != nil {
		return managed.SessionRef{}, err
	}
	if err := r.failure(FailStart); err != nil {
		return managed.SessionRef{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.next++
	id := fmt.Sprintf("session-%d", r.store.next)
	locator, _ := json.Marshal(struct {
		Store string `json:"store"`
	}{Store: "memory"})
	ref := managed.SessionRef{Runtime: Name, ID: id, Revision: 1, LocatorVersion: 1, Locator: locator}
	r.store.sessions[id] = &session{spec: spec, ref: ref, model: r.config.Model}
	return ref, nil
}

func (r *Runtime) Poll(ctx context.Context, ref managed.SessionRef) (managed.Status, error) {
	if err := r.before(ctx, FailPoll, ref); err != nil {
		return managed.Status{}, err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, err := r.sessionLocked(ref)
	if err != nil {
		return managed.Status{}, err
	}
	s.polls++
	status := managed.Status{Ref: s.ref, Model: s.model, State: managed.StateRunning, StartedAt: time.Unix(1, 0), Usage: managed.Usage{Model: s.model, InputFresh: int64(s.polls)}}
	if s.cancelled {
		status.State = managed.StateFailed
		status.TerminalReason = managed.ReasonCancelled
		status.FinishedAt = time.Unix(2, 0)
		return status, nil
	}
	if s.polls < r.config.PollsBeforeDone {
		return status, nil
	}
	status.State = managed.StateDone
	status.TerminalReason = managed.ReasonSucceeded
	status.FinishedAt = time.Unix(2, 0)
	if s.spec.Evaluation != nil {
		grade := r.config.Grade
		if grade == managed.GradeUnknown {
			grade = managed.GradePassed
		}
		status.Verdict = &managed.Verdict{Grade: grade, Explanation: "recorded by memory runtime"}
		if grade != managed.GradePassed {
			status.State = managed.StateFailed
			status.TerminalReason = managed.ReasonEvaluationFailed
		}
	}
	return status, nil
}

func (r *Runtime) Events(ctx context.Context, ref managed.SessionRef) ([]managed.Event, error) {
	if err := r.before(ctx, FailEvents, ref); err != nil {
		return nil, err
	}
	return append([]managed.Event(nil), r.config.Events...), nil
}

func (r *Runtime) ListArtifacts(ctx context.Context, ref managed.SessionRef) ([]managed.Artifact, error) {
	if err := r.before(ctx, FailList, ref); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(r.config.Files))
	for filePath := range r.config.Files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	artifacts := make([]managed.Artifact, 0, len(paths))
	for index, filePath := range paths {
		artifacts = append(artifacts, managed.Artifact{ID: fmt.Sprintf("artifact-%d", index+1), Path: filePath})
	}
	return artifacts, nil
}

func (r *Runtime) FetchArtifact(ctx context.Context, ref managed.SessionRef, artifact managed.Artifact) ([]byte, error) {
	if err := r.before(ctx, FailFetch, ref); err != nil {
		return nil, err
	}
	artifacts, err := r.ListArtifacts(ctx, ref)
	if err != nil {
		return nil, err
	}
	if slices.Contains(artifacts, artifact) {
		return append([]byte(nil), r.config.Files[artifact.Path]...), nil
	}
	return nil, &managed.NotFoundError{Runtime: Name, ID: artifact.ID}
}

func (r *Runtime) Cancel(ctx context.Context, ref managed.SessionRef) error {
	if err := r.before(ctx, FailCancel, ref); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, err := r.sessionLocked(ref)
	if err != nil {
		return err
	}
	s.cancelled = true
	return nil
}

func (r *Runtime) Release(ctx context.Context, ref managed.SessionRef) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	if err := r.failure(FailRelease); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	s, ok := r.store.sessions[ref.ID]
	if !ok || s.released {
		return nil
	}
	s.released = true
	return nil
}

func (r *Runtime) Specs() []managed.SessionSpec {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	ids := make([]string, 0, len(r.store.sessions))
	for id := range r.store.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]managed.SessionSpec, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.store.sessions[id].spec)
	}
	return result
}

func (r *Runtime) before(ctx context.Context, point FailurePoint, ref managed.SessionRef) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	if err := r.failure(point); err != nil {
		return err
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	_, err := r.sessionLocked(ref)
	return err
}

func (r *Runtime) sessionLocked(ref managed.SessionRef) (*session, error) {
	s, ok := r.store.sessions[ref.ID]
	if !ok || s.released {
		return nil, &managed.NotFoundError{Runtime: Name, ID: ref.ID}
	}
	if !s.ref.SameIdentity(ref) {
		return nil, &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	return s, nil
}

func (r *Runtime) failure(point FailurePoint) error { return r.config.Failures[point] }

func validateRef(ref managed.SessionRef) error {
	if ref.Runtime != Name {
		return &managed.RuntimeMismatchError{Expected: Name, Actual: ref.Runtime}
	}
	return ref.Validate(managed.DefaultMaxLocatorBytes)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}

var _ managed.Runtime = (*Runtime)(nil)
