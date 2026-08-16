// Package paper resolves a paper reference to a stable identity and a
// human-readable slug.
//
// Identity answers "is this the same paper we already audited?" and must not
// drift: it is a resolved external identifier — a DOI, else an arXiv ID, else a
// content digest — never the title, which changes between preprint and
// camera-ready and across indexes. The slug answers "what should the repository
// be called?" and is presentation only; it never determines identity.
//
// A DOI is terminal for the *identity key*, which is derived offline. Its human
// title — and therefore its slug — is enriched from Crossref (the canonical DOI
// metadata service, reliable where publisher pages bot-block) when a fetcher is
// present; without one, or on a failed lookup, the slug degrades to the key. An
// arXiv reference is upgraded: its published DOI is looked up so the preprint and
// the published version collapse to one identity, and a lookup that cannot
// complete errors rather than guessing arxiv-scheme, so identity never drifts
// with connectivity. The network is reached through the Fetcher seam alone, which
// a test injects to run without one.
package paper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Errors returned by this package.
var (
	// ErrUnresolvable reports a reference that carries no identifier and could
	// not be resolved to one.
	ErrUnresolvable = errors.New("cannot resolve reference to a paper identity")
	// ErrLookupFailed reports that an arXiv reference's DOI lookup could not
	// complete. Resolution fails rather than falling back to arxiv-scheme, so a
	// paper with a DOI is never recorded as a preprint on a bad network.
	ErrLookupFailed = errors.New("arxiv metadata lookup could not complete")
)

// arxivAPI is the metadata endpoint an arXiv ID is looked up against.
const arxivAPI = "http://export.arxiv.org/api/query?id_list="

// crossrefAPI is the canonical DOI metadata endpoint a DOI's title is read from.
const crossrefAPI = "https://api.crossref.org/works/"

// Identity is a paper's stable identity together with its presentation slug.
//
// Key is the bare identifier; Scheme names its kind. The Scheme:Key rendering,
// from String, is presentation only.
type Identity struct {
	Key     string
	Scheme  Scheme
	Title   string
	Slug    string
	Sources []string
	// Record is the paper's bibliography, as far as the source that named it
	// declares one (037). Empty for a reference nothing could be looked up for,
	// and never guessed.
	Record Record
}

// String renders the canonical "scheme:key" identity string.
func (id Identity) String() string { return id.Scheme.String() + ":" + id.Key }

// Fetcher retrieves the bytes at a URL. It is the one network seam in this
// package: injecting one lets resolution run without real network access, and
// its absence makes a network-required resolution fail rather than guess.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// FetcherFunc adapts a function to the Fetcher interface.
type FetcherFunc func(ctx context.Context, url string) ([]byte, error)

// Fetch calls f.
func (f FetcherFunc) Fetch(ctx context.Context, url string) ([]byte, error) { return f(ctx, url) }

// Option configures resolution.
type Option func(*resolver)

// WithFetcher supplies the network seam resolution uses to look up arXiv
// metadata and to inspect publisher pages.
func WithFetcher(f Fetcher) Option { return func(r *resolver) { r.fetch = f } }

type resolver struct {
	fetch Fetcher
}

// Resolve turns a reference — a DOI, an arXiv URL or bare ID, a publisher URL,
// or a direct document URL — into an Identity. It never mutates state.
func Resolve(ctx context.Context, reference string, opts ...Option) (Identity, error) {
	r := &resolver{}
	for _, o := range opts {
		o(r)
	}
	ref := strings.TrimSpace(reference)
	if ref == "" {
		return Identity{}, fmt.Errorf("%w: empty reference", ErrUnresolvable)
	}

	// A DOI anywhere in the reference is the terminal identity key, derived
	// offline: Key() never fetches, and a resolution with no fetcher stays offline
	// with a key-based slug, so identity never rides connectivity. With a fetcher,
	// enrich the human title from Crossref — the canonical DOI metadata service,
	// reliable where publisher pages bot-block — falling back to the publisher page
	// when the reference is a URL. A resolved title drives the slug (a readable
	// repository name) and the project title; a failed lookup degrades to the
	// key-based slug, so a blocked network yields the same identity a no-fetcher
	// resolution would.
	if doi, ok := extractDOI(ref); ok {
		title, rec := "", Record{}
		if r.fetch != nil {
			title, rec = r.metaFromCrossref(ctx, doi)
			// The publisher's own page is the second source, for a DOI Crossref
			// does not answer for and for the fields its record leaves out.
			if (title == "" || rec.Empty()) && looksLikeURL(ref) {
				pageTitle, pageRec := r.metaFromPage(ctx, ref)
				title = firstNonEmpty(title, pageTitle)
				rec = mergeRecord(rec, pageRec)
			}
		}
		return r.finish(SchemeDOI, doi, title, ref, rec), nil
	}

	// An arXiv ID is not terminal: look it up to discover a possible DOI.
	if id, ok := extractArXiv(ref); ok {
		return r.resolveArXiv(ctx, id, ref)
	}

	// A URL with no embedded identifier: fetch and inspect it, else digest it.
	if looksLikeURL(ref) {
		return r.resolveURL(ctx, ref)
	}

	return Identity{}, fmt.Errorf("%w: %q is not a DOI, arXiv ID, or URL", ErrUnresolvable, ref)
}

