# Specs

Designs, and the record of what got built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out. This page is the map: what exists today, how it fits, what is only designed, and the order it gets built.

Feasibility questions and scope extensions that are *not* on the roadmap — multi-study papers, hard-to-replicate instruments, unpublished manuscripts — live in [`BACKLOG.md`](BACKLOG.md), deliberately unsequenced. One of them, [016](016-a-paper-that-is-not-published-yet.md), needed enough argument to get a deferred spec of its own.

## What replichai is

Give it a paper. It resolves the paper's identity, runs an agent that reproduces the machine-runnable inner loop, publishes the reproduction as its own GitHub repository with the agent's commit history, and returns an independent verdict — recording every decision the paper left unwritten. It never simulates the outer loop (the human study); placing and defending that boundary is the point.

## Current status

**Built and working today — the engine and the live publish loop:**

```
replichai audit <paper> --publish [--detach] [--invite <login>]
   resolve identity (001)                          internal/paper
   → registry warns on prior reproductions,        internal/registry (003)
     allocates <canonical-slug>-<suffix>
   → create a private repo under open-hai          internal/github
   → mount it into the agent's sandbox             internal/provider (RepoMount → github_repository)
   → the agent commits its reproduction LIVE       internal/audit (006f)
replichai finish <owner>/<repo> <paper> --session <id>
   → reattach, wait, verify the committed tree,    internal/publish (VerifyTree)
     commit replication.json, invite the user      internal/github (PutFile, Invite)
```

This was proven end to end: a real reproduction (`open-hai/short-form-videos-…-wtmk`) with agent-authored history, an independent manifest, and a collaborator invitation. `audit --publish` blocks; `--detach` starts it and hands you a `finish` command to run later, since the agent commits independently of the CLI.

**Implemented:** 001 (identity), 002 (publishing + the redistribution gate), 003 (registry), 004 (the Go port), 005 (transcript), 007a (the instrument), 007b/007c (experiments and the synthetic pilot — engine halves), and the **web application core**: 006a (HTTP + SSE), 006b (job model + Postgres), 006c (OIDC login + the fail-closed access gate). **006f is implemented at its core** (mount, publish, finish, verifier, invite); two pieces stay open (below). **010** provisions production — domain, database, k8s manifests, and a tag-triggered deploy pipeline.

**The app runs end to end**: `docker compose up` (or `make up`) brings up Postgres + the server, which serves the React frontend (006e) and the API. A researcher signs in, submits a paper URL, and watches its phases stream live to `done` behind the real fail-closed gate. Production is provisioned and fail-closed.

**Built (pending a full prod run):** [006i](006i-server-side-mount-and-publish.md) wires the CLI's live-mount publish into the server `engineRunner`, so a web-submitted session gets a real repository, manifest, and committed transcript rather than evaporating with the sandbox — the backend half of 006f, with the CLI's finalize extracted into a shared `publish.FinalizeLive` so the two paths cannot drift.

**Built:** [006j](006j-the-new-project-flow.md) makes "New project" a deliberate act — name it, preview each paper link (`POST /resolve`), start several papers at once, and invite collaborators, through a dedicated `POST /projects` (plus `PATCH /projects/{id}` rename).

**Built:** [012](012-a-project-holds-papers.md) separates the two acts 006j fused. A paper becomes a row a project holds (`project_papers`, keyed by the `paper_key` a session already carries), not an argument to starting a run: create records papers and enqueues nothing, and a member starts a session per paper from the project screen. Creating a project stopped being spending — which is what used to refuse an empty wallet at the door, for the act of naming a container — so the credit check is asked once, on the Start button, with the project's pot on the same screen. `POST /papers` is retired; four routes under `/projects/{id}` replace it, and `fundNewProject`, `undoProject`, the 422, and the frontend's partial-success apparatus all go with the enqueue that needed them.

**Built — running the seed phase from inside the app:** [006k](006k-the-platform-admin-console.md) gives the admin a screen (grants, promotions, the platform policy row, per-person credit) instead of `curl` and a redeploy; [011](011-what-a-session-costs.md) spends against the numbers it stores — one check inside `Enqueue`, in the inserting transaction, settled in the transaction that ends the session; [006m](006m-the-signed-in-person.md) renders the name and avatar the IdP already has. [006l](006l-the-session-quota.md) was the first attempt at that meter and is superseded, kept for the argument 011 inherits.

