// Package seo writes the head of a document for a reader that never runs
// JavaScript: a crawler, a link unfurler, a citation tool.
//
// The app is a client-rendered SPA with one static head (042), so every address
// it serves — a published reproduction, a researcher's page, the registry —
// previews as the landing page in every client that unfurls a link, because
// none of them execute the script that would fix it. This package holds the
// vocabulary for saying something truer: one Page per address, rendered into
// the shell by internal/spa, and the paths that address is built from, shared
// with the sitemap so a canonical URL and a listed URL cannot disagree.
//
// It is deliberately domain-free. What a project's title is belongs to the
// router that can read one; what a title looks like in a head belongs here.
package seo

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"
)

// DefaultBase is where the app lives when nothing configured a base URL. A
// canonical link is resolved by a crawler against nothing, so it must be
// absolute even in a deployment that never told the server its own address.
const DefaultBase = "https://replichai.latere.ai"

// SiteName is the og:site_name every card carries.
const SiteName = "ReplicHAI"

// CardImage is the one social card the whole site shares, as a path. There is
// no per-page picture: the image is the product's mark, and a generated card
// per project would be a rendering pipeline for a thumbnail nobody reads.
const CardImage = "/og.png"

// Page is everything an injected head says. A zero Page with NoIndex set is the
// other legal shape: the shell keeps its default head and the response is
// marked not to be indexed, which is what a private or unknown record gets.
type Page struct {
	Title       string
	Description string
	Canonical   string // absolute; the og:url too, always
	Type        string // og:type — "website", "article", "profile"
	// Image is the absolute card URL. Empty falls back to the site's own, so a
	// Page built by hand in a test still renders a valid card.
	Image string
	// JSONLD is a schema.org document rendered into an ld+json script. Nil
	// writes no script rather than an empty one.
	JSONLD any
	// NoIndex marks the response with X-Robots-Tag and a robots meta. It is set
	// on anything a stranger cannot read and on every screen only an operator
	// can render — pages that exist, and that a search result must never offer.
	NoIndex bool
}

// Site turns a path into the absolute URL a crawler needs, from the deployment's
// configured base. It is the single place that join happens, so the canonical in
// a head and the entry in the sitemap are the same string by construction.
type Site struct{ Base string }

// NewSite trims a configured base URL to the form Abs expects; an empty one
// falls back to production, because a document is served from somewhere.
func NewSite(base string) Site {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultBase
	}
	return Site{Base: base}
}

// Abs is the absolute URL of an app path.
func (s Site) Abs(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return s.Base + path
}

/* ─── The paths, mirroring the client's pathOf (013) ─── */

// LandingPath, RegistryPath, ProjectPath, SessionPath and ProfilePath are the
// addresses of the four public screens. They mirror `pathOf` in
// frontend/src/routes.ts — the client's single mapping stays the client's, and
// this copy exists because nothing in the frontend can run before the response
// is written. routes_test.go fails when the two disagree.
const (
	LandingPath  = "/"
	RegistryPath = "/registry"
)

// ProjectPath is a project's own address. A tab (`/verdict`, `/people`) is a
// view of this record, not a second one, so a tab canonicalises to here.
func ProjectPath(id string) string { return "/p/" + id }

// SessionPath is a session's address under its project, for the same reason.
func SessionPath(projectID, jobID string) string { return "/p/" + projectID + "/s/" + jobID }

// ProfilePath is a researcher's public page (006r).
func ProfilePath(handle string) string { return "/u/" + handle }

/* ─── Rendering ─── */

