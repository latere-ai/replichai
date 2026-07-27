package registry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

type fakeSource struct {
	names     []string
	manifests map[string]publish.Manifest
	errs      map[string]error
	files     map[string][]byte // "repo/path" -> content
	listCalls int
}

func (f *fakeSource) List(context.Context) ([]string, error) {
	f.listCalls++
	return f.names, nil
}

func (f *fakeSource) Manifest(_ context.Context, repo string) (publish.Manifest, error) {
	if e := f.errs[repo]; e != nil {
		return publish.Manifest{}, e
	}
	return f.manifests[repo], nil
}

func (f *fakeSource) File(_ context.Context, repo, path string) ([]byte, bool, error) {
	if raw, ok := f.files[repo+"/"+path]; ok {
		return raw, true, nil
	}
	return nil, false, nil
}

func (f *fakeSource) Tree(_ context.Context, repo string) ([]string, error) {
	var out []string
	for k := range f.files {
		if p, ok := strings.CutPrefix(k, repo+"/"); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func manifestOf(repo, key, slug string, created time.Time) publish.Manifest {
	return publish.Manifest{
		Paper: publish.ManifestPaper{Key: key, Scheme: paper.SchemeDOI, Slug: slug},
		Reproduction: publish.Reproduction{
			Repo: "open-hai/" + repo, Created: created, Verdict: provider.GradePassed,
		},
	}
}

func doiID(key string) paper.Identity { return paper.Identity{Key: key, Scheme: paper.SchemeDOI} }

var (
	t1 = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	t2 = t1.Add(24 * time.Hour)
)

// The PleaSQLarify case: one paper, two reproductions under different slugs
// (its two titles). Lookup must relate them by identity, never by slug.
func pleasQLarifySource() *fakeSource {
	return &fakeSource{
		names: []string{"repo-b", "repo-a", "other"},
		manifests: map[string]publish.Manifest{
			"repo-a": manifestOf("repo-a", "10.1145/x", "pleasqlarify-querying", t1),
			"repo-b": manifestOf("repo-b", "10.1145/x", "pleasqlarify-interfaces", t2),
			"other":  manifestOf("other", "10.1145/z", "another-paper", t1),
		},
	}
}

func TestLookupMatchesIdentityNotSlug(t *testing.T) {
	r := New(pleasQLarifySource())
	priors, err := r.Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(priors) != 2 {
		t.Fatalf("want 2 reproductions of one identity, got %d", len(priors))
	}
	// Earliest first, regardless of source order.
	if priors[0].Repo != "open-hai/repo-a" || priors[1].Repo != "open-hai/repo-b" {
		t.Errorf("not earliest-first: %v", []string{priors[0].Repo, priors[1].Repo})
	}
	// A different identity is not swept in.
	if got, _ := r.Lookup(context.Background(), doiID("10.1145/z")); len(got) != 1 {
		t.Errorf("other identity should have one reproduction, got %d", len(got))
	}
}

func TestCanonicalSlugFromEarliest(t *testing.T) {
	r := New(pleasQLarifySource())
	slug, ok, err := r.CanonicalSlug(context.Background(), doiID("10.1145/x"))
	if err != nil || !ok {
		t.Fatalf("CanonicalSlug: ok=%v err=%v", ok, err)
	}
	// Earliest reproduction fixed the slug, even though a later one differs.
	if slug != "pleasqlarify-querying" {
		t.Errorf("canonical slug = %q, want the earliest", slug)
	}
	if _, ok, _ := r.CanonicalSlug(context.Background(), doiID("10.9999/none")); ok {
		t.Error("an unreproduced identity must report absence, not a slug")
	}
}

func TestAllocateNameUsesCanonicalSlugAndAvoidsTaken(t *testing.T) {
	src := pleasQLarifySource()
	r := New(src)
	name, err := r.AllocateName(context.Background(), doiID("10.1145/x"))
	if err != nil {
		t.Fatalf("AllocateName: %v", err)
	}
	if !strings.HasPrefix(name, "pleasqlarify-querying-") {
		t.Errorf("name %q should use the canonical slug", name)
	}
	for _, taken := range src.names {
		if name == taken {
			t.Errorf("allocated an existing name %q", name)
		}
	}
}

func TestAllocateNameFirstReproductionUsesPaperSlug(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]publish.Manifest{}})
	id := paper.Identity{Key: "10.1145/new", Scheme: paper.SchemeDOI, Slug: "fresh-paper"}
	name, err := r.AllocateName(context.Background(), id)
	if err != nil {
		t.Fatalf("AllocateName: %v", err)
	}
	if !strings.HasPrefix(name, "fresh-paper-") {
		t.Errorf("first reproduction should use the paper's own slug, got %q", name)
	}
}

func TestAllocateNameRetriesAndWidens(t *testing.T) {
	src := &fakeSource{
		names:     []string{"fresh-paper-aaaa"}, // the four-char draw collides
		manifests: map[string]publish.Manifest{},
	}
	scripted := []string{"aaaa", "aaaa", "aaaa", "aaaa", "aaaa", "cccccc"}
	i := 0
	r := New(src, WithSuffixer(func(int) (string, error) {
		s := scripted[i]
		i++
		return s, nil
	}))
	id := paper.Identity{Key: "10.1145/new", Scheme: paper.SchemeDOI, Slug: "fresh-paper"}
	name, err := r.AllocateName(context.Background(), id)
	if err != nil {
		t.Fatalf("AllocateName: %v", err)
	}
	// Five colliding four-char draws, then a widened six-char one that is free.
	if name != "fresh-paper-cccccc" {
		t.Errorf("name = %q, want the widened suffix after exhausting four-char draws", name)
	}
}

func TestMalformedManifestIsSkippedNotFatal(t *testing.T) {
	var warned string
	src := &fakeSource{
		names:     []string{"good", "bad"},
		manifests: map[string]publish.Manifest{"good": manifestOf("good", "10.1145/x", "s", t1)},
		errs:      map[string]error{"bad": errors.New("malformed json")},
	}
	r := New(src, WithWarner(func(m string) { warned = m }))
	priors, err := r.Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil {
		t.Fatalf("a malformed repo must not fail the registry: %v", err)
	}
	if len(priors) != 1 {
		t.Errorf("the good reproduction should still be found, got %d", len(priors))
	}
	if !strings.Contains(warned, "bad") {
		t.Errorf("the skipped repo should be warned about, got %q", warned)
	}
}

func TestSnapshotIsCachedForTheTTL(t *testing.T) {
	now := t1
	src := pleasQLarifySource()
	r := New(src, WithClock(func() time.Time { return now }), WithTTL(time.Minute))

	_, _ = r.Lookup(context.Background(), doiID("10.1145/x"))
	_, _ = r.Lookup(context.Background(), doiID("10.1145/x"))
	if src.listCalls != 1 {
		t.Errorf("within the TTL the source should be read once, got %d", src.listCalls)
	}
	now = now.Add(2 * time.Minute)
	_, _ = r.Lookup(context.Background(), doiID("10.1145/x"))
	if src.listCalls != 2 {
		t.Errorf("past the TTL the source should be reread, got %d", src.listCalls)
	}
}

func TestLookupIsConcurrencySafe(t *testing.T) {
	r := New(pleasQLarifySource())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Lookup(context.Background(), doiID("10.1145/x")); err != nil {
				t.Errorf("Lookup: %v", err)
			}
		}()
	}
	wg.Wait()
}
