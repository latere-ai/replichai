# 042a — The head the server writes, and the client keeps current

**Status:** designed.

**Scope:** `internal/seo` (new), `internal/spa`, `internal/httpapi` route registration, `frontend/index.html`, and one hook in the frontend. No change to how the app boots or renders.

**Depends on:** [042](042-the-site-a-crawler-can-read.md).

## Problem

One head serves every screen (017's, unchanged since), and nothing updates it. The consequence a reader meets is a tab that says *does the paper actually reproduce?* on the wallet screen. The consequence the product meets is that a shared link to a published reproduction previews as the homepage in every client that unfurls links, because none of them run JavaScript.

## Decisions

**A `seo.Page` is the whole vocabulary.** Title, description, canonical, card image, `og:type`, an optional JSON-LD document, and a `NoIndex` flag. Everything that writes a head writes one of these; `Page.Head()` renders the tags. Nothing else in the codebase knows the tag names.

**The router computes it; the shell renders it.** `internal/httpapi` registers the public app paths — `/registry`, `/p/{id}`, `/p/{id}/{tab}`, `/p/{id}/s/{job}`, `/p/{id}/s/{job}/{tab}`, `/u/{handle}` — as SPA routes that first resolve a `seo.Page`, attach it to the request context, and then call the same SPA handler as before. `internal/spa` reads the context and substitutes; with no page in context it serves the default head, which is what every other path gets. The two packages share a type, not a callback.

These paths cannot collide with the API: 013 chose `/registry` and `/p/…` precisely because `/public`, `/projects` and `/profiles` are the API's, and `routes.test.ts` already holds that rule.

**Substitution replaces a region, never appends.** `index.html` marks its head with `<!--seo-->` … `<!--/seo-->`; the server replaces what is between them. Appending a second `<title>` does not reliably override the first, and the region keeps the inline theme script and the font preload — which must not move — outside it. The response carries exactly one `<title>`, asserted.

**Public means public, not permitted.** The page is built only when `Visibility == project.VisibilityPublic && !Trashed()`, or for a profile when `Public` is set. Not through `mayViewProject`, which admits an owner to their private project: an owner's browser does not need injected metadata, and keying on the caller would put the decision one identity away from a crawler. Anything else gets the default head plus `noindex`.

**The injected head never leaves the database.** A session's title comes from the project, its paper row and its job row — never from `ReproReader`, which is a forge call. A crawler must not be able to make this server hit GitHub once per URL.

**Tabs fold to the record.** Canonical for `/p/{id}/verdict` is `/p/{id}`; for `/p/{id}/s/{job}/artifacts` it is `/p/{id}/s/{job}`. `og:url` is the canonical, always.

**The client keeps the tab honest.** A `useHead(view)` effect writes `document.title` and the canonical/description on every in-app navigation. It writes the same strings the server would, from data the screen has already fetched — a title the client does not have yet is simply not written, rather than written wrong.

**Absolute URLs, from `BASE_URL`.** Canonical and card URLs are absolute because a crawler resolves them against nothing. The base is the configured `BaseURL`, falling back to `https://replichai.latere.ai` when unset, so an offline run still renders a valid document.

**JSON-LD is what the record already says.** The landing carries `WebSite` + `Organization`. A public project carries `Report` with `about` a `ScholarlyArticle` (the paper's title, and its DOI as `sameAs` when the paper resolved to one). A public profile carries `ProfilePage` → `Person`, with `affiliation` and `sameAs` for ORCID and Scholar. Nothing is claimed that is not already rendered on the page.

**Escaping is the renderer's job, once.** Titles and descriptions are researcher-supplied text. Attribute values are HTML-escaped and the JSON-LD is JSON-encoded inside a `<script type="application/ld+json">`; a title containing `</script>` or a quote is a test case, not a discovery.

## Criteria

1. `GET /p/{id}` for a public project returns HTML whose `<title>` and `og:title` name that project, and whose description is the project's own.
2. The same request for a private, trashed, or unknown project returns the default head and `X-Robots-Tag: noindex`; the project's title appears nowhere in the response.
3. `GET /u/{handle}` for a public profile names the researcher; a private handle gets the default head and `noindex`.
4. `GET /p/{id}/s/{job}` for a published session under a public project names the paper it is a run of.
5. Every injected response carries exactly one `<title>`, one canonical, and an absolute `og:url` equal to it; a tab path canonicalises to its record's path.
6. Injection is byte-identical to the default shell in every other respect: the theme script, the font preload and the module script are untouched.
7. A project titled `</script><img onerror=alert(1)>` reaches the client escaped, in both the meta tags and the JSON-LD.
8. The head injection performs no forge call; a router built with a `ReproReader` that fails every read still serves every page.
9. Navigating in the app updates `document.title` and the canonical to match the screen.
10. Go tests cover 1–8; `frontend/test/` covers 9 in a real browser.

## Not in scope

`robots.txt`, the sitemap, and the 404 — 042b and 042c. Per-page card *images* (one `og.png` for the whole site stands).
