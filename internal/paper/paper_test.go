// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package paper

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeFetcher returns canned bytes for URLs matched by substring, so a test
// resolves with no real network. A non-nil err makes every fetch fail.
type fakeFetcher struct {
	responses map[string][]byte
	err       error
	calls     int
	// urls is every URL asked for, in order.
	urls []string
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	f.calls++
	f.urls = append(f.urls, url)
	if f.err != nil {
		return nil, f.err
	}
	for sub, body := range f.responses {
		if strings.Contains(url, sub) {
			return body, nil
		}
	}
	return nil, fmt.Errorf("fakeFetcher: no canned response for %s", url)
}

const theDOI = "10.1145/3772318.3791265"

// TestEveryLookupTravelsOverTLS checks that every metadata lookup uses TLS.
// The arXiv feed carries the DOI that decides a paper's identity, and a plain
// HTTP request lets anything on the path answer with a different paper.
func TestEveryLookupTravelsOverTLS(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": arxivFeedWithDOI(pleasQLarifyTitle, theDOI),
		"api.crossref.org": crossrefWork(pleasQLarifyTitle),
	}}
	if _, err := Resolve(context.Background(), "https://arxiv.org/abs/2603.01795", WithFetcher(fetch)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(fetch.urls) == 0 {
		t.Fatal("an arXiv reference resolved without a lookup")
	}
	for _, url := range fetch.urls {
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("lookup over plain http: %s", url)
		}
	}
}

func arxivFeedWithDOI(title, doi string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <title>` + title + `</title>
    <arxiv:doi>` + doi + `</arxiv:doi>
  </entry>
</feed>`)
}

// crossrefWork is a minimal Crossref /works response; an empty title yields an
// empty title array so the no-title fallback can be exercised.
func crossrefWork(title string) []byte {
	titles := "[]"
	if title != "" {
		titles = `["` + title + `"]`
	}
	return []byte(`{"message":{"title":` + titles + `}}`)
}

func arxivFeedNoDOI(title string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry><title>` + title + `</title></entry>
</feed>`)
}

// AC1: a DOI, an arXiv abstract URL, an arXiv PDF URL, and a bare arXiv ID for
// one paper all resolve to a single identity.
func TestResolveFourFormsOneIdentity(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": arxivFeedWithDOI(pleasQLarifyTitle, theDOI),
	}}
	refs := []string{
		theDOI,
		"https://arxiv.org/abs/2603.01795",
		"https://arxiv.org/pdf/2603.01795v1.pdf",
		"2603.01795",
	}
	var first Identity
	for i, ref := range refs {
		id, err := Resolve(context.Background(), ref, WithFetcher(fetch))
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		if id.Scheme != SchemeDOI || id.Key != theDOI {
			t.Errorf("Resolve(%q) = %s, want doi:%s", ref, id, theDOI)
		}
		if i == 0 {
			first = id
		} else if id.String() != first.String() {
			t.Errorf("Resolve(%q) = %s, differs from %s", ref, id, first)
		}
	}
}

// AC2: an arXiv entry carrying a DOI resolves to the DOI scheme; one carrying
// none resolves to the arXiv scheme, which is the case of a completed lookup
// with no DOI.
func TestResolveArXivUpgradesToDOI(t *testing.T) {
	withDOI := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": arxivFeedWithDOI(pleasQLarifyTitle, theDOI),
	}}
	id, err := Resolve(context.Background(), "arxiv:2603.01795", WithFetcher(withDOI))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("carrying a DOI: got %s, want doi:%s", id, theDOI)
	}

	preprint := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": arxivFeedNoDOI("A Preprint With No DOI"),
	}}
	id, err = Resolve(context.Background(), "arxiv:2603.01795", WithFetcher(preprint))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeArXiv || id.Key != "2603.01795" {
		t.Errorf("no DOI: got %s, want arxiv:2603.01795", id)
	}
}

// AC2, the fetch-failure case: a lookup that cannot complete returns an error
// rather than falling back to the arxiv scheme, so identity does not depend on
// connectivity.
func TestResolveArXivErrorsWhenLookupFails(t *testing.T) {
	// A failing fetcher.
	failing := &fakeFetcher{err: errors.New("network down")}
	if _, err := Resolve(context.Background(), "arxiv:2603.01795", WithFetcher(failing)); !errors.Is(err, ErrLookupFailed) {
		t.Errorf("failing lookup: err = %v, want ErrLookupFailed", err)
	}
	// No fetcher supplied at all.
	if _, err := Resolve(context.Background(), "arxiv:2603.01795"); !errors.Is(err, ErrLookupFailed) {
		t.Errorf("no fetcher: err = %v, want ErrLookupFailed", err)
	}
}

