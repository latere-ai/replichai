// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"encoding/xml"
	"log/slog"
	"net/http"
	"time"

	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/seo"
)

// The list of what is worth reading (042b).
//
// Without it the public zone is discoverable only by following links from a
// landing page that links to none of it. The content is a table — the projects
// somebody published, the sessions under them, and the researchers who signed
// them off — so the file is generated from that table rather than written by
// hand and left to go stale.
//
// Every URL comes from seo's own path helpers, so a listed address and the
// canonical link on the page it names are the same string by construction.

// sitemapTTL is how long a crawler may reuse the file. The queries behind it are
// the registry's own and are small, but a crawler asks often enough to make that
// a hot read; an hour is short enough that a publication appears the
// same day it happens.
const sitemapTTL = "public, max-age=3600"

// urlset is the sitemaps.org document, as a shape rather than a string. Building
// XML by concatenation is how a researcher's handle with an ampersand in it ends
// up producing a file no crawler will parse.
type urlset struct {
	XMLName xml.Name  `xml:"urlset"`
	NS      string    `xml:"xmlns,attr"`
	URLs    []sitemap `xml:"url"`
}

type sitemap struct {
	Loc string `xml:"loc"`
	// LastMod is omitted when the app cannot substantiate one. A date that is
	// merely "today" tells a crawler the page changed when it did not.
	LastMod string `xml:"lastmod,omitempty"`
}

const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

// sitemapXML lists the public zone: the landing, the registry, every published
// project, every published session under one, and the page of every researcher
// who signed one.
//
// A store failure answers 500 rather than an empty urlset — an empty one is a
// statement that there is nothing to index, and a crawler will believe it.
func (rt *Router) sitemapXML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := rt.projects.ListPublic(ctx)
	if err != nil {
		slog.WarnContext(r.Context(), "sitemap: listing the public zone", "error", err)
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	// 018's modified time: the newest of the project, its papers and its
	// sessions. A read failure costs the dates, not the file.
	modified, err := rt.projects.ModifiedAt(ctx, ids)
	if err != nil {
		slog.WarnContext(r.Context(), "sitemap: reading modified times", "error", err)
		modified = nil
	}

	urls := []sitemap{{Loc: rt.site.Abs(seo.LandingPath)}, {Loc: rt.site.Abs(seo.RegistryPath)}}
	handles := map[string]bool{}
	for _, p := range projects {
		urls = append(urls, sitemap{
			Loc:     rt.site.Abs(seo.ProjectPath(p.ID)),
			LastMod: stamp(later(p.CreatedAt, modified[p.ID])),
		})
		urls = append(urls, rt.sessionURLs(r, p)...)
		// The researchers worth listing are exactly those who signed public
		// work, and signerHandle already returns nothing for a profile its owner
		// has not made public — so there is no separate "list public profiles"
		// read, and no way for this file to name a page that does not exist.
		if h := rt.signerHandle(ctx, p); h != "" && !handles[h] {
			handles[h] = true
			urls = append(urls, sitemap{Loc: rt.site.Abs(seo.ProfilePath(h))})
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", sitemapTTL)
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(urlset{NS: sitemapNS, URLs: urls}); err != nil {
		slog.ErrorContext(r.Context(), "sitemap: encoding", "error", err)
	}
}

// sessionURLs lists the sessions under a public project that have something
// published to read — the same predicate the session page describes itself on,
// so the sitemap cannot offer an address whose head says noindex.
func (rt *Router) sessionURLs(r *http.Request, p project.Project) []sitemap {
	sessions, err := rt.jobs.ListByProject(r.Context(), p.ID)
	if err != nil {
		slog.WarnContext(r.Context(), "sitemap: listing sessions", "project", p.ID, "error", err)
		return nil
	}
	out := make([]sitemap, 0, len(sessions))
	for _, s := range sessions {
		if s.RepoName == "" {
			continue
		}
		out = append(out, sitemap{
			Loc:     rt.site.Abs(seo.SessionPath(p.ID, s.ID)),
			LastMod: stamp(later(s.CreatedAt, s.UpdatedAt)),
		})
	}
	return out
}

// stamp renders a time the way a sitemap wants it, and renders nothing for a
// time the app never recorded.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