// Key extracts a paper's stable identity offline — with no network — for the
// double-trigger guard and the project a submit opens. A DOI or arXiv ID in the
// reference yields its canonical "scheme:key"; a plain URL yields a normalised
// "url:<ref>" so the same link submitted twice keys alike; a reference that is
// neither a DOI, an arXiv ID, nor URL-shaped is rejected, so a garbage submit
// fails fast at the edge. It never fetches, so a submit never rides arXiv's
// availability — the worker's Resolve phase does the enriching lookup, and the
// key it guards on need not equal the identity that lookup finally records.
func Key(reference string) (string, error) {
	ref := strings.TrimSpace(reference)
	if ref == "" {
		return "", fmt.Errorf("%w: empty reference", ErrUnresolvable)
	}
	if doi, ok := extractDOI(ref); ok {
		return SchemeDOI.String() + ":" + doi, nil
	}
	if id, ok := extractArXiv(ref); ok {
		return SchemeArXiv.String() + ":" + id, nil
	}
	if looksLikeURL(ref) {
		return "url:" + normalizeURLKey(ref), nil
	}
	return "", fmt.Errorf("%w: %q is not a DOI, arXiv ID, or URL", ErrUnresolvable, ref)
}

// normalizeURLKey folds a URL to a stable guard key: lower-cased scheme and
// host, no fragment, no trailing slash — so http/HTTPS, a stray fragment, or a
// trailing slash on the same link do not read as two papers. It is deliberately
// conservative: distinct URLs for the same paper are not collapsed (that is the
// worker's resolving job), only trivially different renderings of one link.
func normalizeURLKey(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return strings.TrimRight(ref, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// finish assembles an Identity, deriving the slug from the title and falling
// back to a slug of the key when the title folds to nothing (spec AC5).
func (r *resolver) finish(scheme Scheme, key, title, source string, rec Record) Identity {
	id := Identity{Key: key, Scheme: scheme, Title: collapseWS(title), Record: rec}
	id.Slug = Slug(id.Title)
	if id.Slug == "" {
		id.Slug = Slug(key)
	}
	if source != "" {
		id.Sources = []string{source}
	}
	return id
}

// resolveArXiv upgrades an arXiv ID to its DOI when the paper carries one.
func (r *resolver) resolveArXiv(ctx context.Context, id, source string) (Identity, error) {
	if r.fetch == nil {
		return Identity{}, fmt.Errorf("%w: arXiv %s needs a fetcher", ErrLookupFailed, id)
	}
	meta, err := r.fetchArXivMeta(ctx, id)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: arXiv %s: %w", ErrLookupFailed, id, err)
	}
	if meta.DOI != "" {
		doi, ok := extractDOI(meta.DOI)
		if !ok {
			doi = strings.ToLower(strings.TrimSpace(meta.DOI))
		}
		// The DOI is now the identity, so Crossref is the better source for its
		// record; the preprint's own authors and year fill what Crossref omits.
		title, rec := r.metaFromCrossref(ctx, doi)
		return r.finish(SchemeDOI, doi, firstNonEmpty(title, meta.Title), source, mergeRecord(rec, meta.Record)), nil
	}
	return r.finish(SchemeArXiv, id, meta.Title, source, meta.Record), nil
}

// resolveURL handles a URL with no identifier in the string itself. It fetches
// the document and reads the Highwire / Google Scholar citation_* meta tags
// academic publishers emit — a structured, per-page declaration of the paper's
// own identity, not a scrape of the freeform body. A declared DOI wins; a
// declared arXiv ID re-enters the deterministic arXiv lookup; a page with
// neither falls back to a content digest. It needs a fetcher; without one, or
// on a fetch that fails, there is nothing to inspect and resolution errors.
func (r *resolver) resolveURL(ctx context.Context, ref string) (Identity, error) {
	if r.fetch == nil {
		return Identity{}, fmt.Errorf("%w: %q needs a fetcher", ErrUnresolvable, ref)
	}
	body, err := r.fetch.Fetch(ctx, ref)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: fetching %q: %w", ErrUnresolvable, ref, err)
	}
	title := titleFromBody(body)
	rec := recordFromBody(body)

	if doi := metaContent(body, citationDOIRe); doi != "" {
		if normalised, ok := extractDOI(doi); ok {
			crossTitle, crossRec := r.metaFromCrossref(ctx, normalised)
			return r.finish(SchemeDOI, normalised, firstNonEmpty(crossTitle, title), ref, mergeRecord(crossRec, rec)), nil
		}
	}
	if raw := metaContent(body, citationArxivRe); raw != "" {
		if id, ok := extractArXiv(raw); ok {
			return r.resolveArXiv(ctx, id, ref)
		}
	}

	sum := sha256.Sum256(body)
	return r.finish(SchemeSHA256, hex.EncodeToString(sum[:]), title, ref, rec), nil
}

