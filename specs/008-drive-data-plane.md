---
title: "Drive as the artifact data plane"
status: deferred
---

# 008 — Drive as the artifact data plane

> **Amended 2026-09-12.** Drive no longer hosts git (drive removed
> its smart-HTTP and LFS outright, with no migration). Where this spec
> assumed drive as the git store, read Latere Code (Origo, Latere's git hosting service)
> for git hosting and drive for reproduction files, sharing and the public
> zone. The `Store` seam argument is unchanged; the second implementation
> now spans two services rather than one.

**Status:** deferred
**Scope:** moving reproduction storage, sharing, and the public zone from the GitHub forge onto [drive](https://drive.latere.ai), and git hosting onto Latere Code (code.latere.ai), with GitHub demoted to an optional external mirror. A second implementation of the `Store` seam, not a rewrite.
**Depends on:** the web MVP (006 and its children) shipping on GitHub first.

## Why deferred

The GitHub workflow is verified and live (006f). Rebuilding it on drive before the product has shipped would replace working code with a better architecture before it is needed. So this is the *last* item on the web track: a storage-backend swap once the MVP works, not a prerequisite.

## What drive already is

`drive.latere.ai` is a live data plane for latere products: user-owned files and **agent-produced code repos** as mountable folders (their git history lives on Latere Code, `code.latere.ai/<owner>/<repo>.git`), an ownership/sharing/approvals model, **provenance zones** (a public zone), a Drive-style file-tree UI, `auth.latere.ai` identity, quotas, and admin governance. Most of what 006 builds by hand on GitHub, drive already ships.

## The seam this depends on (built in the MVP, spec 006)

006 puts repository access, file reads, visibility, and membership behind a single `Store` interface with **one GitHub implementation**. This spec is the **second implementation**, a drive-backed `Store`, plus the wiring to make it the default. Nothing above the seam (the job model, the HTTP handlers, the frontend file tree) changes.

## Decisions (to firm up when it is taken)

- **Drive is the primary store; GitHub is an optional mirror.** A project's artifacts live in drive (private by default, drive's sharing model for collaborators and the public zone). A user who connects a GitHub account gets their project mirrored out to a `github.com` repo they can operate; going public may opt the mirror out.
- **The agent's commit target is the open spike.** The native mount proved for GitHub (`github_repository`) may or may not accept a Latere Code remote. If it does, the agent commits straight to Latere Code and drive mounts the checkout; if not, it commits to a scratch GitHub repo and drive syncs from it. A small spike, like the GitHub-mount one, settles this before the design firms up.
- **Sharing and visibility become drive's, not the app's.** The app's project/membership/visibility model (006) either delegates to drive's ownership/sharing or stays the source of truth with drive as storage, decided when this is taken.

## Out of scope

Everything until the GitHub MVP ships. This spec is a placeholder that names the seam and the direction so the MVP does not become GitHub-only by accident.
