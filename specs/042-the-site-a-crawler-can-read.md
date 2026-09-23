---
title: "The site a machine that is not a browser can read"
status: built
---

# 042 — The site a machine that is not a browser can read

**Status:** built. All three children shipped; the release smoke checks each in production.

**Scope:** the HTML the server sends to a client that never runs JavaScript, and the two files a crawler asks for before anything else. No SSR, no prerender, no headless browser: the app boots exactly as it does today.

**Depends on:** [013](013-the-address-bar-is-the-state.md) (every screen has an address), [028](028-the-registry-opens.md) (the public zone a stranger may read), [006r](006r-the-public-profile.md) (`/u/{handle}`), [029](029-the-landing-is-a-place.md)/[033](033-the-landing-rebuilt-from-the-design.md) (the landing).

## Problem

The public zone exists and none of it is readable by anything that does not execute JavaScript.

`GET /public` returns published projects with paper titles and a paragraph each on what reproducing the work would settle, for example *There is more to context than location* and *Short-Form Videos Degrade Our Capacity to Retain Intentions*. That is the long-tail material somebody searches for. A crawler, a link unfurler, and a citation tool are each served the same 1.5 KB shell whose head has been fixed since 017:

```
$ curl -s https://replichai.latere.ai/p/85b8713a3be15656e0b3651f5c31acfc | grep '<title>'
<title>ReplicHAI — does the paper actually reproduce?</title>
```

Three separate failures sit behind that one line.

**The head never changes.** `grep document.title frontend/src` returns nothing. One static head in `index.html` serves every screen. There is no `<link rel="canonical">` anywhere in the repository, so `/p/{id}`, `/p/{id}/verdict` and `/p/{id}/people` are three addresses with one page and no statement about which is the canonical one.

**Slack, X, LinkedIn and iMessage do not run JavaScript.** No client-side work can fix this. Every link anyone has shared to a published reproduction previews as the homepage: same title, same picture, same sentence. The main action the product asks of a researcher, to publish and send the link to somebody, produces a link that says nothing about what was published.

**`robots.txt` and `sitemap.xml` return the app.** `internal/spa` falls through to `index.html` for anything not in `dist/`, so both answer `200 text/html`. So does `/this-page-does-not-exist-xyz`, and `viewOf` maps it to the dashboard while `useRouter` rewrites the address to `/`. The result is an unbounded set of URLs that all return 200 with identical content.

## Decisions

**The head is written by the server, and the client keeps it current.** First load is what an unfurler and a crawler see, and only the server can write that. In-app navigation is what a reader sees, and only the client can update that. Both are needed; otherwise the tab title is wrong as soon as somebody clicks anything.

**Only public records get a head.** The injected head is built from `Visibility == public && !Trashed()` and `profile.Public`, never from the caller's identity and never through `mayViewProject`. A private project read by its own owner gets the default head and `noindex`. The rule is a single predicate, so a test can rule out "a private title reaches crawlable HTML" rather than relying on a review to notice it.

**Tabs fold to the record.** `/p/{id}/verdict` canonicalizes to `/p/{id}`, `/p/{id}/s/{jobId}/artifacts` to `/p/{id}/s/{jobId}`. A tab is a view of one record, not a second record.

**An address that does not exist says so.** Go learns the route shapes the client already knows, and anything outside them is a 404 that carries the shell. The app still renders, and renders a not-found screen rather than the dashboard under a corrected address.

**A record page is specific; the site is not.** The entity pages (a project, a session, a researcher) describe one record as precisely as the record allows, because that specificity is what a reader arriving from a search needs. The site-level surfaces (the landing, the registry, the `WebSite` and `Organization` documents) describe what the platform is for: research that runs, published as evidence anyone can check, under the name of the person who signed it off. Reproduction is the act that can be automated and graded today, and it earns the trust the rest of the roadmap needs; it is not the category this platform intends to stay inside, and nothing written into a head here should say otherwise. What would be expensive to change is not an adjective but the address vocabulary, and `/p/{id}` is a project, `/u/{handle}` a researcher, `/registry` a public record. None of them says "reproduction", and none needs renaming when 007's instruments and experiments arrive.

**The structured data is metadata we hold, not markup we invent.** A public project is a report about a paper with a DOI (`internal/paper` already enriches from Crossref); a researcher has an ORCID and a Scholar URL. `Report` → `about: ScholarlyArticle`, and `ProfilePage` → `Person` with `sameAs`. Nothing is asserted that is not already on the page.

## The children

| | | |
|---|---|---|
| [042a](042a-the-head-the-server-writes.md) | The head the server writes | per-route title, description, canonical, card and JSON-LD — injected in Go, kept current in React |
| [042b](042b-robots-and-the-sitemap.md) | robots.txt, and a sitemap of the public zone | a static robots file, and a sitemap generated from `ListPublic` |
| [042c](042c-an-address-that-does-not-exist.md) | An address that does not exist | route shapes in Go, a real 404, `noindex` on the operator screens |

Order: 042a, then 042b (the sitemap needs the canonical URLs 042a settles), then 042c.

## Not in scope

**Server-side rendering, and prerendering.** The app is a hand-rolled Vite SPA with a hand-rolled router; either would require rewriting the frontend to fix its head. Injection gives the crawler and the unfurler what they need without changing how the app runs.

**Content and keyword work.** The landing's copy is 033's and stays as written.

**Analytics, Search Console verification, or a submission pipeline.** Publishing the sitemap is this spec; notifying search engines is an operations task.