**Built:** [006n](006n-who-signed-off.md) gives the public sign-off a person — a researcher profile (name, institution, public contact, ORCID, Scholar) kept in settings, prefilled into a step-by-step publish wizard, and **snapshotted onto the project** so a later profile edit never rewrites a published attestation. The signer's own email is derived from the session and stored redacted; attestation v2.

**Built:** [006o](006o-withdrawing-a-publication.md) gives the one-way public flip an end — an owner may withdraw a publication back to private, the forge repository follows, and the retraction is recorded *beside* the sign-off rather than erasing it. Withdrawal is not deletion (which cascades and stays an admin action), which is why an owner can be trusted with it.

**Built:** [006p](006p-the-invitation-that-arrives.md) sends the project invitation 006f never sent. auth's mail transport moved to a shared `latere.ai/x/pkg/email` — transports only, so each service keeps its own copy and escaping — and `internal/notify` owns what replichai says to somebody who is not looking at it. The message is an announcement, not an offer: the membership already exists, so there is no token and nothing to accept. Sending is best-effort, because the membership is the durable thing.

**Built:** [006q](006q-sign-in-is-open-budget-is-not.md) splits *may sign in* from *may spend*. Signing in self-grants `TierUser`, a new account arrives with nothing to spend, and the meter — not an admin's guest list — is what protects the credential. Revocation becomes recorded state rather than a delete, because a self-granting sign-in would re-create a deleted row and turn a ban into a logout. With 006p, an invited collaborator now receives mail, follows it, signs in, and sees the project — which nobody could do before.

**Built:** [006r](006r-the-public-profile.md) gives a researcher a handle and a public page at `/u/{handle}`, listing the public work signed under that name, and requires one before a project may be published — asserting a result under your name means the name has to resolve to something. The sign-off card links to it through a field resolved per read and never snapshotted, so 006n's invariant holds: making a profile private later removes the link and changes nothing on the record.

**Built:** [006s](006s-reading-a-session-in-time.md) makes a 771-event transcript navigable — the run's real clock as the spine (proportional phase bands, a histogram of where it spent itself, a scrubber that scrolls to a moment), search, a selected-event inspector, and the per-event token usage the server parsed out of `raw.model_usage` and then discarded. A revision of 006h: that spec made the transcript visible, this one makes it readable. Its two corrections are the interesting part — the bar spans the *session*, not its events, because every event is emitted inside `running`; and it weighs *fresh* tokens, not the total, because cache reads are 99% of a real total and measure conversation length rather than work.

**Built:** [013](013-the-address-bar-is-the-state.md) gives every screen a URL. Navigation was one `useState` and the address bar never moved, so a reload started over at the dashboard, no screen could be linked to, and Back left the app. The path is the state now — one `pathOf`/`viewOf` bijection over the `View` union, so a screen cannot exist without an address — and history follows one default: push when the path changes, replace when it does not. The registry and the console live at `/registry` and `/console` because the API owns `/public` and `/admin`; `/p/{id}` and `/u/{handle}` keep the meaning 006p's mail and 006r's pages already published.

**Built:** [015](015-a-verdict-belongs-to-a-session.md) puts a verdict and a repository back on the session that produced them. The project's Artifacts and Verdict tabs read `sessions.find((s) => s.repo_name)` — the newest session anywhere in the project with a repository — and showed it under an address that named the project, so on a project holding two papers the second hid the first and one paper's reproduction was reachable by nothing. That line is 012's split left standing in the two screens that report results; the same root cause named the project after the wrong paper. Both panes moved to `/p/{id}/s/{jobId}/{verdict,artifacts}`, the project's artifacts tab is gone (a project has as many repositories as published sessions), its verdict tab became the read across papers it was pretending to be, and sessions nest under the paper they are runs of. `repo_name` stopped standing in for success: it marks that publishing *began*, and `publishing → failed` is legal.

**Built:** [017](017-the-first-screen.md) gives the product a first screen and an identity. The surface a visitor met was a 440px card holding the character `r`, the name, one sentence and a button — and that `r` was set in a font (`Instrument Serif`) the app has never shipped, so the mark had always rendered in Georgia. The landing is now two columns, the claim and the inner/outer boundary beside the way in, collapsing on a phone to an order that keeps the button reachable; the mark is a drawn SVG of the thing the product actually defends, a closed inner ring inside an open one; and `index.html` finally has a description, a tab icon and a social card, so a link pasted into Slack stops being a bare URL.

