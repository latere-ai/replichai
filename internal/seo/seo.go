// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package seo writes the head of a document for clients that do not run
// JavaScript: crawlers, link unfurlers and citation tools.
//
// The app is a client-rendered SPA with one static head (042). Without this
// package every address it serves, whether a published reproduction, a
// researcher's page or the registry, previews as the landing page in any
// client that unfurls a link, because none of them run the script that would
// set the head. This package defines one Page per address, rendered into the
// shell by internal/spa, and the path constructors an address is built from.
// The sitemap uses the same constructors, so a canonical URL and a listed URL
// cannot disagree.
//
// The package is domain-free. The router reads a project and decides its
// title; this package decides how a title is rendered in a head.
package seo

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// DefaultBase is the base URL used when none is configured. A crawler has no
// base to resolve a canonical link against, so the link must be absolute even
// in a deployment that never configured its own address.
const DefaultBase = "https://replichai.latere.ai"

// SiteName is the og:site_name every card carries.
const SiteName = "ReplicHAI"

// CardImage is the path of the one social card the whole site shares. There is
// no per-page image: the card is the product's mark, and generating one per
// project would need a rendering pipeline for a thumbnail with little value.
const CardImage = "/og.png"

// Page is the content of an injected head. A zero Page with NoIndex set is the
// other valid shape: the shell keeps its default head and the response is
// marked not to be indexed. A private or unknown record gets that shape.
type Page struct {
	Title       string
	Description string
	Canonical   string // absolute; also used as og:url
	Type        string // og:type: "website", "article" or "profile"
	// Image is the absolute card URL. Empty falls back to the site's own card,
	// so a Page built by hand in a test still renders a valid card.
	Image string
	// JSONLD is a schema.org document rendered into an ld+json script. Nil
	// writes no script rather than an empty one.
	JSONLD any
	// NoIndex marks the response with X-Robots-Tag and a robots meta. It is set
	// on anything an anonymous reader cannot read and on every screen only an
	// operator can render: pages that exist but must not appear in a search
	// result.
	NoIndex bool
}

// Site resolves an app path to the absolute URL a crawler needs, from the
// deployment's configured base. It is the only place that join happens, so the
// canonical in a head and the entry in the sitemap are the same string by
// construction.
type Site struct{ Base string }

// NewSite trims a configured base URL to the form Abs expects. An empty base
// falls back to DefaultBase.
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
// addresses of the public screens. They mirror pathOf in
// frontend/src/routes.ts, which remains the client's single mapping. This copy
// exists because nothing in the frontend can run before the response is
// written. routes_test.go fails when the two disagree.
const (
	LandingPath  = "/"
	RegistryPath = "/registry"
)

// ProjectPath is a project's address. A tab such as `/verdict` or `/people` is
// a view of the same record, not a second record, so a tab canonicalises to
// this path.
func ProjectPath(id string) string { return "/p/" + id }

// SessionPath is a session's address under its project. Its tabs canonicalise
// to it for the same reason.
func SessionPath(projectID, jobID string) string { return "/p/" + projectID + "/s/" + jobID }

// ProfilePath is a researcher's public page (006r).
func ProfilePath(handle string) string { return "/u/" + handle }

/* ─── Rendering ─── */

// Head renders the tags that replace the shell's default head region. It is the
// only place in the codebase that knows these tag names.
//
// Every value is escaped here rather than by the caller: titles and
// descriptions are researcher-supplied text, and a title containing a quote or
// a `</script>` is ordinary input, not an attack.
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

// With attaches the page an address resolved to. The router computes it, since
// only the router can read a project. internal/spa renders it, since only the
// shell knows where the head is. They share a type rather than a callback, so
// neither package depends on how the other is wired.
func With(ctx context.Context, p Page) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// From reads the page a request resolved to, if any. A request with none gets
// the shell's default head; that covers every address this package does not
// describe.
func From(r *http.Request) (Page, bool) {
	p, ok := r.Context().Value(key{}).(Page)
	return p, ok
}

/* ─── Rendering the shell ─── */

// headOpen and headClose mark the head region index.html hands over.
// Everything between them is what the document says about itself. Everything
// outside, including the icon, the font preload and the theme script, belongs
// to the shell and is never touched.
var (
	headOpen  = []byte("<!--seo-->")
	headClose = []byte("<!--/seo-->")
)

