---
title: "Settings is a place, and the ledger names its projects"
status: built
---

# 024 — Settings is a place, and the ledger names its projects

**Status:** built.

**Scope:** make personal settings a first-class destination in the rail with its own sub-tabs, and make a wallet's credit history name the project each move went to — durably, so a deleted project still reads in the ledger. Two tightly-scoped children build it: [024a](024a-settings-as-a-destination.md) is the frontend home and its tabs (no schema change, ships alone); [024b](024b-the-ledger-names-its-project.md) is the ledger attribution that carries the interface change and a migration.

**Depends on:** [013](013-the-address-bar-is-the-state.md) (every screen is a URL; the sub-tabs follow the project-tab pattern), [011](011-what-a-session-costs.md) (the wallet, the ledger, and the allocate/reclaim moves this names), [018](018-one-list-of-projects.md) and [019](019-a-project-can-be-thrown-away.md) (a project can be trashed then purged, which is why a link is not always available and a snapshot is required).

## Problem

Settings is reachable only by clicking the person's own card at the foot of the rail — there is no destination for it, and the one page stacks identity, access, credits, appearance, and the researcher profile into a single scroll. As more lands under "my account" (a wallet statement, a researcher record, a place top-ups will arrive from [022](022-money-comes-in-without-an-admin.md)), one scroll is the wrong shape.

And the statement it already shows cannot answer the first question a reader has of it. A wallet entry reads `Allocated to a project` with no project named, because a wallet-side `allocate` entry stores no project reference at all — the project id lives only on the paired pot-side entry ([011](011-what-a-session-costs.md)'s `transfer`). The reader cannot tell *which* project their credits went to, cannot follow it there, and once a project is purged there is nothing left to name it by.

## The split

**024a — the place.** A `Settings` item in the rail's foot group, below Trash. The screen becomes three sub-tabs, each a real path: `/settings/account`, `/settings/profile`, `/settings/credits`. Pure frontend; the credits tab keeps rendering today's statement unchanged. Independently shippable, and the half the user sees first.

**024b — the attribution.** The credit ledger gains a project reference and a snapshot of the project's name at the moment of the move, populated on allocate and reclaim, surfaced in the wallet API. The credits tab then names the project on each such row and links to it while it exists. Forward-only: entries written before this lands keep the generic label.

## Leaves out

- Backfilling historical `allocate`/`reclaim` rows (the pre-024b entries carry no id to recover reliably). Named in 024b as a deliberate non-goal.
- Anything about the pot-side (project) ledger view; this is the *wallet* owner's statement.
- Top-up arrival itself ([022](022-money-comes-in-without-an-admin.md)); the credits tab is only its future home.
