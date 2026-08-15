// Package registry derives the set of published reproductions by reading their
// manifests, so there is no second source of truth to drift from reality.
//
// It answers three questions: has this paper been reproduced before (Lookup),
// what slug did its first reproduction fix (CanonicalSlug), and what unique
// name should the next reproduction get (AllocateName). Identity, never slug or
// title, is what relates reproductions of one paper.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// DefaultTTL is how long a derived snapshot is reused before refetching.
const DefaultTTL = time.Minute

// ErrNoManifest is what a Source returns for a repository that carries no
// replication.json — a run that never finalized, which is the normal state of a
// repository the agent is still committing into.
//
// It exists because the caller has to tell that apart from a forge that did not
// answer. Both used to arrive as a bare error, and the publish gate read either
// one as "this reproduction is contaminated" — telling an owner their tree was
// flagged when the truth was that their run had not finished, or that GitHub
// had blipped. A missing file and an unreachable forge are different facts and
// only one of them is about the reproduction.
var ErrNoManifest = errors.New("no manifest")

// Source enumerates published reproductions and reads their files. A GitHub
// source lists an organisation; a filesystem source reads a directory.
type Source interface {
	List(ctx context.Context) ([]string, error)
	Manifest(ctx context.Context, repo string) (publish.Manifest, error)
	// File reads one file from a repository — the transcript (005) and any other
	// committed artifact — for the read surface's pass-through. ok is false for a
	// file that is genuinely absent; a transport failure is an error, never a
	// disguised miss (the 003 read-error rule).
	File(ctx context.Context, repo, path string) ([]byte, bool, error)
	// Tree lists a repository's committed file paths, so the app can serve the
	// artifact file tree (006f) — an OSF-style pass-through that works even when
	// the underlying forge repository stays private.
	Tree(ctx context.Context, repo string) ([]string, error)
}

// PriorReproduction is one existing reproduction of a paper, read from a
// repository's manifest.
type PriorReproduction struct {
	Repo         string
	Created      time.Time
	MethodDigest string
	Verdict      provider.Grade
}

// Registry is derived from a Source, never stored. It is safe for concurrent
// use, and caches the derived snapshot for a short TTL.
type Registry struct {
	source Source
	ttl    time.Duration
	now    func() time.Time
	warn   func(string)

	suffix func(n int) (string, error)

	mu      sync.Mutex
	snap    *snapshot
	fetched time.Time
}

// Option configures a Registry.
type Option func(*Registry)

// WithTTL sets how long a snapshot is cached.
func WithTTL(d time.Duration) Option { return func(r *Registry) { r.ttl = d } }

// WithClock overrides the clock, for tests.
func WithClock(now func() time.Time) Option { return func(r *Registry) { r.now = now } }

// WithWarner installs a sink for skipped-repository warnings.
func WithWarner(f func(string)) Option { return func(r *Registry) { r.warn = f } }

// WithSuffixer overrides the suffix source, so a test can force a collision.
func WithSuffixer(f func(n int) (string, error)) Option {
	return func(r *Registry) { r.suffix = f }
}

// New builds a registry over source.
func New(source Source, opts ...Option) *Registry {
	r := &Registry{
		source: source, ttl: DefaultTTL, now: time.Now,
		warn: func(string) {}, suffix: publish.NewSuffixN,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// snapshot is one derived view of the organisation.
type snapshot struct {
	names []string            // every repository name, for collision checks
	byKey map[string][]record // identity string -> reproductions, earliest first
}

type record struct {
	manifest publish.Manifest
}

// load returns a fresh-enough snapshot, refetching when the TTL has lapsed. A
// repository with a missing or malformed manifest is skipped with a warning,
// not a failure, so the registry survives hand-edited repositories.
func (r *Registry) load(ctx context.Context) (*snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap != nil && r.now().Sub(r.fetched) < r.ttl {
		return r.snap, nil
	}
	names, err := r.source.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list reproductions: %w", err)
	}
	snap := &snapshot{names: names, byKey: map[string][]record{}}
	for _, name := range names {
		m, err := r.source.Manifest(ctx, name)
		if err != nil {
			r.warn(fmt.Sprintf("registry: skipping %s: %v", name, err))
			continue
		}
		key := identityKey(m)
		snap.byKey[key] = append(snap.byKey[key], record{manifest: m})
	}
	for key := range snap.byKey {
		recs := snap.byKey[key]
		sort.SliceStable(recs, func(i, j int) bool {
			return recs[i].manifest.Reproduction.Created.Before(recs[j].manifest.Reproduction.Created)
		})
	}
	r.snap = snap
	r.fetched = r.now()
	return snap, nil
}

// identityKey is the "scheme:key" identity string of a manifest — the same form
// paper.Identity.String produces, so lookups match on identity, never slug.
func identityKey(m publish.Manifest) string {
	return m.Paper.Scheme.String() + ":" + m.Paper.Key
}

// Lookup returns every prior reproduction of an identity, earliest first,
// matched on the identity key alone. Empty, not an error, when none exist.
func (r *Registry) Lookup(ctx context.Context, id paper.Identity) ([]PriorReproduction, error) {
	snap, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	recs := snap.byKey[id.String()]
	priors := make([]PriorReproduction, 0, len(recs))
	for _, rec := range recs {
		priors = append(priors, PriorReproduction{
			Repo:         rec.manifest.Reproduction.Repo,
			Created:      rec.manifest.Reproduction.Created,
			MethodDigest: rec.manifest.Reproduction.MethodDigest,
			Verdict:      rec.manifest.Reproduction.Verdict,
		})
	}
	return priors, nil
}

// CanonicalSlug returns the slug the earliest reproduction of an identity fixed,
// so a paper whose title later changed keeps its first slug. The bool is false
// when the identity has never been reproduced.
func (r *Registry) CanonicalSlug(ctx context.Context, id paper.Identity) (string, bool, error) {
	snap, err := r.load(ctx)
	if err != nil {
		return "", false, err
	}
	recs := snap.byKey[id.String()]
	if len(recs) == 0 {
		return "", false, nil
	}
	return recs[0].manifest.Paper.Slug, true, nil
}

// AllocateName returns "<canonical-slug>-<suffix>" for the next reproduction of
// an identity: the canonical slug (or the paper's own, for a first
// reproduction) with a suffix checked against every existing repository name.
// It retries on collision, widening the suffix from four to six characters
// after five attempts, so it never returns a name that already exists.
func (r *Registry) AllocateName(ctx context.Context, id paper.Identity) (string, error) {
	slug, ok, err := r.CanonicalSlug(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		slug = id.Slug
	}
	if slug == "" {
		return "", fmt.Errorf("cannot allocate a name for an identity with no slug")
	}

	snap, err := r.load(ctx)
	if err != nil {
		return "", err
	}
	taken := make(map[string]bool, len(snap.names))
	for _, n := range snap.names {
		taken[n] = true
	}

	for _, width := range []int{4, 4, 4, 4, 4, 6, 6, 6, 6, 6} {
		suffix, err := r.suffix(width)
		if err != nil {
			return "", err
		}
		name := slug + "-" + suffix
		if !taken[name] {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not allocate a free name for %q after widening", slug)
}