// WriteShell writes the app shell for this request, with the head of whatever
// page the request resolved to and the header that page needs. It is the only
// place a described document is produced, so the server and the tests share
// one implementation of the rule.
func WriteShell(w http.ResponseWriter, r *http.Request, index []byte) {
	page, described := From(r)
	status := http.StatusOK
	if !described {
		// No route described this address: it is a screen the app has, an
		// operator screen, or an address that does not exist (042c).
		page, status = shellFor(r.URL.Path)
	}
	if page.NoIndex {
		// A page that exists and must not appear in a search result: a private
		// project, or an operator screen that renders as nothing for a
		// signed-out reader. This header governs a crawler that already holds
		// the URL, which robots.txt cannot.
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	// G705: this response is HTML by design; it is the app shell. Safety comes
	// from escaping, not from the content type. index is the frontend build
	// embedded at compile time by internal/spa and never request-supplied.
	// Every value Page.Head interpolates goes through html.EscapeString, and
	// the JSON-LD document is written by json.Marshal, which escapes `<`, `>`
	// and `&` so a title cannot close the script.
	_, _ = w.Write(Describe(index, page, described)) //nolint:gosec // G705: the shell is build-time bytes and every interpolated value is escaped in Page.Head.
}

// Describe substitutes the head region for a page the router resolved. A page
// with no title, which is the not-indexed shape, keeps the default head: the
// response is already marked, and there is nothing to say about an address the
// reader may not read.
func Describe(index []byte, p Page, described bool) []byte {
	if !described || p.Title == "" {
		return index
	}
	start := bytes.Index(index, headOpen)
	end := bytes.Index(index, headClose)
	// A shell without the markers was not built from this repository: it is
	// the committed placeholder or a stale dist/. It is served unchanged, since
	// inserting a head would produce a document with two titles.
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

/* ─── What is an address here, and what is not (042c) ─── */

// Screens are the fixed addresses the app has. Operator reports the subset
// only a signed-in operator can render.
//
// This is a second copy of SCREENS in frontend/src/routes.ts, which remains
// the client's single mapping. It exists for the same reason index.html holds
// a second copy of the theme resolution: nothing in the frontend can run
// before this response is written. Before this list existed, every address
// not in it answered 200 with the dashboard, an unbounded set of URLs with
// identical content. routes_test.go reads routes.ts and fails when the two
// copies disagree.
var Screens = []string{
	LandingPath,
	RegistryPath,
	"/trash",
	"/new",
	"/wallet",
	"/settings",
	"/settings/profile",
	"/console",
}

// operator is the subset of Screens at which a signed-out reader, which is
// what a crawler is, renders nothing. robots.txt asks a crawler not to fetch
// them; the noindex they carry governs a crawler that already has the URL.
var operator = map[string]bool{
	"/trash":            true,
	"/new":              true,
	"/wallet":           true,
	"/settings":         true,
	"/settings/profile": true,
	"/console":          true,
}

// record matches the addresses of the record shapes: a project, a project
// tab, a session, a session tab, and a researcher. The id character classes
// are the client's own.
var record = regexp.MustCompile(`^(/p/[A-Za-z0-9_-]+(/[a-z]+)?(/s/[A-Za-z0-9_-]+(/[a-z]+)?)?|/u/[^/?#]+)$`)

// Known reports whether a path is an address this app has. Anything else is a
// mistyped or foreign link.
func Known(path string) bool {
	path = trimSlash(path)
	if slices.Contains(Screens, path) {
		return true
	}
	return record.MatchString(path)
}

// Operator reports a screen only a signed-in operator can render.
func Operator(path string) bool { return operator[trimSlash(path)] }

// trimSlash removes a trailing slash, matching the client's viewOf: /wallet/
// and /wallet are one address.
func trimSlash(path string) string {
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
		if path == "" {
			return "/"
		}
	}
	if path == "" {
		return "/"
	}
	return path
}

// shellFor is the response for an address nothing described: 200 for a screen
// the app has, 200 with noindex for one only an operator renders, and 404 for
// an address that does not exist. The 404 still carries the shell, so a reader
// who mistyped gets a page that can navigate rather than a bare error.
func shellFor(path string) (Page, int) {
	switch {
	case Operator(path):
		return Page{NoIndex: true}, http.StatusOK
	case Known(path):
		return Page{}, http.StatusOK
	default:
		return Page{NoIndex: true}, http.StatusNotFound
	}
}
