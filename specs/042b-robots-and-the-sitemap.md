---
title: "robots.txt, and a sitemap of the public zone"
status: built
---

# 042b — robots.txt, and a sitemap of the public zone

**Status:** built. All eight criteria hold.

**Scope:** one static file in `frontend/public/`, one generated route in `internal/httpapi`.

**Depends on:** [042a](042a-the-head-the-server-writes.md) (the canonical URLs a sitemap lists), [028](028-the-registry-opens.md).

## Problem

```
$ curl -so /dev/null -w '%{http_code} %{content_type}\n' https://replichai.latere.ai/robots.txt
200 text/html; charset=utf-8
```

Both files answer with the app. A crawler asking what it may read is handed a React shell, and there is no list anywhere of the pages worth reading — so the public zone is discoverable only by following links from a landing page that links to none of it.

## Decisions

**robots.txt is a static file, not a handler.** Put it in `frontend/public/` and it lands in `dist/`, where `exists(dist, p)` already finds it and `http.FileServer` already types it. A handler would be a second way to serve a constant.

It allows everything, disallows the operator screens (`/new`, `/wallet`, `/settings`, `/trash`, `/console`) because no signed-out reader can render them, and names the sitemap by absolute URL — the one place a relative one is not allowed.

**The sitemap is generated, because its content is a table.** `GET /sitemap.xml`, registered on the mux ahead of the SPA fallback, `Content-Type: application/xml`. It lists the landing, the registry, every public project, every published session under one, and the profile of every researcher who signed one.

**The profiles come from the projects.** There is no "list public profiles" store method and this spec does not add one: the profiles worth listing are exactly those that signed public work, which is `PrincipalHandle` over the signers already being listed — the same resolution the project view does, which returns nothing for a profile that is not public.

**`lastmod` is the modified time already computed for the registry** (018's `ModifiedAt` over the project, its papers and its sessions), and a session's is its `UpdatedAt`. A date the app cannot substantiate is worse than no date, so a zero time omits the element rather than emitting today.

**It is read from the store on request, cached for an hour.** The public zone is small and the query is the registry's own. `Cache-Control: public, max-age=3600` keeps a crawler from making it a hot query, and an hour is short enough that a publication appears the same day.

**A failed read is a 500, not an empty sitemap.** An empty `<urlset>` is a statement that there is nothing to index, and a crawler will believe it.

## Criteria

1. `GET /robots.txt` returns `text/plain`, allows the public zone, disallows the five operator paths, and names the absolute sitemap URL.
2. `GET /sitemap.xml` returns `application/xml` and a valid `urlset`.
3. It lists `/`, `/registry`, every public project, every published session under a public project, and the handle of every signer with a public profile.
4. It lists no private project, no trashed project, no unpublished session, and no private profile.
5. Every URL is absolute against `BaseURL` and equal to that page's canonical from 042a — asserted against the same function, not a second copy of the rule.
6. `lastmod`, where present, is the project's modified time or the session's updated time, in W3C date format; a zero time emits no element.
7. A store error answers 500 rather than an empty sitemap.
8. Go tests cover 1–7.

## Not in scope

Submitting the sitemap to any search engine, and sitemap indexes — one file holds the public zone by three orders of magnitude.