type arxivMeta struct {
	Title, DOI string
	Record     Record
}

// arxivFeed is the fragment of the arXiv Atom response resolution reads. The
// element names match on local name, so the arxiv: namespace prefix on <doi>
// needs no special handling.
type arxivFeed struct {
	Entries []struct {
		Title     string `xml:"title"`
		DOI       string `xml:"doi"`
		Published string `xml:"published"`
		// A preprint updated after acceptance carries where it appeared.
		JournalRef string `xml:"journal_ref"`
		Authors    []struct {
			Name string `xml:"name"`
		} `xml:"author"`
	} `xml:"entry"`
}

func (r *resolver) fetchArXivMeta(ctx context.Context, id string) (arxivMeta, error) {
	body, err := r.fetch.Fetch(ctx, arxivAPI+id)
	if err != nil {
		return arxivMeta{}, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return arxivMeta{}, fmt.Errorf("parsing arXiv metadata: %w", err)
	}
	if len(feed.Entries) == 0 {
		return arxivMeta{}, errors.New("no entry in arXiv response")
	}
	e := feed.Entries[0]
	rec := Record{Venue: collapseWS(e.JournalRef), Year: yearFrom(e.Published)}
	for _, a := range e.Authors {
		if n := collapseWS(a.Name); n != "" {
			rec.Authors = append(rec.Authors, n)
		}
	}
	return arxivMeta{Title: collapseWS(e.Title), DOI: strings.TrimSpace(e.DOI), Record: rec}, nil
}

var (
	doiRe           = regexp.MustCompile(`10\.\d{4,9}/[^\s"'<>#?]+`)
	arxivCtxRe      = regexp.MustCompile(`(?i)arxiv(?:\.org)?(?:/(?:abs|pdf))?[:/]\s*(\d{4}\.\d{4,5})`)
	bareArxivRe     = regexp.MustCompile(`^(\d{4}\.\d{4,5})(?:v\d+)?$`)
	citationDOIRe   = metaRe(`citation_doi`)
	citationArxivRe = metaRe(`citation_arxiv_id`)
	citationTitleRe = metaRe(`citation_title`)
	htmlTitleRe     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// extractDOI finds and normalises a DOI in a string: lowercased, trailing
// sentence punctuation trimmed. A DOI is case-insensitive, so lowercasing is
// the normal form that makes two spellings one identity.
func extractDOI(s string) (string, bool) {
	m := doiRe.FindString(s)
	if m == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimRight(m, ".,);:]")), true
}

// extractArXiv finds a versionless arXiv ID, either in an arxiv: / arxiv.org
// context or as a bare id that is the whole reference. The version suffix is
// dropped so v1 and v2 are one paper.
func extractArXiv(ref string) (string, bool) {
	if m := arxivCtxRe.FindStringSubmatch(ref); m != nil {
		return strings.ToLower(m[1]), true
	}
	if m := bareArxivRe.FindStringSubmatch(strings.TrimSpace(ref)); m != nil {
		return strings.ToLower(m[1]), true
	}
	return "", false
}

// metaContent returns the trimmed content of the first meta tag matched by re,
// or "" if none. re must capture the content attribute in group 1.
func metaContent(body []byte, re *regexp.Regexp) string {
	if m := re.FindSubmatch(body); m != nil {
		return collapseWS(captured(m))
	}
	return ""
}

// titleFromCrossref asks Crossref — the canonical DOI metadata service — for a
// DOI's title. It is the primary title source for a DOI because it is reliable
// and unblocked where publisher pages often bot-block. Returns "" on any failure
// (unreachable, not found, malformed): enriching the title is best-effort and
// must never fail a resolution that already has its key.
func (r *resolver) metaFromCrossref(ctx context.Context, doi string) (string, Record) {
	body, err := r.fetch.Fetch(ctx, crossrefAPI+doi)
	if err != nil {
		return "", Record{}
	}
	return parseCrossref(body)
}

// titleFromBody reads a page's declared paper title — the Highwire
// citation_title meta tag academic publishers emit, else the HTML <title> — or
// "" when it declares neither.
func titleFromBody(body []byte) string {
	return firstNonEmpty(metaContent(body, citationTitleRe), htmlTitle(body))
}

// metaFromPage fetches a publisher URL and returns what it declares about
// itself: its paper's title and its bibliography, or zero values when the page
// cannot be fetched or declares neither. It never errors: enrichment is
// best-effort and must not disturb a resolution that already has its key and
// slug from the DOI in the reference.
func (r *resolver) metaFromPage(ctx context.Context, ref string) (string, Record) {
	body, err := r.fetch.Fetch(ctx, ref)
	if err != nil {
		return "", Record{}
	}
	return titleFromBody(body), recordFromBody(body)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func htmlTitle(body []byte) string {
	if m := htmlTitleRe.FindSubmatch(body); m != nil {
		return collapseWS(string(m[1]))
	}
	return ""
}

func looksLikeURL(ref string) bool {
	return strings.Contains(ref, "://") || strings.HasPrefix(ref, "www.")
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }
