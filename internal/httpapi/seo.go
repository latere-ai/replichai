// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/seo"
)

// The head the server writes (042a).
//
// The app is client-rendered, so every address it serves arrives at a crawler
// and a link unfurler as one fixed head: the same title, the same picture, the
// same sentence, whatever was published there. The routes below resolve an
// address to what it is *about* before the shell goes out, and hand the shell a
// seo.Page to render in place of its default head.
//
// Two rules govern every builder here, and neither is optional.
//
// The first is that a page is described only when the thing at that address is
// public — `Visibility == public && !Trashed()`, or a profile whose owner turned
// their page on. Deliberately *not* mayViewProject, which admits an owner to
// their own private project: an owner's browser has no use for injected
// metadata, and keying on the caller would leave "does a private title reach
// crawlable HTML" one identity away from being true. Everything else gets the
// default head and X-Robots-Tag: noindex.
//
// The second is that describing a page never leaves the database. A session's
// verdict lives in a manifest behind a forge call, and a crawler walking the
// public zone must not turn into one GitHub request per URL, so what a session
// page says comes from the project, the paper row and the job row.

// spaRoutes registers the public app addresses so each can describe itself. They
// sit in front of the SPA fallback and end in it: the response is the same shell
// with the same status, carrying a head that names what is actually there.
//
// The paths cannot collide with the API's — 013 chose /registry and /p/… exactly
// because /public, /projects and /profiles belong to the API, and routes.test.ts
// holds that rule on the client side.
func (rt *Router) spaRoutes() {
	if rt.spa == nil {
		return
	}
	rt.openHandle("GET", "/{$}", rt.described(rt.landingPage))
	rt.openHandle("GET", seo.RegistryPath, rt.described(rt.registryPage))
	rt.openHandle("GET", "/p/{id}", rt.described(rt.projectPage))
	rt.openHandle("GET", "/p/{id}/{tab}", rt.described(rt.projectPage))
	rt.openHandle("GET", "/p/{id}/s/{job}", rt.described(rt.sessionPage))
	rt.openHandle("GET", "/p/{id}/s/{job}/{tab}", rt.described(rt.sessionPage))
	rt.openHandle("GET", "/u/{handle}", rt.described(rt.profilePage))
}

// described resolves a page and serves the shell with it attached.
func (rt *Router) described(build func(*http.Request) seo.Page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := build(r)
		rt.spa.ServeHTTP(w, r.WithContext(seo.With(r.Context(), page)))
	}
}

// hidden is what an address gets when the record behind it is not public, or is
// not there at all. The two are one answer on purpose: a head that differed
// between "private" and "absent" would report the existence of every private
// project to anybody walking ids, which is the leak 028 closed on the API.
var hidden = seo.Page{NoIndex: true}

/* ─── The four public screens ─── */

func (rt *Router) landingPage(*http.Request) seo.Page {
	return rt.page(seo.Page{
		Title:       "ReplicHAI · Does the paper reproduce?",
		Description: "replichai audits the reproducibility of research papers. It implements and re-runs the parts of a paper a machine can run. It does not simulate studies with human participants. Each audit returns a verdict, published with the full transcript.",
		Canonical:   rt.site.Abs(seo.LandingPath),
		JSONLD: map[string]any{
			"@context": schemaOrg,
			"@graph": []any{
				map[string]any{
					"@type": "WebSite",
					"name":  seo.SiteName,
					"url":   rt.site.Abs(seo.LandingPath),
					// The site-level description says what the platform is for,
					// not only what it does first. Reproduction is the act that
					// can be automated and graded today, which is what earns the
					// trust the rest of the roadmap needs; the sentence is
					// written so it stays true as research on this platform
					// becomes something a researcher runs rather than only
					// something the platform re-runs for them.
					"description": "A platform where research is implemented, run and published as evidence anyone can check, under the name of the researcher who signed it off.",
					"publisher":   latere,
				},
				latere,
			},
		},
	})
}

func (rt *Router) registryPage(*http.Request) seo.Page {
	return rt.page(seo.Page{
		Title:       "The registry: published reproductions · ReplicHAI",
		Description: "The public record on ReplicHAI: the research that has been run, what reproduced and what did not, the researcher who signed off each result, and the full transcript behind it.",
		Canonical:   rt.site.Abs(seo.RegistryPath),
		JSONLD: map[string]any{
			"@context":    schemaOrg,
			"@type":       "CollectionPage",
			"name":        "The registry",
			"url":         rt.site.Abs(seo.RegistryPath),
			"description": "The public record of research run on ReplicHAI.",
			"isPartOf":    map[string]any{"@type": "WebSite", "name": seo.SiteName, "url": rt.site.Abs(seo.LandingPath)},
		},
	})
}

// projectPage describes one published reproduction. A tab is a view of this
// record rather than a second one, so /p/{id}/verdict canonicalises to /p/{id}.
func (rt *Router) projectPage(r *http.Request) seo.Page {
	proj, ok := rt.publicProject(r, r.PathValue("id"))
	if !ok {
		return hidden
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		papers = nil // a description without its bibliography beats no description
	}
	url := rt.site.Abs(seo.ProjectPath(proj.ID))
	return rt.page(seo.Page{
		Title:       title(proj.Title, "A reproduction"),
		Description: summarize(proj.Description, describes(proj.Title)),
		Canonical:   url,
		Type:        "article",
		JSONLD:      reportLD(rt.site, proj, papers, url, proj.Title),
	})
}

// sessionPage describes one published session under a public project. It is the
// address a shared link most often carries — the transcript and the verdict of a
// single run — and it says which paper that run is of.
func (rt *Router) sessionPage(r *http.Request) seo.Page {
	proj, ok := rt.publicProject(r, r.PathValue("id"))
	if !ok {
		return hidden
	}
	j, err := rt.jobs.Get(r.Context(), r.PathValue("job"))
	// A session belonging to another project is not this address's session; a
	// run that never began publishing has no published evidence to describe.
	if err != nil || j.ProjectID != proj.ID || j.RepoName == "" {
		return hidden
	}
	papers, _ := rt.projects.ListPapers(r.Context(), proj.ID)
	subject := paperNamed(papers, j.PaperKey)
	name := title(subject.Title, proj.Title)
	url := rt.site.Abs(seo.SessionPath(proj.ID, j.ID))
	return rt.page(seo.Page{
		Title:       "Reproduction of " + name + " · ReplicHAI",
		Description: summarize(proj.Description, "One reproduction session of "+name+": what the agent did, what it committed, and the verdict."),
		Canonical:   url,
		Type:        "article",
		JSONLD:      reportLD(rt.site, proj, []project.Paper{subject}, url, "Reproduction of "+name),
	})
}

// profilePage describes a researcher whose page is public (006r).
func (rt *Router) profilePage(r *http.Request) seo.Page {
	if rt.profiles == nil {
		return hidden
	}
	handle := strings.ToLower(r.PathValue("handle"))
	p, err := rt.profiles.ByHandle(r.Context(), handle)
	if err != nil || !p.Public {
		return hidden
	}
	name := title(p.DisplayName, "@"+handle)
	url := rt.site.Abs(seo.ProfilePath(handle))
	desc := name + " on ReplicHAI"
	if p.Institution != "" {
		desc = name + ", " + p.Institution
	}
	return rt.page(seo.Page{
		Title:       name + " · ReplicHAI",
		Description: desc + ". The reproductions published under this name, each signed off by this researcher.",
		Canonical:   url,
		Type:        "profile",
		JSONLD: map[string]any{
			"@context":   schemaOrg,
			"@type":      "ProfilePage",
			"url":        url,
			"mainEntity": personLD(name, p.Institution, p.ORCID, p.Scholar, url),
		},
	})
}

/* ─── The reads, and the one predicate that gates them ─── */

// publicProject reads a project and admits it only if a stranger may. Nothing
// here consults the caller: see the two rules at the top of this file.
func (rt *Router) publicProject(r *http.Request, id string) (project.Project, bool) {
	if rt.projects == nil || id == "" {
		return project.Project{}, false
	}
	proj, err := rt.projects.Get(r.Context(), id)
	if err != nil || proj.Trashed() || proj.Visibility != project.VisibilityPublic {
		return project.Project{}, false
	}
	return proj, true
}

// page fills in what every described page shares: the site's card, and the
// canonical it was built from.
func (rt *Router) page(p seo.Page) seo.Page {
	p.Image = rt.site.Abs(seo.CardImage)
	return p
}

/* ─── The structured data, which is only what the page already says ─── */

const schemaOrg = "https://schema.org"

// latere is the publisher behind the product, named once.
var latere = map[string]any{"@type": "Organization", "name": "Latere AI", "url": "https://latere.ai"}

// reportLD renders a reproduction as a Report about the paper it audits. The
// bibliography is the paper's own record (037) — authors, venue, year — and the
// identifier resolves to the work itself, so a machine reading this page can
// join it to the literature rather than to a title string.
func reportLD(site seo.Site, p project.Project, papers []project.Paper, url, name string) map[string]any {
	ld := map[string]any{
		"@context":  schemaOrg,
		"@type":     "Report",
		"name":      name,
		"url":       url,
		"publisher": map[string]any{"@type": "Organization", "name": seo.SiteName, "url": site.Abs(seo.LandingPath)},
	}
	if p.Description != "" {
		ld["description"] = summarize(p.Description, "")
	}
	if p.SignedOffAt != nil && !p.SignedOffAt.IsZero() {
		ld["datePublished"] = p.SignedOffAt.UTC().Format(time.RFC3339)
	}
	// The signer, as they were when they signed (006n) — the snapshot on the
	// project, never a profile read now, so this document says what the
	// attestation says.
	if p.SignedOffName != "" {
		ld["author"] = personLD(p.SignedOffName, p.SignedOffInstitution, p.SignedOffORCID, p.SignedOffScholar, "")
	}
	var about []any
	for _, pp := range papers {
		if pp.Key == "" && pp.Title == "" {
			continue
		}
		about = append(about, articleLD(pp))
	}
	if len(about) == 1 {
		ld["about"] = about[0]
	} else if len(about) > 1 {
		ld["about"] = about
	}
	return ld
}

// articleLD is the paper a reproduction is about.
func articleLD(p project.Paper) map[string]any {
	a := map[string]any{"@type": "ScholarlyArticle", "name": title(p.Title, p.Ref)}
	if u := sourceURL(p); u != "" {
		a["sameAs"] = u
	}
	if len(p.Record.Authors) > 0 {
		authors := make([]any, 0, len(p.Record.Authors))
		for _, name := range p.Record.Authors {
			authors = append(authors, map[string]any{"@type": "Person", "name": name})
		}
		a["author"] = authors
	}
	if p.Record.Venue != "" {
		a["isPartOf"] = map[string]any{"@type": "Periodical", "name": p.Record.Venue}
	}
	if p.Record.Year > 0 {
		a["datePublished"] = strconv.Itoa(p.Record.Year)
	}
	if p.Record.Publisher != "" {
		a["publisher"] = map[string]any{"@type": "Organization", "name": p.Record.Publisher}
	}
	return a
}

// personLD is a researcher, with the identifiers that resolve to them. ORCID is
// stored as the bare iD and becomes the resolver URL here, because sameAs is a
// link and an iD is not one.
func personLD(name, institution, orcid, scholar, url string) map[string]any {
	person := map[string]any{"@type": "Person", "name": name}
	if institution != "" {
		person["affiliation"] = map[string]any{"@type": "Organization", "name": institution}
	}
	if url != "" {
		person["url"] = url
	}
	var same []any
	if orcid != "" {
		same = append(same, "https://orcid.org/"+orcid)
	}
	if scholar != "" {
		same = append(same, scholar)
	}
	if len(same) > 0 {
		person["sameAs"] = same
	}
	return person
}

// sourceURL resolves a paper key to where the work lives. The key's scheme is
// the identity 001 settled — a DOI and an arXiv id each have exactly one
// resolver — and a url-scheme key is already the address.
func sourceURL(p project.Paper) string {
	scheme, rest, ok := strings.Cut(p.Key, ":")
	if ok {
		switch scheme {
		case paper.SchemeDOI.String():
			return "https://doi.org/" + rest
		case paper.SchemeArXiv.String():
			return "https://arxiv.org/abs/" + rest
		case "url":
			if strings.HasPrefix(rest, "http") {
				return rest
			}
		}
	}
	if strings.HasPrefix(p.Ref, "http") {
		return p.Ref
	}
	return ""
}

/* ─── Small text rules ─── */

// title picks the first non-empty of a value and its fallback, collapsed to one
// line: a title pasted out of a PDF arrives with newlines in it.
func title(value, fallback string) string {
	v := strings.Join(strings.Fields(value), " ")
	if v != "" {
		return v
	}
	return strings.Join(strings.Fields(fallback), " ")
}

// describes is the sentence a project with no description of its own gets. It
// says what the page is rather than what the project claims, which is the only
// thing true of every project.
func describes(name string) string {
	name = title(name, "a paper")
	return "An independent reproduction of " + name + " on ReplicHAI: what reproduced, what did not, and the full transcript behind the verdict."
}

// maxDescription is where a description is cut. Search results and link cards
// both stop well before this; the limit exists so a 2,000-character abstract
// does not become a 2,000-character meta tag.
const maxDescription = 300

// summarize collapses whitespace and cuts at a word boundary, adding an ellipsis
// only when something was actually dropped.
func summarize(s, fallback string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return fallback
	}
	if len(s) <= maxDescription {
		return s
	}
	cut := s[:maxDescription]
	if i := strings.LastIndex(cut, " "); i > maxDescription/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}

// paperNamed finds the paper a session is a run of. A session whose paper row
// has since been removed still has a project, which is what the fallback title
// then names.
func paperNamed(papers []project.Paper, key string) project.Paper {
	for _, p := range papers {
		if p.Key == key {
			return p
		}
	}
	if len(papers) == 1 {
		return papers[0]
	}
	return project.Paper{Key: key}
}