// Head renders the tags that replace the shell's default head region. It is the
// only place in the codebase that knows these tag names.
//
// Every value is escaped here rather than by its caller: titles and descriptions
// are researcher-supplied text, and a title containing a quote or a `</script>`
// is ordinary, not hostile.
func (p Page) Head() string {
	var b strings.Builder
	title := p.Title
	if title == "" {
		title = SiteName
	}
	kind := p.Type
	if kind == "" {
		kind = "website"
	}
	img := p.Image
	if img == "" {
		img = DefaultBase + CardImage
	}

	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n    ")
	if p.Description != "" {
		meta(&b, "name", "description", p.Description)
	}
	if p.Canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + html.EscapeString(p.Canonical) + "\" />\n    ")
	}
	if p.NoIndex {
		meta(&b, "name", "robots", "noindex, follow")
	}
	meta(&b, "property", "og:type", kind)
	meta(&b, "property", "og:site_name", SiteName)
	meta(&b, "property", "og:title", title)
	if p.Description != "" {
		meta(&b, "property", "og:description", p.Description)
	}
	if p.Canonical != "" {
		meta(&b, "property", "og:url", p.Canonical)
	}
	meta(&b, "property", "og:image", img)
	meta(&b, "property", "og:image:width", "1200")
	meta(&b, "property", "og:image:height", "630")
	meta(&b, "name", "twitter:card", "summary_large_image")
	meta(&b, "name", "twitter:title", title)
	if p.Description != "" {
		meta(&b, "name", "twitter:description", p.Description)
	}
	meta(&b, "name", "twitter:image", img)
	if p.JSONLD != nil {
		// json.Marshal escapes <, > and & by default, so a `</script>` inside a
		// researcher's title cannot close this element.
		if doc, err := json.Marshal(p.JSONLD); err == nil {
			b.WriteString(`<script type="application/ld+json">` + string(doc) + "</script>\n    ")
		}
	}
	return strings.TrimRight(b.String(), " \n")
}

func meta(b *strings.Builder, attr, name, content string) {
	b.WriteString(`<meta ` + attr + `="` + name + `" content="` + html.EscapeString(content) + "\" />\n    ")
}

/* ─── Carrying a page from the router to the shell ─── */

type key struct{}

// With attaches the page an address resolved to. The router computes it (only
// the router can read a project); internal/spa renders it (only the shell knows
// where the head is). They share a type rather than a callback, so neither
// package has to know how the other is wired.
func With(ctx context.Context, p Page) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// From reads the page a request resolved to, if any. A request with none gets
// the shell's default head, which is every address this package says nothing
// about.
func From(r *http.Request) (Page, bool) {
	p, ok := r.Context().Value(key{}).(Page)
	return p, ok
}

/* ─── Rendering the shell ─── */

// The markers around the head region index.html hands over. Everything between
// them is what the document says about itself; everything outside — the icon,
// the font preload, the theme script — is the shell's and is never touched.
var (
	headOpen  = []byte("<!--seo-->")
	headClose = []byte("<!--/seo-->")
)

// WriteShell writes the app shell for this request, with the head of whatever
// page the request resolved to and the header that page needs. It is the one
// place a described document is produced, so what the server serves and what a
// test asserts cannot be two implementations of the same rule.
func WriteShell(w http.ResponseWriter, r *http.Request, index []byte) {
	page, described := From(r)
	if described && page.NoIndex {
		// A page that exists and must never be offered in a search result:
		// somebody's private project, an operator screen a signed-out reader
		// renders as nothing. This header governs a crawler that already holds
		// the URL, which robots.txt cannot.
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	_, _ = w.Write(Describe(index, page, described))
}

// Describe substitutes the head region for a page the router resolved. A page
// with no title (the not-indexed case) keeps the default head: there is nothing
// truer to say about an address a reader may not read, and the response is
// already marked.
func Describe(index []byte, p Page, described bool) []byte {
	if !described || p.Title == "" {
		return index
	}
	start := bytes.Index(index, headOpen)
	end := bytes.Index(index, headClose)
	// A shell without the markers is one nobody built from this repository —
	// the committed placeholder, or a stale dist/. Serving it unchanged is the
	// only honest answer; the alternative is a document with two titles.
	if start < 0 || end < start {
		return index
	}
	head := p.Head()
	out := make([]byte, 0, len(index)+len(head))
	out = append(out, index[:start+len(headOpen)]...)
	out = append(out, '\n', ' ', ' ', ' ', ' ')
	out = append(out, head...)
	out = append(out, '\n', ' ', ' ', ' ', ' ')
	return append(out, index[end:]...)
}