**Built:** [018](018-one-list-of-projects.md) merges "My projects" and "Shared with me" into one list. The screen split a reader's work by a fact they did not choose — whether they had created a project or been invited to it — so finding work started with remembering who had started it, and the two halves disagreed about the same project, because the owned list counted only the sessions *you* had submitted. Ownership is a column now, not a place: `GET /shared` is gone, one query summarises a project's sessions however started them, the owner is named through their profile (never their address — the redaction rule that governs a signer's email governs this too), and the list sorts on a modified time derived from the newest of the project, its papers and its sessions rather than on when a container was opened.

**Built:** [019](019-a-project-can-be-thrown-away.md) lets an owner throw a project away. Nothing in the product removed one before: `Delete` existed in the store and no route had ever called it, because 006o's argument for giving owners withdrawal rested on withdrawal *not* being deletion. Removing is two steps now, and the guards are what make the second one safe. The trash is reversible and a `deleted_at` column, so a project leaves every list without taking its papers, sessions or repositories with it; a running session refuses it, and a public project is withdrawn on the way out with the retraction recorded. The permanent delete is refused while the pot holds credits — a balance nobody can account for is what the ledger exists to prevent — and it closes the published repositories rather than destroying them, because a reproduction the agent wrote is evidence. The trashed check lives at the three gates every read and write already pass, so the sessions and transcripts under a trashed project close in one edit rather than five.

**Built:** [020](020-download-the-whole-project.md) gives a project one download. The app proxies committed files one at a time because the repositories are usually private, which meant there was no way to get the work out: a project with three sessions and forty files each was a hundred and twenty clicks and a hundred and twenty loose files with no record of which session each came from. `GET /projects/{id}/archive` streams a zip organised by session — `sessions/{job-id}/…`, because 015 established that a repository belongs to the session that produced it and a flat merge would collide two sessions' files. The status code is committed before the walk finishes, so a forge read that fails is recorded in the archive's own `MANIFEST.txt` rather than truncating the download silently. The gate is view rights, not ownership: a collaborator takes away the project they work in, and a reader takes away what was published.

**Built:** [030](030-the-app-on-a-phone.md) makes the app work on a phone. `.shell` was a flex row with `.rail` at a hard 236px and no shell-level media query anywhere in the stylesheet, so a 390px screen got **154px** of content — on all fifteen screens — and the topbar's controls ran to x=579 where nothing could tap them. Below 760px the rail leaves the flow and becomes a drawer over a scrim, opened by a hamburger in the topbar and closed by the scrim, Escape, its own control, or simply opening a destination. The drawer is deliberately not the fold: `sidebar.ts` holds that a width the operator chose is never overridden by one the window happened to be, so `useSidebar` is untouched, the new `useDrawer` never persists, and the stored fold is neutralised in CSS rather than read in JS. What made this invisible for so long is the second half of the fix's test: `.screen` is `overflow-y: auto`, which computes `overflow-x` to `auto` as well, so the spill lived in a pane that scrolled sideways and `documentElement.scrollWidth` read clean while nothing was legible. `frontend/test/mobile.test.mjs` therefore asks both questions across every screen.

**Designed, not built — the outer loop as a product:** [023](023-the-outer-loop-as-a-product.md) wires the built experiment engine to a person. The engine (007b/007c) and the job seam (`KindExperiment`) exist; nothing user-facing reaches them. The chain fills exactly the "still open in the server" gap below — [023a](023a-experiments-persisted-and-dispatched.md) the Postgres stores and the dispatching runner (and it keys an experiment to the *session* that produced its instrument, not the project — 015's rule), [023b](023b-the-experiment-http-surface.md) the authenticated routes, [023c](023c-running-an-experiment-from-the-app.md) the screen. It ships the two sources with live input first — `synthetic:generated` and `uploaded` — and [023d](023d-ground-truth-for-a-cohort.md) enables `synthetic:model` and `released` behind the held-released-data gate. A census of every published `instrument.json` found 0/26 servable components and 0/3 papers' released data held, so hosting (007d) and the ground-truth sources are blocked on *corpus* — a paper to reproduce, not code to write.

**Designed, not built:** 006d (PDF ingestion) and the hosted-study spec 007d. The 007b/007c product halves land as the [023](023-the-outer-loop-as-a-product.md) chain. 006c's forge-account link lands with its consumer, 006f.

**Built — money comes in without an admin:** [022](022-money-comes-in-without-an-admin.md) (six leaves, 022a–022f) adds the front door 011 left locked. A person buys credits into their own wallet with a card: a Stripe hosted Checkout session creates the charge, a signed webhook is the one thing that posts the credit, and the ledger dedupes that post on an `external_ref` so a retried delivery cannot double it. Margin is a visible spread at purchase (you pay $20, your wallet gains $18), which keeps the rate card raw so a session's cost still reconciles against vendor rates. The checkout is created in **USD** (a credit is prepaid USD compute) and an EU customer pays the euro equivalent via Stripe Adaptive Pricing — a dashboard toggle, so no code charges, reads, or converts EUR; EU-consumer VAT is a flagged, deferred spec. Refunds are a negative `adjust` and may take a wallet negative — and a negative wallet **freezes the account** into view-only mode (every write refused `402 account_frozen` bar the top-up that thaws it), with a one-shot admin override and a frozen/negative filter on the admin people table, so a chargeback-after-spend cannot quietly keep costing compute. Coupling a customer payment to the platform's own Anthropic account is a stated non-goal, replaced by a solvency number on the console. Supersedes only 011's closing line — admin top-up stays as the way a grant or comp enters a wallet.

**Designed, not built — the money follow-ups 022 named:** [025](025-the-solvency-an-operator-can-act-on.md) turns the raw outstanding-liability number into a signal — a comparison against what the vendor account holds, and an alert before the platform is under-funded — while keeping vendor funding out of the platform (auto-reload, not a transfer). [026](026-the-people-table-at-scale.md) makes the admin people table hold a thousand-plus accounts: pagination, search, one aggregated balance query instead of the current N+1, and a server-side owing filter — without a stored balance or a user table until one is measured necessary. [027](027-eu-vat-and-the-invoice.md) builds the EU-VAT piece 022b deferred — Stripe Tax computes the buyer's-country rate, collects a VAT ID for B2B reverse charge, and emits a compliant invoice, all behind a `vat_enabled` flag so an unregistered deployment stays byte-for-byte 022b; the tax rides the purchase, never the credit, and the ledger stays tax-free. All three are beta-deferred: the current surfaces are fine at today's scale, and the specs exist so the fix is known before the numbers force it. 027 is blocked on the operator, not the code — OSS registration, a Stripe Tax config, and one legal classification (are prepaid credits a single-purpose voucher?) come first. Still owed and not yet specced: recovering a negative project *pot*.

**Built and deployed:** the project/session rework — 006g (projects as first-class records holding replication sessions, the double-trigger guard, the CLI backfill migrated into three one-session projects) and 006h (the session transcript viewer: trajectory + token usage, usage derived from the transcript for backfilled sessions).

## Architecture at a glance

**The neutral core, the named edges.** The orchestrator (`internal/audit`) and the contract (`internal/provider`) name only roles; the one backend adapter (`internal/provider/anthropic`) and the composition root (`cmd/`) name the vendor. A `memory` provider runs nothing, so the whole orchestrator is testable offline.

**The package map:**

| Package | Role |
|---|---|
| `internal/paper` | resolve a reference to a stable identity + slug (001) |
| `internal/method` | the method — instructions, rubric, task template, experiment doctrine — and its digest |
| `internal/instrument` | the `instrument.json` schema and its validation (007a) |
| `internal/provider` | the neutral backend contract; `anthropic` and `memory` adapters |
| `internal/audit` | the orchestrator: run an audit, optionally into a mounted repo |
| `internal/experiment` | run a reproduction's declared analysis on new data (007b); never imported by `audit` |
| `internal/publish` | the manifest, the redistribution gate, naming, the filesystem forge |
| `internal/github` | the small GitHub client publishing and the registry need |
| `internal/registry` | the derived registry: relate reproductions of one paper (003) |
| `internal/job` | the job lifecycle: the state machine, the store seam, the worker (006b) |
| `internal/access` | the authorisation model: grants, tiers, project membership, seed-and-bind (006c) |
| `internal/platform` | the policy an admin tunes at runtime: the settings row — start reserve, overdraft floor, rate card (006k, 011) |
| `internal/credit` | the ledger: wallets, project pots, holds and charges, in micro-USD (011) |
| `internal/profile` | the self-asserted researcher identity a public sign-off is published under (006n) |
| `internal/httpapi` | the HTTP server: submit, status, SSE, read surfaces, the gate seam (006a) |
| `internal/webauth` | the OIDC login, session cookie, gate policy, and dev bypass (006c) |
| `internal/spa` | embeds and serves the built frontend as the app shell (006e) |
| `frontend/` | the researcher UI — bun + React, i18n scaffolding (English only today) (006e) |
| `cmd/replichai` | the CLI: `audit`, `finish`, `resolve`, `publish`, `providers` |
| `cmd/server` | the web app composition root: wires the engine + stores + gate, runs the worker + HTTP server (006a/b) |
| `deploy/` + `.github/` | k8s kustomize (prod/staging) and the CI/release pipeline (010) |

**The trust model — a reproduction is a claim / evidence / verdict triad**, and who authors each is deliberate: the subject of an audit never writes its own grade.

| Layer | Author | Artifact |
|---|---|---|
| Claim | the agent, in the sandbox | the reproduction — code, history, `REPRODUCIBILITY.md` |
| Evidence | the backend's event log, fetched by the system | the transcript (005) |
| Verdict | the system, outside the sandbox | `replication.json` |

**Two publish paths, on purpose.** The *mount* path (006f) is the real one: the agent commits into a live GitHub repo, and the gate becomes a post-run *verifier* (`VerifyTree`, a blocklist of third-party data, because an agent-authored repo is a full project, not a fixed file set). The *filesystem* path (002: `Forge`/`FSForge`, `Classify` allow-list, the `publish` CLI command) stays as the offline, no-backend way to exercise publishing.

## Load-bearing decisions (audit these)

- **The project is the tenant** — no org in the product; each user owns private projects (owner + invited contributors), private by default, made public by the owner. One backend credential pays for every run. The app owns the ownership model; artifacts live in a `Store` (the GitHub forge for the MVP, [drive](https://drive.latere.ai) later, behind one seam — 008). (006)
- **The registry is derived, not stored** — read the org's manifests; no second source of truth. Job-lifecycle state (a later web concern) is the only thing that gets a database. (003, 006b)
- **Ownership by invitation, never delegation** — GitHub OAuth proves identity only; the org invites the user's linked login as a collaborator. The service never holds a user's repo credentials. (006f, 006c)
- **The redistribution gate fails closed** — third-party data is never published; the method keeps downloads out of the mounted repo, and the verifier bars a leaked repo from ever going public. (002, 006f)
- **Never simulate the outer loop** — outer-loop execution is a downstream engine, `verdict`/`rate` stay audit-only, a synthetic cohort is labelled indelibly. Regression-tested in `evals/case-01`. (007)
- **Open sign-in, metered spending** — anyone signs in with a latere account and is granted `user`; what limits them is money, not a guest list, and they arrive with none. The gate stays fail-closed and per-request. (006c, 006q, 011)

**006f is built and deployed:** the per-project collaboration API, the owner's public flip gated by a recorded responsibility sign-off, the public zone, and withdrawal (006o) all shipped, and 006i wired the server-side mount-and-publish 006f had left open. What remains of 006f is the **two-token split** — a fine-grained, per-repo mount token distinct from the org credential — which is now **009**. Its first two commits shipped: the transcript-leak gate (a foreign secret in the transcript now bars the flip under a `secret_in_transcript` review, closing the 005 divergence) and a GitHub App credential that moves every *server-side* operation off the org PAT, with installation-token refresh. Its third commit — minting a per-repo mount token, revoking and scrubbing it — is **deferred on a hard constraint**: the mount sets its credential once at session creation and a GitHub App installation token lives ≤1h, shorter than some runs (the real 006i run was 53 min), so a minted mount token would lose push access mid-run. The mount keeps the non-expiring org PAT until audits are bounded under an hour or the backend can refresh a mount token.

## The spec map

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | implemented | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | implemented | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | implemented | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented | — |
| 005 | [Capture and publish the reproduction transcript](005-transcript.md) | implemented | 002, 006f |
| 006 | [The web application](006-web-application.md) | draft | 002, (003, 005) |
| 006a | [HTTP API and streaming](006a-http-api-and-streaming.md) | built | 006b, (003, 005) |
| 006b | [Job model and persistence](006b-job-model-and-persistence.md) | built | 002, 004 |
| 006c | [Identity, tiers, and the access gate](006c-identity-and-access.md) | implemented | 006b |
| 006d | [PDF ingestion](006d-pdf-ingestion.md) | draft | 001, 006a |
| 006e | [Frontend](006e-frontend.md) | implemented | 006a |
| 006f | [Taking a reproduction out](006f-taking-a-reproduction-out.md) | core built | 002, 004, 006g, (006c) |
| 006g | [Projects and replication sessions](006g-projects-and-replication-sessions.md) | implemented, deployed | 006b, 006c |
| 006h | [Session transcript viewer](006h-session-transcript-viewer.md) | implemented, deployed | 006g, 005, 003 |
| 006i | [Server-side mount and publish](006i-server-side-mount-and-publish.md) | built | 006f, 006b |
| 006j | [The New-project flow](006j-the-new-project-flow.md) | built | 006g, 006c |
| 006k | [The platform admin console](006k-the-platform-admin-console.md) | built; its policy row reshaped by 011 | 006c, 006b |
| 006l | [The session quota](006l-the-session-quota.md) | **superseded by 011** | 006k, 006b, 006g, 006c |
| 006m | [The signed-in person](006m-the-signed-in-person.md) | built | 006c |
| 006n | [Who signed off](006n-who-signed-off.md) | built | 006f, 006g |
| 006o | [Withdrawing a publication](006o-withdrawing-a-publication.md) | built | 006f, 006n |
| 006p | [The invitation that arrives](006p-the-invitation-that-arrives.md) | built | 006f, 006c |
| 006q | [Sign-in is open, budget is not](006q-sign-in-is-open-budget-is-not.md) | built; half superseded by 011 | 006c, 006l, 006k |
| 006r | [The public profile](006r-the-public-profile.md) | built | 006n, 006f |
| 006s | [Reading a session in time](006s-reading-a-session-in-time.md) | built | 006h, 006g |
| 007 | [From reproduction to replication](007-from-reproduction-to-replication.md) | draft | 002, 004, 006f |
| 007a | [The study protocol as an artifact](007a-the-study-protocol-as-an-artifact.md) | implemented | 004 |
| 007b | [Experiments over instruments](007b-experiments-over-instruments.md) | engine built | 007a, 006f |
| 007c | [The synthetic pilot](007c-the-synthetic-pilot.md) | engine built | 007b |
| 007d | [Hosting a study](007d-hosting-a-study.md) | draft | 007b, 006a–006c, 006e |
| 008 | [Drive as the artifact data plane](008-drive-data-plane.md) | deferred | 006 (MVP first) |
| 009 | [Secret containment when the agent commits live](009-secret-containment-when-the-agent-commits-live.md) | commits 1–2 shipped; 3 deferred | 002, 005, 006f, 006i |
| 010 | [Deployment and release](010-deployment-and-release.md) | built | 006a, 006b, 006c |
| 011 | [What a session costs](011-what-a-session-costs.md) | built | 006b, 006c, 006g, 006k, 006h |
| 012 | [A project holds papers](012-a-project-holds-papers.md) | built | 006g, 006j, 006c, 011, 001 |
| 014a | [Managed runs close truthfully](014a-managed-runs-close-truthfully.md) | built | 004, 006b, 011 |
| 014b | [Job-state writes are required](014b-job-state-writes-are-required.md) | built | 006b, 011, 014a |
| 014c | [Publication requires evidence](014c-publication-requires-evidence.md) | built | 002, 005, 006i, 009 |
| 014d | [An auth outage is not sign-out](014d-an-auth-outage-is-not-sign-out.md) | built | 006c, 006e, 013 |
| 015 | [A verdict belongs to a session](015-a-verdict-belongs-to-a-session.md) | built | 012, 013, 006f, 006s |
| 016 | [A paper that is not published yet](016-a-paper-that-is-not-published-yet.md) | deferred (ideation) | 001, 006d, 012, 007b, 008 |
| 017 | [The first screen](017-the-first-screen.md) | built | 006e, 006q, 013 |
| 018 | [One list of projects](018-one-list-of-projects.md) | built | 006g, 006f, 006r, 013 |
| 019 | [A project can be thrown away](019-a-project-can-be-thrown-away.md) | built | 006g, 006o, 011, 013, 018 |
| 020 | [Download the whole project](020-download-the-whole-project.md) | built | 006f, 005, 015, 018 |
| 021 | [What the run actually found](021-what-the-run-actually-found.md) | built | 002, 005, 007a, 011, 015, 018 |
| 021a | [The findings as data](021a-the-findings-as-data.md) | built | 021, 007a, 002, 006i |
| 021b | [A verdict that reads like one](021b-a-verdict-that-reads-like-one.md) | built | 021a, 015, 006s |
| 021c | [What the authors released](021c-what-the-authors-released.md) | built | 021a, 015, 002 |
| 021d | [Watching a session run](021d-watching-a-session-run.md) | built (revised down) | 006a, 006s, 005, 011 |
| 021e | [The project rail](021e-the-project-rail.md) | built | 021a, 021d, 018, 006n, 011 |
| 022 | [Money comes in without an admin](022-money-comes-in-without-an-admin.md) | designed | 011, 006c, 006k, 010 |
| 022a | [The idempotent paid top-up](022a-the-idempotent-paid-topup.md) | built | 011 |
| 022b | [Checkout and the webhook](022b-checkout-and-the-webhook.md) | built | 022a |
| 022c | [Refunds and the solvency read](022c-refunds-and-the-solvency-read.md) | built | 022b |
| 022d | [The freeze, server-side](022d-a-negative-wallet-is-frozen.md) | built | 022c |
| 022e | [Buying credits](022e-buying-credits.md) | built | 022b |
| 022f | [The frozen account, in the UI](022f-the-frozen-account-in-the-ui.md) | built | 022c, 022d, 022e |
| 023 | [The outer loop, as a product](023-the-outer-loop-as-a-product.md) | draft | 007b, 007c, 006b, 015 |
| 023a | [Experiments persisted and dispatched](023a-experiments-persisted-and-dispatched.md) | draft | 007b, 007c, 006b, 015 |
| 023b | [The experiment HTTP surface](023b-the-experiment-http-surface.md) | draft | 023a, 006a, 006c |
| 023c | [Running an experiment from the app](023c-running-an-experiment-from-the-app.md) | draft | 023b, 006e, 013, 021 |
| 023d | [Ground truth for a cohort](023d-ground-truth-for-a-cohort.md) | draft | 023c, 007c |
| 024 | [Settings is a place, and the ledger names its projects](024-settings-is-a-place.md) | built | 013, 011, 018, 019 |
| 024a | [Settings as a destination, with sub-tabs](024a-settings-as-a-destination.md) | built | 013 |
| 024b | [The ledger names its project](024b-the-ledger-names-its-project.md) | built | 024a, 011, 019 |
| 025 | [The solvency an operator can act on](025-the-solvency-an-operator-can-act-on.md) | designed | 022c, 006k, 022d |
| 026 | [The people table at scale](026-the-people-table-at-scale.md) | designed | 006k, 022f, 011 |
| 027 | [EU VAT, and the invoice that comes with it](027-eu-vat-and-the-invoice.md) | designed | 022b, 022c, 022e, 006k, 011 |
| 028 | [The registry opens](028-the-registry-opens.md) | built | 006f, 006r, 013, 017, 018, 021, 011 |
| 029 | [The landing is a place](029-the-landing-is-a-place.md) | built | 028, 017, 013, 006q |
| 030 | [The app on a phone](030-the-app-on-a-phone.md) | built | 006e, 013, 017 |
| 031 | [The model is a choice](031-the-model-is-a-choice.md) | designed | 011, 006b, 012, 006e |

## The three tracks

**The engine (001–005) — done.** 001 decides what a paper *is*; 002 decides where a reproduction *goes* and gates what may be republished; 003 relates repeat reproductions of one paper by identity; 004 is the record of why the core is Go. 005 closed the evidence layer: the agent's full session transcript is fetched and committed, by the system, in the same finalize step as the manifest — so each reproduction is claim + evidence + verdict.

**The web application (006).** A single-tenant service that wraps the engine: submit a paper (URL or dropped PDF), watch it run, be granted the resulting repository, and inspect the transcript and cost afterwards. It spawns six children — **006a** the HTTP API and SSE, **006b** the job model and Postgres, **006c** identity and the fail-closed access gate, **006d** PDF ingestion, **006e** the frontend, **006f** taking a reproduction out (the mount publish, invitation, and visibility flip — the one child already built at its core). Closed beta: signing in grants nothing until a platform admin seeds you.

**Replication (007) — the track past the inner loop.** It turns a reproduction into a re-runnable **instrument** — the study protocol, the analysis entrypoint, and a servability verdict, declared as data (**007a**) — then generalises the unit of work: an **experiment** binds one instrument to one **data source**, of which there are four. The paper's released data gives re-analysis; a researcher's uploaded dataset gives a new finding on the paper's instrument (**007b**); an LLM cohort gives a *pilot* that shakes the apparatus down and never becomes evidence about people (**007c**); real participants, through a page the platform hosts, give re-observation (**007d**). One generalisation, four bindings — "run it on my own data" is a row in a table, not a feature of its own.

What 007 does **not** do is touch the audit: the never-simulate-the-outer-loop boundary is a rubric failure condition and the product's thesis. 007 also revises **006b** (a job runs an audit *or* an experiment) and **006c** (project membership beside the tiers) — written before either is built, because afterwards it would be a migration.

## Build sequence

Engine first; the web shell last, because every screen it renders needs an engine capability the earlier specs deliver.

```
002 ✓ → 003 ✓ → 006f-core ✓ → 005 ✓ → 007a ✓ → 007b ✓ → 007c ✓
     → 006a ✓ / 006b ✓ (runs on Postgres via compose) → 006c (identity, next) → 006d (PDF) → 006e (frontend) → 007d → 008
```

Done so far: 001–005, 006f's core, 007a, the engine halves of 007b and 007c, and **006a and 006b** — the job state machine and its in-process worker (`internal/job`, with in-memory and Postgres stores behind one contract), the HTTP router, SSE stream, read surfaces, and fail-closed gate seam (`internal/httpapi`), and the composition root (`cmd/server`) that wires them to the engine. **The web app runs locally**: `docker compose up` brings up Postgres + the server, a submitted paper walks `submitted → done` through the real HTTP + worker path, and the job survives a restart. It defaults to the in-process memory provider (no API key); `--provider anthropic` does real audits.

**006c's core is built**: the authorisation model (`internal/access` — grants, three tiers, project membership, seed-by-verified-email with one-way binding, in-memory and Postgres against one contract) and the fail-closed gate policy (`internal/webauth`) that resolves a grant per request. What remains of 006c is the **OIDC login front door** — sign-in via `latere.ai/x/pkg/oidclogin`, the session cookie holding only `sub`+email, `/me`, the `/admin/access` endpoints, the dev-bypass off-loopback refusal, and rewiring `cmd/server` from the `--dev` gate to the real one. Until that lands the server still runs behind `--dev`.

**The web application is usable end to end.** What remains is depth, not the core loop: **006f packaged** for reuse (the `publishing` phase — a pass-through in the server today, so runs are graded but not yet pushed to a repository), **006d** (dropping a PDF to submit), a server-side job list, **011** (credits replacing the session counter, so what limits a run is what it actually costs), and 007d.

**Production is provisioned** ([010](010-deployment-and-release.md)): `replichai.latere.ai` DNS, the `replichai` database and its secret (terraform, applied), the k8s kustomize manifests (`deploy/prod` in the `latere` namespace), and the CI/release pipeline — a `v*` tag builds, deploys to prod, and smokes. Prod is **fail-closed**: with no login yet it admits nobody, and the release smoke fails if the dev bypass ever reaches production.

Still open in the server: the experiment `Kind` in `job.Runner` (the dispatch is there; the experiment callee is 007b's server wiring), the Postgres stores for 007b/007c, and reading a transcript through `ReproReader`. The first two are now scoped as the [023](023-the-outer-loop-as-a-product.md) chain — [023a](023a-experiments-persisted-and-dispatched.md) fills them, [023b](023b-the-experiment-http-surface.md)/[023c](023c-running-an-experiment-from-the-app.md) put a person in front of them.

Within 007 the order inverted how the capabilities were first asked for — the synthetic pilot (007c) came third, because a cohort is only checkable where the paper released real data to check it against.
