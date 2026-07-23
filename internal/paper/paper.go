// Package paper resolves a paper reference to a stable identity and a
// human-readable slug.
//
// Identity answers "is this the same paper we already audited?" and must not
// drift: it is a resolved external identifier — a DOI, else an arXiv ID, else a
// content digest — never the title, which changes between preprint and
// camera-ready and across indexes. The slug answers "what should the repository
// be called?" and is presentation only; it never determines identity.
//
// A DOI is terminal and resolves with no network. An arXiv reference is
// upgraded: its published DOI is looked up so the preprint and the published
// version collapse to one identity, and a lookup that cannot complete errors
// rather than guessing arxiv-scheme, so identity never drifts with
// connectivity. The network is reached through the Fetcher seam alone, which a
// test injects to run without one.
package paper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
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

// PageResolver proposes a paper identifier from a fetched page whose URL
// carries none. What it returns is a *candidate*, not identity: resolveURL
// re-extracts it and checks it actually occurs in the page, so no resolver —
// deterministic or agent-backed — can name an identifier the document does not
// contain. That is what lets a nondeterministic agent read arbitrary sites
// without letting identity drift between runs.
type PageResolver interface {
	// Identify inspects a fetched page and returns a reference candidate — a
	// DOI, an arXiv ID, or "" when it finds none — and a best-effort title.
	Identify(ctx context.Context, url string, body []byte) (candidate, title string, err error)
}

// Option configures resolution.
type Option func(*resolver)

// WithFetcher supplies the network seam resolution uses to look up arXiv
// metadata and to inspect publisher pages.
func WithFetcher(f Fetcher) Option { return func(r *resolver) { r.fetch = f } }

// WithPageResolver overrides how a URL with no embedded identifier is resolved.
// The default reads the citation_* meta standard; an AgentPageResolver reads
// the page with an LLM. Either way the candidate is verified against the page.
func WithPageResolver(p PageResolver) Option { return func(r *resolver) { r.page = p } }

type resolver struct {
	fetch Fetcher
	page  PageResolver
}

// Resolve turns a reference — a DOI, an arXiv URL or bare ID, a publisher URL,
// or a direct document URL — into an Identity. It never mutates state.
func Resolve(ctx context.Context, reference string, opts ...Option) (Identity, error) {
	r := &resolver{page: metaPageResolver{}}
	for _, o := range opts {
		o(r)
	}
	ref := strings.TrimSpace(reference)
	if ref == "" {
		return Identity{}, fmt.Errorf("%w: empty reference", ErrUnresolvable)
	}

	// A DOI anywhere in the reference is terminal: highest priority, and
	// nothing can enrich it, so it resolves with no network (spec AC7).
	if doi, ok := extractDOI(ref); ok {
		return r.finish(SchemeDOI, doi, "", ref), nil
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

// finish assembles an Identity, deriving the slug from the title and falling
// back to a slug of the key when the title folds to nothing (spec AC5).
func (r *resolver) finish(scheme Scheme, key, title, source string) Identity {
	id := Identity{Key: key, Scheme: scheme, Title: collapseWS(title)}
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
		return r.finish(SchemeDOI, doi, meta.Title, source), nil
	}
	return r.finish(SchemeArXiv, id, meta.Title, source), nil
}

// resolveURL handles a URL with no identifier in the string itself: it fetches
// the document, asks the page resolver for a candidate identifier, verifies the
// candidate actually occurs in the page, and digests the content as a last
// resort. It needs a fetcher; without one, or on a fetch that fails, there is
// nothing to inspect and resolution errors.
func (r *resolver) resolveURL(ctx context.Context, ref string) (Identity, error) {
	if r.fetch == nil {
		return Identity{}, fmt.Errorf("%w: %q needs a fetcher", ErrUnresolvable, ref)
	}
	body, err := r.fetch.Fetch(ctx, ref)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: fetching %q: %w", ErrUnresolvable, ref, err)
	}
	candidate, title, err := r.page.Identify(ctx, ref, body)
	if err != nil {
		// A resolver that could not decide is not licence to guess: erroring
		// keeps a transient failure from minting a digest identity for a paper
		// that has a DOI.
		return Identity{}, fmt.Errorf("%w: resolving %q: %w", ErrUnresolvable, ref, err)
	}
	title = firstNonEmpty(title, htmlTitle(body))
	lowerBody := strings.ToLower(string(body))

	// Verify the candidate against the page: a DOI or arXiv ID is honoured only
	// if the document actually contains it, so a hallucinated one is rejected.
	if doi, ok := extractDOI(candidate); ok && strings.Contains(lowerBody, doi) {
		return r.finish(SchemeDOI, doi, title, ref), nil
	}
	if id, ok := extractArXiv(candidate); ok && strings.Contains(lowerBody, id) {
		return r.resolveArXiv(ctx, id, ref) // re-enters the deterministic arXiv path
	}

	sum := sha256.Sum256(body)
	return r.finish(SchemeSHA256, hex.EncodeToString(sum[:]), title, ref), nil
}

// metaPageResolver is the default: it reads the Highwire / Google Scholar
// citation_* meta tags academic publishers emit. It never calls the network and
// never guesses — a page without the tags yields no candidate, which resolves
// to a content digest.
type metaPageResolver struct{}

// Identify returns the page's declared DOI (preferred) or arXiv ID, and title.
func (metaPageResolver) Identify(_ context.Context, _ string, body []byte) (string, string, error) {
	title := metaContent(body, citationTitleRe)
	if title == "" {
		title = htmlTitle(body)
	}
	if doi := metaContent(body, citationDOIRe); doi != "" {
		return doi, title, nil
	}
	if id := metaContent(body, citationArxivRe); id != "" {
		return id, title, nil
	}
	return "", title, nil
}

type arxivMeta struct{ Title, DOI string }

// arxivFeed is the fragment of the arXiv Atom response resolution reads. The
// element names match on local name, so the arxiv: namespace prefix on <doi>
// needs no special handling.
type arxivFeed struct {
	Entries []struct {
		Title string `xml:"title"`
		DOI   string `xml:"doi"`
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
	return arxivMeta{Title: collapseWS(e.Title), DOI: strings.TrimSpace(e.DOI)}, nil
}

var (
	doiRe           = regexp.MustCompile(`10\.\d{4,9}/[^\s"'<>#?]+`)
	arxivCtxRe      = regexp.MustCompile(`(?i)arxiv(?:\.org)?(?:/(?:abs|pdf))?[:/]\s*(\d{4}\.\d{4,5})`)
	bareArxivRe     = regexp.MustCompile(`^(\d{4}\.\d{4,5})(?:v\d+)?$`)
	citationDOIRe   = regexp.MustCompile(`(?i)<meta[^>]+name=["']citation_doi["'][^>]+content=["']([^"']+)["']`)
	citationArxivRe = regexp.MustCompile(`(?i)<meta[^>]+name=["']citation_arxiv_id["'][^>]+content=["']([^"']+)["']`)
	citationTitleRe = regexp.MustCompile(`(?i)<meta[^>]+name=["']citation_title["'][^>]+content=["']([^"']+)["']`)
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
		return collapseWS(string(m[1]))
	}
	return ""
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