// AC3: the two PleaSQLarify title variants, the case that motivated this
// package, resolve to the same identity, and because they differ only after
// the 48-character limit, to the same slug.
func TestResolvePleaSQLarifyTitleDriftOneIdentity(t *testing.T) {
	const authorsTitle = "PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Interfaces"

	fromArXiv := &fakeFetcher{responses: map[string][]byte{
		"export.arxiv.org": arxivFeedWithDOI(pleasQLarifyTitle, theDOI),
	}}
	arxivID, err := Resolve(context.Background(), "arxiv:2603.01795", WithFetcher(fromArXiv))
	if err != nil {
		t.Fatalf("arXiv resolve: %v", err)
	}

	authorsPage := []byte(`<html><head>` +
		`<meta name="citation_doi" content="` + theDOI + `">` +
		`<title>` + authorsTitle + `</title></head><body>PleaSQLarify</body></html>`)
	fromSite := &fakeFetcher{responses: map[string][]byte{
		"authors.example": authorsPage,
	}}
	siteID, err := Resolve(context.Background(), "https://authors.example/pleasqlarify", WithFetcher(fromSite))
	if err != nil {
		t.Fatalf("authors-site resolve: %v", err)
	}

	// Same identity from two different titles. This is the motivating assertion.
	if arxivID.Key != siteID.Key || arxivID.Scheme != siteID.Scheme {
		t.Fatalf("identity drifted: %s vs %s", arxivID, siteID)
	}
	if arxivID.Title == siteID.Title {
		t.Fatalf("test is not exercising title drift: both titles are %q", arxivID.Title)
	}
	// Same slug: the titles differ only in a word past the truncation window.
	if arxivID.Slug != siteID.Slug {
		t.Errorf("slugs differ: %q vs %q; drift should be absorbed", arxivID.Slug, siteID.Slug)
	}
}

// TestSlugTracksTitleWhenDifferenceIsInWindow uses a synthetic pair that
// differs within the limit to show that a slug can also follow the title, so
// that coverage does not rest on the PleaSQLarify case.
func TestSlugTracksTitleWhenDifferenceIsInWindow(t *testing.T) {
	a := Slug("Retrieval Augmented Generation")
	b := Slug("Retrieval Augmented Summarisation")
	if a == b {
		t.Errorf("slugs should differ: %q vs %q", a, b)
	}
}

// AC6: a publisher URL whose fetch is blocked falls back to the identifier
// parsed from the URL, here the DOI in the path, rather than failing. The DOI
// is the final identity, so a blocked fetch yields exactly the identity a
// no-fetcher resolution would: same key, same key-derived slug, empty title.
// The fetch only ever attempts to enrich the title.
func TestResolvePublisherURLDegradesToURLDOI(t *testing.T) {
	const ref = "https://dl.acm.org/doi/" + theDOI

	offline, err := Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve (no fetcher): %v", err)
	}

	blocked := &fakeFetcher{err: errors.New("403 forbidden")}
	id, err := Resolve(context.Background(), ref, WithFetcher(blocked))
	if err != nil {
		t.Fatalf("Resolve (blocked): %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("got %s, want doi:%s", id, theDOI)
	}
	if !reflect.DeepEqual(id, offline) {
		t.Errorf("a blocked fetch must degrade to the offline identity: got %+v, want %+v", id, offline)
	}
}

// TestResolveDOIEnrichesViaCrossref checks that a DOI takes its title from
// Crossref, the canonical metadata service, that the key is unchanged, and
// that the title drives a readable slug, which is the repository name.
func TestResolveDOIEnrichesViaCrossref(t *testing.T) {
	crossref := &fakeFetcher{responses: map[string][]byte{
		"api.crossref.org": crossrefWork(pleasQLarifyTitle),
	}}
	id, err := Resolve(context.Background(), "doi:"+theDOI, WithFetcher(crossref))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("key drifted: got %s, want doi:%s", id, theDOI)
	}
	if id.Title != pleasQLarifyTitle {
		t.Errorf("title = %q, want the Crossref title", id.Title)
	}
	// The slug follows the title, giving a readable repository name. This is
	// safe because Crossref is deterministic, unlike a publisher page.
	if want := Slug(pleasQLarifyTitle); id.Slug != want {
		t.Errorf("slug = %q, want title-derived %q", id.Slug, want)
	}
}

// TestResolveDOIFallsBackToPublisherPage checks that when Crossref has no title
// and the reference is a URL, resolution falls back to the page's declared
// citation_title, and the title still drives the slug.
func TestResolveDOIFallsBackToPublisherPage(t *testing.T) {
	const ref = "https://dl.acm.org/doi/" + theDOI
	// Crossref returns an empty title set; the page declares one.
	fetch := &fakeFetcher{responses: map[string][]byte{
		"api.crossref.org": crossrefWork(""),
		"dl.acm.org":       []byte(`<meta name="citation_title" content="` + pleasQLarifyTitle + `">`),
	}}
	id, err := Resolve(context.Background(), ref, WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Title != pleasQLarifyTitle {
		t.Errorf("title = %q, want the page's declared title (Crossref fallback)", id.Title)
	}
	if want := Slug(pleasQLarifyTitle); id.Slug != want {
		t.Errorf("slug = %q, want title-derived %q", id.Slug, want)
	}
}

// TestResolveDOIWithoutFetcherIsOffline checks that the identity key never
// depends on the network: with no fetcher, a DOI resolves offline to its key
// and a key-derived slug, with no title. Key is likewise always offline; this
// test covers the Resolve side of that guarantee.
func TestResolveDOIWithoutFetcherIsOffline(t *testing.T) {
	id, err := Resolve(context.Background(), "doi:"+theDOI) // no fetcher
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeDOI || id.Key != theDOI {
		t.Errorf("got %s, want doi:%s", id, theDOI)
	}
	if id.Title != "" {
		t.Errorf("no fetcher must yield no title, got %q", id.Title)
	}
	if want := Slug(theDOI); id.Slug != want {
		t.Errorf("no-fetcher slug = %q, want key-derived %q", id.Slug, want)
	}
}

// A direct document URL with no identifier falls back to a content digest, and
// its slug falls back to the key when the page has no usable title.
func TestResolveDigestsWhenNoIdentifier(t *testing.T) {
	fetch := &fakeFetcher{responses: map[string][]byte{
		"example.org": []byte("raw document bytes with no doi and no title"),
	}}
	id, err := Resolve(context.Background(), "https://example.org/paper.pdf", WithFetcher(fetch))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Scheme != SchemeSHA256 {
		t.Fatalf("got scheme %s, want sha256", id.Scheme)
	}
	if len(id.Key) != 64 {
		t.Errorf("key %q is not a sha-256 hex digest", id.Key)
	}
	if id.Slug == "" {
		t.Error("slug should fall back to the key, not be empty")
	}
}

func TestResolveEmptyReference(t *testing.T) {
	if _, err := Resolve(context.Background(), "   "); !errors.Is(err, ErrUnresolvable) {
		t.Errorf("err = %v, want ErrUnresolvable", err)
	}
}

// TestKeyIsOfflineAndStable checks the offline guard key: it must never fetch,
// must fold trivial URL renderings to one key, must fail on an invalid
// reference, and for an arXiv or DOI reference must agree with Resolve's
// identity string, so the guard keys on the same paper the run records.
func TestKeyIsOfflineAndStable(t *testing.T) {
	cases := []struct {
		name, ref, want string
	}{
		{"doi", "https://doi.org/10.1145/3313831", "doi:10.1145/3313831"},
		{"arxiv abs", "https://arxiv.org/abs/2302.03714", "arxiv:2302.03714"},
		{"arxiv pdf same key", "https://arxiv.org/pdf/2302.03714", "arxiv:2302.03714"},
		{"bare arxiv id", "2302.03714", "arxiv:2302.03714"},
		{"url trailing slash", "https://example.com/paper/", "url:https://example.com/paper"},
		{"url fragment and case", "HTTPS://Example.com/paper#sec2", "url:https://example.com/paper"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Key(c.ref)
			if err != nil {
				t.Fatalf("Key(%q) errored: %v", c.ref, err)
			}
			if got != c.want {
				t.Errorf("Key(%q) = %q, want %q", c.ref, got, c.want)
			}
		})
	}
}

func TestKeyRejectsGarbage(t *testing.T) {
	for _, ref := range []string{"   ", "not a paper", "just some words"} {
		if _, err := Key(ref); !errors.Is(err, ErrUnresolvable) {
			t.Errorf("Key(%q) err = %v, want ErrUnresolvable", ref, err)
		}
	}
}

// TestKeyAgreesWithResolveForDOI checks that Key and Resolve agree on the
// canonical identity for the offline schemes, so guarding on Key and recording
// Resolve's identity never disagree about a DOI.
func TestKeyAgreesWithResolveForDOI(t *testing.T) {
	ref := "https://doi.org/10.1145/3313831"
	id, err := Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	key, err := Key(ref)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if key != id.String() {
		t.Errorf("Key = %q, Resolve identity = %q; want equal", key, id.String())
	}
}
